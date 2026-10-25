import { Link } from "react-router-dom";
import LegalPage from "./LegalPage";
import { CONTACT_EMAIL, RECOVERY_DAYS, VERIFY_MINUTES } from "../../lib/legal";

// NOTE: this is product-accurate working text, not lawyer-reviewed. Have
// counsel review it (governing law, liability limits, local consumer
// rules) before launch, then bump TermsVersion in the backend.
export default function TermsPage() {
  return (
    <LegalPage title="Terms of Service">
      <p>
        These terms are the agreement between you and Build553 (&ldquo;we&rdquo;, &ldquo;us&rdquo;) for using
        DeepArchive, our encrypted backup service. By creating an account you agree to them and to our{" "}
        <Link to="/privacy">Privacy Policy</Link>.
      </p>

      <h2>1. Who can use DeepArchive</h2>
      <p>
        You must be at least 13 years old. If you are under the age at which you can legally agree to these terms
        where you live, a parent or guardian must agree for you. You must give a real email address you control and
        keep your account details accurate.
      </p>

      <h2>2. Your account</h2>
      <p>
        You are responsible for what happens under your account. Keep your password and any authenticator codes
        private, and tell us promptly if you think someone else has access. New accounts must confirm their email
        within {VERIFY_MINUTES} minutes; if you don&rsquo;t, the sign-up lapses and you can simply sign up again.
      </p>

      <h2>3. Your data and your passphrase</h2>
      <p>
        Your files are encrypted on your own device before they are uploaded. We store the encrypted result and
        cannot read it. That means <strong>we cannot recover your files if you lose your vault passphrase</strong>.
        Keep it somewhere safe. You own your data; you give us only the permission needed to store it, move it
        between our systems and return it to you.
      </p>

      <h2>4. Acceptable use</h2>
      <p>
        Do not use DeepArchive to store or share anything unlawful, to attack or probe our systems or other people,
        to avoid your storage limits, or to interfere with the service. We may suspend an account that does.
      </p>

      <h2>5. Storage limits and plans</h2>
      <p>
        Each account has a storage limit and a retention policy for old backups. When you reach the limit, new
        backups are refused until you free space. Old backups beyond your retention policy are removed
        automatically.
      </p>

      <h2>6. Deactivating and restoring your account</h2>
      <p>
        You can deactivate your account at any time from the Account page. For {RECOVERY_DAYS} days afterwards you can
        restore it by signing in and entering a one-time code we email to you. After {RECOVERY_DAYS} days an account
        can no longer be restored. Deactivated accounts cannot be used until restored.
      </p>

      <h2>7. Availability and backups of your backups</h2>
      <p>
        We work hard to keep DeepArchive reliable, but we provide it &ldquo;as is&rdquo; and &ldquo;as
        available&rdquo; without promises of uninterrupted service. DeepArchive is one copy of your data in addition
        to the originals, not a replacement for keeping the files you cannot afford to lose somewhere else as well.
      </p>

      <h2>8. Ending the agreement</h2>
      <p>
        You can stop using DeepArchive whenever you like. We may suspend or end accounts that break these terms or
        put the service or others at risk. We will tell you why unless the law prevents us.
      </p>

      <h2>9. Liability</h2>
      <p>
        To the fullest extent the law allows, we are not liable for indirect or consequential losses, or for loss of
        data caused by a lost passphrase, your own deletions, or events outside our reasonable control. Our total
        liability for any claim is limited to the amount you paid us in the 12 months before it. Nothing here limits
        rights you have by law that cannot be limited.
      </p>

      <h2>10. Changes</h2>
      <p>
        We may update these terms. For material changes we will give notice in the app or by email before they
        apply. Continuing to use DeepArchive afterwards means you accept the new terms.
      </p>

      {CONTACT_EMAIL && (
        <>
          <h2>11. Contact</h2>
          <p>
            Questions about these terms: <a href={`mailto:${CONTACT_EMAIL}`}>{CONTACT_EMAIL}</a>.
          </p>
        </>
      )}
    </LegalPage>
  );
}
