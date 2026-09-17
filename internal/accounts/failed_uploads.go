package accounts

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Midnight679/kickoff-cloud-sync/internal/config"
)

// RetryFailedUploadsResult summarizes what a retry pass over the
// failed-uploads queue did — attempted/succeeded counts for the manual
// "Retry now" button's own feedback, since individual results are
// already reported through the normal upload-complete/upload-error
// events like any other upload.
type RetryFailedUploadsResult struct {
	Attempted int `json:"attempted"`
	Succeeded int `json:"succeeded"`
}

// GetFailedUploadsRetryHour returns the configured local hour (0-23)
// the once-daily failed-uploads retry pass targets.
func (m *Manager) GetFailedUploadsRetryHour() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg.RetryHour()
}

// SetFailedUploadsRetryHour updates the target hour and wakes the
// retry loop so a change takes effect immediately — the same idea as
// SetPollIntervalSecs waking the scheduled poll loop — rather than
// only being picked up the next time the loop happens to wake up on
// its own.
func (m *Manager) SetFailedUploadsRetryHour(hour int) error {
	if hour < 0 || hour > 23 {
		return fmt.Errorf("retry hour must be between 0 and 23")
	}
	m.mu.Lock()
	m.cfg.FailedUploadsRetryHour = &hour
	m.mu.Unlock()

	m.wakeFailedUploadsRetryLoop()

	return m.persist()
}

// GetFailedUploadsMaxCount returns the configured failed-uploads cap.
func (m *Manager) GetFailedUploadsMaxCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg.MaxFailedUploads()
}

// SetFailedUploadsMaxCount updates the cap. A lower cap doesn't
// immediately evict anything on its own — the next replay that
// actually moves into failed-uploads is what triggers eviction down to
// the new limit (see evictOldestFailedUploadsIfFull), the same way a
// smaller PollIntervalSecs doesn't retroactively rewrite history.
func (m *Manager) SetFailedUploadsMaxCount(n int) error {
	if n < 1 {
		return fmt.Errorf("failed-uploads cap must be at least 1")
	}
	m.mu.Lock()
	m.cfg.FailedUploadsMaxCount = n
	m.mu.Unlock()
	return m.persist()
}

// RetryFailedUploadsNow runs the failed-uploads retry pass
// immediately — the manual counterpart to PollAccountNow's "Poll Now",
// for the "Retry now" button in Settings. Also resets the once-daily
// loop's wait so it doesn't fire again almost immediately after,
// re-attempting the same files a manual run just finished with.
func (m *Manager) RetryFailedUploadsNow(ctx context.Context) RetryFailedUploadsResult {
	attempted, succeeded := m.retryFailedUploads(ctx)
	m.recordFailedUploadsRetryRan()
	m.wakeFailedUploadsRetryLoop()
	return RetryFailedUploadsResult{Attempted: attempted, Succeeded: succeeded}
}

// recordFailedUploadsRetryRan persists "now" as the last time a
// failed-uploads retry pass ran, shared by the scheduled loop and the
// manual "Retry now" trigger.
func (m *Manager) recordFailedUploadsRetryRan() {
	now := time.Now()
	m.mu.Lock()
	m.cfg.LastFailedUploadsRetry = &now
	m.mu.Unlock()
	_ = m.persist()
}

// wakeFailedUploadsRetryLoop signals runFailedUploadsRetryLoop to
// recompute its wait right away — non-blocking, since a signal already
// pending covers the same thing just as well.
func (m *Manager) wakeFailedUploadsRetryLoop() {
	select {
	case m.failedUploadsRetryChanged <- struct{}{}:
	default:
	}
}

// hasFailedUpload reports whether a match's replay is already sitting
// in the failed-uploads queue. handleMatch checks this the same way it
// checks hasPendingReplay, so a match that permanently failed isn't
// redundantly re-downloaded and re-attempted on every regular poll —
// it only gets attempted again by the once-daily pass.
func hasFailedUpload(accountID, matchID string) bool {
	if !isSafePathComponent(accountID) || !isSafePathComponent(matchID) {
		return false
	}
	dir, err := config.FailedUploadsDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(dir, accountID+pendingFileSeparator+matchID+".replay"))
	return err == nil
}

// moveToFailedUploads relocates a replay that ballchasing has
// permanently rejected (see uploader.UploadError.Permanent) from
// wherever it currently is — a fresh download in handleMatch, or the
// pending-uploads cache during a retry pass — into the failed-uploads
// directory, enforcing the configured cap on the way in. Uses the same
// accountID__matchID.replay naming as pending-uploads, so
// parsePendingFilename works unchanged against either directory.
func (m *Manager) moveToFailedUploads(srcPath, accountID, matchID string) error {
	if !isSafePathComponent(accountID) || !isSafePathComponent(matchID) {
		return fmt.Errorf("refusing to move replay to failed-uploads: unexpected characters in account or match ID")
	}

	dir, err := config.FailedUploadsDir()
	if err != nil {
		return err
	}

	m.mu.Lock()
	maxCount := m.cfg.MaxFailedUploads()
	m.mu.Unlock()
	evictOldestFailedUploadsIfFull(dir, maxCount)

	dest := filepath.Join(dir, accountID+pendingFileSeparator+matchID+".replay")

	if err := os.Rename(srcPath, dest); err == nil {
		return nil
	}

	// Cross-device fallback, same as cachePendingReplay.
	in, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Remove(srcPath)
}

