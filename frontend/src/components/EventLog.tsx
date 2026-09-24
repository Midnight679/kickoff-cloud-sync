import type { LogEntry } from "../types";

interface Props {
  entries: LogEntry[];
}

export function EventLog({ entries }: Props) {
  return (
    <div className="log-box">
      {entries.length === 0 && <div className="log-box__empty">No events yet.</div>}
      {entries.map((entry) => (
        <div key={entry.id}>{entry.text}</div>
      ))}
    </div>
  );
}
