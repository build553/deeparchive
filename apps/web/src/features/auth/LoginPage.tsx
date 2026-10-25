import { useState, type FormEvent } from "react";
import { useNavigate, useLocation, Link } from "react-router-dom";
import { useAuth } from "./AuthContext";
import { api, ApiRequestError } from "../../lib/apiClient";
import LegalFooter from "../../components/LegalFooter";

export default function LoginPage() {
  const { login, completeAdminOtp } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();
  const returnTo = (location.state as { from?: string } | null)?.from ?? "/";
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [mfaCode, setMfaCode] = useState("");
  const [mfaRequired, setMfaRequired] = useState(false);
  const [otpChallengeId, setOtpChallengeId] = useState<string | null>(null);
  const [otpCode, setOtpCode] = useState("");
  const [recoveryDays, setRecoveryDays] = useState<number | null>(null);
  const [unverified, setUnverified] = useState(false);
  const [resendNote, setResendNote] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setUnverified(false);
    setResendNote(null);
    setSubmitting(true);
    try {
      if (otpChallengeId) {
        await completeAdminOtp(otpChallengeId, otpCode);
      } else {
        await login(email, password, mfaRequired ? mfaCode : undefined);
      }
      navigate(returnTo, { replace: true });
    } catch (err) {
      if (err instanceof ApiRequestError && err.code === "mfa_required") {
        setMfaRequired(true);
        setError("Enter your authenticator code to continue.");
      } else if (err instanceof ApiRequestError && err.code === "admin_otp_required") {
        const challengeId = err.details?.challenge_id;
        setOtpChallengeId(typeof challengeId === "string" ? challengeId : null);
        setError("We emailed a verification code to this account. Enter it below to continue.");
      } else if (err instanceof ApiRequestError && err.code === "account_recovery_required") {
        const challengeId = err.details?.challenge_id;
        const days = err.details?.days_left;
        setOtpChallengeId(typeof challengeId === "string" ? challengeId : null);
        setRecoveryDays(typeof days === "number" ? days : null);
        setError(null);
      } else if (err instanceof ApiRequestError && err.code === "email_not_verified") {
        setUnverified(true);
        setError("Please verify your email first. We sent you a link when you signed up.");
      } else if (err instanceof ApiRequestError) {
        setError(err.message);
      } else {
        setError("Something went wrong. Please try again.");
      }
    } finally {
      setSubmitting(false);
    }
  }

  async function onResend() {
    setResendNote(null);
    try {
      await api.resendVerification(email);
      setResendNote("If that account is waiting for verification, a new link is on its way.");
    } catch {
      setResendNote("Couldn't send right now. Please try again in a moment.");
    }
  }

  const recovering = recoveryDays !== null;

  return (
    <div className="auth-page">
      <form className="auth-form" onSubmit={onSubmit}>
        <h1>{recovering ? "Welcome back" : otpChallengeId ? "Check your email" : "Log in"}</h1>
        {recovering && (
          <p className="hint">
            This account was deactivated. We emailed you a code to restore it. You can restore it for{" "}
            <strong>
              {recoveryDays} more {recoveryDays === 1 ? "day" : "days"}
            </strong>
            ; after that it can&rsquo;t be recovered.
          </p>
        )}
        {otpChallengeId ? (
          <label>
            Verification code
            <input
              type="text"
              inputMode="numeric"
              autoComplete="one-time-code"
              value={otpCode}
              onChange={(e) => setOtpCode(e.target.value)}
              required
              autoFocus
            />
          </label>
        ) : (
          <>
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
            <label>
              Password
              <input
                type="password"
                autoComplete="current-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
              />
            </label>
            {mfaRequired && (
              <label>
                Authenticator code
                <input
                  type="text"
                  inputMode="numeric"
                  value={mfaCode}
                  onChange={(e) => setMfaCode(e.target.value)}
                  required
                  autoFocus
                />
              </label>
            )}
          </>
        )}
        {error && <p className="form-error">{error}</p>}
        {unverified && (
          <p className="auth-switch">
            <button type="button" className="link-button" onClick={onResend}>
              Resend verification email
            </button>
          </p>
        )}
        {resendNote && <p className="hint">{resendNote}</p>}
        <button type="submit" disabled={submitting}>
          {submitting
            ? otpChallengeId
              ? "Verifying…"
              : "Logging in…"
            : recovering
              ? "Restore account"
              : otpChallengeId
                ? "Verify code"
                : "Log in"}
        </button>
        {otpChallengeId ? (
          <p className="auth-switch">
            <button
              type="button"
              className="link-button"
              onClick={() => {
                setOtpChallengeId(null);
                setRecoveryDays(null);
                setOtpCode("");
                setError(null);
              }}
            >
              Back to login
            </button>
          </p>
        ) : (
          <>
            <p className="auth-switch">
              No account? <Link to="/register">Register</Link>
            </p>
            <p className="auth-switch">
              <Link to="/forgot-password">Forgot password?</Link>
            </p>
            <p className="auth-switch">
              <Link to="/dev/inbox">Dev: view sent emails &rarr;</Link>
            </p>
          </>
        )}
      </form>
      <LegalFooter />
    </div>
  );
}
