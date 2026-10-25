import { RECOVERY_DAYS } from "../../lib/legal";
import { useCallback, useEffect, useState, type FormEvent } from "react";
import { useNavigate } from "react-router-dom";
import QRCode from "qrcode";
import { useAuth } from "../auth/AuthContext";
import { api, ApiRequestError } from "../../lib/apiClient";
import { LoadingState } from "../../components/Feedback";
import { formatDateTime, summarizeUserAgent } from "../../lib/format";
import type { SessionSummary } from "../../lib/types";

// Account and Security used to be separate tabs. They're both "things
// about my one account," so a real user checking on their account
// shouldn't have to guess which of two tabs has what — merged into one
// page instead.
export default function AccountPage() {
  const { me, refreshMe, logout } = useAuth();
  const navigate = useNavigate();
  const [displayName, setDisplayName] = useState(me?.display_name ?? "");
  const [savingName, setSavingName] = useState(false);
  const [nameMessage, setNameMessage] = useState<string | null>(null);

  const [password, setPassword] = useState("");
  const [deactivating, setDeactivating] = useState(false);
  const [deactivateError, setDeactivateError] = useState<string | null>(null);

  const [resending, setResending] = useState(false);
  const [resendMessage, setResendMessage] = useState<string | null>(null);

  async function onSaveName(e: FormEvent) {
    e.preventDefault();
    setSavingName(true);
    setNameMessage(null);
    try {
      await api.updateMe(displayName);
      await refreshMe();
      setNameMessage("Saved.");
    } catch (err) {
      setNameMessage(err instanceof ApiRequestError ? err.message : "Failed to save.");
    } finally {
      setSavingName(false);
    }
  }

  async function onResendVerification() {
    if (!me) return;
    setResending(true);
    setResendMessage(null);
    try {
      await api.resendVerification(me.email);
      setResendMessage("Sent — check your inbox (and spam folder) for a new link.");
    } catch {
      // Enumeration-safe on the backend — this practically never
      // throws, but show something rather than nothing if it does.
      setResendMessage("Sent — check your inbox (and spam folder) for a new link.");
    } finally {
      setResending(false);
    }
  }

  async function onDeactivate(e: FormEvent) {
    e.preventDefault();
    if (
      !confirm(
        `Deactivate your account? You will be logged out everywhere. You can restore it for ${RECOVERY_DAYS} days by signing in and entering a code we email you. After that it can no longer be recovered.`,
      )
    )
      return;
    setDeactivating(true);
    setDeactivateError(null);
    try {
      await api.deactivateMe(password);
      await logout();
      navigate("/login", { replace: true });
    } catch (err) {
      setDeactivateError(err instanceof ApiRequestError ? err.message : "Failed to deactivate account.");
    } finally {
      setDeactivating(false);
    }
  }

  if (!me) return null;

  return (
    <div>
      <header className="page-header">
        <h1>Account</h1>
      </header>

      <div className="card">
        <dl className="detail-list">
          <dt>Email</dt>
          <dd>
            {me.email}{" "}
            {me.email_verified ? (
              <span className="status-badge status-active">verified</span>
            ) : (
              <span className="status-badge status-pending">unverified</span>
            )}
          </dd>
          <dt>Status</dt>
          <dd>
            <span className={`status-badge status-${me.status}`}>{me.status}</span>
          </dd>
          <dt>Two-factor authentication</dt>
          <dd>{me.mfa_enabled ? "Enabled" : "Not enabled"}</dd>
        </dl>
        {!me.email_verified && (
          <div style={{ marginTop: "var(--space-3)" }}>
            <button type="button" className="btn-secondary" disabled={resending} onClick={onResendVerification}>
              {resending ? "Sending…" : "Resend verification email"}
            </button>
            {resendMessage && <p className="form-hint" style={{ marginTop: "var(--space-2)" }}>{resendMessage}</p>}
          </div>
        )}
      </div>

      <div className="card">
        <h3>Display name</h3>
        <form className="inline-form" onSubmit={onSaveName}>
          <input type="text" value={displayName} onChange={(e) => setDisplayName(e.target.value)} required />
          <button type="submit" disabled={savingName}>
            {savingName ? "Saving…" : "Save"}
          </button>
          {nameMessage && <span className="form-success">{nameMessage}</span>}
        </form>
      </div>

      <PasswordSection />
      <MfaSection enabled={me.mfa_enabled} onChanged={refreshMe} />
      <SessionsSection />

      <div className="card">
        <h3 className="danger-heading">Deactivate account</h3>
        <p className="hint">
          This logs you out everywhere. Changed your mind? For <strong>{RECOVERY_DAYS} days</strong> you can restore
          your account by signing in and entering a code we email you. After {RECOVERY_DAYS} days it can no longer be
          recovered.
        </p>
        <form className="inline-form" onSubmit={onDeactivate}>
          <input
            type="password"
            placeholder="Current password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
          />
          <button type="submit" className="danger" disabled={deactivating}>
            {deactivating ? "Deactivating…" : "Deactivate account"}
          </button>
        </form>
        {deactivateError && <p className="form-error">{deactivateError}</p>}
      </div>
    </div>
  );
}

