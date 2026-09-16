import { useState } from "react";
import { api, errorMessage } from "../api";

type Step =
  | { kind: "login"; url?: string }
  | { kind: "code"; url: string }
  | { kind: "confirm"; pendingId: string; epicDisplayName: string };

interface Props {
  onDone: (addedName: string) => void;
  onCancel: () => void;
}

export function AddAccountWizard({ onDone, onCancel }: Props) {
  const [step, setStep] = useState<Step>({ kind: "login" });
  const [authCode, setAuthCode] = useState("");
  const [token, setToken] = useState("");
  const [friendlyName, setFriendlyName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function openLogin() {
    setBusy(true);
    setError("");
    try {
      const url = await api.beginAddAccount();
      setStep({ kind: "code", url });
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }

  async function submitCode() {
    if (step.kind !== "code" || !authCode.trim()) return;
    setBusy(true);
    setError("");
    try {
      const pending = await api.submitAddAccountCode(authCode.trim());
      setStep({ kind: "confirm", pendingId: pending.pending_id, epicDisplayName: pending.epic_display_name });
    } catch (e) {
      // The auth code is single-use — on failure the whole login has
      // to restart, so send them back to step 1 rather than letting
      // them retry the same (now-consumed) code.
      setError(`${errorMessage(e)} — start over with a fresh login.`);
      setStep({ kind: "login" });
      setAuthCode("");
    } finally {
      setBusy(false);
    }
  }

  async function confirm() {
    if (step.kind !== "confirm") return;
    setBusy(true);
    setError("");
    try {
      await api.confirmAddAccount(step.pendingId, token, friendlyName);
      onDone(friendlyName ? `${step.epicDisplayName} (${friendlyName})` : step.epicDisplayName);
    } catch (e) {
      // Pending login is preserved server-side on failure — let the
      // user just retry with a different token.
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }

  async function cancel() {
    if (step.kind === "confirm") {
      try {
        await api.cancelAddAccount(step.pendingId);
      } catch {
        // best-effort cleanup only
      }
    }
    onCancel();
  }

  return (
    <div className="modal-backdrop">
      <div className="modal">
        <h3>Add Account</h3>

        {step.kind === "login" && (
          <>
            <p>
              This opens Epic's login page in your default browser. Log in there (choosing
              "Log in with Steam" also works for Steam-linked accounts).
            </p>
            <div className="modal__row">
              <button className="btn btn--primary" disabled={busy} onClick={openLogin}>
                Open Epic Login
              </button>
            </div>
          </>
        )}

        {step.kind === "code" && (
          <>
            <p>
              After logging in, Epic redirects to a page showing a small JSON blob. Copy the{" "}
              <code>authorizationCode</code> value and paste it below.
            </p>
            <div className="modal__row">
              <a href={step.url} target="_blank" rel="noreferrer">
                Reopen login link
              </a>
            </div>
            <div className="modal__row">
              <label>Authorization code</label>
              <input
                className="input"
                autoFocus
                value={authCode}
                onChange={(e) => setAuthCode(e.target.value)}
                placeholder="paste code here"
              />
            </div>
            <div className="modal__row">
              <button className="btn btn--primary" disabled={busy || !authCode.trim()} onClick={submitCode}>
                Submit Code
              </button>
            </div>
          </>
        )}

        {step.kind === "confirm" && (
          <>
            <h4>Logged in as {step.epicDisplayName}</h4>
            <div className="modal__row">
              <label>Friendly name (optional)</label>
              <input
                className="input"
                value={friendlyName}
                onChange={(e) => setFriendlyName(e.target.value)}
                placeholder="e.g. Main, Smurf, Grinding acct"
              />
            </div>
            <div className="modal__row">
              <label>ballchasing.com API token</label>
              <input
                className="input"
                type="password"
                value={token}
                onChange={(e) => setToken(e.target.value)}
                placeholder="paste your token"
              />
            </div>
            <div className="modal__row">
              <button className="btn btn--primary" disabled={busy || !token} onClick={confirm}>
                Confirm
              </button>
            </div>
          </>
        )}

        {error && <div className="error-text">{error}</div>}

        <div className="modal__row">
          <button className="btn" disabled={busy} onClick={cancel}>
            Cancel
          </button>
        </div>
      </div>
    </div>
  );
}
