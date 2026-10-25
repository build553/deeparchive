import { test, expect } from "@playwright/test";
import { uniqueEmail, acceptSignupTerms } from "./helpers";

test.describe("Dev Inbox — register, verify, and reset-password loops", () => {
  test("register → read the real verification email → verify → log in", async ({ page }) => {
    const email = uniqueEmail("pw-inbox-verify");
    const password = "a-perfectly-fine-password-123";

    await page.goto("/register");
    await page.getByLabel("Display name").fill("Inbox Test");
    await page.getByLabel("Email").fill(email);
    await page.getByLabel("Password").fill(password);
    await acceptSignupTerms(page);
    await page.getByRole("button", { name: "Create account" }).click();
    await expect(page.getByRole("heading", { name: "Check your email" })).toBeVisible();

    await page.getByRole("link", { name: /Dev: view sent emails/ }).click();
    await expect(page).toHaveURL(/\/dev\/inbox$/);

    // Filter on the subject line specifically (not hasText on the whole
    // card): email addresses here embed the test name, so a
    // "pw-inbox-reset-…" address makes a whole-card "Reset" match hit
    // both the Verify and Reset cards for the same address.
    const card = page
      .locator(".dev-email-card", { hasText: email })
      .filter({ has: page.locator(".dev-email-meta strong", { hasText: "Verify your DeepArchive account" }) });
    await expect(card).toBeVisible({ timeout: 10000 }); // the inbox polls every 3s
    await card.getByRole("button", { name: "Verify email" }).click();
    await expect(card.getByText(/Verified/)).toBeVisible();

    await page.getByRole("link", { name: "Back to login" }).click();
    await page.getByLabel("Email").fill(email);
    await page.getByLabel("Password", { exact: true }).fill(password);
    await page.getByRole("button", { name: "Log in" }).click();
    await expect(page.getByRole("heading", { name: "Devices" })).toBeVisible();
  });

  test("forgot-password → read the real reset email → set a new password → log in with it", async ({ page }) => {
    // Register and verify a fresh account first so this test is independent.
    const email = uniqueEmail("pw-inbox-reset");
    const oldPassword = "the-original-password-123";
    const newPassword = "a-brand-new-password-456";

    await page.goto("/register");
    await page.getByLabel("Display name").fill("Reset Test");
    await page.getByLabel("Email").fill(email);
    await page.getByLabel("Password").fill(oldPassword);
    await acceptSignupTerms(page);
    await page.getByRole("button", { name: "Create account" }).click();
    // Wait for the real register request to actually complete before
    // navigating away — an earlier version of this test navigated to
    // /dev/inbox immediately after the click, which aborted the
    // in-flight request when RegisterPage unmounted, so the account was
    // never created at all.
    await expect(page.getByRole("heading", { name: "Check your email" })).toBeVisible();

    await page.goto("/dev/inbox");
    const verifyCard = page
      .locator(".dev-email-card", { hasText: email })
      .filter({ has: page.locator(".dev-email-meta strong", { hasText: "Verify your DeepArchive account" }) });
    await expect(verifyCard).toBeVisible({ timeout: 10000 });
    await verifyCard.getByRole("button", { name: "Verify email" }).click();

    // Now trigger the real forgot-password flow through the actual UI.
    await page.goto("/login");
    await page.getByRole("link", { name: "Forgot password?" }).click();
    await page.getByLabel("Email").fill(email);
    await page.getByRole("button", { name: "Send reset link" }).click();
    await expect(page.getByRole("heading", { name: "Check your email" })).toBeVisible();

    await page.goto("/dev/inbox");
    const resetCard = page
      .locator(".dev-email-card", { hasText: email })
      .filter({ has: page.locator(".dev-email-meta strong", { hasText: "Reset your DeepArchive password" }) });
    await expect(resetCard).toBeVisible({ timeout: 10000 });
    await resetCard.getByPlaceholder("New password").fill(newPassword);
    await resetCard.getByRole("button", { name: "Reset password" }).click();
    // Exact success message, not /reset/i — that regex also matches the
    // subject, the token body, and the button's own static label, all
    // already present before the click resolves anything.
    await expect(resetCard.getByText("Password reset — log in with the new password.")).toBeVisible();

    await page.getByRole("link", { name: "Back to login" }).click();
    await page.getByLabel("Email").fill(email);
    await page.getByLabel("Password", { exact: true }).fill(newPassword);
    await page.getByRole("button", { name: "Log in" }).click();
    await expect(page.getByRole("heading", { name: "Devices" })).toBeVisible();
  });

  test("Dev Inbox page itself renders and polls without a crash", async ({ page }) => {
    await page.goto("/dev/inbox");
    await expect(page.getByRole("heading", { name: "Dev Inbox" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Refresh now" })).toBeVisible();
  });
});
