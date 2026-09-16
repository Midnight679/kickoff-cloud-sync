interface Props {
  entries: string[];
}

export function EventLog({ entries }: Props) {
  return (
    <div className="log-box">
      {entries.length === 0 && <div className="log-box__empty">No events yet.</div>}
      {entries.map((line, i) => (
        <div key={i}>{line}</div>
      ))}
    </div>
  );
}
