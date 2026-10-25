import { test, expect } from "@playwright/test";
import { login, SEED_EMAIL, SEED_PASSWORD } from "./helpers";

const API_BASE_URL = "http://localhost:8080";

test("device code approval via the browser page", async ({ page }) => {
  const startResp = await fetch(`${API_BASE_URL}/api/v1/auth/device/code`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ client_name: "backup-cli" }),
  });
  const start = await startResp.json();

  await login(page, SEED_EMAIL, SEED_PASSWORD);
  await expect(page.getByRole("heading", { name: "Devices" })).toBeVisible();

  // Simulate clicking the verification_uri_complete link the CLI prints.
  await page.goto(`/device?code=${start.user_code}`);
  await expect(page.getByText("wants to sign in to your DeepArchive account")).toBeVisible();
  await page.getByRole("button", { name: "Approve" }).click();
  await expect(page.getByRole("heading", { name: "Device connected" })).toBeVisible();

  // The CLI's poll should now redeem a real session.
  let resolved = false;
  for (let i = 0; i < 5 && !resolved; i++) {
    const tokenResp = await fetch(`${API_BASE_URL}/api/v1/auth/device/token`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ device_code: start.device_code }),
    });
    const body = await tokenResp.json();
    if (body.access_token) {
      resolved = true;
      expect(body.refresh_token).toBeTruthy();
    } else {
      await new Promise((r) => setTimeout(r, 1500));
    }
  }
  expect(resolved).toBe(true);
});

test("device code deny via the browser page", async ({ page }) => {
  const startResp = await fetch(`${API_BASE_URL}/api/v1/auth/device/code`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ client_name: "backup-cli" }),
  });
  const start = await startResp.json();

  await login(page, SEED_EMAIL, SEED_PASSWORD);
  await expect(page.getByRole("heading", { name: "Devices" })).toBeVisible();
  await page.goto("/device");
  await page.getByPlaceholder("XXXX-XXXX").fill(start.user_code);
  await page.getByRole("button", { name: "Continue" }).click();
  await expect(page.getByText("wants to sign in")).toBeVisible();
  await page.getByRole("button", { name: "Deny" }).click();
  await expect(page.getByRole("heading", { name: "Request denied" })).toBeVisible();
});

test("invalid code shows an error, not a crash", async ({ page }) => {
  await login(page, SEED_EMAIL, SEED_PASSWORD);
  await expect(page.getByRole("heading", { name: "Devices" })).toBeVisible();
  await page.goto("/device");
  await page.getByPlaceholder("XXXX-XXXX").fill("ZZZZ-ZZZZ");
  await page.getByRole("button", { name: "Continue" }).click();
  await expect(page.getByText(/invalid|expired/i)).toBeVisible();
});

test("logged-out visit to the CLI's link signs in, then returns to the approval prompt", async ({ page }) => {
  const startResp = await fetch(`${API_BASE_URL}/api/v1/auth/device/code`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ client_name: "backup-cli" }),
  });
  const start = await startResp.json();

  await page.goto(`/device?code=${start.user_code}`);
  await expect(page).toHaveURL(/\/login$/);
  await page.getByLabel("Email").fill(SEED_EMAIL);
  await page.getByLabel("Password", { exact: true }).fill(SEED_PASSWORD);
  await page.getByRole("button", { name: "Log in" }).click();

  await expect(page.getByText("wants to sign in to your DeepArchive account")).toBeVisible();
  await expect(page).toHaveURL(/\/device\?code=/);
});
