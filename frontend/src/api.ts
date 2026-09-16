// Thin typed wrapper around the bound Go methods on App (see
// ../../app.go). Written by hand against app.go's actual signatures
// rather than via wails codegen, so it stays exact regardless of
// whether `wails dev`/`wails build` has been run to regenerate
// frontend/wailsjs/go bindings — both call the same
// `window.go.main.App.*` object underneath.

import type { AccountView, PendingAccountView } from "./types";

function app() {
  const w = window as unknown as { go: { main: { App: Record<string, (...args: unknown[]) => Promise<unknown>> } } };
  if (!w.go) {
    throw new Error("Wails bridge not available — this only works inside the Wails app or its dev server.");
  }
  return w.go.main.App;
}

export const api = {
  listAccounts: () => app().ListAccounts() as Promise<AccountView[]>,

  beginAddAccount: () => app().BeginAddAccount() as Promise<string>,
  submitAddAccountCode: (authCode: string) =>
    app().SubmitAddAccountCode(authCode) as Promise<PendingAccountView>,
  confirmAddAccount: (pendingId: string, ballchasingToken: string, friendlyName: string) =>
    app().ConfirmAddAccount(pendingId, ballchasingToken, friendlyName) as Promise<AccountView>,
  cancelAddAccount: (pendingId: string) => app().CancelAddAccount(pendingId) as Promise<void>,

  beginReauth: (id: string) => app().BeginReauth(id) as Promise<string>,
  submitReauthCode: (id: string, authCode: string) =>
    app().SubmitReauthCode(id, authCode) as Promise<void>,
  cancelReauth: (id: string) => app().CancelReauth(id) as Promise<void>,

  pauseAccount: (id: string) => app().PauseAccount(id) as Promise<void>,
  resumeAccount: (id: string) => app().ResumeAccount(id) as Promise<void>,
  removeAccount: (id: string) => app().RemoveAccount(id) as Promise<void>,
  setAccountBallchasingToken: (id: string, token: string) =>
    app().SetAccountBallchasingToken(id, token) as Promise<void>,
  setFriendlyName: (id: string, name: string) => app().SetFriendlyName(id, name) as Promise<void>,
  pollAccountNow: (id: string) => app().PollAccountNow(id) as Promise<void>,

  getPollIntervalSecs: () => app().GetPollIntervalSecs() as Promise<number>,
  setPollIntervalSecs: (secs: number) => app().SetPollIntervalSecs(secs) as Promise<void>,
  getHttpTimeoutSecs: () => app().GetHTTPTimeoutSecs() as Promise<number>,
  setHttpTimeoutSecs: (secs: number) => app().SetHTTPTimeoutSecs(secs) as Promise<void>,
};

/** Extracts a readable message from whatever Wails hands back for a Go `error`. */
export function errorMessage(e: unknown): string {
  if (e instanceof Error) return e.message;
  if (typeof e === "string") return e;
  return String(e);
}
