package uploader

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestUploadError_Permanent covers the classification the
// failed-uploads split (see internal/accounts) relies on: a genuine
// "this file will never work" rejection versus something worth
// retrying — a persistent 429 (already retried by doWithRetry before
// ever reaching here) and 401/403 (a new token can fix it) must stay
// on the retryable side despite being in the 4xx range.
func TestUploadError_Permanent(t *testing.T) {
	cases := []struct {
		status int
		want   bool
	}{
		{http.StatusBadRequest, true},          // 400 — ballchasing rejected the file itself
		{http.StatusUnprocessableEntity, true}, // 422
		{http.StatusUnauthorized, false},       // 401 — bad token, not a bad file
		{http.StatusForbidden, false},          // 403
		{http.StatusTooManyRequests, false},    // 429 — rate limited, not rejected
		{http.StatusInternalServerError, false},
		{http.StatusBadGateway, false},
		{http.StatusServiceUnavailable, false},
	}
	for _, c := range cases {
		err := &UploadError{StatusCode: c.status, Body: "test"}
		if got := err.Permanent(); got != c.want {
			t.Errorf("status %d: Permanent() = %v, want %v", c.status, got, c.want)
		}
	}
}

// TestCustomTeamPair_BothTeamsNamed verifies the happy path a scrim
// series relies on: two custom team names come back as a pair,
// regardless of which side (blue/orange) each one was on.
func TestCustomTeamPair_BothTeamsNamed(t *testing.T) {
	details := &ReplayDetails{
		PlaylistName: "Private",
		Blue:         ReplayTeam{Name: "WHISKER GOBLINS"},
		Orange:       ReplayTeam{Name: "POLAR BEARS"},
	}
	pair, ok := CustomTeamPair(details)
	if !ok {
		t.Fatal("expected ok=true for two custom-named teams")
	}
	want := [2]string{"POLAR BEARS", "WHISKER GOBLINS"} // sorted
	if pair != want {
		t.Errorf("got pair %v, want %v", pair, want)
	}
}

// TestCustomTeamPair_SortedRegardlessOfSide verifies that the same
// two teams swapping blue/orange sides between games (common in a
// real series) still produces an identical pair, so the grouping
// streak isn't broken by a side swap.
func TestCustomTeamPair_SortedRegardlessOfSide(t *testing.T) {
	game1 := &ReplayDetails{
		PlaylistName: "Private",
		Blue:         ReplayTeam{Name: "POLAR BEARS"},
		Orange:       ReplayTeam{Name: "WHISKER GOBLINS"},
	}
	game2 := &ReplayDetails{
		PlaylistName: "Private",
		Blue:         ReplayTeam{Name: "WHISKER GOBLINS"},
		Orange:       ReplayTeam{Name: "POLAR BEARS"},
	}
	pair1, ok1 := CustomTeamPair(game1)
	pair2, ok2 := CustomTeamPair(game2)
	if !ok1 || !ok2 {
		t.Fatal("expected ok=true for both games")
	}
	if pair1 != pair2 {
		t.Errorf("side swap changed the pair: %v vs %v", pair1, pair2)
	}
}

// TestCustomTeamPair_RejectsUnnamedOrNonPrivate covers the cases that
// must NOT produce a groupable pair: a non-private match, and a
// private match where the host left one or both sides on the
// "Blue"/"Orange" default.
func TestCustomTeamPair_RejectsUnnamedOrNonPrivate(t *testing.T) {
	cases := []struct {
		name    string
		details *ReplayDetails
	}{
		{
			name: "not private",
			details: &ReplayDetails{
				PlaylistName: "Ranked Doubles",
				Blue:         ReplayTeam{Name: "POLAR BEARS"},
				Orange:       ReplayTeam{Name: "WHISKER GOBLINS"},
			},
		},
		{
			name: "one side still default",
			details: &ReplayDetails{
				PlaylistName: "Private",
				Blue:         ReplayTeam{Name: "Blue"},
				Orange:       ReplayTeam{Name: "WHISKER GOBLINS"},
			},
		},
		{
			name: "both sides default",
			details: &ReplayDetails{
				PlaylistName: "Private",
				Blue:         ReplayTeam{Name: "Blue"},
				Orange:       ReplayTeam{Name: "Orange"},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, ok := CustomTeamPair(c.details); ok {
				t.Errorf("expected ok=false for %s", c.name)
			}
		})
	}
}

// TestDoWithRetry_RetriesOn429 verifies the actual retry/backoff path
// against a real HTTP round-trip (via httptest), not just reasoning
// about the code — a server that returns 429 twice, then 200, should
// result in exactly three requests and a final 200 response.
func TestDoWithRetry_RetriesOn429(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	resp, err := doWithRetry(func() (*http.Request, error) {
		return http.NewRequest(http.MethodGet, server.URL, nil)
	})
	if err != nil {
		t.Fatalf("doWithRetry returned an error: %v", err)
	}
	defer resp.Body.Close()

	if attempts != 3 {
		t.Errorf("expected 3 requests (2 rate-limited + 1 success), got %d", attempts)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected final status 200, got %d", resp.StatusCode)
	}
}

// TestDoWithRetry_GivesUpAfterMaxAttempts verifies a server that
// never stops returning 429 eventually surfaces an error instead of
// retrying forever.
func TestDoWithRetry_GivesUpAfterMaxAttempts(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	_, err := doWithRetry(func() (*http.Request, error) {
		return http.NewRequest(http.MethodGet, server.URL, nil)
	})
	if err == nil {
		t.Fatal("expected an error after persistent 429s, got nil")
	}
	if attempts != 4 {
		t.Errorf("expected exactly 4 attempts before giving up, got %d", attempts)
	}
}
