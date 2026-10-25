import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { Link } from "react-router-dom";
import { api, ApiRequestError } from "../../lib/apiClient";
import { EmptyState } from "../../components/Feedback";
import { formatDateTime } from "../../lib/format";
import type { CapturedEmail } from "../../lib/types";

// Mirrors backend/tests/integration's extractToken test helper: every
// token email this project sends reads "<Noun> token: <token> (expires
// in ...)" — find a word ending in ":" and take the next one.
function extractToken(body: string): string | null {
  const parts = body.split(/\s+/);
  for (let i = 0; i < parts.length; i++) {
    if (parts[i].endsWith(":") && i + 1 < parts.length) {
      return parts[i + 1];
    }
  }
  return null;
}

export default function DevInboxPage() {
  const [emails, setEmails] = useState<CapturedEmail[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const load = useCallback(async () => {
    try {
      const resp = await api.devListEmails();
      setEmails(resp.emails);
      setError(null);
    } catch (err) {
      setEmails(null);
      setError(
        err instanceof ApiRequestError
          ? `${err.message} — the Dev Inbox only exists when the API is started with APP_ENV=development.`
          : "Failed to load the Dev Inbox.",
      );
    }
  }, []);

  useEffect(() => {
    load();
    pollRef.current = setInterval(load, 3000);
    return () => {
      if (pollRef.current) clearInterval(pollRef.current);
    };
  }, [load]);

  return (
    <div className="auth-page">
      <div className="auth-form dev-inbox">
        <h1>Dev Inbox</h1>
        <p className="hint">
          Stands in for a real email provider in local development — see <code>docs/local-dev-walkthrough.md</code>.
          Refreshes automatically every few seconds.
        </p>
        <p>
          <button type="button" className="btn-secondary" onClick={load}>
            Refresh now
          </button>
        </p>
        {error && <p className="form-error">{error}</p>}
        {emails && emails.length === 0 && (
          <EmptyState title="No emails captured yet.">
            <p>Trigger a registration, password reset, or admin login to see it appear here.</p>
          </EmptyState>
        )}
        {emails?.map((e) => (
          // Not the list index: a new email at the top would shift every
          // index and remount (reset) the cards below it.
          <EmailCard key={`${e.sent_at}-${e.to}-${e.subject}`} email={e} onActionDone={load} />
        ))}
        <p className="auth-switch">
          <Link to="/login">Back to login</Link>
        </p>
      </div>
    </div>
  );
}

function EmailCard({ email, onActionDone }: { email: CapturedEmail; onActionDone: () => void }) {
  const token = extractToken(email.body);
  const [message, setMessage] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [newPassword, setNewPassword] = useState("");
  const [busy, setBusy] = useState(false);

  async function onVerify() {
    if (!token) return;
    setBusy(true);
    setError(null);
    try {
      await api.verifyEmail(token);
      setMessage("Verified — you can log in now.");
    } catch (err) {
      setError(err instanceof ApiRequestError ? err.message : "Verification failed.");
    } finally {
      setBusy(false);
      onActionDone();
    }
  }

  async function onReset(e: FormEvent) {
    e.preventDefault();
    if (!token) return;
    setBusy(true);
    setError(null);
    try {
      await api.resetPassword(token, newPassword);
      setMessage("Password reset — log in with the new password.");
    } catch (err) {
      setError(err instanceof ApiRequestError ? err.message : "Reset failed.");
    } finally {
      setBusy(false);
      onActionDone();
    }
  }

  function onCopyToken() {
    if (!token) return;
    navigator.clipboard?.writeText(token).catch(() => undefined);
    setMessage("Token copied — paste it into the Accept an invitation form on the Organizations page.");
  }

  return (
    <div className="dev-email-card">
      <div className="dev-email-meta">
        <strong>{email.subject}</strong>
        <span className="hint">
          to {email.to} · {formatDateTime(email.sent_at)}
        </span>
      </div>
      <pre className="code-block">{email.body}</pre>
      {email.html && (
        <details>
          <summary className="hint">Preview the HTML version</summary>
          <iframe className="email-preview" title="Email preview" sandbox="" srcDoc={email.html} />
        </details>
      )}
      {token && (
        <div className="row-actions">
          {email.subject.includes("Verify") && (
            <button type="button" disabled={busy} onClick={onVerify}>
              Verify email
            </button>
          )}
          {email.subject.includes("Reset") && (
            <form className="inline-form" onSubmit={onReset}>
              <input
                type="password"
                placeholder="New password"
                value={newPassword}
                onChange={(ev) => setNewPassword(ev.target.value)}
                minLength={12}
                required
              />
              <button type="submit" disabled={busy}>
                Reset password
              </button>
            </form>
          )}
          {email.subject.includes("invited") && (
            <button type="button" disabled={busy} onClick={onCopyToken}>
              Copy invitation token
            </button>
          )}
        </div>
      )}
      {message && <p className="form-hint">{message}</p>}
      {error && <p className="form-error">{error}</p>}
    </div>
  );
}
