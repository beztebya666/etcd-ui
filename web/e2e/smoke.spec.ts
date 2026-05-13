import { test, expect } from "@playwright/test";

test.describe("etcd-ui smoke", () => {
  test("loads dashboard and shows nav", async ({ page }) => {
    await page.goto("/");
    // Onboarding overlay covers the screen on first load — dismiss it.
    const skip = page.getByRole("button", { name: /skip tour/i });
    if (await skip.isVisible({ timeout: 2000 }).catch(() => false)) {
      await skip.click();
    }
    await expect(page.getByRole("heading", { name: /fleet overview|обзор/i })).toBeVisible();

    // Sidebar nav entries
    for (const label of ["Dashboard", "Keys", "Watch", "Cluster", "Settings"]) {
      await expect(page.getByRole("link", { name: label })).toBeVisible();
    }
  });

  test("command palette opens via shortcut", async ({ page }) => {
    await page.goto("/");
    const skip = page.getByRole("button", { name: /skip tour/i });
    if (await skip.isVisible({ timeout: 2000 }).catch(() => false)) await skip.click();
    await page.keyboard.press("ControlOrMeta+K");
    await expect(page.getByPlaceholder(/type a command/i)).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(page.getByPlaceholder(/type a command/i)).toBeHidden();
  });

  test("settings page renders theme + language toggles", async ({ page }) => {
    await page.goto("/settings");
    const skip = page.getByRole("button", { name: /skip tour/i });
    if (await skip.isVisible({ timeout: 2000 }).catch(() => false)) await skip.click();
    await expect(page.getByRole("button", { name: "Dark" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Light" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Русский" })).toBeVisible();
  });
});
