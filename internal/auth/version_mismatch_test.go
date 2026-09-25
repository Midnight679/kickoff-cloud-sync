package auth

import (
	"errors"
	"fmt"
	"testing"
)

// TestIsVersionMismatch covers the actual error shapes this can show
// up wrapped in — finishLogin/CompleteEpicLogin/EpicLoginWithRefreshToken
// all wrap the underlying PsyNet error with additional fmt.Errorf
// layers, and the server's own error could plausibly put the phrase in
// either its "Type" field (no space, e.g. "VersionMismatch") or its
// "Message" field (spaced, e.g. "version mismatch") — this doesn't
// assume which.
func TestIsVersionMismatch(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"unrelated error", errors.New("network unreachable"), false},
		{
			"wrapped Type-style (no space)",
			fmt.Errorf("authenticating with PsyNet: %w", fmt.Errorf("failed to authenticate player: %w", errors.New("VersionMismatch: client is out of date"))),
			true,
		},
		{
			"wrapped Message-style (spaced, mixed case)",
			fmt.Errorf("login failed: %w", errors.New("InvalidRequest: Version Mismatch")),
			true,
		},
		{"unrelated auth rejection", errors.New("InvalidCredentials: bad auth ticket"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsVersionMismatch(tt.err); got != tt.want {
				t.Errorf("IsVersionMismatch(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
