import { useEffect, useRef, useState } from "react";
import { api } from "../api";
import { EventsOn, EventsOff } from "../../wailsjs/runtime/runtime";
import type { LogEntry } from "../types";

const MAX_LINES = 1000;

export function ServerLog() {
  const [lines, setLines] = useState<LogEntry[]>([]);
  const nextId = useRef(0);

  useEffect(() => {
    // Initial snapshot is oldest-first; reverse once so newest stays
    // on top, matching how live updates get prepended below.
    api.getServerLogs().then((initial) => {
      const reversed = [...initial].reverse();
      setLines(reversed.map((text) => ({ id: nextId.current++, text })));
    });

    const onLine = (text: string) => {
      setLines((prev) => [{ id: nextId.current++, text }, ...prev].slice(0, MAX_LINES));
    };
    EventsOn("server-log", onLine);
    return () => EventsOff("server-log");
  }, []);

  return (
    <div className="log-box">
      {lines.length === 0 && <div className="log-box__empty">No server log output yet.</div>}
      {lines.map((entry) => (
        <div key={entry.id}>{entry.text}</div>
      ))}
    </div>
  );
}
