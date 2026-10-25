import { test, expect } from "@playwright/test";
import { acceptSignupTerms, uniqueEmail } from "./helpers";

const API = "http://localhost:8080";
const PASSWORD = "a-long-password-123";

const post = (path: string, body: object, token?: string) =>
  fetch(`${API}/api/v1/${path}`, {
    method: "POST",
    headers: { "Content-Type": "application/json", ...(token ? { Authorization: `Bearer ${token}` } : {}) },
    body: JSON.stringify(body),
  });

async function emailTo(to: string, subject: string) {
  for (let i = 0; i < 40; i++) {
    const { emails } = await (await fetch(`${API}/api/v1/dev/emails`)).json();
    const found = emails.find((e: { to: string; subject: string }) => e.to === to && e.subject.includes(subject));
    if (found) return found;
    await new Promise((r) => setTimeout(r, 300));
  }
  throw new Error(`no "${subject}" email for ${to}`);
}

async function signUpVerified(email: string) {
  const reg = await post("auth/register", { email, password: PASSWORD, display_name: "Lifecycle", terms_accepted: true, age_confirmed: true });
  expect(reg.status).toBe(202);
  const mail = await emailTo(email, "Verify");
  const token = mail.body.match(/token: (\S+)/)[1];
  expect((await post("auth/verify-email", { token })).status).toBe(200);
}

test.describe("sign-up consent and legal pages", () => {
  test.use({ storageState: { cookies: [], origins: [] } });

  test("create account stays disabled until both boxes are ticked", async ({ page }) => {
    await page.goto("/register");
    await page.getByLabel("Display name").fill("Consent");
    await page.getByLabel("Email").fill(uniqueEmail("pw-consent"));
    await page.getByLabel("Password").fill(PASSWORD);
    const submit = page.getByRole("button", { name: "Create account" });
    await expect(submit).toBeDisabled();
    await page.getByLabel("I am 13 years old or older.").check();
    await expect(submit).toBeDisabled();
    await page.getByLabel(/I agree to the/).check();
    await expect(submit).toBeEnabled();
  });

  test("server refuses sign-up without consent", async () => {
    const r = await post("auth/register", { email: uniqueEmail("pw-noconsent"), password: PASSWORD, display_name: "X" });
    expect(r.status).toBe(400);
    expect((await r.json()).error.code).toBe("consent_required");
    const r2 = await post("auth/register", { email: uniqueEmail("pw-noage"), password: PASSWORD, display_name: "X", terms_accepted: true });
    expect(r2.status).toBe(400);
  });

  test("terms and privacy pages are public and linked from sign-up", async ({ page, context }) => {
    await page.goto("/register");
    const [terms] = await Promise.all([context.waitForEvent("page"), page.getByRole("link", { name: "Terms of Service" }).click()]);
    await expect(terms.getByRole("heading", { name: "Terms of Service", level: 1 })).toBeVisible();
    await terms.goto("/privacy");
    await expect(terms.getByRole("heading", { name: "Privacy Policy", level: 1 })).toBeVisible();
    await expect(terms.getByText("13")).not.toHaveCount(0);
  });

  test("storage notice shows once and stays dismissed", async ({ page }) => {
    await page.goto("/login");
    const notice = page.getByRole("region", { name: "Cookies and storage" });
    await expect(notice).toBeVisible();
    await notice.getByRole("button", { name: "Allow all cookies" }).click();
    await expect(notice).toBeHidden();
    await page.reload();
    await expect(page.getByRole("heading", { name: "Log in" })).toBeVisible();
    await expect(notice).toBeHidden();
  });
});

test.describe("account lifecycle", () => {
  test("unverified accounts cannot log in and can resend the link", async ({ page }) => {
    const email = uniqueEmail("pw-unverified");
    await post("auth/register", { email, password: PASSWORD, display_name: "Pending", terms_accepted: true, age_confirmed: true });
    await page.goto("/login");
    await page.getByLabel("Email").fill(email);
    await page.getByLabel("Password", { exact: true }).fill(PASSWORD);
    await page.getByRole("button", { name: "Log in" }).click();
    await expect(page.getByText("Please verify your email first")).toBeVisible();
    await page.getByRole("button", { name: "Resend verification email" }).click();
    await expect(page.getByText(/new link is on its way/)).toBeVisible();
  });

  test("a deactivated account is restored with an emailed code", async ({ page }) => {
    const email = uniqueEmail("pw-recover");
    await signUpVerified(email);
    const session = await (await post("auth/login", { email, password: PASSWORD })).json();
    const del = await fetch(`${API}/api/v1/me`, {
      method: "DELETE",
      headers: { "Content-Type": "application/json", Authorization: `Bearer ${session.access_token}` },
      body: JSON.stringify({ password: PASSWORD }),
    });
    expect(del.status).toBe(200);

    await page.goto("/login");
    await page.getByLabel("Email").fill(email);
    await page.getByLabel("Password", { exact: true }).fill(PASSWORD);
    await page.getByRole("button", { name: "Log in" }).click();
    await expect(page.getByRole("heading", { name: "Welcome back" })).toBeVisible();
    await expect(page.getByText(/15 more days/)).toBeVisible();

    const mail = await emailTo(email, "Restore your DeepArchive account");
    const code = mail.body.match(/code is: (\d{6})/)[1];
    await page.getByLabel("Verification code").fill(code);
    await page.getByRole("button", { name: "Restore account" }).click();
    await expect(page.getByRole("heading", { name: "Devices" })).toBeVisible();
  });

  test("account page warns about the 15-day recovery window", async ({ page }) => {
    const email = uniqueEmail("pw-disclaimer");
    await signUpVerified(email);
    await page.goto("/login");
    await page.getByLabel("Email").fill(email);
    await page.getByLabel("Password", { exact: true }).fill(PASSWORD);
    await page.getByRole("button", { name: "Log in" }).click();
    await expect(page.getByRole("heading", { name: "Devices" })).toBeVisible();
    await page.getByRole("link", { name: "Account" }).click();
    await expect(page.getByText(/entering a code we email you/)).toBeVisible();
    await expect(page.getByText("15 days", { exact: true })).toBeVisible();
  });
});
