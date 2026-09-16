package auth

import (
	"context"
	"fmt"

	"github.com/dank/rlapi"
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
	egs := rlapi.NewEGS()

	token, err := egs.AuthenticateWithCode(authCode)
	if err != nil {
		return LoginResult{}, fmt.Errorf("authenticating with code: %w", err)
	}

	return finishLogin(egs, token)
}

// EpicLoginWithRefreshToken re-authenticates using a previously
// saved refresh token, avoiding a repeated browser login on every
// app start for an already-known account. The returned RefreshToken
// must be persisted in place of the old one — Epic rotates it on
// every use, so re-saving the same value here would break the next
// silent login.
func EpicLoginWithRefreshToken(ctx context.Context, refreshToken string) (LoginResult, error) {
	egs := rlapi.NewEGS()

	token, err := egs.AuthenticateWithRefreshToken(refreshToken)
	if err != nil {
		return LoginResult{}, fmt.Errorf("authenticating with refresh token: %w", err)
	}

	return finishLogin(egs, token)
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
	}, nil
}
