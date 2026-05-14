// Record a video of the K8s edit round-trip flow, then convert to GIF.
//
//   node scripts/screenshots/record-edit.mjs http://127.0.0.1:8080 k8s-prod
//
// Produces docs/screenshots/k8s-edit.webm; pipe through ffmpeg to get the
// final docs/screenshots/k8s-edit.gif. The two-stage record-then-encode
// gives us palettegen + fps control, which gets the GIF from ~15 MB
// (naive screenshot loop) down to ~1 MB without visible quality loss.

import { chromium } from "playwright";
import { mkdir } from "node:fs/promises";
import { existsSync } from "node:fs";

const BASE = process.argv[2] || "http://127.0.0.1:8080";
const CLUSTER = process.argv[3] || "k8s-prod";
const OUT = "docs/screenshots";

async function main() {
  if (!existsSync(OUT)) await mkdir(OUT, { recursive: true });

  const browser = await chromium.launch();
  const ctx = await browser.newContext({
    viewport: { width: 1600, height: 900 },
    deviceScaleFactor: 1,
    colorScheme: "dark",
    recordVideo: { dir: OUT, size: { width: 1600, height: 900 } },
  });
  await ctx.addInitScript((c) => {
    const stored = localStorage.getItem("etcd-ui-state");
    let s = stored ? JSON.parse(stored) : { state: {}, version: 0 };
    s.state = s.state || {};
    s.state.selectedCluster = c;
    s.state.tourCompleted = true;
    s.state.simpleMode = false;
    s.state.theme = "dark";
    localStorage.setItem("etcd-ui-state", JSON.stringify(s));
  }, CLUSTER);
  const page = await ctx.newPage();

  await page.goto(`${BASE}/browse?prefix=/registry/pods/shop/`, {
    waitUntil: "networkidle",
  });
  await page.waitForTimeout(1500);

  // Drill into a Pod
  for (const folder of ["registry", "pods", "shop"]) {
    await page
      .locator('[role="treeitem"]')
      .filter({ hasText: folder })
      .first()
      .click({ timeout: 5000 })
      .catch(() => {});
    await page.waitForTimeout(400);
  }
  const leaf = page
    .locator('[role="treeitem"]')
    .filter({ hasText: "orders-api" })
    .first();
  await leaf.click({ timeout: 5000 });
  await page.waitForTimeout(2000);

  // Click "Edit" on the DecodedView toolbar
  await page.getByRole("button", { name: "Edit" }).first().click().catch(() => {});
  await page.waitForTimeout(800);

  // Find the textarea, replace image tag
  const ta = page.locator("textarea").first();
  const cur = await ta.inputValue();
  const next = cur.replace(/v3\.14\.\d+/, "v9.9.9-DEMO");
  await ta.fill(next);
  await page.waitForTimeout(1200);

  // Done editing → preview again with colour highlights
  await page.getByRole("button", { name: "Done editing" }).click().catch(() => {});
  await page.waitForTimeout(1500);

  // Save
  await page.getByRole("button", { name: /^Save$/ }).click().catch(() => {});
  await page.waitForTimeout(2000);

  await ctx.close();
  await browser.close();

  console.log(`recording saved under ${OUT}/`);
  console.log(
    "next: ffmpeg -i <video>.webm -vf 'fps=12,scale=1200:-1:flags=lanczos,palettegen' palette.png && " +
    "ffmpeg -i <video>.webm -i palette.png -lavfi 'fps=12,scale=1200:-1:flags=lanczos [x]; [x][1:v] paletteuse' -y k8s-edit.gif",
  );
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
