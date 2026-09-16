import { useState } from "react";
import type { AccountView } from "../types";
import { api, errorMessage } from "../api";

const STATUS_LABEL: Record<AccountView["auth_status"], string> = {
  authenticated: "Authenticated",
  needs_reauth: "Needs reauth",
  authenticating: "Authenticating…",
};

function fmtTime(iso?: string): string {
  return iso ? new Date(iso).toLocaleTimeString() : "—";
}

function fmtName(a: AccountView): string {
  return a.friendly_name ? `${a.display_name} (${a.friendly_name})` : a.display_name;
}

interface Props {
  account: AccountView;
  onChanged: () => void;
  onReauth: (account: AccountView) => void;
  onLog: (msg: string) => void;
}

export function AccountCard({ account, onChanged, onReauth, onLog }: Props) {
  // Local, not derived from props on every render — so a poll-driven
  // accounts-changed refresh (which happens often) never wipes out
  // whatever the user is mid-typing here.
  const [token, setToken] = useState("");
  const [friendlyName, setFriendlyName] = useState(account.friendly_name ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function run(action: () => Promise<void>, label: string) {
    setBusy(true);
    setError("");
    try {
      await action();
      onChanged();
    } catch (e) {
      const msg = errorMessage(e);
      setError(msg);
      onLog(`[${fmtName(account)}] ${label} failed: ${msg}`);
    } finally {
      setBusy(false);
    }
  }

  async function saveToken() {
    if (!token) return;
    await run(async () => {
      await api.setAccountBallchasingToken(account.id, token);
      onLog(`[${fmtName(account)}] ballchasing token saved.`);
      setToken("");
    }, "saving token");
  }

  async function saveFriendlyName() {
    await run(async () => {
      await api.setFriendlyName(account.id, friendlyName);
    }, "saving friendly name");
  }

  async function pollNow() {
    // Log before awaiting, not after — PollAccountNow blocks on the Go
    // side until the whole poll finishes, so its outcome event (e.g.
    // "poll succeeded") can otherwise reach the log before this line
    // does, making a manual poll look like it started after it ended.
    onLog(`[${fmtName(account)}] manual poll triggered.`);
    await run(async () => {
      await api.pollAccountNow(account.id);
    }, "polling");
  }

  async function togglePause() {
    await run(async () => {
      if (account.paused) await api.resumeAccount(account.id);
      else await api.pauseAccount(account.id);
    }, account.paused ? "resuming" : "pausing");
  }

  async function remove() {
    if (!window.confirm(`Remove ${fmtName(account)}? This deletes its stored credentials too.`)) return;
    await run(async () => {
      await api.removeAccount(account.id);
      onLog(`Removed account: ${fmtName(account)}`);
    }, "removing");
  }

  return (
    <div className="account-card">
      <div className="account-card__header">
        <h3>{fmtName(account)}</h3>
        <span className={`status-pill status-pill--${account.auth_status}`}>
          {STATUS_LABEL[account.auth_status]}
        </span>
      </div>

      {account.auth_status !== "authenticated" && (
        <button className="btn btn--warn" disabled={busy} onClick={() => onReauth(account)}>
          Reauthenticate
        </button>
      )}

      <div className="account-card__row account-card__meta">
        <span>Last poll: {fmtTime(account.last_poll_time)}</span>
        <span>Next poll: {account.paused ? "paused" : fmtTime(account.next_poll_time)}</span>
      </div>

      <div className="account-card__row">
        <input
          className="input"
          placeholder="Friendly name"
          value={friendlyName}
          onChange={(e) => setFriendlyName(e.target.value)}
          onBlur={saveFriendlyName}
          disabled={busy}
        />
      </div>

      <div className="account-card__row">
        <input
          className="input"
          type="password"
          placeholder={account.has_token ? "•••••••• (token set)" : "ballchasing.com API token"}
          value={token}
          onChange={(e) => setToken(e.target.value)}
          disabled={busy}
        />
        <button className="btn" disabled={busy || !token} onClick={saveToken}>
          Save
        </button>
      </div>

      {error && <div className="error-text">{error}</div>}

      <div className="account-card__row account-card__actions">
        <button className="btn" disabled={busy} onClick={pollNow}>
          Poll Now
        </button>
        <button className="btn" disabled={busy} onClick={togglePause}>
          {account.paused ? "Resume" : "Pause"}
        </button>
        <button className="btn btn--danger" disabled={busy} onClick={remove}>
          Remove
        </button>
      </div>
    </div>
  );
}
