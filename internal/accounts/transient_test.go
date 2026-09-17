package accounts

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"testing"
)

// The error chains below mirror what actually reaches ensureConnected:
// rlapi wraps the transport error with %w, then internal/auth wraps
// that again.
func TestIsTransientNetErr(t *testing.T) {
	wrap := func(inner error) error {
		rlapiErr := fmt.Errorf("failed to send request: %w", inner)
		return fmt.Errorf("authenticating with refresh token: %w", rlapiErr)
	}

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "http client error (offline, reset, timeout)",
			err:  wrap(&url.Error{Op: "Post", URL: "https://example.invalid", Err: errors.New("connection reset")}),
			want: true,
		},
		{
			name: "dns failure",
			err:  wrap(&net.DNSError{Err: "no such host", Name: "example.invalid", IsNotFound: true}),
			want: true,
		},
		{
			name: "websocket dial failure",
			err:  wrap(&net.OpError{Op: "dial", Net: "tcp", Err: errors.New("network is unreachable")}),
			want: true,
		},
		{
			name: "epic rejected the refresh token",
			err:  wrap(errors.New("authentication failed: 400, errors.com.epicgames.account.auth_token.invalid_refresh_token - Sorry the refresh token is invalid")),
			want: false,
		},
	}
	for _, tc := range cases {
		if got := isTransientNetErr(tc.err); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}
