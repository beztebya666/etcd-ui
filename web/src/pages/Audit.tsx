import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, type AuditEvent } from "../lib/api";
import { ScrollText, User, Filter, RotateCcw, Info, Download, Inbox, FileSearch } from "lucide-react";
import { cn } from "../lib/cn";
import { confirm } from "../components/Confirm";
import { toast } from "../components/Toast";
import { Skeleton } from "../components/Skeleton";

export function AuditPage() {
  const qc = useQueryClient();
  const initial = useQuery({ queryKey: ["audit"], queryFn: () => api.audit(500) });
  const me = useQuery({ queryKey: ["me"], queryFn: () => api.me().catch(() => null) });
  const [live, setLive] = useState<AuditEvent[]>([]);
  const [filter, setFilter] = useState("");

  // Subscribe to the live tail *after* the initial fetch has resolved.
  // Otherwise the SSE prime fires first, paints 50 events, then the
  // initial 500 lands and the list shifts visibly. Waiting eliminates
  // the jump — list pops in once, in final order.
  useEffect(() => {
    if (!initial.isFetched) return;
    const stop = api.auditStream((e) =>
      setLive((prev) => [e, ...prev].slice(0, 500)),
    );
    return stop;
  }, [initial.isFetched]);

  const events = mergeAndDedup(initial.data ?? [], live);
  const f = filter.trim().toLowerCase();
  const filtered = f
    ? events.filter((e) =>
        [e.actor, e.action, e.path, e.cluster ?? "", e.method, String(e.status), e.key ?? ""]
          .join(" ")
          .toLowerCase()
          .includes(f),
      )
    : events;

  const restore = useMutation({
    mutationFn: async (vars: { cluster: string; key: string; value: string }) => {
      return api.put(vars.cluster, { key: vars.key, value: vars.value });
    },
    onSuccess: () => {
      toast.success("Restored from audit");
      qc.invalidateQueries({ queryKey: ["range"] });
    },
    onError: (e: Error) => toast.error(e.message),
  });

  const onRestore = async (e: AuditEvent) => {
    const value = decodePrev(e.note ?? "");
    if (!value && value !== "") {
      toast.error("Audit event has no captured previous value");
      return;
    }
    const ok = await confirm({
      title: `Restore "${e.key}"?`,
      body:
        `Will PUT the previous value captured at delete time (${value.length} B) back to ` +
        `cluster ${e.cluster}. Any current value at this key will be overwritten.`,
      danger: false,
      confirmLabel: "Restore",
    });
    if (ok && e.cluster && e.key) {
      restore.mutate({ cluster: e.cluster, key: e.key, value });
    }
  };

  return (
    <div className="space-y-4">
      <div className="flex items-end justify-between gap-3 flex-wrap">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight flex items-center gap-2">
            <ScrollText className="w-5 h-5 text-accent-500" /> Audit log
          </h1>
          <p className="muted mt-1">
            Every write performed via this UI is recorded here and persisted on disk. Single-key
            deletes capture the previous value for one-click <strong>Restore</strong>.
          </p>
        </div>
        {me.data?.authDisabled && (
          <span className="tag tag-warn" title="No AUTH_USERS / OIDC configured — every actor reads as 'anonymous'.">
            <Info className="w-4 h-4" /> auth disabled — actor = anonymous
          </span>
        )}
        <div className="flex items-center gap-2">
          <div
            className="flex items-center gap-2 h-10 px-3 rounded-lg"
            style={{ background: "rgb(var(--panel))", border: "1px solid rgb(var(--line))" }}
          >
            <Filter className="w-4 h-4 muted" />
            <input
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              placeholder="filter…"
              className="bg-transparent outline-none text-sm w-56"
            />
          </div>
          <a
            href="/api/audit/events/download"
            className="btn"
            title="Download the full audit log (JSONL)"
          >
            <Download className="w-4 h-4" /> Download
          </a>
        </div>
      </div>

      <div className="panel overflow-hidden min-h-[200px]">
        {initial.isLoading && !initial.data && (
          <div className="p-3 space-y-2">
            {Array.from({ length: 8 }).map((_, i) => (
              <Skeleton key={i} className="h-9" />
            ))}
          </div>
        )}
        <ul className="divide-y overflow-x-auto" style={{ borderColor: "rgb(var(--line))" }}>
          {filtered.map((e) => {
            const restorable =
              e.action === "kv.delete" && !!e.cluster && !!e.key && hasPrev(e.note);
            const link = noteLink(e.note);
            return (
              <li
                key={e.id}
                className="row-hover px-4 py-2.5 grid grid-cols-[64px_56px_180px_1fr_auto] items-center gap-3 text-sm min-w-[640px]"
              >
                <span
                  className={cn(
                    "pill !rounded-md justify-center !text-[10px] uppercase tracking-wider",
                    e.status >= 400 ? "text-danger border-danger/40" : "text-accent-500 border-accent-500/40",
                  )}
                >
                  {e.method}
                </span>
                <span className="text-xs muted font-mono">{e.status}</span>
                <span className="font-mono text-xs muted truncate">
                  {new Date(e.time).toLocaleString()}
                </span>
                <div className="min-w-0">
                  <div className="truncate">
                    <span className="font-medium">{e.action}</span>
                    {e.cluster && <span className="muted"> · {e.cluster}</span>}
                  </div>
                  {e.key && (
                    <div className="font-mono text-[11px] muted truncate" title={e.key}>
                      {e.key}
                    </div>
                  )}
                </div>
                <div className="flex items-center gap-2 justify-end whitespace-nowrap">
                  {restorable && (
                    <button
                      onClick={() => onRestore(e)}
                      className="btn !h-7 !px-2 !text-xs"
                      disabled={restore.isPending}
                      title="Recreate the deleted key with its previous value"
                    >
                      <RotateCcw className="w-3 h-3" /> Restore
                    </button>
                  )}
                  {link?.scheme === "acl-history" && (
                    <a
                      href="/permissions"
                      onClick={(ev) => {
                        ev.preventDefault();
                        // Tell the Permissions page to open its history
                        // drawer scrolled to this snapshot.
                        sessionStorage.setItem("acl-history:focus", link.id);
                        window.location.assign("/permissions");
                      }}
                      className="btn !h-7 !px-2 !text-xs"
                      title="Open the ACL snapshot from this edit"
                    >
                      <FileSearch className="w-3 h-3" /> Snapshot
                    </a>
                  )}
                  <span className="muted text-xs truncate max-w-[260px]" title={`${e.actor} · ${e.ip}`}>
                    <User className="inline w-3 h-3 mr-1" />
                    {e.actor}
                  </span>
                </div>
              </li>
            );
          })}
          {filtered.length === 0 && (
            <li className="py-12 text-center text-sm">
              <Inbox className="w-8 h-8 mx-auto mb-2 muted" />
              <div className="font-medium">No audit events {filter && "match"}</div>
              <div className="muted mt-1">
                {filter
                  ? "Try clearing the filter."
                  : "Make a change in the UI — every write lands here automatically."}
              </div>
            </li>
          )}
        </ul>
      </div>
    </div>
  );
}

