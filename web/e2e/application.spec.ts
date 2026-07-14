import { expect, test } from "@playwright/test";
import { mockApplication } from "./fixtures";

test("email or passkey user opens a real empty-position dashboard", async ({ page }) => {
  await mockApplication(page);
  await page.addInitScript(() => localStorage.setItem("robin:e2e-auth", "logged-out"));
  await page.goto("/app");
  await expect(page.getByRole("heading", { name: "Run Robin from one account." })).toBeVisible();
  await expect(page.getByText("create, fund, and operate your live AAPL basis agent", { exact: false })).toBeVisible();
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("heading", { name: "Strategy overview" })).toBeVisible();
  await expect(page.getByText("1,000 USDG").first()).toBeVisible();
  await expect(page.getByText("Robinhood Chain mainnet", { exact: true })).toBeVisible();
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

test("onboarding creates the fixed live agent and execution account", async ({ page }) => {
  await mockApplication(page, { withVault: false, withAgent: false });
  await page.goto("/app/onboarding");
  await expect(page.getByRole("heading", { name: "Launch your AAPL agent" })).toBeVisible();
  await expect(page.getByText("basis-aapl-v1")).toBeVisible();
  await page.getByRole("button", { name: "Create execution account" }).click();
  await expect(page).toHaveURL(/\/app\/strategy$/);
  await expect(page.getByRole("heading", { name: "Agent provisioning" })).toBeVisible();
});

test("user can close an agent that is stuck before coordinator registration", async ({ page }) => {
  await mockApplication(page, { withVault: false, withAgent: false });
  await page.goto("/app/onboarding");
  await page.getByRole("button", { name: "Create execution account" }).click();
  await expect(page.getByRole("heading", { name: "Agent provisioning" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Launch agent" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Pause and unwind" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Resume agent" })).toHaveCount(0);
  await page.getByRole("button", { name: "Close agent" }).click();
  await expect(page.getByRole("heading", { name: "Agent closed" })).toBeVisible();
});

test("wallet conflict opens the account recovery path", async ({ page }) => {
  await mockApplication(page);
  await page.route("**/api/app/v1/me/wallets/sync", (route) => route.fulfill({
    status: 409,
    contentType: "application/json",
    body: JSON.stringify({ error: "conflict", message: "This wallet is linked to another account." }),
  }));
  await page.goto("/app/wallets");
  await page.getByRole("button", { name: "Link wallet" }).click();
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
  await expect(page.getByRole("heading", { name: "Run Robin from one account." })).toBeVisible();
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

test("user launches a Robin agent from the strategy page", async ({ page }) => {
  await mockApplication(page, { withAgent: false });
  await page.goto("/app/strategy");
  await expect(page.getByRole("heading", { name: "Not launched" })).toBeVisible();
  await page.getByRole("button", { name: "Create mainnet agent" }).click();
  await expect(page.getByRole("heading", { name: "Agent setup" })).toBeVisible();
  await expect(page.getByText("basis-aapl-v1")).toBeVisible();
  await expect(page.getByText("Robinhood USDG")).toBeVisible();
  await expect(page.getByText("Lighter USDC")).toBeVisible();
  await expect(page.getByText("Pays deployment and owner transactions without sponsorship", { exact: false })).toBeVisible();
});

test("owner pays ETH to deploy the prepared mainnet graph", async ({ page }) => {
  await mockApplication(page, { withAgent: false });
  await page.goto("/app/strategy");
  await page.getByRole("button", { name: "Create mainnet agent" }).click();
  await page.getByRole("button", { name: "Set up execution" }).click();
  await page.getByRole("button", { name: "Prepare Robinhood deployment" }).click();
  await expect(page.getByRole("button", { name: "Deploy with owner ETH" })).toBeVisible();
  await page.getByRole("button", { name: "Deploy with owner ETH" }).click();
  await expect(page.getByRole("button", { name: "Authorize execution agent" })).toBeVisible();
  await page.getByRole("button", { name: "Authorize execution agent" }).click();
  await expect(page.getByText("Robinhood request robinhood-request: linked", { exact: false })).toBeVisible();
});

test("user discovers and links an empty Lighter subaccount without numeric inputs", async ({ page }) => {
  await mockApplication(page, { withAgent: false });
  await page.goto("/app/strategy");
  await page.getByRole("button", { name: "Create mainnet agent" }).click();
  await page.getByRole("button", { name: "Set up execution" }).click();
  await expect(page.getByLabel("Lighter subaccount index")).toHaveCount(0);
  await expect(page.getByLabel("Lighter change nonce")).toHaveCount(0);
  await expect(page.getByRole("link", { name: "create an empty subaccount in Lighter ↗" })).toHaveAttribute("href", "https://app.lighter.xyz/");
  await page.getByRole("button", { name: "Find empty Lighter subaccount" }).click();
  await expect(page.getByRole("button", { name: "Sign Lighter association" })).toBeVisible();
  await page.getByRole("button", { name: "Sign Lighter association" }).click();
  await expect(page.getByText("Lighter request lighter-request: linked", { exact: false })).toBeVisible();
});

test("missing Lighter subaccount gives a direct create-and-retry path", async ({ page }) => {
  await mockApplication(page, { withAgent: false });
  await page.route("**/api/app/v1/agents/*/lighter/link-request", (route) => route.fulfill({
    status: 400,
    contentType: "application/json",
    body: JSON.stringify({
      error: "invalid_request",
      message: "No eligible empty Lighter subaccount found; create a new empty subaccount in the Lighter app, then retry.",
    }),
  }));
  await page.goto("/app/strategy");
  await page.getByRole("button", { name: "Create mainnet agent" }).click();
  await page.getByRole("button", { name: "Set up execution" }).click();
  await page.getByRole("button", { name: "Find empty Lighter subaccount" }).click();
  await expect(page.getByText("No eligible empty Lighter subaccount found", { exact: false })).toBeVisible();
  await expect(page.getByRole("link", { name: "create an empty subaccount in Lighter ↗" })).toBeVisible();
});

test("deployment finality retry reuses the submitted transaction", async ({ page }) => {
  await mockApplication(page, { withAgent: false });
  let confirmations = 0;
  await page.route("**/api/app/v1/agents/*/robinhood/confirm", async (route) => {
    confirmations += 1;
    if (confirmations === 1) {
      await route.fulfill({
        status: 503,
        contentType: "application/json",
        body: JSON.stringify({ error: "not_final", message: "Deployment is awaiting finality." }),
      });
      return;
    }
    await route.fallback();
  });
  await page.goto("/app/strategy");
  await page.getByRole("button", { name: "Create mainnet agent" }).click();
  await page.getByRole("button", { name: "Set up execution" }).click();
  await page.getByRole("button", { name: "Prepare Robinhood deployment" }).click();
  await page.getByRole("button", { name: "Deploy with owner ETH" }).click();
  await expect(page.getByRole("button", { name: "Retry finality check" })).toBeVisible();
  expect(await page.evaluate(() => localStorage.getItem("robin:mainnet-deployment:robinhood-request"))).toContain("0xcdcd");
  await page.getByRole("button", { name: "Retry finality check" }).click();
  await expect(page.getByRole("button", { name: "Authorize execution agent" })).toBeVisible();
  await page.getByRole("button", { name: "Authorize execution agent" }).click();
  await expect(page.getByText("Robinhood request robinhood-request: linked", { exact: false })).toBeVisible();
  expect(await page.evaluate(() => localStorage.getItem("robin:mainnet-deployment:robinhood-request"))).toBeNull();
  expect(confirmations).toBe(2);
});

test("user completes the live mainnet lifecycle from account setup to withdrawal", async ({ page }) => {
  const journey = await mockApplication(page, { withVault: false, withAgent: false, liveJourney: true });
  await page.goto("/app/onboarding");
  await page.getByRole("button", { name: "Create execution account" }).click();
  await expect(page).toHaveURL(/\/app\/strategy$/);

  await page.getByRole("button", { name: "Find empty Lighter subaccount" }).click();
  await page.getByRole("button", { name: "Sign Lighter association" }).click();
  await page.getByRole("button", { name: "Prepare Robinhood deployment" }).click();
  await page.getByRole("button", { name: "Deploy with owner ETH" }).click();
  await page.getByRole("button", { name: "Authorize execution agent" }).click();

  await page.getByLabel("USDG to deposit").fill("25");
  await page.getByRole("button", { name: "Deposit USDG with owner ETH" }).click();
  journey.observeRobinhoodFunding();
  await expect(page.getByText("Fund Lighter account 42 with USDC", { exact: false })).toBeVisible();
  journey.observeLighterFunding();
  journey.observeExecutionGas();
  for (const requirement of ["Robinhood USDG", "Lighter USDC", "User ETH gas", "Execution ETH gas"]) {
    await expect(page.getByText(requirement, { exact: true })).toBeVisible();
  }
  await expect(page.getByText("Ready", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Launch agent" }).click();
  await expect(page.getByRole("heading", { name: "Agent running" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Live execution" })).toBeVisible();
  await expect(page.getByText("hedged", { exact: true })).toBeVisible();
  await expect(page.getByText("0.1 AAPL", { exact: true })).toBeVisible();
  await expect(page.getByText("0.1 AAPL-PERP", { exact: true })).toBeVisible();
  await expect(page.getByText("50 USD", { exact: true })).toBeVisible();
  await expect(page.getByText("Lighter entry order lighte…-123", { exact: false })).toBeVisible();
  await expect(page.getByRole("link", { name: "Robinhood entry transaction", exact: false })).toHaveAttribute("href", /\/tx\/0x3434/);
  await page.goto("/app");
  await expect(page.getByRole("heading", { name: "Live execution" })).toBeVisible();
  await expect(page.getByText("0.1 AAPL", { exact: true })).toBeVisible();
  await page.goto("/app/activity");
  await expect(page.getByRole("heading", { name: "Live execution" })).toBeVisible();
  await expect(page.getByText("0.1 AAPL-PERP", { exact: true })).toBeVisible();
  await page.goto("/app/strategy");
  await page.getByRole("button", { name: "Pause and unwind" }).click();
  await expect(page.getByRole("heading", { name: "Agent reducing" })).toBeVisible();
  await expect(page.getByText("Reducing", { exact: true })).toBeVisible();
  await page.evaluate(() => localStorage.removeItem("robin:agent-command:agent-id:pause"));
  await page.reload();
  await expect(page.getByRole("heading", { name: "Agent reducing" })).toBeVisible();
  journey.reconcilePause();
  await expect(page.getByRole("heading", { name: "Agent paused" })).toBeVisible();
  await expect(page.getByText("Flat", { exact: true })).toBeVisible();
  await expect(page.getByText("Lighter unwind order lighte…-456", { exact: false })).toBeVisible();
  await expect(page.getByRole("link", { name: "Robinhood unwind transaction", exact: false })).toHaveAttribute("href", /\/tx\/0x7878/);
  await page.getByRole("button", { name: "Resume agent" }).click();
  await expect(page.getByRole("heading", { name: "Agent running" })).toBeVisible();
  await page.getByRole("button", { name: "Close agent" }).click();
  await expect(page.getByRole("heading", { name: "Agent closed" })).toBeVisible();
  await expect(page.getByText("Flat", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Prepare owner withdrawal" }).click();
  await expect(page.getByRole("button", { name: "Prepare owner withdrawal" })).toBeHidden();
  await page.getByRole("button", { name: "Sign owner withdrawal" }).click();
  await expect(page.getByRole("button", { name: "Awaiting reconciliation" })).toBeVisible();
  await page.reload();
  await expect(page.getByRole("button", { name: "Awaiting reconciliation" })).toBeVisible();
  journey.reconcileWithdrawal();
  await expect(page.getByText("Owner withdrawal completed.", { exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: "Submitted owner transaction", exact: false })).toHaveAttribute("href", /\/tx\/0xefef/);
  await page.reload();
  await expect(page.getByRole("link", { name: "Submitted owner transaction", exact: false })).toHaveAttribute("href", /\/tx\/0xefef/);
});
