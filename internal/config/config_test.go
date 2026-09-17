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
	lastRetry := now
	retryHour := 4
	orig := Config{
		PollIntervalSecs: 600,
		Accounts: []Account{
			{ID: "a", UploadedMatches: map[string]string{"m1": "r1"}, LastPollTime: &now},
			{ID: "b"}, // nil map and nil time must survive as-is
		},
		LastFailedUploadsRetry: &lastRetry,
		FailedUploadsRetryHour: &retryHour,
	}

	clone := orig.Clone()

	orig.Accounts[0].UploadedMatches["m2"] = "r2"
	orig.Accounts[0].ID = "changed"
	*orig.Accounts[0].LastPollTime = now.Add(time.Hour)
	*orig.LastFailedUploadsRetry = now.Add(time.Hour)
	*orig.FailedUploadsRetryHour = 5

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
	if !clone.LastFailedUploadsRetry.Equal(want) {
		t.Error("clone shares the LastFailedUploadsRetry pointer with the original")
	}
	if clone.FailedUploadsRetryHour == nil || *clone.FailedUploadsRetryHour != 4 {
		t.Error("clone shares the FailedUploadsRetryHour pointer with the original")
	}
}

// TestCloneLeavesNilPointersNil guards the nil cases separately, since
// the happy-path test above always sets them.
func TestCloneLeavesNilPointersNil(t *testing.T) {
	clone := Config{}.Clone()
	if clone.LastFailedUploadsRetry != nil {
		t.Error("clone should leave a nil LastFailedUploadsRetry as nil")
	}
	if clone.FailedUploadsRetryHour != nil {
		t.Error("clone should leave a nil FailedUploadsRetryHour as nil")
	}
}

func TestRetryHourDefault(t *testing.T) {
	if got := (Config{}).RetryHour(); got != DefaultFailedUploadsRetryHour {
		t.Errorf("got %d, want default %d", got, DefaultFailedUploadsRetryHour)
	}
	hour := 0 // midnight — a legitimate explicit choice, not "unset"
	if got := (Config{FailedUploadsRetryHour: &hour}).RetryHour(); got != 0 {
		t.Errorf("got %d, want the explicitly configured 0", got)
	}
}

func TestMaxFailedUploadsDefault(t *testing.T) {
	if got := (Config{}).MaxFailedUploads(); got != DefaultFailedUploadsMaxCount {
		t.Errorf("got %d, want default %d", got, DefaultFailedUploadsMaxCount)
	}
	if got := (Config{FailedUploadsMaxCount: 25}).MaxFailedUploads(); got != 25 {
		t.Errorf("got %d, want the explicitly configured 25", got)
	}
}
