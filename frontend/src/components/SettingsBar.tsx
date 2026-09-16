import { useEffect, useState } from "react";
import { api, errorMessage } from "../api";

interface Props {
  onLog: (msg: string) => void;
}

export function SettingsBar({ onLog }: Props) {
  const [pollInterval, setPollInterval] = useState<number | "">("");
  const [httpTimeout, setHttpTimeout] = useState<number | "">("");
  const [error, setError] = useState("");

  useEffect(() => {
    api.getPollIntervalSecs().then(setPollInterval);
    api.getHttpTimeoutSecs().then(setHttpTimeout);
  }, []);

  async function savePollInterval() {
    if (pollInterval === "") return;
    try {
      await api.setPollIntervalSecs(pollInterval);
      onLog(`Poll interval set to ${pollInterval}s.`);
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
        <label>Poll interval (secs)</label>
        <input
          className="input input--narrow"
          type="number"
          min={10}
          value={pollInterval}
          onChange={(e) => setPollInterval(e.target.value === "" ? "" : Number(e.target.value))}
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
