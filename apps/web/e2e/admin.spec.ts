import { test, expect } from "@playwright/test";
import { ADMIN_EMAIL, ADMIN_PASSWORD, login, loginAsAdmin, SEED_EMAIL, SEED_PASSWORD } from "./helpers";

const API_BASE_URL = "http://localhost:8080";

// Logs in as the platform admin purely over fetch (no page), completing
// the mandatory email-OTP second factor by reading the real code back
// out of the Dev Inbox — the API-level equivalent of helpers.loginAsAdmin,
// needed here because restoreAliceDefaults runs outside any page context.
async function adminAccessToken(): Promise<string> {
  const loginResp = await fetch(`${API_BASE_URL}/api/v1/auth/login`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ email: ADMIN_EMAIL, password: ADMIN_PASSWORD }),
  });
  const { challenge_id: challengeId } = await loginResp.json();

  let code = "";
  for (let i = 0; i < 20 && !code; i++) {
    const emailsResp = await fetch(`${API_BASE_URL}/api/v1/dev/emails`);
    const { emails } = await emailsResp.json();
    const otpEmail = emails.find((e: { subject: string }) => e.subject.includes("admin sign-in code"));
    code = otpEmail?.body.match(/code is: (\d{6})/)?.[1] ?? "";
    if (!code) await new Promise((r) => setTimeout(r, 200));
  }

  const verifyResp = await fetch(`${API_BASE_URL}/api/v1/auth/login/admin-otp/verify`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ challenge_id: challengeId, code }),
  });
  const { access_token } = await verifyResp.json();
  return access_token;
}

