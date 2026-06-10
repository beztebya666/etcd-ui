// Demo bootstrap (etcd-ui): swap fetch + WebSocket + EventSource for the in-browser
// mock before the app loads. Gated on VITE_DEMO=1. Each browser is its own sandbox.
import { demoFetch } from "./server";
import { DemoWebSocket, DemoEventSource } from "./stream";
import { getDB } from "./db";

export { resetDemo } from "./db";

export function isDemo(): boolean {
  try { const env = (import.meta as unknown as { env?: Record<string, string> }).env; if (env && env.VITE_DEMO === "1") return true; } catch { /* */ }
  return typeof window !== "undefined" && (window as unknown as { __DEMO__?: boolean }).__DEMO__ === true;
}

let installed = false;
export function installDemo() {
  if (installed) return; installed = true;
  (window as unknown as { __DEMO__?: boolean }).__DEMO__ = true;
  window.fetch = ((input: RequestInfo | URL, init?: RequestInit) => demoFetch(input, init)) as typeof window.fetch;
  (window as unknown as { WebSocket: unknown }).WebSocket = DemoWebSocket;
  (window as unknown as { EventSource: unknown }).EventSource = DemoEventSource;
  getDB();
}
