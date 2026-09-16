package accounts

import (
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/Midnight679/kickoff-cloud-sync/internal/config"
	"github.com/Midnight679/kickoff-cloud-sync/internal/secrets"
	"github.com/Midnight679/kickoff-cloud-sync/internal/uploader"
)

// pendingFileSeparator joins accountID and matchID in the cached
// filename, e.g. "3f9a1c2b__a1b2c3.replay". Both IDs are plain hex
// (newID()) or whatever rlapi's match ID format turns out to be —
// double underscore is chosen to be unlikely to collide with either.
const pendingFileSeparator = "__"

// cachePendingReplay is called when a replay downloaded successfully
// but the ballchasing upload failed — rather than losing the
// download (or leaving an untracked file sitting in the OS temp
// dir), it's moved into a persistent directory so it can be retried
// on a later poll. Tries a fast os.Rename first; falls back to
// copy+remove if the temp dir and the pending dir are on different
// filesystems (os.Rename fails across devices).
func cachePendingReplay(srcPath, accountID, matchID string) error {
	dir, err := config.PendingUploadsDir()
	if err != nil {
		return err
	}
	dest := filepath.Join(dir, accountID+pendingFileSeparator+matchID+".replay")

	if err := os.Rename(srcPath, dest); err == nil {
		return nil
	}

	// Cross-device fallback: copy then remove the original.
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

// hasPendingReplay reports whether a match's replay is already
// downloaded and cached for retry (see cachePendingReplay). handleMatch
// checks this before doing a fresh download so a match stuck on a
// retryable failure (e.g. ballchasing's daily upload quota) isn't
// redundantly re-downloaded and re-attempted on every poll on top of
// retryPendingUploads already handling it once per cycle.
func hasPendingReplay(accountID, matchID string) bool {
	dir, err := config.PendingUploadsDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(dir, accountID+pendingFileSeparator+matchID+".replay"))
	return err == nil
}

// retryPendingUploads scans the pending-uploads directory and
// attempts each cached replay again, using that account's *current*
// ballchasing token (so a token fixed after the original failure
// works without any other action). Runs once per scheduled poll
// cycle, before the normal per-account match check — see runCycle.
// manual is threaded through to emitted events the same as elsewhere,
// so the frontend log can tell a manual "Poll Now" retry pass apart
// from a scheduled one.
func (m *Manager) retryPendingUploads(ctx context.Context, manual bool) {
	dir, err := config.PendingUploadsDir()
	if err != nil {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".replay") {
			continue
		}
		accountID, matchID, ok := parsePendingFilename(entry.Name())
		if !ok {
			continue // not one of ours / malformed — leave it alone
		}

		m.mu.Lock()
		idx := m.indexOf(accountID)
		var hasToken bool
		var alreadyUploaded bool
		if idx != -1 {
			hasToken = m.cfg.Accounts[idx].HasBallchasingToken
			_, alreadyUploaded = m.cfg.Accounts[idx].UploadedMatches[matchID]
		}
		m.mu.Unlock()

		fullPath := filepath.Join(dir, entry.Name())

		if idx == -1 {
			// Account was removed since this was cached — nothing
			// sensible to retry against; clean it up.
			_ = os.Remove(fullPath)
			continue
		}
		if alreadyUploaded {
			// Got uploaded some other way (e.g. a manual poll) since
			// this was cached — just clean up the leftover file.
			_ = os.Remove(fullPath)
			continue
		}
		if !hasToken {
			continue // still no token configured — leave cached, try again next cycle
		}
		accountSecrets, err := secrets.Load(accountID)
		if err != nil || accountSecrets.BallchasingToken == "" {
			continue // couldn't read the token right now — try again next cycle
		}
		token := accountSecrets.BallchasingToken

		result, err := uploader.UploadReplay(token, fullPath, "public")
		if err != nil {
			log.Printf("retry upload failed for %s: %v", entry.Name(), err)
			m.emit(EventUploadError, EventPayload{AccountID: accountID, MatchID: matchID, Message: err.Error(), Manual: manual})
			continue // leave the file in place for the next cycle
		}

		m.mu.Lock()
		idx = m.indexOf(accountID)
		if idx != -1 {
			m.cfg.Accounts[idx].UploadedMatches[matchID] = result.ID
			m.resetCacheIfOversized(idx, matchID, result.ID)
		}
		m.mu.Unlock()
		_ = m.persist()

		_ = os.Remove(fullPath)
		m.emit(EventUploadComplete, EventPayload{AccountID: accountID, MatchID: matchID, Message: result.Location, Manual: manual})
	}
}

func parsePendingFilename(name string) (accountID, matchID string, ok bool) {
	base := strings.TrimSuffix(name, ".replay")
	parts := strings.SplitN(base, pendingFileSeparator, 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// pendingUploadsSummary is a small debug/log helper — not currently
// bound to the frontend, but handy if you want to surface "N replays
// waiting to retry" somewhere later.
func pendingUploadsSummary() (int, error) {
	dir, err := config.PendingUploadsDir()
	if err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".replay") {
			count++
		}
	}
	return count, nil
}
