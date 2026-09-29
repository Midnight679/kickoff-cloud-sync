package accounts

import (
	"errors"
	"reflect"
	"testing"

	"github.com/Midnight679/kickoff-cloud-sync/internal/uploader"
)

// privateDetails builds ReplayDetails for a private match between two
// custom-named teams, the only shape assignReplayGroupTo's caller
// (finalizeReplay, via GetReplayWithRetry) ever passes it.
func privateDetails(blueName, orangeName string) *uploader.ReplayDetails {
	return &uploader.ReplayDetails{
		PlaylistName: "Private",
		Blue:         uploader.ReplayTeam{Name: blueName},
		Orange:       uploader.ReplayTeam{Name: orangeName},
	}
}

// TestAssignReplayGroupTo_FailedCreateGroupPreservesEarlierMatches
// reproduces the real-world bug found from a user's actual ballchasing
// history: three consecutive matches against the same opponent all
// ended up in no group at all. The original code overwrote its single
// "earlier match" pointer with whichever match triggered a failed
// CreateGroup call, permanently losing the previous one; this asserts
// every match seen while CreateGroup keeps failing is still swept into
// the group once it finally succeeds.
func TestAssignReplayGroupTo_FailedCreateGroupPreservesEarlierMatches(t *testing.T) {
	rt := &runtimeState{}
	createGroupCalls := 0
	createGroup := func(token, name string) (*uploader.CreateGroupResult, error) {
		createGroupCalls++
		if createGroupCalls < 3 {
			return nil, errors.New("simulated ballchasing outage")
		}
		return &uploader.CreateGroupResult{ID: "group-1"}, nil
	}
	var addedToGroup []string
	setReplayGroup := func(token, replayID, groupID string) error {
		addedToGroup = append(addedToGroup, replayID)
		return nil
	}

	details := privateDetails("NIGHTFALL", "POLAR BEARS")

	// Match 1: first of the pair, just tracked.
	assignReplayGroupTo(rt, "token", "match1", details, createGroup, setReplayGroup)
	if rt.lastPrivateGroupID != "" || len(rt.pendingPrivateReplayIDs) != 1 {
		t.Fatalf("after match1: groupID=%q pending=%v, want no group and 1 pending", rt.lastPrivateGroupID, rt.pendingPrivateReplayIDs)
	}

	// Match 2: confirms the pair, but CreateGroup fails (1st failure).
	assignReplayGroupTo(rt, "token", "match2", details, createGroup, setReplayGroup)
	if rt.lastPrivateGroupID != "" {
		t.Fatalf("after match2 (CreateGroup failed): groupID=%q, want still empty", rt.lastPrivateGroupID)
	}
	if want := []string{"match1", "match2"}; !reflect.DeepEqual(rt.pendingPrivateReplayIDs, want) {
		t.Fatalf("after match2: pending=%v, want %v — match1 must not be dropped by the failed attempt", rt.pendingPrivateReplayIDs, want)
	}
	if len(addedToGroup) != 0 {
		t.Fatalf("no replay should have been added to any group yet, got %v", addedToGroup)
	}

	// Match 3: same pair again, CreateGroup fails again (2nd failure).
	assignReplayGroupTo(rt, "token", "match3", details, createGroup, setReplayGroup)
	if want := []string{"match1", "match2", "match3"}; !reflect.DeepEqual(rt.pendingPrivateReplayIDs, want) {
		t.Fatalf("after match3: pending=%v, want %v", rt.pendingPrivateReplayIDs, want)
	}

	// Match 4: same pair, CreateGroup finally succeeds (3rd call) — every
	// accumulated match, not just match4, must end up in the group.
	assignReplayGroupTo(rt, "token", "match4", details, createGroup, setReplayGroup)
	if rt.lastPrivateGroupID != "group-1" {
		t.Fatalf("lastPrivateGroupID = %q, want group-1", rt.lastPrivateGroupID)
	}
	if rt.pendingPrivateReplayIDs != nil {
		t.Fatalf("pendingPrivateReplayIDs = %v, want nil after a successful sweep", rt.pendingPrivateReplayIDs)
	}
	want := []string{"match1", "match2", "match3", "match4"}
	if !reflect.DeepEqual(addedToGroup, want) {
		t.Fatalf("replays added to the group = %v, want %v (all four, none dropped)", addedToGroup, want)
	}

	// Match 5: same pair, group already exists — just adds directly.
	assignReplayGroupTo(rt, "token", "match5", details, createGroup, setReplayGroup)
	if createGroupCalls != 3 {
		t.Errorf("CreateGroup called %d times, want exactly 3 (not called again once a group exists)", createGroupCalls)
	}
	if got := addedToGroup[len(addedToGroup)-1]; got != "match5" {
		t.Errorf("last replay added to the group = %q, want match5", got)
	}
}
