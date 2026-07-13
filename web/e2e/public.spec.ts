import { expect, test } from "@playwright/test";

test("homepage leads with no-code strategy access", async ({ page }) => {
  await page.goto("/");

  await expect(page.getByText("Open your strategy account", { exact: true })).toBeVisible();
  await expect(page.getByText("typed contract layer live", { exact: false })).toBeVisible();
  await expect(page.getByText("Source-verified governance, custody, risk, and routing", { exact: false })).toBeVisible();
  await expect(page.getByText("No extension, seed phrase, CLI", { exact: false })).toBeVisible();
  await expect(page.getByText(/testnet/i)).toHaveCount(0);
  await expect(page.getByText("git clone", { exact: false })).toHaveCount(0);
  await expect(page.getByRole("link", { name: "open app", exact: false }).first()).toHaveAttribute("href", "/app");
  await expect(page.getByRole("link", { name: "inspect onchain", exact: false }).first()).toHaveAttribute(
    "href",
    "https://robinhoodchain.blockscout.com/tx/0xe8b7ca77feaf117e287eab146d7e79bdef83737a93453534bc9077da0e0ac961",
  );
});

test("documentation has a dedicated route", async ({ page }) => {
  await page.goto("/docs");

  await expect(page).toHaveURL(/\/docs$/);
  await expect(page.getByRole("heading", { name: "Overview" })).toBeVisible();
  await expect(page.getByRole("link", { name: "docs", exact: true }).first()).toHaveClass(/nav-active/);

  await page.getByRole("link", { name: "home", exact: true }).first().click();
  await expect(page).toHaveURL(/\/$/);
});
