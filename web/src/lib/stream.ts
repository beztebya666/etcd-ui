// Unified stream wrapper. Tries WebSocket first (cleaner under reverse
// proxies that buffer SSE); falls back to EventSource if the upgrade fails
// or the browser/proxy doesn't support it. Same shape to the caller:
//
//   const s = openStream("/api/clusters/foo/watch?prefix=/", {
//     onMessage: (data) => …,
//     onError:   (err)  => …,
//   });
//   …
//   s.close();
//
// `data` is the raw JSON string (caller decodes). Auto-reconnect with
// exponential backoff up to 30s.
//
// Keep-alive: the browser WebSocket API can't send native Ping frames from
// JS, so we send a small `{"type":"ping"}` text frame every 25s. The Go
// server accepts that as a logical keep-alive and replies with a pong
// (received as a normal message that the caller filters out — done here
// inside the wrapper so consumers never see it). The server also sends its
// own control-frame Pings which the browser auto-pongs.
//
// All active streams publish their (transport, state, nextAttemptAt) to a
// tiny pub-sub bus so the Shell can render "WS · 2" or "reconnect in 4s".
// Lifecycle events also land in a rolling localStorage buffer (lib/streamStats)
// so we can hand operators a forensic trace when WS drops over a corp proxy.

import { record as recordStat } from "./streamStats";

export type Transport = "ws" | "sse" | "connecting" | "closed";

export type StreamStatusEntry = {
  transport: Transport;
  /** Wall-clock ms timestamp of the scheduled next reconnect; 0 if N/A. */
  nextAttemptAt: number;
  /** Current backoff (ms) used for the *next* schedule. */
  backoffMs: number;
};

type StatusListener = (snapshot: Map<string, StreamStatusEntry>) => void;

const liveStreams = new Map<string, StreamStatusEntry>();
const listeners = new Set<StatusListener>();
let nextID = 0;

function publishUpdate(id: string, patch: Partial<StreamStatusEntry> & { closed?: boolean }) {
  if (patch.closed) {
    liveStreams.delete(id);
  } else {
    const prev = liveStreams.get(id) ?? {
      transport: "connecting" as Transport,
      nextAttemptAt: 0,
      backoffMs: 0,
    };
    liveStreams.set(id, { ...prev, ...patch });
  }
  const snap = new Map(liveStreams);
  for (const l of listeners) l(snap);
}

export function subscribeStreamStatus(fn: StatusListener): () => void {
  listeners.add(fn);
  fn(new Map(liveStreams));
  return () => {
    listeners.delete(fn);
  };
}

export type StreamOpts = {
  onMessage: (data: string) => void;
  onError?: (err: Error) => void;
  onOpen?: () => void;
  /** force a transport for tests; default is "ws" with SSE fallback */
  transport?: "ws" | "sse" | "auto";
};

export type StreamHandle = {
  close: () => void;
  transport: () => Transport;
};

const MAX_BACKOFF = 30_000;
const PING_INTERVAL_MS = 25_000;

export function openStream(path: string, opts: StreamOpts): StreamHandle {
  let ws: WebSocket | null = null;
  let es: EventSource | null = null;
  let closed = false;
  let backoff = 500;
  let kind: Transport = "connecting";
  let reconnectTimer: number | null = null;
  let pingTimer: number | null = null;
  const id = "s" + ++nextID;
  publishUpdate(id, { transport: "connecting", nextAttemptAt: 0, backoffMs: backoff });

  const setKind = (k: Transport) => {
    kind = k;
    publishUpdate(id, { transport: k });
  };

  const prefer = opts.transport ?? "auto";

  function clearPing() {
    if (pingTimer != null) {
      window.clearInterval(pingTimer);
      pingTimer = null;
    }
  }

  function startPing() {
    clearPing();
    pingTimer = window.setInterval(() => {
      if (ws && ws.readyState === WebSocket.OPEN) {
        try {
          ws.send('{"type":"ping"}');
        } catch {
          /* ignore — onclose will reconnect */
        }
      }
    }, PING_INTERVAL_MS);
  }

  function scheduleReconnect() {
    if (closed) return;
    if (reconnectTimer != null) return;
    const delay = backoff;
    const nextAttemptAt = Date.now() + delay;
    publishUpdate(id, { transport: "connecting", nextAttemptAt, backoffMs: delay });
    recordStat({ kind: "reconnect", backoffMs: delay });
    reconnectTimer = window.setTimeout(() => {
      reconnectTimer = null;
      backoff = Math.min(backoff * 2, MAX_BACKOFF);
      connect();
    }, delay);
  }

  function connect() {
    if (closed) return;
    if (prefer === "sse" || typeof WebSocket === "undefined") {
      openSSE();
      return;
    }
    openWS();
  }

  function openWS() {
    try {
      const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
      const u = new URL(path, window.location.origin);
      const wsURL = proto + "//" + window.location.host + u.pathname + u.search;
      const tok = sessionStorage.getItem("etcd-ui-bearer") || "";
      const subprotocols = tok
        ? ["etcd-ui.bearer", tok, "etcd-ui.v1"]
        : ["etcd-ui.v1"];
      ws = new WebSocket(wsURL, subprotocols);
    } catch (err) {
      opts.onError?.(err as Error);
      openSSE();
      return;
    }
    ws.onopen = () => {
      setKind("ws");
      backoff = 500;
      publishUpdate(id, { nextAttemptAt: 0, backoffMs: 500 });
      recordStat({ kind: "open", transport: "ws" });
      startPing();
      opts.onOpen?.();
    };
    ws.onmessage = (ev) => {
      const raw = String(ev.data);
      // Suppress server pong replies so consumers never see them.
      if (raw === "" || raw === '{"type":"pong"}') return;
      opts.onMessage(raw);
    };
    ws.onerror = () => {
      // onclose fires next; the reconnect/fallback logic lives there.
    };
    ws.onclose = (ev) => {
      ws = null;
      clearPing();
      if (closed) return;
      recordStat({ kind: "close", transport: "ws", reason: `code=${ev.code}` });
      if (kind === "connecting" && prefer === "auto") {
        recordStat({ kind: "transport", transport: "sse", reason: "ws-fallback" });
        openSSE();
        return;
      }
      if (ev.code !== 1000) scheduleReconnect();
    };
  }

  function openSSE() {
    try {
      es = new EventSource(path, { withCredentials: true });
    } catch (err) {
      opts.onError?.(err as Error);
      scheduleReconnect();
      return;
    }
    es.onopen = () => {
      setKind("sse");
      backoff = 500;
      publishUpdate(id, { nextAttemptAt: 0, backoffMs: 500 });
      recordStat({ kind: "open", transport: "sse" });
      opts.onOpen?.();
    };
    es.onmessage = (ev) => opts.onMessage(String(ev.data));
    es.onerror = () => {
      if (es && es.readyState === 2) {
        es.close();
        es = null;
        scheduleReconnect();
      }
    };
  }

  connect();

  return {
    close() {
      closed = true;
      if (reconnectTimer != null) window.clearTimeout(reconnectTimer);
      clearPing();
      if (ws) {
        ws.close(1000);
        ws = null;
      }
      if (es) {
        es.close();
        es = null;
      }
      publishUpdate(id, { closed: true });
    },
    transport: () => kind,
  };
}
