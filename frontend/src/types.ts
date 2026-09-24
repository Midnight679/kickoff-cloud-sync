// Mirrors the Go structs in internal/accounts (AccountView,
// PendingAccountView) and the App.go event payload. Kept by hand
// rather than via wails codegen since app.go's actual shape is the
// source of truth here.

export type AuthStatus = "authenticated" | "needs_reauth" | "authenticating";

export type ReplayVisibility = "public" | "unlisted" | "private";

export interface PollResult {
  found: number;
  uploaded: number;
  failed: number;
}

export interface AccountView {
  id: string;
  display_name: string;
  friendly_name?: string;
  auth_status: AuthStatus;
  paused: boolean;
  last_poll_time?: string;
  next_poll_time?: string;
  has_token: boolean;
  replay_visibility: ReplayVisibility;
  // Only this account's most recently completed poll — not history,
  // overwritten every cycle. Absent until the first poll this run.
  last_poll?: PollResult;
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

export interface UpdateInfo {
  available: boolean;
  current_version: string;
  latest_version?: string;
  url?: string;
}

export interface RetryFailedUploadsResult {
  attempted: number;
  succeeded: number;
}

export interface ImportSettingsResult {
  accounts_matched: number;
  accounts_skipped: number;
}

// A single line in the event log or server log. id is a stable,
// monotonically increasing identity assigned once when the line is
// appended — used as the React key instead of array index, since both
// logs prepend new lines (shifting every existing entry's index) and
// an index key would make React rewrite every rendered line's text on
// every single new entry instead of just inserting the new one.
export interface LogEntry {
  id: number;
  text: string;
}
