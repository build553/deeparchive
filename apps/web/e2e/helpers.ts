import { expect, type Page } from "@playwright/test";

// alice@example.test is created by `make seed` (backend/cmd/seed) — a
// real account in the local dev database, not a mock. See
// docs/local-dev-walkthrough.md.
export const SEED_EMAIL = "alice@example.test";
export const SEED_PASSWORD = "DevPassword123!";

// admin@example.test is the seeded platform super-admin — platform_role
// = "admin", created/promoted by `make seed` alongside alice/bob/carol.
export const ADMIN_EMAIL = "admin@example.test";
export const ADMIN_PASSWORD = "DevPassword123!";

export async function login(page: Page, email: string, password: string) {
  await page.goto("/login");
  await page.getByLabel("Email").fill(email);
  await page.getByLabel("Password", { exact: true }).fill(password);
  await page.getByRole("button", { name: "Log in" }).click();
}

// Platform admins get a mandatory emailed one-time code on top of the
// password (see identity.Service.Login's PlatformRole == "admin"
// branch) — a plain login() stops at the "Check your email" step for
// ADMIN_EMAIL, so every admin-authenticated test needs this instead.
// Reads the real code from the Dev Inbox API rather than the UI so it
// doesn't depend on a second page navigation succeeding first.
export async function loginAsAdmin(page: Page) {
  await login(page, ADMIN_EMAIL, ADMIN_PASSWORD);
  await expect(page.getByRole("heading", { name: "Check your email" })).toBeVisible();

  const base = new URL(page.url()).origin.replace(":5173", ":8080");
  let code = "";
  await expect(async () => {
    const resp = await page.request.get(`${base}/api/v1/dev/emails`);
    const { emails } = await resp.json();
    const otpEmail = emails.find((e: { subject: string }) => e.subject.includes("admin sign-in code"));
    code = otpEmail?.body.match(/code is: (\d{6})/)?.[1] ?? "";
    expect(code).not.toBe("");
  }).toPass({ timeout: 10000 });

  await page.getByLabel("Verification code").fill(code);
  await page.getByRole("button", { name: "Verify code" }).click();
}

export function uniqueEmail(prefix: string): string {
  return `${prefix}-${Date.now()}@example.test`;
}

// The sign-up form requires both consent boxes (Terms/Privacy and 13+).
export async function acceptSignupTerms(page: Page) {
  await page.getByLabel("I am 13 years old or older.").check();
  await page.getByLabel(/I agree to the/).check();
}
