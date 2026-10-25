import { test, expect } from "@playwright/test";

const API = "http://localhost:8080";

async function emailsTo(to: string, subject: string) {
  for (let i = 0; i < 40; i++) {
    const { emails } = await (await fetch(`${API}/api/v1/dev/emails`)).json();
    const found = emails.find((e: { to: string; subject: string }) => e.to === to && e.subject.includes(subject));
    if (found) return found;
    await new Promise((r) => setTimeout(r, 300));
  }
  throw new Error(`no "${subject}" email for ${to}`);
}

test("signup email has a working button; verifying sends the welcome email", async ({ page }) => {
  const email = `mail-${Date.now()}@example.test`;
  const reg = await fetch(`${API}/api/v1/auth/register`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ email, password: "a-long-password-123", display_name: "Mailer", terms_accepted: true, age_confirmed: true }),
  });
  expect(reg.status).toBe(202);

  const verify = await emailsTo(email, "Verify");
  expect(verify.html).toContain("viewport"); // responsive
  expect(verify.html).toContain("prefers-color-scheme:dark");
  const link = verify.html.match(/href="([^"]*verify-email[^"]*)"/)[1].replace(/&amp;/g, "&");

  // No welcome yet: the account only counts once verified.
  const before = (await (await fetch(`${API}/api/v1/dev/emails`)).json()).emails;
  expect(before.some((e: { to: string; subject: string }) => e.to === email && e.subject.includes("Welcome"))).toBe(false);

  await page.goto(link);
  await expect(page.getByRole("heading", { name: "You're verified" })).toBeVisible();

  const welcome = await emailsTo(email, "Welcome");
  expect(welcome.html).toContain("Welcome, Mailer");
  expect(welcome.html).toContain("Build553");
});

test("password-reset email button opens the reset page and sets a new password", async ({ page }) => {
  const email = `reset-${Date.now()}@example.test`;
  const post = (path: string, body: object) =>
    fetch(`${API}/api/v1/auth/${path}`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
  await post("register", { email, password: "a-long-password-123", display_name: "Resetter", terms_accepted: true, age_confirmed: true });
  await post("password/forgot", { email });

  const reset = await emailsTo(email, "Reset");
  const link = reset.html.match(/href="([^"]*reset-password[^"]*)"/)[1].replace(/&amp;/g, "&");
  await page.goto(link);
  await page.getByLabel("New password").fill("a-brand-new-password-9");
  await page.getByRole("button", { name: "Update password" }).click();
  await expect(page.getByRole("heading", { name: "Password updated" })).toBeVisible();
});
