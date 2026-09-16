// Package httpclient provides one shared *http.Client used by every
// outbound call in this app (replay download, ballchasing upload,
// token validation), with a timeout that can be changed at runtime
// from the user-configurable setting in accounts.Manager.
//
// A plain package-level *http.Client with a mutable .Timeout field
// would be a data race if read (during a request) and written
// (via SetTimeout) concurrently. Using atomic.Pointer[http.Client]
// means SetTimeout swaps in a whole new client atomically — in-flight
// requests keep running against the client they started with, and
// new requests pick up the new timeout, with no locking needed on
// the read side.
package httpclient

import (
	"net/http"
	"sync/atomic"
	"time"
)

const DefaultTimeout = 30 * time.Second

var current atomic.Pointer[http.Client]

func init() {
	SetTimeout(DefaultTimeout)
}

// SetTimeout replaces the shared client with one using the given
// timeout. Call this once at startup with the persisted setting,
// and again whenever the user changes it.
func SetTimeout(d time.Duration) {
	current.Store(&http.Client{Timeout: d})
}

// Client returns the current shared *http.Client. Safe to call
// concurrently with SetTimeout.
func Client() *http.Client {
	return current.Load()
}