// evictOldestFailedUploadsIfFull deletes the oldest file(s) in dir (by
// modification time — set the moment each one was moved into
// failed-uploads) until adding one more would no longer exceed
// maxCount. Best-effort: a failure here is logged rather than blocking
// the move that triggered it — a folder briefly over the cap is a much
// smaller problem than losing a replay that just permanently failed.
func evictOldestFailedUploadsIfFull(dir string, maxCount int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	type failedFile struct {
		name    string
		modTime time.Time
	}
	var files []failedFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".replay") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, failedFile{name: e.Name(), modTime: info.ModTime()})
	}

	if len(files) < maxCount {
		return
	}

	sort.Slice(files, func(i, j int) bool { return files[i].modTime.Before(files[j].modTime) })

	toRemove := len(files) - maxCount + 1
	for i := 0; i < toRemove; i++ {
		if err := os.Remove(filepath.Join(dir, files[i].name)); err != nil {
			log.Printf("could not evict oldest failed upload %s to make room: %v", files[i].name, err)
		}
	}
}

// retryFailedUploads attempts every replay in the failed-uploads
// directory once, using each account's *current* ballchasing token —
// exactly like retryPendingUploads, but over the slow-retry directory
// instead, and without relocating a file that fails again: once a
// replay is here, it stays on the once-daily schedule regardless of
// which particular error a given attempt produces, rather than
// bouncing between directories based on each attempt's classification.
// Returns how many files were considered and how many of those
// succeeded, for RetryFailedUploadsNow's summary.
func (m *Manager) retryFailedUploads(ctx context.Context) (attempted, succeeded int) {
	dir, err := config.FailedUploadsDir()
	if err != nil {
		return 0, 0
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".replay") {
			continue
		}
		accountID, matchID, ok := parsePendingFilename(entry.Name())
		if !ok {
			continue
		}

		m.mu.Lock()
		if m.polling[accountID] {
			m.mu.Unlock()
			continue // busy elsewhere right now; this account's files wait for tomorrow's pass
		}
		m.polling[accountID] = true
		idx := m.indexOf(accountID)
		var hasToken, alreadyUploaded bool
		var visibility string
		if idx != -1 {
			hasToken = m.cfg.Accounts[idx].HasBallchasingToken
			_, alreadyUploaded = m.cfg.Accounts[idx].UploadedMatches[matchID]
			visibility = m.cfg.Accounts[idx].Visibility()
		}
		m.mu.Unlock()

		attempted++
		if m.attemptCachedUpload(dir, entry.Name(), accountID, matchID, idx, hasToken, alreadyUploaded, visibility, false) == cachedUploadSucceeded {
			succeeded++
		}

		m.mu.Lock()
		delete(m.polling, accountID)
		m.mu.Unlock()
	}
	return attempted, succeeded
}

// durationUntilNextRetryHour returns how long to wait until the next
// occurrence of the given local hour (0-23), recomputed from the
// actual wall-clock target each time (rather than just adding a fixed
// 24h) so it stays correct across a daylight-saving change instead of
// silently drifting an hour.
func durationUntilNextRetryHour(hour int, now time.Time) time.Duration {
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next.Sub(now)
}

// nextFailedUploadsRetryWait returns how long
// runFailedUploadsRetryLoop should wait before its next attempt:
// immediately if a retry is already overdue (never run before, or
// more than 24 hours since the last one — catching up after the app
// was closed or the machine was asleep through the target hour),
// otherwise until the next occurrence of hour. "At least once every 24
// hours" takes priority over hitting the target hour exactly once a
// retry is already overdue.
func nextFailedUploadsRetryWait(last *time.Time, hour int, now time.Time) time.Duration {
	if last == nil || now.Sub(*last) >= 24*time.Hour {
		return 0
	}
	return durationUntilNextRetryHour(hour, now)
}

// runFailedUploadsRetryLoop runs retryFailedUploads once every 24
// hours (see nextFailedUploadsRetryWait) until ctx is cancelled.
// failedUploadsRetryChanged (signalled by SetFailedUploadsRetryHour
// and RetryFailedUploadsNow) interrupts an in-progress wait so a
// changed target hour, or a manual run that just happened, takes
// effect immediately instead of only on the next natural wake-up.
func (m *Manager) runFailedUploadsRetryLoop(ctx context.Context) {
	for {
		m.mu.Lock()
		last := m.cfg.LastFailedUploadsRetry
		hour := m.cfg.RetryHour()
		m.mu.Unlock()

		timer := time.NewTimer(nextFailedUploadsRetryWait(last, hour, time.Now()))

		ready := false
		for !ready {
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
				ready = true
			case <-m.failedUploadsRetryChanged:
				m.mu.Lock()
				last = m.cfg.LastFailedUploadsRetry
				hour = m.cfg.RetryHour()
				m.mu.Unlock()
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(nextFailedUploadsRetryWait(last, hour, time.Now()))
			}
		}

		m.retryFailedUploads(ctx)
		m.recordFailedUploadsRetryRan()
	}
}