// Restores alice to the seed script's defaults directly through the API
// (not the UI), in a test.finally so it runs even if an assertion above
// throws mid-test — a UI-only restore step can be skipped by a failure,
// which is exactly what left alice stuck "suspended" with a modified
// quota/retention after an earlier run of this test failed partway
// through. See the "Users:" test below.
async function restoreAliceDefaults() {
  const token = await adminAccessToken();
  const usersResp = await fetch(`${API_BASE_URL}/api/v1/admin/users?q=alice&limit=1&offset=0`, {
    headers: { Authorization: `Bearer ${token}` },
  });
  const { users } = await usersResp.json();
  const aliceId = users[0].id;
  const patch = (path: string, body: unknown) =>
    fetch(`${API_BASE_URL}/api/v1/admin/users/${aliceId}${path}`, {
      method: "PATCH",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
  await patch("/status", { status: "active" });
  await patch("/quota", { storage_quota_bytes: 5368709120 });
  await patch("/retention", { retention_keep_snapshots: 10 });
}

test.describe("Admin panel (platform_role === admin only)", () => {
  test("a non-admin (alice) sees no Admin nav link and is redirected away from /admin", async ({ page }) => {
    await login(page, SEED_EMAIL, SEED_PASSWORD);
    await expect(page.getByRole("heading", { name: "Devices" })).toBeVisible();
    await expect(page.getByRole("link", { name: "Admin" })).toHaveCount(0);

    await page.goto("/admin");
    await expect(page.getByRole("heading", { name: "Devices" })).toBeVisible();
    await expect(page).toHaveURL(/\/$/);
  });

  test.describe("as the seeded platform admin", () => {
    test.beforeEach(async ({ page }) => {
      await loginAsAdmin(page);
      await expect(page.getByRole("heading", { name: "Devices" })).toBeVisible();
    });

    test("Admin nav link is visible and leads to the Overview page with real stats", async ({ page }) => {
      await page.getByRole("link", { name: "Admin" }).click();
      await expect(page.getByRole("heading", { name: "Admin panel" })).toBeVisible();
      // "Users"/"Organizations" appear both as nav links and as stat
      // labels — scope to the stat cards specifically.
      await expect(page.locator(".admin-stat-label", { hasText: "Users" })).toBeVisible();
      await expect(page.locator(".admin-stat-label", { hasText: "Organizations" })).toBeVisible();
      await expect(page.getByText("Storage used (platform-wide)")).toBeVisible();
    });

    test("Users: search for alice, open her detail, edit quota and retention, toggle status", async ({ page }) => {
      try {
        await page.goto("/admin/users");
        await page.getByPlaceholder("Search by email or name…").fill("alice");
        await page.getByRole("button", { name: "Search" }).click();
        await expect(page.getByRole("link", { name: SEED_EMAIL })).toBeVisible();
        await page.getByRole("link", { name: SEED_EMAIL }).click();

        await expect(page.getByRole("heading", { name: SEED_EMAIL })).toBeVisible();

        // Quota
        const quotaInput = page.locator("h3:has-text('Storage quota') + form input");
        await quotaInput.fill("777777777");
        await page.locator("h3:has-text('Storage quota') + form button[type=submit]").click();
        await expect(page.getByText("Storage quota updated.")).toBeVisible();

        // Retention
        const retentionInput = page.locator("h3:has-text('Retention') + form input");
        await retentionInput.fill("3");
        await page.locator("h3:has-text('Retention') + form button[type=submit]").click();
        await expect(page.getByText("Retention limit updated.")).toBeVisible();

        // Status: suspend then re-activate, verifying the UI reflects
        // each change. Each click triggers a native window.confirm() —
        // same pattern as AccountPage's deactivate button — which
        // Playwright auto-dismisses unless a dialog handler explicitly
        // accepts it. Re-registering per click (rather than once up
        // front) avoids a real flake this test hit once: a single
        // page.on("dialog", ...) handler occasionally missed the
        // *second* dialog in the same test, silently no-opping the
        // "Activate" click and leaving alice stuck suspended.
        page.once("dialog", (dialog) => dialog.accept());
        await page.getByRole("button", { name: "Suspend" }).click();
        await expect(page.getByText("Status set to suspended.")).toBeVisible();
        await expect(page.locator("dd .status-suspended")).toBeVisible();

        page.once("dialog", (dialog) => dialog.accept());
        await page.getByRole("button", { name: "Activate", exact: true }).click();
        await expect(page.getByText("Status set to active.")).toBeVisible();
        await expect(page.locator("dd .status-active")).toBeVisible();

        await quotaInput.fill("5368709120");
        await page.locator("h3:has-text('Storage quota') + form button[type=submit]").click();
        await expect(page.getByText("Storage quota updated.")).toBeVisible();
        await retentionInput.fill("10");
        await page.locator("h3:has-text('Retention') + form button[type=submit]").click();
        await expect(page.getByText("Retention limit updated.")).toBeVisible();
      } finally {
        // Belt-and-suspenders: restore alice via the API directly even
        // if an assertion above threw mid-test, so a failure here never
        // leaves the shared seed account broken for every test (or
        // manual walkthrough) that runs after it.
        await restoreAliceDefaults();
      }
    });

    test("an admin cannot change their own platform role", async ({ page }) => {
      await page.goto("/admin/users");
      await page.getByPlaceholder("Search by email or name…").fill("admin@example.test");
      await page.getByRole("button", { name: "Search" }).click();
      await page.getByRole("link", { name: "admin@example.test" }).click();
      await expect(page.getByText("This is your own account — self role changes are blocked.")).toBeVisible();
      await expect(page.getByRole("button", { name: "Admin", exact: true })).toBeDisabled();
      await expect(page.getByRole("button", { name: "Operator" })).toBeDisabled();
      await expect(page.getByRole("button", { name: "None" })).toBeDisabled();
    });

    test("Organizations: search for Acme Dev Org, see its real members, edit quota", async ({ page }) => {
      await page.goto("/admin/organizations");
      await page.getByPlaceholder("Search by name or slug…").fill("Acme Dev Org");
      await page.getByRole("button", { name: "Search" }).click();
      await expect(page.getByRole("link", { name: "Acme Dev Org" })).toBeVisible();
      await page.getByRole("link", { name: "Acme Dev Org" }).click();

      await expect(page.getByRole("heading", { name: "Acme Dev Org" })).toBeVisible();
      await expect(page.getByRole("cell", { name: "alice@example.test" })).toBeVisible();
      await expect(page.getByRole("cell", { name: "bob@example.test" })).toBeVisible();
      await expect(page.getByRole("cell", { name: "carol@example.test" })).toBeVisible();

      const quotaInput = page.locator("h3:has-text('Storage quota') + form input");
      const original = await quotaInput.inputValue();
      await quotaInput.fill("999999999");
      await page.locator("h3:has-text('Storage quota') + form button[type=submit]").click();
      await expect(page.getByText("Storage quota updated.")).toBeVisible();

      // Restore, same reasoning as the user quota test above.
      await quotaInput.fill(original);
      await page.locator("h3:has-text('Storage quota') + form button[type=submit]").click();
      await expect(page.getByText("Storage quota updated.")).toBeVisible();
    });

    test("Activity feed renders real audit events with no console errors", async ({ page }) => {
      const consoleErrors: string[] = [];
      page.on("console", (msg) => {
        if (msg.type() === "error") consoleErrors.push(msg.text());
      });

      await page.goto("/admin/activity");
      await expect(page.getByRole("table")).toBeVisible();
      // The admin's own just-performed login is itself a real audit row.
      await expect(page.getByText("auth.login").first()).toBeVisible();

      expect(consoleErrors, `console errors: ${consoleErrors.join(" | ")}`).toHaveLength(0);
    });
  });
});
