package matches

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dank/rlapi"

	"github.com/Midnight679/kickoff-cloud-sync/internal/httpclient"
)

type fakeRPC func(ctx context.Context) ([]rlapi.MatchEntry, error)

func (f fakeRPC) GetMatchHistory(ctx context.Context) ([]rlapi.MatchEntry, error) { return f(ctx) }

// rlapi v0.1.23 dereferences a nil response when the socket closes
// with a request outstanding. PollOnce must turn that into an error,
// not let it end the process.
func TestPollOnceRecoversFromRLAPIPanic(t *testing.T) {
	rpc := fakeRPC(func(ctx context.Context) ([]rlapi.MatchEntry, error) {
		var resp *rlapi.PsyResponse
		_ = resp.Error // the same nil dereference as rlapi's awaitResponse
		return nil, nil
	})

	called := false
	count, err := PollOnce(context.Background(), rpc, func(string, string) { called = true })
	if err == nil {
		t.Fatal("expected an error")
	}
	if count != 0 || called {
		t.Errorf("no matches should be reported after a failed fetch (count=%d, called=%v)", count, called)
	}
}

// A request the server never answers must end at the network timeout
// even though the caller's ctx is never cancelled.
func TestPollOnceAppliesDeadline(t *testing.T) {
	httpclient.SetTimeout(50 * time.Millisecond)
	defer httpclient.SetTimeout(httpclient.DefaultTimeout)

	rpc := fakeRPC(func(ctx context.Context) ([]rlapi.MatchEntry, error) {
		<-ctx.Done() // blocks forever with the old code
		return nil, ctx.Err()
	})

	done := make(chan error, 1)
	go func() {
		_, err := PollOnce(context.Background(), rpc, func(string, string) {})
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("got %v, want context.DeadlineExceeded", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("PollOnce did not return; no deadline was applied")
	}
}

func TestPollOnceReportsEveryMatch(t *testing.T) {
	rpc := fakeRPC(func(ctx context.Context) ([]rlapi.MatchEntry, error) {
		return []rlapi.MatchEntry{
			{ReplayUrl: "http://example.com/1", Match: rlapi.Match{MatchGUID: "A"}},
			{ReplayUrl: "http://example.com/2", Match: rlapi.Match{MatchGUID: "B"}},
		}, nil
	})
	var seen []string
	count, err := PollOnce(context.Background(), rpc, func(id, _ string) { seen = append(seen, id) })
	if err != nil || count != 2 || len(seen) != 2 || seen[0] != "A" || seen[1] != "B" {
		t.Errorf("count=%d err=%v seen=%v", count, err, seen)
	}
}
