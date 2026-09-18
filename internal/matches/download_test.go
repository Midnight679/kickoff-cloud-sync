package matches

import (
	"bytes"
	"io"
	"testing"
)

// TestIsDisallowedReplayHost's DNS-name cases (api.rlpp.psynet.gg,
// ballchasing.com) exercise a real net.LookupHost call and so need
// working network/DNS access to pass — matching how api.rlpp.psynet.gg
// and ballchasing.com are expected to always resolve in practice, per
// isDisallowedReplayHost's own fail-closed behavior on a resolution
// failure (see the "unresolvable name" test below).
func TestIsDisallowedReplayHost(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"", true},
		{"localhost", true},
		{"LOCALHOST", true},
		{"127.0.0.1", true},
		{"127.0.0.2", true}, // not the one literal spelling the old blocklist caught
		{"0.0.0.0", true},
		{"::1", true},
		{"10.0.0.5", true},
		{"172.16.0.1", true},
		{"192.168.1.1", true},
		{"169.254.1.1", true},
		{"fe80::1", true},
		{"api.rlpp.psynet.gg", false},
		{"ballchasing.com", false},
		{"8.8.8.8", false},
	}
	for _, c := range cases {
		if got := isDisallowedReplayHost(c.host); got != c.want {
			t.Errorf("isDisallowedReplayHost(%q) = %v, want %v", c.host, got, c.want)
		}
	}
}

// TestIsDisallowedReplayHost_UnresolvableNameFailsClosed reproduces
// the DNS-rebinding gap: a hostname (as opposed to an address literal)
// used to be allowed through unconditionally, without ever being
// resolved. .invalid is reserved by RFC 2606 specifically for names
// guaranteed to never resolve, so this is deterministic regardless of
// real DNS state — a resolution failure must fail closed (disallowed),
// not sail through as "not a literal, so nothing to check."
func TestIsDisallowedReplayHost_UnresolvableNameFailsClosed(t *testing.T) {
	if !isDisallowedReplayHost("this-should-never-exist.invalid") {
		t.Error("a hostname that fails to resolve should be treated as disallowed, not allowed through unchecked")
	}
}

func TestValidateReplayURL(t *testing.T) {
	cases := []struct {
		url     string
		wantErr bool
	}{
		{"http://api.rlpp.psynet.gg/replay/1", false},
		{"https://api.rlpp.psynet.gg/replay/1", false},
		{"ftp://api.rlpp.psynet.gg/replay/1", true},
		{"http://localhost/replay/1", true},
		{"http://127.0.0.2/replay/1", true},
		{"http://192.168.1.1/replay/1", true},
		{"not a url", true},
	}
	for _, c := range cases {
		err := validateReplayURL(c.url)
		if (err != nil) != c.wantErr {
			t.Errorf("validateReplayURL(%q) error = %v, wantErr %v", c.url, err, c.wantErr)
		}
	}
}

func TestCopyCapped_AllowsUnderCap(t *testing.T) {
	data := bytes.Repeat([]byte{1}, 1024)
	var buf bytes.Buffer
	if err := copyCapped(&buf, bytes.NewReader(data)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), data) {
		t.Error("copied data doesn't match the source")
	}
}

func TestCopyCapped_RejectsOversized(t *testing.T) {
	var buf bytes.Buffer
	src := io.LimitReader(zeroes{}, maxReplayBytes+1)
	if err := copyCapped(&buf, src); err == nil {
		t.Fatal("expected an error for a source over the cap")
	}
}

// zeroes is an infinite source of zero bytes, for exercising
// copyCapped without actually allocating a maxReplayBytes-sized slice.
type zeroes struct{}

func (zeroes) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}
