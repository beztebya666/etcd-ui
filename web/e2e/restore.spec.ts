// Audit → Restore flow. After a delete is recorded with prev=<base64>, the
// Audit page must show a Restore button that issues PUT with the recovered
// value.

import { test, expect } from "@playwright/test";

test.describe("Audit restore", () => {
  test.beforeEach(async ({ page }) => {
    await page.route("**/api/auth/me", (route) =>
      route.fulfill({ json: { user: "alice", src: "basic" } }),
    );
  });

  test("renders Restore button on kv.delete with prev", async ({ page }) => {
    const prevB64 = Buffer.from("hello-world").toString("base64");
    await page.route("**/api/audit/events**", (route) =>
      route.fulfill({
        json: [
          {
            id: 1,
            time: new Date().toISOString(),
            actor: "alice",
            source: "gateway",
            method: "POST",
            cluster: "local",
            path: "/api/clusters/local/delete",
            action: "kv.delete",
            key: "/foo/bar",
            status: 200,
            note: "prev=" + prevB64,
          },
        ],
      }),
    );
    await page.route("**/api/audit/events/stream", (route) =>
      route.fulfill({ status: 204, body: "" }),
    );

    let restored: { key?: string; value?: string } = {};
    await page.route("**/api/clusters/local/put", async (route) => {
      restored = JSON.parse(route.request().postData() || "{}");
      await route.fulfill({ json: { revision: 99 } });
    });

    await page.goto("/audit");
    const skip = page.getByRole("button", { name: /skip tour/i });
    if (await skip.isVisible({ timeout: 1500 }).catch(() => false)) await skip.click();

    await expect(page.getByText("/foo/bar")).toBeVisible();
    await page.getByRole("button", { name: /restore/i }).first().click();
    // Confirm dialog
    await page.getByRole("button", { name: /^restore$/i }).click();

    await expect.poll(() => restored.key).toBe("/foo/bar");
    expect(restored.value).toBe("hello-world");
  });

  test("no Restore button on events without prev=", async ({ page }) => {
    await page.route("**/api/audit/events**", (route) =>
      route.fulfill({
        json: [
          {
            id: 1,
            time: new Date().toISOString(),
            actor: "alice",
            source: "gateway",
            method: "POST",
            cluster: "local",
            path: "/api/clusters/local/put",
            action: "kv.put",
            key: "/foo/bar",
            status: 200,
          },
        ],
      }),
    );
    await page.route("**/api/audit/events/stream", (route) =>
      route.fulfill({ status: 204, body: "" }),
    );
    await page.goto("/audit");
    const skip = page.getByRole("button", { name: /skip tour/i });
    if (await skip.isVisible({ timeout: 1500 }).catch(() => false)) await skip.click();
    await expect(page.getByText("/foo/bar")).toBeVisible();
    await expect(page.getByRole("button", { name: /restore/i })).toHaveCount(0);
  });
});
