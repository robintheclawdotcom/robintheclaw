import { expect, test } from "@playwright/test";

test("documentation has a dedicated route", async ({ page }) => {
  await page.goto("/docs");

  await expect(page).toHaveURL(/\/docs$/);
  await expect(page.getByRole("heading", { name: "Overview" })).toBeVisible();
  await expect(page.getByRole("link", { name: "docs", exact: true }).first()).toHaveClass(/nav-active/);

  await page.getByRole("link", { name: "home", exact: true }).first().click();
  await expect(page).toHaveURL(/\/$/);
});
