package config

import (
	"testing"
	"time"
)

// TestCloneSharesNoMutableState guards the property persist() relies
// on: mutating the original after Clone must never show through.
func TestCloneSharesNoMutableState(t *testing.T) {
	now := time.Now()
	want := now // orig holds &now, so keep an untouched copy to compare against
	orig := Config{
		PollIntervalSecs: 600,
		Accounts: []Account{
			{ID: "a", UploadedMatches: map[string]string{"m1": "r1"}, LastPollTime: &now},
			{ID: "b"}, // nil map and nil time must survive as-is
		},
	}

	clone := orig.Clone()

	orig.Accounts[0].UploadedMatches["m2"] = "r2"
	orig.Accounts[0].ID = "changed"
	*orig.Accounts[0].LastPollTime = now.Add(time.Hour)

	if _, leaked := clone.Accounts[0].UploadedMatches["m2"]; leaked {
		t.Error("clone shares the UploadedMatches map with the original")
	}
	if clone.Accounts[0].ID != "a" {
		t.Error("clone shares the Accounts backing array with the original")
	}
	if !clone.Accounts[0].LastPollTime.Equal(want) {
		t.Error("clone shares the LastPollTime pointer with the original")
	}
	if clone.Accounts[1].UploadedMatches != nil || clone.Accounts[1].LastPollTime != nil {
		t.Error("clone should leave nil map / nil time as nil")
	}
}
