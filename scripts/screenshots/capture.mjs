// Screenshot recorder: visits every major page of a running etcd-ui
// instance, saves a PNG per view to docs/screenshots/.
//
//   node scripts/screenshots/capture.mjs http://127.0.0.1:8080
//
// Run after seeding the target etcd with realistic data (the
// internal/k8sdecode/testseed program puts a useful spread of Pods,
// Deployments, Services, ConfigMaps in /registry/).

import { chromium } from "playwright";
import { mkdir, writeFile } from "node:fs/promises";
import { existsSync } from "node:fs";

const BASE = process.argv[2] || "http://127.0.0.1:8080";
const OUT = "docs/screenshots";
const VIEWPORT = { width: 1600, height: 1000 };

const shots = [
  { name: "dashboard", path: "/dashboard", wait: 1500 },
  { name: "browser-tree", path: "/browse", wait: 2500 },
  {
    name: "browser-pod-decoded",
    path: "/browse?prefix=/registry/pods/shop/",
    wait: 3000,
    // Open a Pod key so the structured K8s JSON view shows. URL param
    // sets the prefix; tree still loads collapsed though, so we walk
    // it open: registry → pods → shop → first pod.
    actions: async (page) => {
      // Expand each folder by clicking its label (text). The tree
      // virtualizer renders rows lazily so we click + wait between.
      for (const folder of ["registry", "pods", "shop"]) {
        const row = page.locator('[role="treeitem"]').filter({ hasText: folder }).first();
        await row.click({ timeout: 5000 }).catch(() => {});
        await page.waitForTimeout(500);
      }
      const leaf = page.locator('[role="treeitem"]').filter({ hasText: "orders-api" }).first();
      await leaf.click({ timeout: 8000 }).catch(() => {});
      await page.waitForTimeout(2500);
    },
  },
  { name: "watch", path: "/watch", wait: 1500 },
  { name: "cluster", path: "/cluster", wait: 2000 },
  { name: "metrics", path: "/metrics", wait: 3500 },
  { name: "heatmap", path: "/heatmap", wait: 1500 },
  { name: "locks", path: "/locks", wait: 1500 },
  { name: "maintenance", path: "/maintenance", wait: 2000 },
  { name: "audit", path: "/audit", wait: 1500 },
  { name: "permissions", path: "/permissions", wait: 1500 },
  { name: "federation", path: "/federation", wait: 1500 },
  { name: "settings", path: "/settings", wait: 1000 },
];

async function main() {
  if (!existsSync(OUT)) await mkdir(OUT, { recursive: true });
  const browser = await chromium.launch();
  const ctx = await browser.newContext({
    viewport: VIEWPORT,
    deviceScaleFactor: 2, // crisper screenshots on Retina-rendering README viewers
    colorScheme: "dark",
  });
  // Pre-seed zustand's persisted store so the cluster picker isn't
  // empty and the onboarding modal doesn't cover every screenshot.
  // Store key is "etcd-ui-state" (see src/lib/store.ts:persist name).
  // Override CLUSTER on the command line via the second arg if your
  // etcd-ui registered it under a different name.
  const cluster = process.argv[3] || "k8s-prod";
  await ctx.addInitScript((c) => {
    const stored = localStorage.getItem("etcd-ui-state");
    let s = stored ? JSON.parse(stored) : { state: {}, version: 0 };
    s.state = s.state || {};
    s.state.selectedCluster = c;
    s.state.tourCompleted = true;
    s.state.simpleMode = false;
    s.state.theme = "dark";
    localStorage.setItem("etcd-ui-state", JSON.stringify(s));
  }, cluster);
  const page = await ctx.newPage();

  for (const shot of shots) {
    process.stdout.write(`→ ${shot.name.padEnd(24)} `);
    try {
      await page.goto(`${BASE}${shot.path}`, { waitUntil: "networkidle", timeout: 15000 });
      await page.waitForTimeout(shot.wait);
      if (shot.actions) await shot.actions(page);
      const file = `${OUT}/${shot.name}.png`;
      await page.screenshot({ path: file, fullPage: false });
      console.log(`✓  ${file}`);
    } catch (e) {
      console.log(`✗  ${e.message}`);
    }
  }

  await browser.close();
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
