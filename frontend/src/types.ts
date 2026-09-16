// Mirrors the Go structs in internal/accounts (AccountView,
// PendingAccountView) and the App.go event payload. Kept by hand
// rather than via wails codegen since app.go's actual shape is the
// source of truth here.

export type AuthStatus = "authenticated" | "needs_reauth" | "authenticating";

export interface AccountView {
  id: string;
  display_name: string;
  friendly_name?: string;
  auth_status: AuthStatus;
  paused: boolean;
  last_poll_time?: string;
  next_poll_time?: string;
  has_token: boolean;
}

export interface PendingAccountView {
  pending_id: string;
  epic_display_name: string;
}

export interface EventPayload {
  account_id: string;
  match_id?: string;
  message?: string;
  // true if this event resulted from an explicit "Poll Now" click
  // rather than the shared scheduled cycle.
  manual?: boolean;
}
