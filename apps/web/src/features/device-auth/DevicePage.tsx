import { useEffect, useState, type FormEvent } from "react";
import { useSearchParams } from "react-router-dom";
import { api, ApiRequestError } from "../../lib/apiClient";

// The browser half of a `deepctl login`-style device code flow (the
// same shape as `gh auth login`): the CLI prints a short code and a URL,
// a person opens the URL here (already signed in, since this route sits
// behind ProtectedRoute), confirms the code matches what their terminal
// showed, and approves or denies. The CLI is polling /auth/device/token
// the whole time and picks up the result within one interval.
export default function DevicePage() {
  const [searchParams] = useSearchParams();
  const [code, setCode] = useState(searchParams.get("code") ?? "");
  const [clientName, setClientName] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [checking, setChecking] = useState(false);
  const [deciding, setDeciding] = useState(false);
  const [decision, setDecision] = useState<"approved" | "denied" | null>(null);

  async function checkCode(candidate: string) {
    setError(null);
    setChecking(true);
    try {
      const resp = await api.deviceAuthVerify(candidate);
      setClientName(resp.client_name);
    } catch (err) {
      setClientName(null);
      setError(err instanceof ApiRequestError ? err.message : "Failed to check that code.");
    } finally {
      setChecking(false);
    }
  }

  // A link with ?code=... (the one the CLI prints as
  // verification_uri_complete) should go straight to the approval
  // prompt, not make someone retype the code they just copied.
  useEffect(() => {
    const fromQuery = searchParams.get("code");
    if (fromQuery) checkCode(fromQuery);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  function onCheckCode(e: FormEvent) {
    e.preventDefault();
    checkCode(code);
  }

  async function onApprove() {
    setDeciding(true);
    setError(null);
    try {
      await api.deviceAuthApprove(code);
      setDecision("approved");
    } catch (err) {
      setError(err instanceof ApiRequestError ? err.message : "Failed to approve.");
    } finally {
      setDeciding(false);
    }
  }

  async function onDeny() {
    setDeciding(true);
    setError(null);
    try {
      await api.deviceAuthDeny(code);
      setDecision("denied");
    } catch (err) {
      setError(err instanceof ApiRequestError ? err.message : "Failed to deny.");
    } finally {
      setDeciding(false);
    }
  }

  if (decision === "approved") {
    return (
      <div>
        <header className="page-header">
          <h1>Device connected</h1>
        </header>
        <div className="card">
          <p>
            <strong>{clientName}</strong> is now signed in. You can close this tab and go back to your terminal.
          </p>
        </div>
      </div>
    );
  }

  if (decision === "denied") {
    return (
      <div>
        <header className="page-header">
          <h1>Request denied</h1>
        </header>
        <div className="card">
          <p>That sign-in attempt was denied. You can close this tab.</p>
        </div>
      </div>
    );
  }

  return (
    <div>
      <header className="page-header">
        <div>
          <h1>Connect a device</h1>
          <p className="hint">Enter the code shown in your terminal.</p>
        </div>
      </header>

      {!clientName ? (
        <div className="card">
          <form className="inline-form" onSubmit={onCheckCode}>
            <input
              type="text"
              inputMode="text"
              autoCapitalize="characters"
              placeholder="XXXX-XXXX"
              value={code}
              onChange={(e) => setCode(e.target.value)}
              required
              autoFocus
              style={{ fontFamily: "ui-monospace, monospace", fontSize: "1.1rem", letterSpacing: "0.05em" }}
            />
            <button type="submit" disabled={checking}>
              {checking ? "Checking…" : "Continue"}
            </button>
          </form>
          {error && <p className="form-error">{error}</p>}
        </div>
      ) : (
        <div className="card">
          <p>
            <strong>{clientName}</strong> wants to sign in to your DeepArchive account.
          </p>
          <p className="hint">
            If you didn't run <code>{clientName} login</code> yourself just now, deny this request.
          </p>
          <div className="row-actions" style={{ justifyContent: "flex-start" }}>
            <button type="button" disabled={deciding} onClick={onApprove}>
              {deciding ? "Approving…" : "Approve"}
            </button>
            <button type="button" className="btn-secondary" disabled={deciding} onClick={onDeny}>
              Deny
            </button>
          </div>
          {error && <p className="form-error">{error}</p>}
        </div>
      )}
    </div>
  );
}
