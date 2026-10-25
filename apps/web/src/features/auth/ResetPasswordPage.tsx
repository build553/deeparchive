import { useState, type FormEvent } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { api, ApiRequestError } from "../../lib/apiClient";

// Landing page for the button in the password-reset email.
export default function ResetPasswordPage() {
  const [params] = useSearchParams();
  const token = params.get("token") ?? "";
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [done, setDone] = useState(false);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      await api.resetPassword(token, password);
      setDone(true);
    } catch (err) {
      setError(err instanceof ApiRequestError ? err.message : "Something went wrong. Please try again.");
    } finally {
      setSubmitting(false);
    }
  }

  if (done) {
    return (
      <div className="auth-page">
        <div className="auth-form">
          <h1>Password updated</h1>
          <p className="hint">Sign in with your new password. Other devices were signed out.</p>
          <Link to="/login" className="auth-cta">
            Go to sign in
          </Link>
        </div>
      </div>
    );
  }

  return (
    <div className="auth-page">
      <form className="auth-form" onSubmit={onSubmit}>
        <h1>Choose a new password</h1>
        {!token && <p className="form-error">This link is missing its token. Open it again from the email.</p>}
        <label>
          New password
          <input
            type="password"
            autoComplete="new-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
            minLength={12}
            autoFocus
          />
        </label>
        <span className={password.length > 0 && password.length < 12 ? "form-error" : "form-hint"}>At least 12 characters.</span>
        {error && <p className="form-error">{error}</p>}
        <button type="submit" disabled={submitting || !token}>
          {submitting ? "Saving…" : "Update password"}
        </button>
        <p className="auth-switch">
          <Link to="/login">Back to login</Link>
        </p>
      </form>
    </div>
  );
}
