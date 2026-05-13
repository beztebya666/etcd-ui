// Login flow tests. Stubs /api/auth/providers and /api/auth/me so the test
// doesn't need a live OIDC issuer or basic-auth backend. The point is the SPA
// rendering — backend tests for the same flow live in Go.

import { test, expect } from "@playwright/test";

test.describe("Login page", () => {
  test("renders Basic form when only basic provider is enabled", async ({ page }) => {
    await page.route("**/api/auth/providers", (route) =>
      route.fulfill({ json: { basic: true, oidc: false } }),
    );
    await page.route("**/api/auth/me", (route) =>
      route.fulfill({ status: 401, body: "" }),
    );
    await page.goto("/login");
    await expect(page.getByRole("heading", { name: /sign in/i })).toBeVisible();
    await expect(page.getByLabel("Username")).toBeVisible();
    await expect(page.getByLabel("Password")).toBeVisible();
    await expect(page.getByRole("link", { name: /sign in with sso/i })).toBeHidden();
  });

  test("shows SSO button when oidc provider is enabled", async ({ page }) => {
    await page.route("**/api/auth/providers", (route) =>
      route.fulfill({ json: { basic: true, oidc: true } }),
    );
    await page.goto("/login");
    await expect(page.getByRole("link", { name: /sign in with sso/i })).toBeVisible();
    // SSO link must carry returnTo so the IdP bounces back to the right page.
    const href = await page.getByRole("link", { name: /sign in with sso/i }).getAttribute("href");
    expect(href).toContain("/api/auth/oidc/login");
    expect(href).toContain("returnTo=");
  });

  test("surfaces auth-disabled state", async ({ page }) => {
    await page.route("**/api/auth/providers", (route) =>
      route.fulfill({ json: { basic: false, oidc: false } }),
    );
    await page.goto("/login");
    await expect(page.getByText(/auth isn't configured/i)).toBeVisible();
  });

  test("bad credentials show inline error", async ({ page }) => {
    await page.route("**/api/auth/providers", (route) =>
      route.fulfill({ json: { basic: true, oidc: false } }),
    );
    await page.route("**/api/auth/login", (route) =>
      route.fulfill({ status: 401, body: "invalid credentials" }),
    );
    await page.goto("/login");
    await page.getByLabel("Username").fill("alice");
    await page.getByLabel("Password").fill("wrong");
    await page.getByRole("button", { name: /^sign in$/i }).click();
    await expect(page.getByText("invalid credentials")).toBeVisible();
  });
});
