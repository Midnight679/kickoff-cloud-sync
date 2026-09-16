import { useEffect, useState } from "react";
import { api, errorMessage } from "../api";

interface Props {
  onLog: (msg: string) => void;
}

export function SettingsBar({ onLog }: Props) {
  // The backend (GetPollIntervalSecs/SetPollIntervalSecs) always
  // deals in seconds — this field just displays/edits it in minutes,
  // converting at the boundary.
  const [pollIntervalMins, setPollIntervalMins] = useState<number | "">("");
  const [httpTimeout, setHttpTimeout] = useState<number | "">("");
  const [error, setError] = useState("");

  useEffect(() => {
    api.getPollIntervalSecs().then((secs) => setPollIntervalMins(Math.round(secs / 60)));
    api.getHttpTimeoutSecs().then(setHttpTimeout);
  }, []);

  async function savePollInterval() {
    if (pollIntervalMins === "") return;
    try {
      const secs = pollIntervalMins * 60;
      await api.setPollIntervalSecs(secs);
      onLog(`Poll interval set to ${pollIntervalMins} min.`);
    } catch (e) {
      setError(errorMessage(e));
    }
  }

  async function saveHttpTimeout() {
    if (httpTimeout === "") return;
    try {
      await api.setHttpTimeoutSecs(httpTimeout);
      onLog(`Network timeout set to ${httpTimeout}s.`);
    } catch (e) {
      setError(errorMessage(e));
    }
  }

  return (
    <div className="settings-bar">
      <div className="settings-bar__field">
        <label>Poll interval (mins)</label>
        <input
          className="input input--narrow"
          type="number"
          min={1}
          value={pollIntervalMins}
          onChange={(e) => setPollIntervalMins(e.target.value === "" ? "" : Number(e.target.value))}
        />
        <button className="btn" onClick={savePollInterval}>
          Save
        </button>
      </div>
      <div className="settings-bar__field">
        <label>Network timeout (secs)</label>
        <input
          className="input input--narrow"
          type="number"
          min={5}
          value={httpTimeout}
          onChange={(e) => setHttpTimeout(e.target.value === "" ? "" : Number(e.target.value))}
        />
        <button className="btn" onClick={saveHttpTimeout}>
          Save
        </button>
      </div>
      {error && <div className="error-text">{error}</div>}
    </div>
  );
}