function PasswordSection() {
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [message, setMessage] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setMessage(null);
    setSubmitting(true);
    try {
      await api.changePassword(currentPassword, newPassword);
      setMessage("Password changed. Your other sessions were not affected.");
      setCurrentPassword("");
      setNewPassword("");
    } catch (err) {
      setError(err instanceof ApiRequestError ? err.message : "Failed to change password.");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <section className="card">
      <h3>Password</h3>
      <form className="inline-form" onSubmit={onSubmit}>
        <input
          type="password"
          autoComplete="current-password"
          placeholder="Current password"
          value={currentPassword}
          onChange={(e) => setCurrentPassword(e.target.value)}
          required
        />
        <input
          type="password"
          autoComplete="new-password"
          placeholder="New password"
          value={newPassword}
          onChange={(e) => setNewPassword(e.target.value)}
          required
          minLength={12}
        />
        <button type="submit" disabled={submitting}>
          {submitting ? "Changing…" : "Change password"}
        </button>
      </form>
      {message && <p className="form-success">{message}</p>}
      {error && <p className="form-error">{error}</p>}
    </section>
  );
}

function MfaSection({ enabled, onChanged }: { enabled: boolean; onChanged: () => Promise<void> }) {
  const [enrollment, setEnrollment] = useState<{ secret: string; uri: string } | null>(null);
  const [qrDataUrl, setQrDataUrl] = useState<string | null>(null);
  const [code, setCode] = useState("");
  const [recoveryCodes, setRecoveryCodes] = useState<string[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function onEnroll() {
    setError(null);
    setBusy(true);
    try {
      const resp = await api.enrollMfa();
      setEnrollment(resp);
      // A real, scannable QR code — any TOTP app (Google/Microsoft
      // Authenticator, 1Password, Authy...) can read this directly,
      // same as every other site's 2FA setup screen. The otpauth://
      // URI already has everything the app needs (issuer, account,
      // secret, algorithm); this just renders it as a code instead of
      // making someone type a 32-character secret by hand.
      setQrDataUrl(await QRCode.toDataURL(resp.uri, { width: 200, margin: 1 }));
    } catch (err) {
      setError(err instanceof ApiRequestError ? err.message : "Failed to start MFA enrollment.");
    } finally {
      setBusy(false);
    }
  }

  async function onConfirm(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      const resp = await api.confirmMfa(code);
      setRecoveryCodes(resp.recovery_codes);
      setEnrollment(null);
      setQrDataUrl(null);
      setCode("");
      await onChanged();
    } catch (err) {
      setError(err instanceof ApiRequestError ? err.message : "Invalid code.");
    } finally {
      setBusy(false);
    }
  }

  async function onDisable() {
    if (!confirm("Disable two-factor authentication?")) return;
    setError(null);
    setBusy(true);
    try {
      await api.disableMfa();
      await onChanged();
    } catch (err) {
      setError(err instanceof ApiRequestError ? err.message : "Failed to disable MFA.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="card">
      <h3>Two-factor authentication</h3>
      {recoveryCodes && (
        <div className="code-block">
          <p>
            <strong>Save these recovery codes</strong> — each can be used once if you lose access to your
            authenticator app. They will not be shown again.
          </p>
          {recoveryCodes.map((c) => (
            <div key={c}>{c}</div>
          ))}
        </div>
      )}
      {enabled ? (
        <>
          <p className="hint">Two-factor authentication is enabled.</p>
          <button type="button" className="danger" disabled={busy} onClick={onDisable}>
            Disable
          </button>
        </>
      ) : enrollment ? (
        <form onSubmit={onConfirm}>
          <p className="hint">Scan this with your authenticator app (Google Authenticator, 1Password, Authy…):</p>
          <div className="mfa-qr">
            {qrDataUrl && <img src={qrDataUrl} alt="Scan with your authenticator app to enable two-factor authentication" width={200} height={200} />}
            <details>
              <summary className="hint">Can't scan it? Enter this code manually</summary>
              <p className="code-block">{enrollment.secret}</p>
            </details>
          </div>
          <div className="inline-form">
            <input
              type="text"
              inputMode="numeric"
              placeholder="6-digit code"
              value={code}
              onChange={(e) => setCode(e.target.value)}
              required
              autoFocus
            />
            <button type="submit" disabled={busy}>
              Confirm
            </button>
          </div>
        </form>
      ) : (
        <button type="button" disabled={busy} onClick={onEnroll}>
          Enable two-factor authentication
        </button>
      )}
      {error && <p className="form-error">{error}</p>}
    </section>
  );
}

function SessionsSection() {
  const [sessions, setSessions] = useState<SessionSummary[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      const resp = await api.listSessions();
      setSessions(resp.sessions);
    } catch (err) {
      setError(err instanceof ApiRequestError ? err.message : "Failed to load sessions.");
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  async function onRevoke(id: string) {
    setBusyId(id);
    setError(null);
    try {
      await api.revokeSession(id);
      await load();
    } catch (err) {
      setError(err instanceof ApiRequestError ? err.message : "Failed to revoke session.");
    } finally {
      setBusyId(null);
    }
  }

  async function onRevokeOthers() {
    if (!confirm("Log out every other session?")) return;
    setError(null);
    try {
      await api.revokeOtherSessions();
      await load();
    } catch (err) {
      setError(err instanceof ApiRequestError ? err.message : "Failed to revoke other sessions.");
    }
  }

  return (
    <section className="card">
      <h3>Active sessions</h3>
      {error && <p className="form-error">{error}</p>}
      {!sessions ? (
        <LoadingState label="Loading sessions…" />
      ) : (
        <>
          <table className="data-table">
            <thead>
              <tr>
                <th>Device / browser</th>
                <th>IP</th>
                <th>Last used</th>
                <th>Expires</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {sessions.map((s) => (
                <tr key={s.id}>
                  <td title={s.user_agent || undefined}>{s.user_agent ? summarizeUserAgent(s.user_agent) : "—"}</td>
                  <td>{s.ip_address || "—"}</td>
                  <td>{formatDateTime(s.last_used_at)}</td>
                  <td>{formatDateTime(s.expires_at)}</td>
                  <td className="row-actions">
                    <button type="button" className="danger" disabled={busyId === s.id} onClick={() => onRevoke(s.id)}>
                      Revoke
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          <p style={{ marginTop: "var(--space-4)", marginBottom: 0 }}>
            <button type="button" className="btn-secondary" onClick={onRevokeOthers}>
              Log out all other sessions
            </button>
          </p>
        </>
      )}
    </section>
  );
}
