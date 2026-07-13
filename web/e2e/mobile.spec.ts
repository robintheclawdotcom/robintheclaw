import { expect, test } from "@playwright/test";
import { mockApplication } from "./fixtures";

test("application navigation and dashboard remain usable on mobile", async ({ page }) => {
  await mockApplication(page);
  await page.goto("/app");
  await expect(page.getByRole("heading", { name: "Your strategy account" })).toBeVisible();
  await page.getByRole("button", { name: "Open navigation" }).click();
  await expect(page.getByRole("link", { name: "Wallets", exact: true })).toBeVisible();
  await page.getByRole("link", { name: "Wallets", exact: true }).click();
  await expect(page.getByRole("heading", { name: "One account, multiple funding sources" })).toBeVisible();
});
