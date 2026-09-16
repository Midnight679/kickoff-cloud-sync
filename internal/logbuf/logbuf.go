// Package logbuf captures everything written to Go's standard log
// package into an in-memory ring buffer, so the frontend can show
// real server-side log output (network errors, retry failures, etc.)
// without touching any of the existing log.Printf call sites — wire
// it in once via log.SetOutput in main.go.
package logbuf

import (
	"io"
	"strings"
	"sync"
)

const maxLines = 1000

var (
	mu      sync.Mutex
	lines   []string
	onWrite func(line string)
)

type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// Writer returns an io.Writer suitable for combining with the
// process's real stderr via io.MultiWriter and installing with
// log.SetOutput.
func Writer() io.Writer {
	return writerFunc(write)
}

func write(p []byte) (int, error) {
	line := strings.TrimRight(string(p), "\n")

	mu.Lock()
	lines = append(lines, line)
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	cb := onWrite
	mu.Unlock()

	if cb != nil {
		cb(line)
	}
	return len(p), nil
}

// OnWrite registers a callback invoked with each new line as it's
// written. Intended to be called once, from App.startup, to bridge
// into a Wails runtime event for live updates.
func OnWrite(fn func(line string)) {
	mu.Lock()
	onWrite = fn
	mu.Unlock()
}

// Lines returns a snapshot of everything currently buffered, oldest
// first.
func Lines() []string {
	mu.Lock()
	defer mu.Unlock()
	out := make([]string, len(lines))
	copy(out, lines)
	return out
}
