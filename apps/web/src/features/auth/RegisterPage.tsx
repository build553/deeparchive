import { useState, type FormEvent } from "react";
import { Link } from "react-router-dom";
import { useAuth } from "./AuthContext";
import { ApiRequestError } from "../../lib/apiClient";
import LegalFooter from "../../components/LegalFooter";
import { VERIFY_MINUTES } from "../../lib/legal";

export default function RegisterPage() {
  const { register } = useAuth();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [done, setDone] = useState(false);
  const [agreed, setAgreed] = useState(false);
  const [adult, setAdult] = useState(false);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      await register(email, password, displayName, { termsAccepted: agreed, ageConfirmed: adult });
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
          <p className="hint">
            If that email is valid, we sent a verification link to <strong>{email}</strong>. Open it within {VERIFY_MINUTES}{" "}
            minutes, then come back and log in. If the link lapses, just sign up again.
          </p>
          <p className="auth-switch">
            <Link to="/dev/inbox">Dev: view sent emails &rarr;</Link>
          </p>
          <p className="auth-switch">
            <Link to="/login">Back to login</Link>
          </p>
        </div>
        <LegalFooter />
      </div>
    );
  }

  const passwordTooShort = password.length > 0 && password.length < 12;

  return (
    <div className="auth-page">
      <form className="auth-form" onSubmit={onSubmit}>
        <h1>Create an account</h1>
        <label>
          Display name
          <input
            type="text"
            autoComplete="name"
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
            required
            autoFocus
          />
        </label>
        <label>
          Email
          <input type="email" autoComplete="email" value={email} onChange={(e) => setEmail(e.target.value)} required />
        </label>
        <label>
          Password
          <input
            type="password"
            autoComplete="new-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
            minLength={12}
            aria-describedby="password-hint"
          />
        </label>
        <span id="password-hint" className={passwordTooShort ? "form-error" : "form-hint"}>
          At least 12 characters.
        </span>
        <label className="check-row">
          <input type="checkbox" checked={adult} onChange={(e) => setAdult(e.target.checked)} required />
          <span>I am 13 years old or older.</span>
        </label>
        <label className="check-row">
          <input type="checkbox" checked={agreed} onChange={(e) => setAgreed(e.target.checked)} required />
          <span>
            I agree to the{" "}
            <Link to="/terms" target="_blank" rel="noopener">
              Terms of Service
            </Link>{" "}
            and{" "}
            <Link to="/privacy" target="_blank" rel="noopener">
              Privacy Policy
            </Link>
            .
          </span>
        </label>
        {error && <p className="form-error">{error}</p>}
        <button type="submit" disabled={submitting || !agreed || !adult}>
          {submitting ? "Creating account…" : "Create account"}
        </button>
        <p className="auth-switch">
          Already have an account? <Link to="/login">Log in</Link>
        </p>
        <p className="auth-switch">
          <Link to="/dev/inbox">Dev: view sent emails &rarr;</Link>
        </p>
      </form>
      <LegalFooter />
    </div>
  );
}
