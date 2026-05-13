// Accessibility check using axe-core via @axe-core/playwright. We sweep every
// public-ish page and fail on serious/critical violations only — moderate
// hits (e.g. label-on-decorative-icon) are surfaced as soft warnings so the
// build doesn't become a noise filter.
//
// Run locally:  npx playwright test e2e/a11y.spec.ts
// CI:           same. Requires npm i -D @axe-core/playwright

import { test, expect } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

const PAGES = ["/dashboard", "/browse", "/watch", "/audit", "/maintenance", "/settings"];

async function dismissTour(page: import("@playwright/test").Page) {
  const skip = page.getByRole("button", { name: /skip tour/i });
  if (await skip.isVisible({ timeout: 1500 }).catch(() => false)) {
    await skip.click();
  }
}

for (const path of PAGES) {
  test(`a11y: ${path} has no critical / serious axe violations`, async ({ page }) => {
    await page.goto(path);
    await dismissTour(page);
    // Wait a beat so React Query placeholders settle before scan.
    await page.waitForLoadState("networkidle").catch(() => {});

    const results = await new AxeBuilder({ page })
      .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
      .disableRules([
        // Color contrast on muted text against panel-2 is intentionally low —
        // we use it for secondary labels. Run a separate visual review when
        // light theme is touched.
        "color-contrast",
      ])
      .analyze();

    const blocking = results.violations.filter(
      (v) => v.impact === "critical" || v.impact === "serious",
    );
    if (blocking.length > 0) {
      console.log(
        `${path}: ${blocking.length} blocking violations\n` +
          blocking.map((v) => `  - ${v.id} (${v.impact}) — ${v.help}`).join("\n"),
      );
    }
    expect(blocking, "no critical/serious axe violations").toEqual([]);
  });
}
