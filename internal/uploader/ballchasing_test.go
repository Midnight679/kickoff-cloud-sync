package uploader

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

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
