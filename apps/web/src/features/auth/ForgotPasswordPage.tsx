import { useState, type FormEvent } from "react";
import { Link } from "react-router-dom";
import { api, ApiRequestError } from "../../lib/apiClient";

export default function ForgotPasswordPage() {
  const [email, setEmail] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [done, setDone] = useState(false);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      await api.forgotPassword(email);
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
          <h1>Check your email</h1>
          <p>If that email belongs to an account, a password reset link has been sent.</p>
          <p className="auth-switch">
            <Link to="/dev/inbox">Dev: view sent emails &rarr;</Link>
          </p>
          <p className="auth-switch">
            <Link to="/login">Back to login</Link>
          </p>
        </div>
      </div>
    );
  }

  return (
    <div className="auth-page">
      <form className="auth-form" onSubmit={onSubmit}>
        <h1>Forgot password</h1>
        <label>
          Email
          <input
            type="email"
            autoComplete="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            required
            autoFocus
          />
        </label>
        {error && <p className="form-error">{error}</p>}
        <button type="submit" disabled={submitting}>
          {submitting ? "Sending…" : "Send reset link"}
        </button>
        <p className="auth-switch">
          <Link to="/login">Back to login</Link>
        </p>
      </form>
    </div>
  );
}
