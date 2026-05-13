import { describe, it, expect, beforeEach } from "vitest";
import { recordDBSize, forecast, formatBytes, formatDuration } from "./dbForecast";

// dbForecast keeps an in-process map keyed by cluster id. Different tests use
// different ids so they don't bleed into each other; no need for explicit
// reset.

describe("dbForecast.recordDBSize + forecast", () => {
  let clusterCounter = 0;
  let cluster = "";
  beforeEach(() => {
    clusterCounter += 1;
    cluster = `test-cluster-${clusterCounter}`;
  });

  it("returns null with fewer than 2 samples", () => {
    expect(forecast(cluster)).toBeNull();
    recordDBSize(cluster, 1000, 0);
    expect(forecast(cluster)).toBeNull();
  });

  it("extrapolates a linear trend to the quota", () => {
    // 1 MiB → 2 MiB → 3 MiB across 3 minutes — 1 MiB/min growth.
    for (let i = 0; i <= 3; i++) {
      recordDBSize(cluster, (i + 1) * 1024 * 1024, i * 60_000);
    }
    const fc = forecast(cluster, 2 * 1024 * 1024 * 1024 /* 2 GiB */);
    expect(fc).not.toBeNull();
    expect(fc!.bytesPerMs).toBeGreaterThan(0);
    expect(fc!.msUntilQuota).not.toBeNull();
    // Should take roughly (2GiB - 4MiB) / 1MiB-per-minute → ~2044 minutes.
    expect(fc!.msUntilQuota!).toBeGreaterThan(60_000 * 1000);
  });

  it("returns null msUntilQuota for shrinking trends", () => {
    for (let i = 0; i < 4; i++) {
      recordDBSize(cluster, (10 - i) * 1024 * 1024, i * 60_000);
    }
    const fc = forecast(cluster);
    expect(fc).not.toBeNull();
    expect(fc!.bytesPerMs).toBeLessThan(0);
    expect(fc!.msUntilQuota).toBeNull();
  });

  it("dedups near-instant duplicate timestamps", () => {
    recordDBSize(cluster, 1000, 100);
    recordDBSize(cluster, 2000, 200); // within 500ms, treated as update
    const fc = forecast(cluster);
    // Only one effective sample → still null.
    expect(fc).toBeNull();
  });
});

describe("formatBytes", () => {
  it.each([
    [512, "512 B"],
    [2048, "2.0 KB"],
    [5 * 1024 * 1024, "5.0 MB"],
    [3 * 1024 * 1024 * 1024, "3.00 GB"],
  ])("formats %d bytes as %s", (b, expected) => {
    expect(formatBytes(b)).toBe(expected);
  });
});

describe("formatDuration", () => {
  it.each([
    [42_000, "42s"],
    [10 * 60_000, "10.0 min"],
    [3 * 3600_000, "3.0 h"],
    [10 * 24 * 3600_000, "10.0 d"],
  ])("formats %d ms as %s", (ms, expected) => {
    expect(formatDuration(ms)).toBe(expected);
  });
});
