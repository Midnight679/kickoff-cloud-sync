package accounts

import (
	"errors"
	"reflect"
	"testing"

	"github.com/Midnight679/kickoff-cloud-sync/internal/uploader"
)

// TestAssignReplayGroupTo_FailedSetReplayGroupRetriesOnNextMatch
// reproduces a real-world orphaned replay found in a user's ballchasing
// history on v0.2.15: a group had already been created from the first
// two matches of a series, but the third match in that same series
// never joined it. The original code's only retry path was for a
// failed CreateGroup call — once a group already existed, a failed
// SetReplayGroup call for a later match in the series was just logged
// and dropped forever, with nothing to retry it. This asserts a
// failed SetReplayGroup call is retried on the next same-pair match,
// the same way a failed CreateGroup call already is.
func TestAssignReplayGroupTo_FailedSetReplayGroupRetriesOnNextMatch(t *testing.T) {
	rt := &runtimeState{}
	createGroup := func(token, name string) (*uploader.CreateGroupResult, error) {
		return &uploader.CreateGroupResult{ID: "group-1"}, nil
	}
	var addedToGroup []string
	failNext := map[string]bool{}
	setReplayGroup := func(token, replayID, groupID string) error {
		if failNext[replayID] {
			return errors.New("simulated ballchasing rate limit")
		}
		addedToGroup = append(addedToGroup, replayID)
		return nil
	}

	details := privateDetails("TAX EVADERS", "POLAR BEARS")

	// Match 1: first of the pair, just tracked.
	assignReplayGroupTo(rt, "token", "match1", details, createGroup, setReplayGroup)

	// Match 2: confirms the pair, group is created, both matches swept in.
	assignReplayGroupTo(rt, "token", "match2", details, createGroup, setReplayGroup)
	if rt.lastPrivateGroupID != "group-1" {
		t.Fatalf("lastPrivateGroupID = %q, want group-1", rt.lastPrivateGroupID)
	}
	if want := []string{"match1", "match2"}; !reflect.DeepEqual(addedToGroup, want) {
		t.Fatalf("addedToGroup after match2 = %v, want %v", addedToGroup, want)
	}

	// Match 3: same pair, group already exists, but adding it fails
	// (e.g. a transient rate limit while other series are also being
	// confirmed in the same poll).
	failNext["match3"] = true
	assignReplayGroupTo(rt, "token", "match3", details, createGroup, setReplayGroup)
	if want := []string{"match1", "match2"}; !reflect.DeepEqual(addedToGroup, want) {
		t.Fatalf("addedToGroup after failed match3 = %v, want unchanged %v", addedToGroup, want)
	}
	if want := []string{"match3"}; !reflect.DeepEqual(rt.pendingPrivateReplayIDs, want) {
		t.Fatalf("pendingPrivateReplayIDs after failed match3 = %v, want %v — it must be retried, not dropped", rt.pendingPrivateReplayIDs, want)
	}

	// Match 4: same pair again — match3's earlier failure must be
	// retried alongside match4, not left orphaned forever.
	delete(failNext, "match3")
	assignReplayGroupTo(rt, "token", "match4", details, createGroup, setReplayGroup)
	want := []string{"match1", "match2", "match3", "match4"}
	if !reflect.DeepEqual(addedToGroup, want) {
		t.Fatalf("addedToGroup after match4 = %v, want %v (match3 must be swept in)", addedToGroup, want)
	}
	if rt.pendingPrivateReplayIDs != nil {
		t.Fatalf("pendingPrivateReplayIDs = %v, want nil after a successful retry", rt.pendingPrivateReplayIDs)
	}
}
