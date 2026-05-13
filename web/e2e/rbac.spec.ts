// RBAC page (etcd-side auth + Permissions for the etcd-ui-side ACL matrix).

import { test, expect } from "@playwright/test";

test.describe("RBAC", () => {
  test("empty-state offers Enable when status.enabled=false", async ({ page }) => {
    await page.route("**/api/clusters/local/rbac/status", (route) =>
      route.fulfill({ json: { enabled: false } }),
    );
    await page.route("**/api/clusters/local/rbac/users", (route) =>
      route.fulfill({ json: [] }),
    );
    await page.route("**/api/clusters/local/rbac/roles", (route) =>
      route.fulfill({ json: [] }),
    );
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
            dbSizeBytes: 0,
            dbSizeInUse: 0,
            revision: 0,
            raftTerm: 0,
            lastChecked: new Date().toISOString(),
          },
        ],
      }),
    );

    await page.goto("/rbac");
    const skip = page.getByRole("button", { name: /skip tour/i });
    if (await skip.isVisible({ timeout: 1500 }).catch(() => false)) await skip.click();

    await expect(page.getByText(/auth is disabled on this cluster/i)).toBeVisible();
    await expect(page.getByRole("button", { name: /enable rbac/i })).toBeVisible();
  });

  test("permissions matrix renders rules", async ({ page }) => {
    await page.route("**/api/acl", (route) =>
      route.fulfill({
        json: {
          loaded: true,
          rules: [
            { user: "alice", cluster: "prod", prefix: "/svc/", access: "write" },
            { user: "alice", cluster: "prod", access: "read" },
            { user: "oncall", cluster: "*", access: "admin" },
          ],
        },
      }),
    );
    await page.route("**/api/clusters", (route) =>
      route.fulfill({ json: [] }),
    );

    await page.goto("/permissions");
    const skip = page.getByRole("button", { name: /skip tour/i });
    if (await skip.isVisible({ timeout: 1500 }).catch(() => false)) await skip.click();

    await expect(page.getByText("alice")).toBeVisible();
    await expect(page.getByText("oncall")).toBeVisible();
    await expect(page.getByText("admin", { exact: false })).toBeVisible();
    await expect(page.getByText("/svc/")).toBeVisible();
  });
});
