package auth

import (
	"context"
	"fmt"
	"strings"

	"github.com/dank/rlapi"

	"github.com/Midnight679/kickoff-cloud-sync/internal/httpclient"
)

// LoginResult bundles the authenticated PsyNet RPC connection with
// the refresh token and display name, so the caller can persist the
// token and show the name against a specific account entry. Epic's
// refresh tokens rotate on every use, so RefreshToken here always
// replaces whatever was previously stored — never reuse an old one.
type LoginResult struct {
	RPC          *rlapi.PsyNetRPC
	RefreshToken string
	DisplayName  string

	// AccountID is Epic's stable ID for the account that logged in.
	// Unlike DisplayName it never changes, so it is what identifies
	// "the same Epic account" across logins.
	AccountID string
}

// GetAuthURL returns the Epic Games Store login URL to open in the
// user's browser. There is no automatic callback for this flow —
// after logging in, Epic redirects to a page whose body is a small
// JSON blob containing an "authorizationCode" field. The user copies
// that code out and passes it to CompleteEpicLogin.
func GetAuthURL() string {
	return rlapi.NewEGS().GetAuthURL()
}

// CompleteEpicLogin exchanges a manually-copied authorization code
// (see GetAuthURL) for tokens, then authenticates against PsyNet to
// get a live match-history connection. This is step 2 of the
// add-account / reauth flow.
func CompleteEpicLogin(ctx context.Context, authCode string) (LoginResult, error) {
	return withTimeout(ctx, func() (LoginResult, error) {
		egs := rlapi.NewEGS()

		token, err := egs.AuthenticateWithCode(authCode)
		if err != nil {
			return LoginResult{}, fmt.Errorf("authenticating with code: %w", err)
		}

		return finishLogin(egs, token)
	})
}

// EpicLoginWithRefreshToken re-authenticates using a previously
// saved refresh token, avoiding a repeated browser login on every
// app start for an already-known account. The returned RefreshToken
// must be persisted in place of the old one — Epic rotates it on
// every use, so re-saving the same value here would break the next
// silent login.
func EpicLoginWithRefreshToken(ctx context.Context, refreshToken string) (LoginResult, error) {
	return withTimeout(ctx, func() (LoginResult, error) {
		egs := rlapi.NewEGS()

		token, err := egs.AuthenticateWithRefreshToken(refreshToken)
		if err != nil {
			return LoginResult{}, fmt.Errorf("authenticating with refresh token: %w", err)
		}

		return finishLogin(egs, token)
	})
}

// withTimeout runs fn (the actual EGS/PsyNet login chain) in its own
// goroutine, bounded by the user's configured network timeout layered
// onto ctx. rlapi's login calls take no context of their own
// (dank/rlapi#7 asks for this upstream) — without this, a hung
// network call anywhere in that chain blocks forever, and on the
// silent-reconnect path (ensureConnected) that stalls the whole shared
// poll cycle behind it, with the "Network timeout" setting not
// actually reaching this call at all.
//
// If the deadline wins the race, fn is left running in the
// background; its result is only used to close a PsyNet connection it
// might still go on to open, so a login that "answers late" doesn't
// leak one nothing will ever use.
func withTimeout(ctx context.Context, fn func() (LoginResult, error)) (LoginResult, error) {
	ctx, cancel := context.WithTimeout(ctx, httpclient.Client().Timeout)
	defer cancel()

	type outcome struct {
		result LoginResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := fn()
		done <- outcome{result, err}
	}()

	select {
	case o := <-done:
		return o.result, o.err
	case <-ctx.Done():
		go func() {
			if o := <-done; o.err == nil && o.result.RPC != nil {
				_ = o.result.RPC.Close()
			}
		}()
		return LoginResult{}, fmt.Errorf("logging in: %w", ctx.Err())
	}
}

// IsVersionMismatch reports whether err is Epic/PsyNet rejecting the
// game version dank/rlapi presents (see ARCHITECTURE.md's Epic
// authentication section) rather than a problem with this specific
// account or a network issue. rlapi doesn't export a typed sentinel
// for this — the server's own error type/message just flows through
// as plain text — so this matches on the substring PsyNet's response
// actually uses, with spaces stripped and case folded to catch it
// showing up in either the error's "Type" field (e.g. "VersionMismatch")
// or its "Message" field (e.g. "version mismatch") without depending
// on which one it lands in.
func IsVersionMismatch(err error) bool {
	if err == nil {
		return false
	}
	normalized := strings.ToLower(strings.ReplaceAll(err.Error(), " ", ""))
	return strings.Contains(normalized, "versionmismatch")
}

// finishLogin carries a successful EGS token (however it was
// obtained) the rest of the way: exchange for an EOS token, then
// authenticate against PsyNet to get a live WebSocket RPC connection.
func finishLogin(egs *rlapi.EGS, token *rlapi.TokenResponse) (LoginResult, error) {
	exchangeCode, err := egs.GetExchangeCode(token.AccessToken)
	if err != nil {
		return LoginResult{}, fmt.Errorf("getting exchange code: %w", err)
	}

	eosToken, err := egs.ExchangeEOSToken(exchangeCode)
	if err != nil {
		return LoginResult{}, fmt.Errorf("exchanging EOS token: %w", err)
	}

	psyNet := rlapi.NewPsyNet()
	rpc, err := psyNet.AuthPlayer(eosToken.AccessToken, eosToken.AccountID, token.DisplayName)
	if err != nil {
		return LoginResult{}, fmt.Errorf("authenticating with PsyNet: %w", err)
	}

	return LoginResult{
		RPC:          rpc,
		RefreshToken: token.RefreshToken,
		DisplayName:  token.DisplayName,
		AccountID:    token.AccountID,
	}, nil
}
