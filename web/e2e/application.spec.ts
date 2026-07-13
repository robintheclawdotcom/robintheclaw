import { expect, test } from "@playwright/test";
import { mockApplication } from "./fixtures";

test("email or passkey user opens a real empty-position dashboard", async ({ page }) => {
  await mockApplication(page);
  await page.addInitScript(() => localStorage.setItem("robin:e2e-auth", "logged-out"));
  await page.goto("/app");
  await expect(page.getByRole("heading", { name: "Autonomous strategy. Direct control." })).toBeVisible();
  await expect(page.getByText("email, passkey, or a connected wallet", { exact: false })).toBeVisible();
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("heading", { name: "Strategy overview" })).toBeVisible();
  await expect(page.getByText("1,000 USDG").first()).toBeVisible();
  await expect(page.getByText("Robinhood mainnet", { exact: true })).toBeVisible();
  await expect(page.getByText("No positions", { exact: true })).toBeVisible();
  await expect(page.getByText("No active positions")).toBeVisible();
});

test("multi-wallet funding preference never changes the vault owner", async ({ page }) => {
  await mockApplication(page);
  await page.goto("/app/wallets");
  await expect(page.getByRole("heading", { name: "Wallets", exact: true })).toBeVisible();
  await expect(page.getByText("Vault owner", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Funding wallet" })).toBeDisabled();
  await page.getByRole("button", { name: "Link wallet" }).click();
  await expect(page.getByText("Phantom", { exact: true })).toBeVisible();
  await expect(page.getByText("Vault ownership", { exact: true })).toBeVisible();
  await expect(page.getByText("0x1111…1111").last()).toBeVisible();
});

test("self-funded onboarding confirms the vault transaction", async ({ page }) => {
  await mockApplication(page, { withVault: false });
  await page.goto("/app/onboarding");
  await expect(page.getByRole("heading", { name: "Create your strategy vault" })).toBeVisible();
  await page.getByRole("button", { name: "Create vault" }).click();
  await expect(page).toHaveURL(/\/app$/);
  await expect(page.getByRole("heading", { name: "Strategy overview" })).toBeVisible();
  expect(await page.evaluate(() => localStorage.getItem("robin:onboarding-call-id"))).toBeNull();
});

test("interrupted onboarding resumes from the saved operation", async ({ page }) => {
  await mockApplication(page, { withVault: false });
  await page.addInitScript(() => localStorage.setItem("robin:onboarding-call-id", `0x${"ab".repeat(32)}`));
  await page.goto("/app/onboarding");
  await expect(page.getByRole("heading", { name: "Strategy vault active" })).toBeVisible();
  expect(await page.evaluate(() => localStorage.getItem("robin:onboarding-call-id"))).toBeNull();
});

test("wallet conflict opens the account recovery path", async ({ page }) => {
  await mockApplication(page);
  await page.route("**/api/app/v1/me/wallets/sync", (route) => route.fulfill({
    status: 409,
    contentType: "application/json",
    body: JSON.stringify({ error: "conflict", message: "This wallet is linked to another account." }),
  }));
  await page.goto("/app/wallets");
  await page.getByRole("button", { name: "Sync wallets" }).click();
  await expect(page.getByText("Wallet already linked")).toBeVisible();
  await expect(page.getByRole("button", { name: "Sign in to other account" })).toBeVisible();
});

test("expired API session returns to sign in", async ({ page }) => {
  await mockApplication(page);
  await page.route("**/api/app/v1/dashboard", (route) => route.fulfill({
    status: 401,
    contentType: "application/json",
    body: JSON.stringify({ error: "unauthorized", message: "authentication required" }),
  }));
  await page.goto("/app");
  await expect(page.getByRole("heading", { name: "Autonomous strategy. Direct control." })).toBeVisible();
});

test("funding and withdrawal controls complete without changing ownership", async ({ page }) => {
  await mockApplication(page);
  await page.goto("/app/strategy");
  await page.getByLabel("Amount to add").fill("10");
  await page.getByRole("button", { name: "Add funds" }).click();
  await expect(page.getByLabel("Amount to add")).toHaveValue("");
  await page.getByLabel("Amount to withdraw").fill("5");
  await page.getByRole("button", { name: "Withdraw", exact: true }).click();
  await expect(page.getByLabel("Amount to withdraw")).toHaveValue("");
  await expect(page.getByText("0x1111…1111").first()).toBeVisible();
});
