import { test, expect } from "@playwright/test";
import { login, SEED_EMAIL, SEED_PASSWORD, uniqueEmail, acceptSignupTerms } from "./helpers";

test.describe("Authentication", () => {
  test("unauthenticated visitor is redirected to /login", async ({ page }) => {
    await page.goto("/");
    await expect(page).toHaveURL(/\/login$/);
    await expect(page.getByRole("heading", { name: "Log in" })).toBeVisible();
  });

  test("login page renders the form", async ({ page }) => {
    await page.goto("/login");
    await expect(page.getByLabel("Email")).toBeVisible();
    await expect(page.getByLabel("Password", { exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: "Log in" })).toBeVisible();
    await expect(page.getByRole("link", { name: "Register" })).toBeVisible();
    await expect(page.getByRole("link", { name: /Dev: view sent emails/ })).toBeVisible();
  });

  test("wrong password shows a generic error, no account enumeration", async ({ page }) => {
    await page.goto("/login");
    await page.getByLabel("Email").fill(SEED_EMAIL);
    await page.getByLabel("Password", { exact: true }).fill("definitely-wrong-password");
    await page.getByRole("button", { name: "Log in" }).click();
    await expect(page.getByText(/invalid email or password/i)).toBeVisible();
    await expect(page).toHaveURL(/\/login$/);
  });

  test("a nonexistent account gets the exact same error (no enumeration)", async ({ page }) => {
    await page.goto("/login");
    await page.getByLabel("Email").fill(uniqueEmail("nobody"));
    await page.getByLabel("Password", { exact: true }).fill("whatever-password-123");
    await page.getByRole("button", { name: "Log in" }).click();
    await expect(page.getByText(/invalid email or password/i)).toBeVisible();
  });

  test("seeded account logs in and lands on the Devices page", async ({ page }) => {
    await login(page, SEED_EMAIL, SEED_PASSWORD);
    await expect(page).toHaveURL("http://localhost:5173/");
    await expect(page.getByRole("heading", { name: "Devices" })).toBeVisible();
    await expect(page.locator(".app-header-user")).toContainText("Alice");
  });

  test("logout returns to the login page and re-protects routes", async ({ page }) => {
    await login(page, SEED_EMAIL, SEED_PASSWORD);
    await expect(page.getByRole("heading", { name: "Devices" })).toBeVisible();
    await page.getByRole("button", { name: "Log out" }).click();
    await expect(page).toHaveURL(/\/login$/);
    await page.goto("/backups");
    await expect(page).toHaveURL(/\/login$/);
  });

  test("forgot-password link reaches a real form", async ({ page }) => {
    await page.goto("/login");
    await page.getByRole("link", { name: "Forgot password?" }).click();
    await expect(page).toHaveURL(/\/forgot-password$/);
    await expect(page.getByRole("heading", { name: "Forgot password" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Send reset link" })).toBeVisible();
  });

  test("register page renders and enforces the password policy client-side", async ({ page }) => {
    await page.goto("/register");
    await expect(page.getByRole("heading", { name: "Create an account" })).toBeVisible();
    const password = page.getByLabel("Password");
    await expect(password).toHaveAttribute("minlength", "12");
  });

  test("registering a new account shows the enumeration-safe confirmation", async ({ page }) => {
    await page.goto("/register");
    await page.getByLabel("Display name").fill("Playwright Test User");
    await page.getByLabel("Email").fill(uniqueEmail("pw-register"));
    await page.getByLabel("Password").fill("a-perfectly-fine-password-123");
    await acceptSignupTerms(page);
    await page.getByRole("button", { name: "Create account" }).click();
    await expect(page.getByRole("heading", { name: "Check your email" })).toBeVisible();
    await expect(page.getByRole("link", { name: /Dev: view sent emails/ })).toBeVisible();
  });
});
