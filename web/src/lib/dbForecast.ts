// Light-weight in-memory rolling window of (timestamp, dbSizeBytes) samples
// per cluster. The Maintenance page polls cluster summary every few seconds and
// pushes the result here; the forecast card pulls the regression result back.
//
// We don't persist this — restarting the tab is fine, and storing GBs of
// samples in localStorage would just hurt startup. 20 minutes of 5-second
// polling at 240 samples is plenty for a useful linear fit.

type Sample = { t: number; bytes: number };

const WINDOW_MS = 30 * 60 * 1000; // 30 minutes
const MAX_SAMPLES = 360;
const QUOTA_DEFAULT = 2 * 1024 * 1024 * 1024; // etcd default --quota-backend-bytes

const history = new Map<string, Sample[]>();

export function recordDBSize(cluster: string, bytes: number, t = Date.now()): void {
  if (!cluster || !Number.isFinite(bytes) || bytes <= 0) return;
  let arr = history.get(cluster);
  if (!arr) {
    arr = [];
    history.set(cluster, arr);
  }
  // dedup near-instant duplicates (the same poll firing twice).
  const last = arr[arr.length - 1];
  if (last && Math.abs(t - last.t) < 500) {
    last.bytes = bytes;
    return;
  }
  arr.push({ t, bytes });
  const cutoff = t - WINDOW_MS;
  while (arr.length > 0 && arr[0].t < cutoff) arr.shift();
  if (arr.length > MAX_SAMPLES) arr.splice(0, arr.length - MAX_SAMPLES);
}

export type Forecast = {
  /** bytes per millisecond of cluster growth across the rolling window */
  bytesPerMs: number;
  /** ms until the cluster reaches `quotaBytes`. null if shrinking/flat or no samples. */
  msUntilQuota: number | null;
  /** newest db size we've seen */
  currentBytes: number;
  samples: number;
  windowMs: number;
};

export function forecast(cluster: string, quotaBytes = QUOTA_DEFAULT): Forecast | null {
  const arr = history.get(cluster);
  if (!arr || arr.length < 2) return null;
  // simple least-squares linear regression: bytes = a + b*t
  const n = arr.length;
  let sumT = 0,
    sumB = 0,
    sumTT = 0,
    sumTB = 0;
  const t0 = arr[0].t;
  for (const s of arr) {
    const x = s.t - t0;
    sumT += x;
    sumB += s.bytes;
    sumTT += x * x;
    sumTB += x * s.bytes;
  }
  const denom = n * sumTT - sumT * sumT;
  if (denom === 0) return null;
  const b = (n * sumTB - sumT * sumB) / denom;
  const a = (sumB - b * sumT) / n;
  const current = arr[arr.length - 1].bytes;
  const windowMs = arr[arr.length - 1].t - arr[0].t;

  let msUntilQuota: number | null = null;
  if (b > 0 && current < quotaBytes) {
    const xAtQuota = (quotaBytes - a) / b;
    const xNow = arr[arr.length - 1].t - t0;
    const dx = xAtQuota - xNow;
    if (Number.isFinite(dx) && dx > 0) msUntilQuota = dx;
  }
  return { bytesPerMs: b, msUntilQuota, currentBytes: current, samples: n, windowMs };
}

export function formatBytes(b: number): string {
  if (b < 1024) return `${b} B`;
  if (b < 1024 * 1024) return `${(b / 1024).toFixed(1)} KB`;
  if (b < 1024 * 1024 * 1024) return `${(b / 1024 / 1024).toFixed(1)} MB`;
  return `${(b / 1024 / 1024 / 1024).toFixed(2)} GB`;
}

export function formatDuration(ms: number): string {
  const s = ms / 1000;
  if (s < 60) return `${s.toFixed(0)}s`;
  const m = s / 60;
  if (m < 60) return `${m.toFixed(1)} min`;
  const h = m / 60;
  if (h < 48) return `${h.toFixed(1)} h`;
  const d = h / 24;
  if (d < 60) return `${d.toFixed(1)} d`;
  return `${(d / 30).toFixed(1)} mo`;
}
