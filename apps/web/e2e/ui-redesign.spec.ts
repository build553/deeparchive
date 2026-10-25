import { test, expect } from "@playwright/test";
import { loginAsAdmin, login, SEED_EMAIL, SEED_PASSWORD } from "./helpers";

test.describe("UI redesign (design tokens, cards, responsive, focus)", () => {
  test("login page: card elevation, focus ring, no header", async ({ page }) => {
    const consoleErrors: string[] = [];
    page.on("console", (msg) => {
      if (msg.type() === "error") consoleErrors.push(msg.text());
    });
    page.on("pageerror", (err) => consoleErrors.push(err.message));

    await page.goto("/login");
    await expect(page.getByRole("heading", { name: "Log in" })).toBeVisible();
    await expect(page.locator(".app-header")).toHaveCount(0);

    const card = page.locator(".auth-form");
    const shadow = await card.evaluate((el) => getComputedStyle(el).boxShadow);
    expect(shadow).not.toBe("none");

    await page.getByLabel("Email").focus();
    const outline = await page.getByLabel("Email").evaluate((el) => getComputedStyle(el).boxShadow);
    expect(outline).not.toBe("none");

    expect(consoleErrors, `console errors: ${consoleErrors.join(" | ")}`).toHaveLength(0);
  });

  test("register page: password hint toggles to error state under 12 chars", async ({ page }) => {
    await page.goto("/register");
    const hint = page.locator("#password-hint");
    await expect(hint).toBeVisible();
    await expect(hint).not.toHaveClass(/form-error/);

    await page.getByLabel("Password").fill("short");
    await expect(hint).toHaveClass(/form-error/);

    await page.getByLabel("Password").fill("a-long-enough-password");
    await expect(hint).not.toHaveClass(/form-error/);
  });

  test("logged in as alice: header, cards, nav active state, no horizontal overflow", async ({ page }) => {
    const consoleErrors: string[] = [];
    page.on("console", (msg) => {
      if (msg.type() === "error") consoleErrors.push(msg.text());
    });

    await login(page, SEED_EMAIL, SEED_PASSWORD);
    await expect(page.getByRole("heading", { name: "Devices" })).toBeVisible();
    await expect(page.locator(".app-header")).toBeVisible();

    const devicesLink = page.getByRole("link", { name: "Devices" });
    await expect(devicesLink).toHaveClass(/active/);

    for (const [path, heading] of [
      ["/backups", "Backups"],
      ["/organizations", "Organizations"],
      ["/storage", "Storage"],
      ["/account", "Account"],
    ] as const) {
      await page.goto(path);
      await expect(page.getByRole("heading", { name: heading, exact: true })).toBeVisible();
      const scrollWidth = await page.evaluate(() => document.documentElement.scrollWidth);
      const clientWidth = await page.evaluate(() => document.documentElement.clientWidth);
      expect(scrollWidth, `${path} has horizontal overflow`).toBeLessThanOrEqual(clientWidth + 1);
    }

    expect(consoleErrors, `console errors: ${consoleErrors.join(" | ")}`).toHaveLength(0);
  });

  test("mobile viewport (390x844): header nav scrolls instead of wrapping, no page overflow", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await login(page, SEED_EMAIL, SEED_PASSWORD);
    await expect(page.getByRole("heading", { name: "Devices" })).toBeVisible();

    const nav = page.locator(".app-header nav");
    const overflowX = await nav.evaluate((el) => getComputedStyle(el).overflowX);
    expect(overflowX).toBe("auto");

    const scrollWidth = await page.evaluate(() => document.documentElement.scrollWidth);
    const clientWidth = await page.evaluate(() => document.documentElement.clientWidth);
    expect(scrollWidth).toBeLessThanOrEqual(clientWidth + 1);

    for (const [path, heading] of [
      ["/", "Devices"],
      ["/backups", "Backups"],
      ["/organizations", "Organizations"],
      ["/storage", "Storage"],
      ["/account", "Account"],
    ] as const) {
      await page.goto(path);
      await expect(page.getByRole("heading", { name: heading, exact: true })).toBeVisible();
      const sw = await page.evaluate(() => document.documentElement.scrollWidth);
      const cw = await page.evaluate(() => document.documentElement.clientWidth);
      expect(sw, `${path} overflows at mobile width`).toBeLessThanOrEqual(cw + 1);
    }
  });

  test("admin login: email-OTP step renders and completes, admin nav + stat cards render", async ({ page }) => {
    const consoleErrors: string[] = [];
    page.on("console", (msg) => {
      if (msg.type() === "error") consoleErrors.push(msg.text());
    });

    // The first login attempt intentionally gets a 401 (admin_otp_required)
    // — the app handles it and shows the OTP step; the browser still logs
    // the failed fetch to the console, which isn't an app error (filtered
    // out below).
    await loginAsAdmin(page);

    await expect(page.getByRole("heading", { name: "Devices" })).toBeVisible();
    await expect(page.getByRole("link", { name: "Admin" })).toBeVisible();

    await page.getByRole("link", { name: "Admin" }).click();
    await expect(page.getByRole("heading", { name: "Admin panel" })).toBeVisible();

    const statCard = page.locator(".admin-stat-card").first();
    await expect(statCard).toBeVisible();
    const shadowBefore = await statCard.evaluate((el) => getComputedStyle(el).boxShadow);
    await statCard.hover();
    await expect
      .poll(() => statCard.evaluate((el) => getComputedStyle(el).boxShadow))
      .not.toBe(shadowBefore);

    const unexpected = consoleErrors.filter((e) => !e.includes("401 (Unauthorized)"));
    expect(unexpected, `console errors: ${unexpected.join(" | ")}`).toHaveLength(0);
  });
});
