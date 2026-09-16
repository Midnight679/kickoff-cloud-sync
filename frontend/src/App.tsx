import { useCallback, useEffect, useState } from "react";
import type { AccountView, EventPayload } from "./types";
import { api } from "./api";
import { EventsOn, EventsOff } from "../wailsjs/runtime/runtime";
import { AccountCard } from "./components/AccountCard";
import { AddAccountWizard } from "./components/AddAccountWizard";
import { ReauthDialog } from "./components/ReauthDialog";
import { SettingsBar } from "./components/SettingsBar";
import { EventLog } from "./components/EventLog";
import "./App.css";

const MAX_LOG_ENTRIES = 200;

type Modal = { kind: "add" } | { kind: "reauth"; account: AccountView } | null;

export default function App() {
  const [accounts, setAccounts] = useState<AccountView[]>([]);
  const [modal, setModal] = useState<Modal>(null);
  const [log, setLog] = useState<string[]>([]);

  const appendLog = useCallback((msg: string) => {
    const line = `[${new Date().toLocaleTimeString()}] ${msg}`;
    setLog((prev) => [line, ...prev].slice(0, MAX_LOG_ENTRIES));
  }, []);

  const refreshAccounts = useCallback(() => {
    api.listAccounts().then(setAccounts);
  }, []);

  useEffect(() => {
    refreshAccounts();

    const onMatch = (p: EventPayload) => appendLog(`[${p.account_id}] match detected: ${p.match_id}`);
    const onUploadOk = (p: EventPayload) => appendLog(`[${p.account_id}] uploaded: ${p.message}`);
    const onUploadErr = (p: EventPayload) => appendLog(`[${p.account_id}] upload error: ${p.message}`);
    const onAuthErr = (p: EventPayload) => appendLog(`[${p.account_id}] auth error: ${p.message}`);
    const onCacheCleared = (p: EventPayload) => appendLog(`[${p.account_id}] ${p.message}`);

    EventsOn("accounts-changed", refreshAccounts);
    EventsOn("match-detected", onMatch);
    EventsOn("upload-complete", onUploadOk);
    EventsOn("upload-error", onUploadErr);
    EventsOn("auth-error", onAuthErr);
    EventsOn("cache-cleared", onCacheCleared);

    return () => {
      EventsOff("accounts-changed");
      EventsOff("match-detected");
      EventsOff("upload-complete");
      EventsOff("upload-error");
      EventsOff("auth-error");
      EventsOff("cache-cleared");
    };
  }, [refreshAccounts, appendLog]);

  return (
    <div className="app">
      <header className="app__header">
        <h1>Kickoff Cloud Sync</h1>
        <button className="btn btn--primary" onClick={() => setModal({ kind: "add" })}>
          + Add Account
        </button>
      </header>

      <SettingsBar onLog={appendLog} />

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

      <EventLog entries={log} />

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
    </div>
  );
}
