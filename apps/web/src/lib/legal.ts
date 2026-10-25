// Shared constants for the legal pages and the flows that mention them.
// Keep these in step with the Terms and Privacy text: when either document
// changes materially, bump TermsVersion in backend/internal/identity/handlers.go.

// Where people reach a human about their data. Set VITE_CONTACT_EMAIL at
// build time to a monitored mailbox; when unset the pages simply omit it.
export const CONTACT_EMAIL: string | undefined = import.meta.env.VITE_CONTACT_EMAIL || undefined;

export const LEGAL_UPDATED = "25 October 2026";

// Must match backend identity.RecoveryWindow and emailVerificationTTL.
export const RECOVERY_DAYS = 15;
export const VERIFY_MINUTES = 15;
