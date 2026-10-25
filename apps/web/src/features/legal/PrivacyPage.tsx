import { Link } from "react-router-dom";
import LegalPage from "./LegalPage";
import { CONTACT_EMAIL, RECOVERY_DAYS } from "../../lib/legal";

// NOTE: this is product-accurate working text, not lawyer-reviewed. Have
// counsel review it (GDPR/UK GDPR/CCPA wording, named processors,
// retention) before launch, then bump TermsVersion in the backend.
export default function PrivacyPage() {
  return (
    <LegalPage title="Privacy Policy">
      <p>
        DeepArchive is built so we know as little about your files as possible. This page explains what we do
        collect, why, and the choices you have. It applies together with our <Link to="/terms">Terms of Service</Link>.
      </p>

      <h2>What we can&rsquo;t see</h2>
      <p>
        Your files, their names and folder structure are encrypted on your device with a key derived from your vault
        passphrase before upload. We never receive your passphrase or your unlocked key, so we cannot read your
        backups.
      </p>

      <h2>What we collect</h2>
      <ul>
        <li>
          <strong>Account details:</strong> your name, email address and a salted, slow hash of your password (never
          the password itself). We also record which version of these documents you accepted and when, and that you
          confirmed you are at least 13.
        </li>
        <li>
          <strong>Security and sign-in data:</strong> sign-in sessions, IP address, browser/device description, and a
          log of security-relevant events (sign-ins, password changes, deactivation) so we can protect your account.
        </li>
        <li>
          <strong>Backup metadata:</strong> device names, operating system, the size, number and timing of your
          backups, and your storage usage. This is how we show your dashboard and enforce limits.
        </li>
        <li>
          <strong>Your encrypted data:</strong> encrypted file chunks and an encrypted manifest. We also store your
          vault key <em>in its encrypted form</em>, protected by your passphrase, so you can open files in the
          dashboard by typing only the passphrase. Without the passphrase it is unreadable, but a weak passphrase
          is easier to guess, so choose a strong one.
        </li>
        <li>
          <strong>Emails we send you:</strong> verification, password reset, sign-in and account-restore codes, and
          a welcome message. We don&rsquo;t send marketing email.
        </li>
      </ul>

      <h2>Why we use it</h2>
      <p>
        To run the service you asked for: storing and returning your backups, keeping your account secure,
        preventing abuse, and contacting you about your account. We don&rsquo;t sell your data and we don&rsquo;t use it
        for advertising.
      </p>

      <h2 id="storage">Cookies and browser storage</h2>
      <p>
        We use no advertising, analytics or third-party tracking cookies. DeepArchive stores only the following in
        your browser&rsquo;s local storage, all strictly necessary:
      </p>
      <ul>
        <li>a sign-in token that keeps you logged in until you log out or it expires;</li>
        <li>your light/dark theme choice;</li>
        <li>a note that you have seen the storage notice.</li>
      </ul>
      <p>If we ever add anything optional, we will ask you first and explain it here.</p>

      <h2>Who we share it with</h2>
      <p>
        Only service providers who help us run DeepArchive, under their own confidentiality and security
        obligations: our cloud storage and hosting provider (which holds your encrypted data) and our email delivery
        provider (which handles the emails above). We may disclose information if the law requires it; because your
        files are encrypted, we could not hand over their contents even if asked.
      </p>

      <h2>How long we keep it</h2>
      <p>
        We keep your data while your account is active. Backups beyond your retention policy are removed
        automatically. If you deactivate your account, you can restore it for {RECOVERY_DAYS} days; after that it
        can no longer be restored{CONTACT_EMAIL ? ", and you can ask us to permanently erase your data" : ""}.
        Security logs are kept for as long as needed to protect the service.
      </p>

      <h2>Your rights</h2>
      <p>
        Depending on where you live, you may have the right to access, correct, export or erase your personal data,
        to object to or restrict how we use it, and to complain to your data protection authority. You can update your
        name and password and deactivate your account yourself on the Account page.
        {CONTACT_EMAIL && (
          <>
            {" "}
            For anything else, contact us at <a href={`mailto:${CONTACT_EMAIL}`}>{CONTACT_EMAIL}</a>.
          </>
        )}
      </p>

      <h2>Children</h2>
      <p>
        DeepArchive is not for children under 13, and we don&rsquo;t knowingly collect their data. If you believe a
        child has signed up, tell us and we will remove the account.
      </p>

      <h2>Security</h2>
      <p>
        Data is encrypted in transit and your files are end-to-end encrypted. No system is perfectly secure, so we
        also recommend turning on two-step verification on your Account page.
      </p>

      <h2>Changes</h2>
      <p>We will tell you about material changes in the app or by email before they apply.</p>
    </LegalPage>
  );
}
