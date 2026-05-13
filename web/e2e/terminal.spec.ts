// Terminal page (etcdctl shell-out). Stubs /etcdctl so we don't need a real
// etcd binary in the test image — we only care about UI plumbing here.

import { test, expect } from "@playwright/test";

test.describe("Terminal", () => {
  test.beforeEach(async ({ page }) => {
    await page.route("**/api/clusters", (route) =>
      route.fulfill({
        json: [
          {
            id: "local",
            name: "local",
            source: "env",
            endpoints: ["http://127.0.0.1:2379"],
            healthy: true,
            memberCount: 1,
            dbSizeBytes: 1024,
            dbSizeInUse: 512,
            revision: 42,
            raftTerm: 3,
            lastChecked: new Date().toISOString(),
          },
        ],
      }),
    );
  });

  test("rejects empty input", async ({ page }) => {
    await page.goto("/terminal");
    const skip = page.getByRole("button", { name: /skip tour/i });
    if (await skip.isVisible({ timeout: 1500 }).catch(() => false)) await skip.click();
    const input = page.getByRole("textbox", { name: /etcdctl/i }).first();
    await input.focus();
    await page.keyboard.press("Enter");
    // No request fired — input is still empty.
    await expect(input).toHaveValue("");
  });

  test("inline `help` shows allowlist without round-trip", async ({ page }) => {
    await page.goto("/terminal");
    const skip = page.getByRole("button", { name: /skip tour/i });
    if (await skip.isVisible({ timeout: 1500 }).catch(() => false)) await skip.click();
    const input = page.getByRole("textbox", { name: /etcdctl/i }).first();
    await input.fill("help");
    await page.keyboard.press("Enter");
    await expect(page.getByText(/get put del txn member endpoint/i)).toBeVisible();
  });

  test("`member list` round-trips and renders output", async ({ page }) => {
    await page.route("**/api/clusters/local/etcdctl", (route) =>
      route.fulfill({
        json: {
          stdout: "+-----+---------+---------+\n| ID  | NAME    | …       |\n+-----+---------+---------+\n",
          stderr: "",
          exitCode: 0,
          durationMs: 12,
        },
      }),
    );
    await page.goto("/terminal");
    const skip = page.getByRole("button", { name: /skip tour/i });
    if (await skip.isVisible({ timeout: 1500 }).catch(() => false)) await skip.click();
    const input = page.getByRole("textbox", { name: /etcdctl/i }).first();
    await input.fill("member list");
    await page.keyboard.press("Enter");
    await expect(page.getByText(/\| ID +\| NAME/)).toBeVisible();
  });
});
