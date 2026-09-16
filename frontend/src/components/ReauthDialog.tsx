import { useState } from "react";
import type { AccountView } from "../types";
import { api, errorMessage } from "../api";

type Step = { kind: "login" } | { kind: "code"; url: string };

interface Props {
  account: AccountView;
  onDone: () => void;
  onCancel: () => void;
}

function fmtName(a: AccountView): string {
  return a.friendly_name ? `${a.display_name} (${a.friendly_name})` : a.display_name;
}

export function ReauthDialog({ account, onDone, onCancel }: Props) {
  const [step, setStep] = useState<Step>({ kind: "login" });
  const [authCode, setAuthCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function openLogin() {
    setBusy(true);
    setError("");
    try {
      const url = await api.beginReauth(account.id);
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
      await api.submitReauthCode(account.id, authCode.trim());
      onDone();
    } catch (e) {
      setError(`${errorMessage(e)} — start over with a fresh login.`);
      setStep({ kind: "login" });
      setAuthCode("");
    } finally {
      setBusy(false);
    }
  }

  async function cancel() {
    // BeginReauth (called on entering the "code" step) flips the
    // account to "authenticating" server-side — revert that if we're
    // backing out before submitting a code, or it'd stay stuck there
    // until the next reauth attempt or app restart.
    if (step.kind === "code") {
      try {
        await api.cancelReauth(account.id);
      } catch {
        // best-effort cleanup only
      }
    }
    onCancel();
  }

  return (
    <div className="modal-backdrop">
      <div className="modal">
        <h3>Reauthenticate {fmtName(account)}</h3>

        {step.kind === "login" && (
          <div className="modal__row">
            <button className="btn btn--primary" disabled={busy} onClick={openLogin}>
              Open Epic Login
            </button>
          </div>
        )}

        {step.kind === "code" && (
          <>
            <p>
              After logging in, copy the <code>authorizationCode</code> value from the redirect
              page and paste it below.
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
