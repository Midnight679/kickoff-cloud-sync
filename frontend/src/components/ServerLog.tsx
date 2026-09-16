import { useEffect, useState } from "react";
import { api } from "../api";
import { EventsOn, EventsOff } from "../../wailsjs/runtime/runtime";

const MAX_LINES = 1000;

export function ServerLog() {
  const [lines, setLines] = useState<string[]>([]);

  useEffect(() => {
    // Initial snapshot is oldest-first; reverse once so newest stays
    // on top, matching how live updates get prepended below.
    api.getServerLogs().then((initial) => setLines([...initial].reverse()));

    const onLine = (line: string) => {
      setLines((prev) => [line, ...prev].slice(0, MAX_LINES));
    };
    EventsOn("server-log", onLine);
    return () => EventsOff("server-log");
  }, []);

  return (
    <div className="log-box">
      {lines.length === 0 && <div className="log-box__empty">No server log output yet.</div>}
      {lines.map((line, i) => (
        <div key={i}>{line}</div>
      ))}
    </div>
  );
}
