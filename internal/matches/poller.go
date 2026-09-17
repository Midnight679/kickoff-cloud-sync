package matches

import (
	"context"
	"fmt"

	"github.com/dank/rlapi"

	"github.com/Midnight679/kickoff-cloud-sync/internal/httpclient"
)

// historyFetcher is the one method PollOnce needs from
// *rlapi.PsyNetRPC. An interface so the failure handling in
// fetchHistory can be tested without a live PsyNet connection.
type historyFetcher interface {
	GetMatchHistory(ctx context.Context) ([]rlapi.MatchEntry, error)
}

// MatchHandler is called once per match currently in history, on
// every poll. replayURL is the cloud link to the actual replay file
// — see download.go. There's no cursor/dedup logic here on purpose —
// this fires for every match in the list every poll. The caller is
// responsible for skipping anything already uploaded (see
// internal/accounts, which checks each account's persisted
// UploadedMatches map). This makes manual and scheduled polls behave
// identically, and is robust to the list reordering or a match
// briefly disappearing/reappearing.
type MatchHandler func(matchID, replayURL string)

// PollOnce checks match history a single time for the given
// connection. Scheduling now lives in internal/accounts.Manager,
// which runs one shared cycle across every active account rather
// than each account keeping its own timer — this function is the
// unit of work that cycle calls per account, and is also what a
// manual "poll now" button calls directly.
//
// matchCount is the total number of entries in the history response
// (regardless of whether onMatch's caller treats any of them as
// new) — the caller uses this to tell "the poll succeeded and found
// nothing" apart from "the poll failed before it could check."
func PollOnce(ctx context.Context, rpc historyFetcher, onMatch MatchHandler) (matchCount int, err error) {
	entries, err := fetchHistory(ctx, rpc)
	if err != nil {
		return 0, err
	}

	for _, m := range entries {
		onMatch(m.Match.MatchGUID, m.ReplayUrl)
	}
	return len(entries), nil
}

// fetchHistory wraps the GetMatchHistory call with two protections
// that rlapi (as of v0.1.23) does not provide itself.
//
// A deadline. The ctx this app polls with lives as long as the app
// does. With no deadline, a request the server never answers blocks
// the whole shared poll cycle forever, and the goroutine rlapi starts
// per request (it waits on ctx.Done) is never released, so every poll
// leaks one. The deadline reuses the user's network timeout setting.
//
// A recover. When the socket closes while a request is outstanding
// (PsyNet does exactly this on DuplicateLogin, i.e. when the real game
// client starts), rlapi closes the pending response channel and then
// dereferences the nil it receives from it. The same happens about
// half the time when the deadline above fires. Unrecovered, that panic
// ends the whole app from the background poll goroutine. It is safe to
// turn into an error: the caller reports it, and the next cycle's
// ensureConnected sees the dead connection and reconnects.
func fetchHistory(ctx context.Context, rpc historyFetcher) (entries []rlapi.MatchEntry, err error) {
	timeout := httpclient.Client().Timeout
	if timeout <= 0 {
		timeout = httpclient.DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	defer func() {
		if r := recover(); r != nil {
			entries = nil
			err = fmt.Errorf("connection dropped while waiting for match history (recovered: %v)", r)
		}
	}()
	return rpc.GetMatchHistory(ctx)
}
