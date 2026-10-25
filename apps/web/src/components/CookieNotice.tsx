import { useState } from "react";
import { Link } from "react-router-dom";

const KEY = "deeparchive.notice_ack";

function acknowledged(): boolean {
  try {
    return localStorage.getItem(KEY) === "1";
  } catch {
    return false;
  }
}

// DeepArchive stores only what it needs to work: the sign-in token and the
// theme choice, both in this browser's local storage. There are no ads,
// analytics or third-party trackers, so this is a notice, not a consent
// gate. If a non-essential cookie or tracker is ever added, it must wait
// for an explicit choice here before it loads.
export default function CookieNotice() {
  const [hidden, setHidden] = useState(acknowledged);
  if (hidden) return null;

  function dismiss() {
    try {
      localStorage.setItem(KEY, "1");
    } catch {
      /* private mode: just hide it for this visit */
    }
    setHidden(true);
  }

  return (
    <div className="cookie-notice" role="region" aria-label="Cookies and storage">
      <p>
        We only store what&rsquo;s needed to keep you signed in and remember your theme. No ads, no tracking.{" "}
        <Link to="/privacy#storage">Learn more</Link>
      </p>
      <button type="button" onClick={dismiss}>
        Allow all cookies
      </button>
    </div>
  );
}
