import { useState } from "react";
import { api, errorMessage } from "../api";

interface Props {
  message: string;
  onClose: () => void;
}

const NEW_ISSUE_URL =
  "https://github.com/Midnight679/kickoff-cloud-sync/issues/new?title=" +
  encodeURIComponent("Login fails with a version mismatch");

export function VersionMismatchDialog({ message, onClose }: Props) {
  const [checking, setChecking] = useState(false);
  const [checkResult, setCheckResult] = useState("");

  async function checkForUpdate() {
    setChecking(true);
    setCheckResult("");
    try {
      const info = await api.checkForUpdateNow();
      setCheckResult(
        info.available
          ? `Update available: ${info.latest_version} — see the banner above to view it.`
          : `You're already on the latest release (${info.current_version}). A fix may not be out yet — try again shortly, or open an issue below.`,
      );
    } catch (e) {
      setCheckResult(errorMessage(e));
    } finally {
      setChecking(false);
    }
  }

  return (
    <div className="modal-backdrop">
      <div className="modal">
        <h3>Can't log in — game version mismatch</h3>
        <p>
          Rocket League has updated since this app's last release, and Epic's servers are
          rejecting the now-outdated game version this app presents. This isn't specific to your
          account — no login or reauth will work until a new release fixes it, which usually
          follows within a day or two of a game patch.
        </p>
        <div className="modal__row">
          <button className="btn btn--primary" disabled={checking} onClick={checkForUpdate}>
            {checking ? "Checking…" : "Check for updates"}
          </button>
          <a className="btn" href={NEW_ISSUE_URL} target="_blank" rel="noreferrer">
            Open an issue on GitHub
          </a>
        </div>
        {checkResult && <p>{checkResult}</p>}
        <details className="modal__row">
          <summary>Technical details</summary>
          <p className="error-text">{message}</p>
        </details>
        <div className="modal__row">
          <button className="btn" onClick={onClose}>
            Dismiss
          </button>
        </div>
      </div>
    </div>
  );
}
