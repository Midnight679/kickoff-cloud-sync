package accounts

import (
	"sync"
	"testing"
	"time"
)

// TestGroupTurn_OrdersDespiteOutOfOrderCompletion reproduces the bug
// startFinalize/finalizeReplay's groupTurn baton fixes: before it
// existed, assignReplayGroup ran in whichever order each match's
// (independently timed) GetReplayWithRetry call happened to finish,
// not the order the matches were actually discovered in — an
// unrelated match finishing first could reset the private-series
// streak before the real next match in the series arrived, silently
// splitting one series into two ballchasing groups.
//
// This drives the same grab-a-turn / wait-your-turn / release-the-next
// pattern startFinalize and finalizeReplay use, without going through
// real network calls: match A is "discovered" first (grabs the first
// turn) but its slow work finishes later; match B is discovered
// second but finishes its slow work first. The fix must still make B
// wait for A.
func TestGroupTurn_OrdersDespiteOutOfOrderCompletion(t *testing.T) {
	rt := &runtimeState{}
	grabTurn := func() (myTurn, nextTurn chan struct{}) {
		if rt.groupTurn == nil {
			rt.groupTurn = closedGroupTurn()
		}
		myTurn = rt.groupTurn
		nextTurn = make(chan struct{})
		rt.groupTurn = nextTurn
		return
	}

	myTurnA, nextTurnA := grabTurn() // match A discovered first
	myTurnB, nextTurnB := grabTurn() // match B discovered second

	var mu sync.Mutex
	var order []string
	record := func(name string) {
		mu.Lock()
		order = append(order, name)
		mu.Unlock()
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// B's "slow work" (standing in for GetReplayWithRetry) finishes
	// first, despite being discovered second.
	go func() {
		defer wg.Done()
		defer close(nextTurnB)
		<-myTurnB
		record("B")
	}()
	go func() {
		defer wg.Done()
		defer close(nextTurnA)
		time.Sleep(50 * time.Millisecond) // A's slow work finishes later
		<-myTurnA
		record("A")
	}()

	wg.Wait()

	if len(order) != 2 || order[0] != "A" || order[1] != "B" {
		t.Errorf("got order %v, want [A B] — B finishing its work first must not let it run before A", order)
	}
}
