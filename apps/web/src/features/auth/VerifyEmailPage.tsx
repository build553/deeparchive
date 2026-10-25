import { useEffect, useRef, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { api, ApiRequestError } from "../../lib/apiClient";
import { LoadingState } from "../../components/Feedback";

// Landing page for the button in the verification email.
export default function VerifyEmailPage() {
  const [params] = useSearchParams();
  const token = params.get("token") ?? "";
  const [state, setState] = useState<"working" | "ok" | "failed">(token ? "working" : "failed");
  const [message, setMessage] = useState(token ? "" : "This link is missing its token. Open it again from the email.");
  const started = useRef(false); // tokens are single-use; never fire twice

  useEffect(() => {
    if (!token || started.current) return;
    started.current = true;
    api
      .verifyEmail(token)
      .then(() => setState("ok"))
      .catch((err) => {
        setMessage(err instanceof ApiRequestError ? err.message : "Something went wrong. Please try again.");
        setState("failed");
      });
  }, [token]);

  return (
    <div className="auth-page">
      <div className="auth-form">
        {state === "working" && <LoadingState label="Verifying your email…" />}
        {state === "ok" && (
          <>
            <h1>You're verified</h1>
            <p className="hint">Your email is confirmed. You're all set.</p>
            <Link to="/" className="auth-cta">
              Continue to DeepArchive
            </Link>
          </>
        )}
        {state === "failed" && (
          <>
            <h1>That link didn't work</h1>
            <p className="form-error">{message}</p>
            <p className="hint">Links expire after 15 minutes and work once. Try logging in and choose "Resend verification email", or sign up again.</p>
            <Link to="/login" className="auth-cta">
              Go to sign in
            </Link>
          </>
        )}
      </div>
    </div>
  );
}
