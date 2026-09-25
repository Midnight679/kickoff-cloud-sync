import { useCallback, useEffect, useRef, useState } from "react";
import type { AccountView, EventPayload, LogEntry, UpdateInfo } from "./types";
import { api } from "./api";
import { EventsOn, EventsOff } from "../wailsjs/runtime/runtime";
import { AccountCard } from "./components/AccountCard";
import { AddAccountWizard } from "./components/AddAccountWizard";
import { ReauthDialog } from "./components/ReauthDialog";
import { SettingsView } from "./components/SettingsView";
import { VersionMismatchDialog } from "./components/VersionMismatchDialog";
import "./App.css";

const MAX_LOG_ENTRIES = 1000;

type Modal = { kind: "add" } | { kind: "reauth"; account: AccountView } | null;
type View = "accounts" | "settings";

export default function App() {
  const [accounts, setAccounts] = useState<AccountView[]>([]);
  const [modal, setModal] = useState<Modal>(null);
  const [log, setLog] = useState<LogEntry[]>([]);
  const nextLogId = useRef(0);
  const [view, setView] = useState<View>("accounts");
  const [updateInfo, setUpdateInfo] = useState<UpdateInfo | null>(null);
  const [dismissedVersion, setDismissedVersion] = useState<string | null>(null);
  const [version, setVersion] = useState("");
  const [versionMismatch, setVersionMismatch] = useState<string | null>(null);

  // Persistent until the account is actually reauthenticated — not
  // tied to any single event, so it stays correct across restarts and
  // regardless of which view is currently open.
  const needsAttention = accounts.some((a) => a.auth_status === "needs_reauth");

  const appendLog = useCallback((msg: string) => {
    const text = `[${new Date().toLocaleTimeString()}] ${msg}`;
    setLog((prev) => [{ id: nextLogId.current++, text }, ...prev].slice(0, MAX_LOG_ENTRIES));
  }, []);

  // Poll-triggered events all carry `manual` — surfacing it in the
  // message itself, since otherwise a scheduled poll's "no matches"
  // line looks identical to a manual one.
  const trigger = (p: EventPayload) => (p.manual ? "manual" : "scheduled");

  const refreshAccounts = useCallback(() => {
    api.listAccounts().then(setAccounts);
  }, []);

  useEffect(() => {
    api.getAppVersion().then(setVersion);
  }, []);

  useEffect(() => {
    // Covers the case where the startup check already finished by
    // the time this mounts; the event below covers the live update.
    api.getUpdateInfo().then((info) => info.available && setUpdateInfo(info));
    const onUpdateAvailable = (info: UpdateInfo) => setUpdateInfo(info);
    EventsOn("update-available", onUpdateAvailable);
    return () => EventsOff("update-available");
  }, []);

  useEffect(() => {
    refreshAccounts();

    const onMatch = (p: EventPayload) =>
      appendLog(`[${p.account_id}] match detected: ${p.match_id} (${trigger(p)})`);
    const onUploadOk = (p: EventPayload) =>
      appendLog(`[${p.account_id}] uploaded: ${p.message} (${trigger(p)})`);
    const onUploadErr = (p: EventPayload) =>
      appendLog(`[${p.account_id}] upload error: ${p.message} (${trigger(p)})`);
    const onAuthErr = (p: EventPayload) =>
      appendLog(`[${p.account_id}] auth error: ${p.message} (${trigger(p)})`);
    const onCacheCleared = (p: EventPayload) => appendLog(`[${p.account_id}] ${p.message} (${trigger(p)})`);
    const onNoMatches = (p: EventPayload) => appendLog(`[${p.account_id}] ${p.message} (${trigger(p)})`);
    const onReconnected = (p: EventPayload) => appendLog(`[${p.account_id}] ${p.message} (${trigger(p)})`);
    const onVersionMismatch = (p: EventPayload) => {
      appendLog(`Login failed — game version mismatch: ${p.message}`);
      setVersionMismatch(p.message ?? "");
    };

    EventsOn("accounts-changed", refreshAccounts);
    EventsOn("match-detected", onMatch);
    EventsOn("upload-complete", onUploadOk);
    EventsOn("upload-error", onUploadErr);
    EventsOn("auth-error", onAuthErr);
    EventsOn("cache-cleared", onCacheCleared);
    EventsOn("no-matches", onNoMatches);
    EventsOn("reconnected", onReconnected);
    EventsOn("version-mismatch", onVersionMismatch);

    return () => {
      EventsOff("accounts-changed");
      EventsOff("match-detected");
      EventsOff("upload-complete");
      EventsOff("upload-error");
      EventsOff("auth-error");
      EventsOff("cache-cleared");
      EventsOff("no-matches");
      EventsOff("reconnected");
      EventsOff("version-mismatch");
    };
  }, [refreshAccounts, appendLog]);

  return (
    <div className="app">
      <header className="app__header">
        <h1>Kickoff Cloud Sync</h1>
        <div className="app__header-actions">
          {view === "accounts" && (
            <button className="btn btn--primary" onClick={() => setModal({ kind: "add" })}>
              + Add Account
            </button>
          )}
          <button
            className={`btn btn--icon${needsAttention ? " btn--icon-error" : ""}`}
            onClick={() => setView(view === "accounts" ? "settings" : "accounts")}
            aria-label={needsAttention ? "Settings and logs — an account needs reauthentication" : "Settings and logs"}
            title={needsAttention ? "An account needs reauthentication" : "Settings and logs"}
          >
            ⚙
          </button>
        </div>
      </header>

      {updateInfo?.available && updateInfo.latest_version !== dismissedVersion && (
        <div className="update-banner">
          A new version ({updateInfo.latest_version}) is available.{" "}
          <a href={updateInfo.url} target="_blank" rel="noreferrer">
            View release
          </a>
          <button className="update-banner__link" onClick={() => setDismissedVersion(updateInfo.latest_version ?? null)}>
            Dismiss
          </button>
          <button
            className="update-banner__link"
            onClick={() => {
              const skipped = updateInfo.latest_version;
              setUpdateInfo(null);
              if (skipped) api.skipUpdateVersion(skipped);
            }}
          >
            Skip this version
          </button>
        </div>
      )}

      {view === "settings" ? (
        <SettingsView eventLog={log} onLog={appendLog} onBack={() => setView("accounts")} />
      ) : (
        <div className="account-list">
          {accounts.length === 0 && <p className="empty-state">No accounts yet — add one to get started.</p>}
          {accounts.map((acct) => (
            <AccountCard
              key={acct.id}
              account={acct}
              onChanged={refreshAccounts}
              onReauth={(account) => setModal({ kind: "reauth", account })}
              onLog={appendLog}
            />
          ))}
        </div>
      )}

      {modal?.kind === "add" && (
        <AddAccountWizard
          onDone={(name) => {
            appendLog(`Added account: ${name}`);
            setModal(null);
            refreshAccounts();
          }}
          onCancel={() => setModal(null)}
        />
      )}

      {modal?.kind === "reauth" && (
        <ReauthDialog
          account={modal.account}
          onDone={() => {
            appendLog(`Reauthenticated ${modal.account.display_name}.`);
            setModal(null);
            refreshAccounts();
          }}
          onCancel={() => setModal(null)}
        />
      )}

      {versionMismatch !== null && (
        <VersionMismatchDialog message={versionMismatch} onClose={() => setVersionMismatch(null)} />
      )}

      {version && (
        <a
          className="version-marker"
          href={`https://github.com/Midnight679/kickoff-cloud-sync/releases/tag/v${version}`}
          target="_blank"
          rel="noreferrer"
        >
          v{version}
        </a>
      )}
    </div>
  );
}
