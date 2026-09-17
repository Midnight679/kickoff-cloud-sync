package auth

import (
	"context"
	"testing"
	"time"

	"github.com/Midnight679/kickoff-cloud-sync/internal/httpclient"
)

func TestWithTimeout_ReturnsFnResultWhenFast(t *testing.T) {
	want := LoginResult{DisplayName: "Player", AccountID: "abc"}
	got, err := withTimeout(context.Background(), func() (LoginResult, error) {
		return want, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// TestWithTimeout_TimesOutOnHang verifies a login call that never
// returns (the real-world case: rlapi's calls take no context, so a
// hung network request can't be cancelled directly) still surfaces as
// an error within the configured network timeout, rather than
// blocking forever.
func TestWithTimeout_TimesOutOnHang(t *testing.T) {
	orig := httpclient.Client().Timeout
	httpclient.SetTimeout(30 * time.Millisecond)
	defer httpclient.SetTimeout(orig)

	hang := make(chan struct{})
	defer close(hang) // let the background goroutine finish so the test doesn't leak it

	start := time.Now()
	_, err := withTimeout(context.Background(), func() (LoginResult, error) {
		<-hang
		return LoginResult{}, nil
	})
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("withTimeout took %v to return, expected it to respect the ~30ms timeout", elapsed)
	}
}

// TestWithTimeout_ParentCancellationAlsoTimesOut verifies the caller's
// own ctx cancellation (not just the internally-derived deadline) is
// still respected.
func TestWithTimeout_ParentCancellationAlsoTimesOut(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	hang := make(chan struct{})
	defer close(hang)

	done := make(chan error, 1)
	go func() {
		_, err := withTimeout(ctx, func() (LoginResult, error) {
			<-hang
			return LoginResult{}, nil
		})
		done <- err
	}()

	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error after parent cancellation")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("withTimeout did not return after parent ctx was cancelled")
	}
}
