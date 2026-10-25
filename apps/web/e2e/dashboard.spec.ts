import { test, expect } from "@playwright/test";
import { login, SEED_EMAIL, SEED_PASSWORD } from "./helpers";

test.describe("Dashboard (authenticated as the seeded alice@example.test)", () => {
  test.beforeEach(async ({ page }) => {
    await login(page, SEED_EMAIL, SEED_PASSWORD);
    await expect(page.getByRole("heading", { name: "Devices" })).toBeVisible();
  });

  test("nav links reach every page with no console errors", async ({ page }) => {
    const consoleErrors: string[] = [];
    page.on("console", (msg) => {
      if (msg.type() === "error") consoleErrors.push(msg.text());
    });
    const failedRequests: string[] = [];
    page.on("requestfailed", (req) => failedRequests.push(`${req.method()} ${req.url()}`));

    for (const { link, heading } of [
      { link: "Backups", heading: "Backups" },
      { link: "Organizations", heading: "Organizations" },
      { link: "Storage", heading: "Storage" },
      { link: "Account", heading: "Account" },
      { link: "Devices", heading: "Devices" },
    ]) {
      await page.getByRole("link", { name: link, exact: true }).click();
      await expect(page.getByRole("heading", { name: heading, exact: true })).toBeVisible();
    }

    expect(consoleErrors, `console errors: ${consoleErrors.join(" | ")}`).toHaveLength(0);
    expect(failedRequests, `failed requests: ${failedRequests.join(" | ")}`).toHaveLength(0);
  });

  test("Devices page lists no \"enroll\" button (device enrollment is deepctl's job)", async ({ page }) => {
    await expect(page.locator("code", { hasText: /^deepctl init/ })).toBeVisible();
    await expect(page.getByRole("button", { name: /^Enroll/ })).toHaveCount(0);
  });

  test("Backups page shows the empty state or real snapshots, never a crash", async ({ page }) => {
    await page.getByRole("link", { name: "Backups", exact: true }).click();
    await expect(page.getByRole("heading", { name: "Backups" })).toBeVisible();
    const emptyState = page.getByText("No backups yet.");
    const table = page.locator("table.data-table");
    await expect(emptyState.or(table)).toBeVisible();
  });

  test("Organizations page shows the seeded Acme Dev Org with alice as owner", async ({ page }) => {
    await page.getByRole("link", { name: "Organizations", exact: true }).click();
    await expect(page.getByRole("heading", { name: "Organizations" })).toBeVisible();
    await page.getByRole("link", { name: "Acme Dev Org" }).click();
    await expect(page.getByRole("heading", { name: "Acme Dev Org" })).toBeVisible();
    await expect(page.getByText("Your role:")).toContainText("owner");
    await expect(page.getByRole("cell", { name: "bob@example.test" })).toBeVisible();
    await expect(page.getByRole("cell", { name: "carol@example.test" })).toBeVisible();
  });

  test("Storage page renders usage meters with real numbers", async ({ page }) => {
    await page.getByRole("link", { name: "Storage", exact: true }).click();
    await expect(page.getByRole("heading", { name: "Storage" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Personal" })).toBeVisible();
    // Both the personal and the org usage meter render "... used (...%)" —
    // assert at least one, rather than a single ambiguous match.
    await expect(page.getByText(/used \(/).first()).toBeVisible();
  });

  test("Account page shows profile, password, MFA, and sessions — Security merged in, not a separate tab", async ({
    page,
  }) => {
    await page.getByRole("link", { name: "Account", exact: true }).click();
    // "Account" also substring-matches the "Deactivate account" heading
    // further down the page — require the exact h1.
    await expect(page.getByRole("heading", { name: "Account", exact: true })).toBeVisible();
    await expect(page.getByText(SEED_EMAIL)).toBeVisible();
    await expect(page.getByText("active", { exact: true })).toBeVisible();

    await expect(page.getByRole("heading", { name: "Password" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Two-factor authentication" })).toBeVisible();
    await expect(page.getByText("Enable two-factor authentication")).toBeVisible();
    await expect(page.getByRole("heading", { name: "Active sessions" })).toBeVisible();
    // alice's account accumulates one session per login across every test
    // (and every manual run) — assert "at least one", not exactly one.
    const rowCount = await page.locator("table.data-table tbody tr").count();
    expect(rowCount).toBeGreaterThanOrEqual(1);

    await expect(page.getByRole("link", { name: "Security", exact: true })).toHaveCount(0);
  });
});
