interface Props {
  entries: string[];
}

export function EventLog({ entries }: Props) {
  return (
    <div className="event-log">
      {entries.map((line, i) => (
        <div key={i}>{line}</div>
      ))}
    </div>
  );
}
