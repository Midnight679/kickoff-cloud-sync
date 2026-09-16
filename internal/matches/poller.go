package matches

import (
	"context"

	"github.com/dank/rlapi"
)

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
func PollOnce(ctx context.Context, rpc *rlapi.PsyNetRPC, onMatch MatchHandler) (matchCount int, err error) {
	entries, err := rpc.GetMatchHistory(ctx)
	if err != nil {
		return 0, err
	}

	for _, m := range entries {
		onMatch(m.Match.MatchGUID, m.ReplayUrl)
	}
	return len(entries), nil
}
