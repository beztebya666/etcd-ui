// Generates README screenshots automatically. Run:
//
//   ETCD_ENDPOINTS=http://localhost:2379 make build-image
//   docker run -d --rm --name etcd-ui-screenshots -p 18080:8080 etcd-ui:dev
//   E2E_URL=http://localhost:18080 npx playwright test e2e/screenshots.spec.ts
//   docker stop etcd-ui-screenshots
//
// Output: docs/images/<name>.png at 2x DPR for crisp Retina rendering. The
// suite also drives a small click-through and exports each step — those PNGs
// are easy to assemble into a demo GIF with `convert` (ImageMagick) or `gifski`.

import { test, type Page } from "@playwright/test";

const OUT = "docs/images";

test.use({
  viewport: { width: 1440, height: 900 },
  deviceScaleFactor: 2,
  colorScheme: "dark",
});

async function settle(page: Page) {
  // Dismiss the onboarding overlay so it doesn't dominate every screenshot.
  const skip = page.getByRole("button", { name: /skip tour/i });
  if (await skip.isVisible({ timeout: 1500 }).catch(() => false)) await skip.click();
  await page.waitForLoadState("networkidle").catch(() => {});
}

async function shot(page: Page, name: string) {
  await page.screenshot({ path: `${OUT}/${name}.png`, fullPage: false });
}

test("dashboard", async ({ page }) => {
  await page.goto("/dashboard");
  await settle(page);
  await shot(page, "dashboard");
});

test("browser-keys", async ({ page }) => {
  await page.goto("/browse");
  await settle(page);
  await shot(page, "browser");
});

test("watch", async ({ page }) => {
  await page.goto("/watch");
  await settle(page);
  await shot(page, "watch");
});

test("metrics", async ({ page }) => {
  await page.goto("/metrics");
  await settle(page);
  await shot(page, "metrics");
});

test("terminal", async ({ page }) => {
  await page.goto("/terminal");
  await settle(page);
  await shot(page, "terminal");
});

test("audit", async ({ page }) => {
  await page.goto("/audit");
  await settle(page);
  await shot(page, "audit");
});

test("permissions", async ({ page }) => {
  await page.goto("/permissions");
  await settle(page);
  await shot(page, "permissions");
});

test("light-theme dashboard", async ({ page }) => {
  await page.goto("/settings");
  await settle(page);
  await page.getByRole("button", { name: "Light" }).click();
  await page.goto("/dashboard");
  await settle(page);
  await shot(page, "dashboard-light");
});

// Demo storyboard: a short click-through for assembling an animated GIF.
// `npm run demo:frames` produces docs/images/demo-NN.png; the operator
// then runs `gifski -o docs/demo.gif docs/images/demo-*.png` or similar.
test.describe("demo storyboard", () => {
  let n = 0;
  const frame = async (page: Page) => {
    const num = String(++n).padStart(2, "0");
    await shot(page, `demo-${num}`);
  };

  test("storyboard", async ({ page }) => {
    await page.goto("/dashboard");
    await settle(page);
    await frame(page);

    await page.getByRole("link", { name: "Keys" }).click();
    await settle(page);
    await frame(page);

    await page.getByRole("link", { name: "Watch" }).click();
    await settle(page);
    await frame(page);

    await page.keyboard.press("ControlOrMeta+K");
    await frame(page);
    await page.keyboard.type("audit");
    await frame(page);
    await page.keyboard.press("Enter");
    await settle(page);
    await frame(page);

    await page.getByRole("link", { name: "Maintenance" }).click();
    await settle(page);
    await frame(page);
  });
});
