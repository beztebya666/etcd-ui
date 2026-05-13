// Persistent reconnect statistics. Rolling window of the last N reconnect
// events per browser, kept in localStorage so post-mortems survive a tab
// reload. Drained by the Settings page (devtools-style debug panel) when
// the user files a ticket — "show me when WS dropped during today's outage".
//
// Schema (versioned so we can evolve without wiping):
//
//   localStorage["etcd-ui:stream-stats:v1"] = JSON.stringify({
//     events: [{ t, kind, transport, reason?, backoffMs? }, …]
//   })
//
// kind:
//   "open"         — first time this stream came up
//   "close"        — peer/proxy closed the socket
//   "reconnect"    — scheduled next attempt
//   "transport"    — kind changed (e.g. ws → sse fallback)
//
// We cap at 200 events; old ones drop off the head. Localstorage budget at
// ~120 bytes per event = 24 KB worst-case.

const KEY = "etcd-ui:stream-stats:v1";
const MAX_EVENTS = 200;

type EventKind = "open" | "close" | "reconnect" | "transport";

type StatEvent = {
  t: number;
  kind: EventKind;
  transport?: string;
  reason?: string;
  backoffMs?: number;
};

type Bag = { events: StatEvent[] };

function read(): Bag {
  try {
    const raw = localStorage.getItem(KEY);
    if (!raw) return { events: [] };
    const v = JSON.parse(raw);
    if (!v || !Array.isArray(v.events)) return { events: [] };
    return v;
  } catch {
    return { events: [] };
  }
}

function write(b: Bag) {
  try {
    localStorage.setItem(KEY, JSON.stringify(b));
  } catch {
    /* quota / private mode / disabled — silently drop */
  }
}

export function record(ev: Omit<StatEvent, "t">): void {
  const bag = read();
  bag.events.push({ t: Date.now(), ...ev });
  if (bag.events.length > MAX_EVENTS) {
    bag.events.splice(0, bag.events.length - MAX_EVENTS);
  }
  write(bag);
}

export function snapshot(): StatEvent[] {
  return read().events.slice();
}

export function clear(): void {
  try {
    localStorage.removeItem(KEY);
  } catch {
    /* ignore */
  }
}

// Convenience metrics for the Settings debug panel.
export function aggregates(): {
  total: number;
  opens: number;
  closes: number;
  reconnects: number;
  fallbacks: number;
  oldest?: number;
} {
  const ev = snapshot();
  return {
    total: ev.length,
    opens: ev.filter((e) => e.kind === "open").length,
    closes: ev.filter((e) => e.kind === "close").length,
    reconnects: ev.filter((e) => e.kind === "reconnect").length,
    fallbacks: ev.filter((e) => e.kind === "transport" && e.transport === "sse").length,
    oldest: ev[0]?.t,
  };
}