function mergeAndDedup(a: AuditEvent[], b: AuditEvent[]): AuditEvent[] {
  const seen = new Set<number>();
  const out: AuditEvent[] = [];
  for (const e of [...b, ...a]) {
    if (seen.has(e.id)) continue;
    seen.add(e.id);
    out.push(e);
  }
  return out.sort((x, y) => y.id - x.id);
}

function hasPrev(note?: string): boolean {
  // Note is space-separated tokens — prev=<b64> may co-exist with link=…
  if (!note) return false;
  return note.split(/\s+/).some((t) => t.startsWith("prev="));
}

function decodePrev(note: string): string {
  const tok = note.split(/\s+/).find((t) => t.startsWith("prev="));
  if (!tok) return "";
  try {
    return atob(tok.slice("prev=".length));
  } catch {
    return "";
  }
}

// link=<scheme>:<id> token in Note. Currently `acl-history:<file>` is the
// only producer; surface as a "view snapshot" affordance.
function noteLink(note?: string): { scheme: string; id: string } | null {
  if (!note) return null;
  for (const t of note.split(/\s+/)) {
    if (!t.startsWith("link=")) continue;
    const rest = t.slice("link=".length);
    const colon = rest.indexOf(":");
    if (colon < 1) continue;
    return { scheme: rest.slice(0, colon), id: rest.slice(colon + 1) };
  }
  return null;
}
