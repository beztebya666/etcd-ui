// Fake WebSocket + EventSource for the demo: drive the live /watch stream from
// demo events + a synthetic heartbeat (k8s lease renewals), so the Watch page is
// alive. openStream (lib/stream.ts) tries WS first, so DemoWebSocket is primary.
import { onDemoEvent, getDB } from "./db";

type Cb = ((ev: { data: unknown }) => void) | null;
let synthRev = 900000;

function isWatch(url: string) { return /\/watch(\?|$)/.test(url); }

function watchHeartbeat(emit: (s: string) => void): () => void {
  const keys = ["/registry/leases/kube-system/kube-scheduler", "/registry/leases/kube-system/kube-controller-manager", "/service/patroni/leader", "/myapp/locks/migrate"];
  let i = 0;
  const t = window.setInterval(() => {
    const key = keys[i++ % keys.length];
    synthRev += 1;
    emit(JSON.stringify({ type: "PUT", key, value: JSON.stringify({ renewTime: new Date().toISOString() }), revision: synthRev }));
  }, 3500);
  return () => clearInterval(t);
}

export class DemoWebSocket {
  static readonly CONNECTING = 0; static readonly OPEN = 1; static readonly CLOSING = 2; static readonly CLOSED = 3;
  readonly CONNECTING = 0; readonly OPEN = 1; readonly CLOSING = 2; readonly CLOSED = 3;
  url: string; readyState = 0; binaryType = "blob";
  onopen: Cb = null; onmessage: Cb = null; onclose: Cb = null; onerror: Cb = null;
  private cleanup: Array<() => void> = [];
  private closed = false;
  constructor(url: string, _protocols?: string | string[]) { this.url = url; setTimeout(() => this.start(), 0); }
  private emit(data: string) { if (!this.closed) this.onmessage?.({ data }); }
  private start() {
    if (this.closed) return;
    this.readyState = this.OPEN;
    this.onopen?.({ data: null });
    if (isWatch(this.url)) {
      this.cleanup.push(onDemoEvent((type, payload) => { if (type === "watch") this.emit(JSON.stringify(payload)); }));
      this.cleanup.push(watchHeartbeat((s) => this.emit(s)));
    }
  }
  send(data?: string) { if (typeof data === "string" && data.includes("ping")) this.emit(JSON.stringify({ type: "pong" })); }
  addEventListener() { /* on* props used */ }
  removeEventListener() { /* */ }
  close() { if (this.closed) return; this.closed = true; this.readyState = this.CLOSED; this.cleanup.forEach((f) => f()); this.onclose?.({ data: null }); }
}

export class DemoEventSource {
  static readonly CONNECTING = 0; static readonly OPEN = 1; static readonly CLOSED = 2;
  readonly CONNECTING = 0; readonly OPEN = 1; readonly CLOSED = 2;
  url: string; readyState = 0; withCredentials = false;
  onopen: Cb = null; onmessage: Cb = null; onerror: Cb = null;
  private cleanup: Array<() => void> = [];
  private closed = false;
  constructor(url: string) { this.url = url; setTimeout(() => this.start(), 0); }
  private emit(data: string) { if (!this.closed) this.onmessage?.({ data }); }
  private start() {
    if (this.closed) return;
    this.readyState = this.OPEN; this.onopen?.({ data: null });
    if (isWatch(this.url)) {
      this.cleanup.push(onDemoEvent((type, payload) => { if (type === "watch") this.emit(JSON.stringify(payload)); }));
      this.cleanup.push(watchHeartbeat((s) => this.emit(s)));
    }
  }
  addEventListener(type: string, cb: (ev: { data: unknown }) => void) { if (type === "message") this.onmessage = cb; if (type === "open") this.onopen = cb; if (type === "error") this.onerror = cb; }
  removeEventListener() { /* */ }
  close() { this.closed = true; this.readyState = this.CLOSED; this.cleanup.forEach((f) => f()); }
}

void getDB;
