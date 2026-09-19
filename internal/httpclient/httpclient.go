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

// sharedTransport backs every client SetTimeout creates, so connection
// pooling persists across a timeout change instead of each new
// *http.Client starting from an empty pool. idleConnTimeout is set
// well under Go's own 90-second default: real-world reverse proxies
// and CDNs (ballchasing.com's included, confirmed by "connection
// forcibly closed by the remote host" errors on reuse in practice)
// commonly close an idle keep-alive connection sooner than that. If
// the server closes a connection first, the client only finds out when
// it tries to reuse it — which fails outright for a non-idempotent
// request like the uploads this app makes, since Go won't silently
// retry those on its own. Closing idle connections proactively, before
// the server does, avoids that race in the first place; doWithRetry in
// internal/uploader is the backstop for whenever it still happens
// (e.g. if the real server-side timeout turns out to be even shorter).
const idleConnTimeout = 30 * time.Second

var sharedTransport = &http.Transport{
	IdleConnTimeout: idleConnTimeout,
}

var current atomic.Pointer[http.Client]

func init() {
	SetTimeout(DefaultTimeout)
}

// SetTimeout replaces the shared client with one using the given
// timeout. Call this once at startup with the persisted setting,
// and again whenever the user changes it.
func SetTimeout(d time.Duration) {
	current.Store(&http.Client{Timeout: d, Transport: sharedTransport})
}

// Client returns the current shared *http.Client. Safe to call
// concurrently with SetTimeout.
func Client() *http.Client {
	return current.Load()
}
