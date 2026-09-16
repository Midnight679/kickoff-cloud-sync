import { useState } from "react";
import { SettingsBar } from "./SettingsBar";
import { EventLog } from "./EventLog";
import { ServerLog } from "./ServerLog";

type Tab = "event" | "server";

interface Props {
  eventLog: string[];
  onLog: (msg: string) => void;
  onBack: () => void;
}

export function SettingsView({ eventLog, onLog, onBack }: Props) {
  const [tab, setTab] = useState<Tab>("event");

  return (
    <div className="settings-view">
      <div className="settings-view__header">
        <button className="btn btn--icon" onClick={onBack} aria-label="Back to accounts">
          ←
        </button>
        <h2>Settings and logs</h2>
      </div>

      <SettingsBar onLog={onLog} />

      <div className="tab-bar">
        <button className={`tab ${tab === "event" ? "tab--active" : ""}`} onClick={() => setTab("event")}>
          Event log
        </button>
        <button className={`tab ${tab === "server" ? "tab--active" : ""}`} onClick={() => setTab("server")}>
          Server log
        </button>
      </div>

      <div className="settings-view__log">{tab === "event" ? <EventLog entries={eventLog} /> : <ServerLog />}</div>
    </div>
  );
}
