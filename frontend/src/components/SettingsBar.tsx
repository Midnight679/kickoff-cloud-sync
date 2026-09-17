import { useEffect, useState } from "react";
import { api, errorMessage } from "../api";

interface Props {
  onLog: (msg: string) => void;
}

/** Formats an hour (0-23) as a 12-hour clock label, e.g. 4 -> "4:00 AM". */
function formatHour(hour: number): string {
  const period = hour < 12 ? "AM" : "PM";
  const twelveHour = hour % 12 === 0 ? 12 : hour % 12;
  return `${twelveHour}:00 ${period}`;
}

const HOURS = Array.from({ length: 24 }, (_, h) => h);

export function SettingsBar({ onLog }: Props) {
  // The backend (GetPollIntervalSecs/SetPollIntervalSecs) always
  // deals in seconds — this field just displays/edits it in minutes,
  // converting at the boundary.
  const [pollIntervalMins, setPollIntervalMins] = useState<number | "">("");
  const [httpTimeout, setHttpTimeout] = useState<number | "">("");
  const [launchAtLogin, setLaunchAtLogin] = useState(false);
  const [groupPrivateSeries, setGroupPrivateSeries] = useState(true);
  const [failedUploadsRetryHour, setFailedUploadsRetryHour] = useState(4);
  const [failedUploadsMaxCount, setFailedUploadsMaxCount] = useState<number | "">("");
  const [retryingFailedUploads, setRetryingFailedUploads] = useState(false);
  const [error, setError] = useState("");
  const [checkingUpdate, setCheckingUpdate] = useState(false);
  const [updateCheckResult, setUpdateCheckResult] = useState("");

  useEffect(() => {
    api.getPollIntervalSecs().then((secs) => setPollIntervalMins(Math.round(secs / 60)));
    api.getHttpTimeoutSecs().then(setHttpTimeout);
    api.getLaunchAtLogin().then(setLaunchAtLogin);
    api.getGroupPrivateSeriesEnabled().then(setGroupPrivateSeries);
    api.getFailedUploadsRetryHour().then(setFailedUploadsRetryHour);
    api.getFailedUploadsMaxCount().then(setFailedUploadsMaxCount);
  }, []);

  async function toggleLaunchAtLogin(checked: boolean) {
    // Optimistic — flip immediately, then confirm against the
    // registry (the actual source of truth) so the UI can't show a
    // state that isn't real if the write fails.
    setLaunchAtLogin(checked);
    try {
      await api.setLaunchAtLogin(checked);
      onLog(checked ? "Will launch automatically at Windows login." : "Removed from Windows startup.");
    } catch (e) {
      setError(errorMessage(e));
      api.getLaunchAtLogin().then(setLaunchAtLogin);
    }
  }

  async function toggleGroupPrivateSeries(checked: boolean) {
    setGroupPrivateSeries(checked);
    try {
      await api.setGroupPrivateSeriesEnabled(checked);
      onLog(checked ? "Private match series will be grouped on ballchasing.com." : "Private match series grouping turned off.");
    } catch (e) {
      setError(errorMessage(e));
      api.getGroupPrivateSeriesEnabled().then(setGroupPrivateSeries);
    }
  }

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

  async function saveFailedUploadsRetryHour(hour: number) {
    setFailedUploadsRetryHour(hour);
    try {
      await api.setFailedUploadsRetryHour(hour);
      onLog(`Failed uploads will be retried daily at ${formatHour(hour)}.`);
    } catch (e) {
      setError(errorMessage(e));
      api.getFailedUploadsRetryHour().then(setFailedUploadsRetryHour);
    }
  }

  async function saveFailedUploadsMaxCount() {
    if (failedUploadsMaxCount === "") return;
    try {
      await api.setFailedUploadsMaxCount(failedUploadsMaxCount);
      onLog(`Failed uploads kept capped at ${failedUploadsMaxCount}.`);
    } catch (e) {
      setError(errorMessage(e));
    }
  }

  async function retryFailedUploadsNow() {
    setRetryingFailedUploads(true);
    try {
      const result = await api.retryFailedUploadsNow();
      onLog(
        result.attempted === 0
          ? "No failed uploads to retry."
          : `Retried ${result.attempted} failed upload(s): ${result.succeeded} succeeded.`,
      );
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setRetryingFailedUploads(false);
    }
  }

  async function checkForUpdate() {
    setCheckingUpdate(true);
    setUpdateCheckResult("");
    try {
      const info = await api.checkForUpdateNow();
      setUpdateCheckResult(
        info.available ? `Update available: ${info.latest_version}` : `You're on the latest version (${info.current_version}).`,
      );
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setCheckingUpdate(false);
    }
  }

  return (
    <div className="settings-bar">
      <div className="settings-bar__field">
        <label>Poll interval (mins)</label>
        <input
          className="input input--narrow"
          type="number"
          min={10}
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
      <label className="settings-bar__field settings-bar__checkbox">
        <input
          type="checkbox"
          checked={launchAtLogin}
          onChange={(e) => toggleLaunchAtLogin(e.target.checked)}
        />
        Launch at Windows startup
      </label>
      <label className="settings-bar__field settings-bar__checkbox">
        <input
          type="checkbox"
          checked={groupPrivateSeries}
          onChange={(e) => toggleGroupPrivateSeries(e.target.checked)}
        />
        Group private match series on ballchasing.com
      </label>
      <div className="settings-bar__field">
        <label>Retry failed uploads at</label>
        <select
          className="input input--narrow"
          value={failedUploadsRetryHour}
          onChange={(e) => saveFailedUploadsRetryHour(Number(e.target.value))}
        >
          {HOURS.map((h) => (
            <option key={h} value={h}>
              {formatHour(h)}
            </option>
          ))}
        </select>
      </div>
      <div className="settings-bar__field">
        <label>Failed uploads kept</label>
        <input
          className="input input--narrow"
          type="number"
          min={1}
          value={failedUploadsMaxCount}
          onChange={(e) => setFailedUploadsMaxCount(e.target.value === "" ? "" : Number(e.target.value))}
        />
        <button className="btn" onClick={saveFailedUploadsMaxCount}>
          Save
        </button>
      </div>
      <div className="settings-bar__field">
        <button className="btn" disabled={retryingFailedUploads} onClick={retryFailedUploadsNow}>
          {retryingFailedUploads ? "Retrying…" : "Retry failed uploads now"}
        </button>
      </div>
      <div className="settings-bar__field">
        <button className="btn" disabled={checkingUpdate} onClick={checkForUpdate}>
          {checkingUpdate ? "Checking…" : "Check for updates"}
        </button>
        {updateCheckResult && <span>{updateCheckResult}</span>}
      </div>
      {error && <div className="error-text">{error}</div>}
    </div>
  );
}
