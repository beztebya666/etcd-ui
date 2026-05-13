// Distributed-lock playground. Models locks as etcd lease-bound keys
// under a user-chosen prefix — same primitive `clientv3/concurrency.Mutex`
// uses internally. Lets operators:
//   - Acquire a lock with a TTL and a holder tag (e.g. "kube-scheduler").
//   - Watch the holder + queued waiters update live.
//   - Release the lock (revoke the lease) or let it expire.
//   - Open the same prefix in two tabs to see fair queuing in action.

import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, type LockEntry } from "../lib/api";
import { useStore } from "../lib/store";
import {
  Lock,
  Unlock,
  Plus,
  RefreshCw,
  Crown,
  Hourglass,
  AlertTriangle,
} from "lucide-react";
import { cn } from "../lib/cn";
import { toast } from "../components/Toast";

export function LocksPage() {
  const cluster = useStore((s) => s.selectedCluster);
  const qc = useQueryClient();
  const [prefix, setPrefix] = useState("/locks/demo/");
  const [holderTag, setHolderTag] = useState("");
  const [ttl, setTtl] = useState<number>(60);
  // Lease IDs we minted in *this* browser tab. We surface them with a
  // "yours" badge + a release button so users can find what they spawned
  // without scrolling. Survives nothing — refresh = clean slate.
  const [mine, setMine] = useState<Set<number>>(new Set());

  const list = useQuery({
    queryKey: ["locks", cluster, prefix],
    enabled: !!cluster,
    queryFn: () => api.locksList(cluster!, prefix),
    refetchInterval: 1500,
  });

  const acquire = useMutation({
    mutationFn: () =>
      api.lockAcquire(cluster!, {
        prefix,
        ttlSeconds: ttl,
        holderTag: holderTag || undefined,
      }),
    onSuccess: (r) => {
      setMine((s) => new Set(s).add(r.leaseId));
      toast.success(
        r.acquired ? `Got the lock — lease ${r.leaseId.toString(16)}`
                   : `Queued (#${r.waiters + 1}) behind ${r.holder || "unnamed holder"}`,
      );
      qc.invalidateQueries({ queryKey: ["locks", cluster, prefix] });
    },
    onError: (e: Error) => toast.error(e.message),
  });

  const release = useMutation({
    mutationFn: (id: number) => api.lockRelease(cluster!, id),
    onSuccess: (_, id) => {
      setMine((s) => { const n = new Set(s); n.delete(id); return n; });
      toast.success("Released");
      qc.invalidateQueries({ queryKey: ["locks", cluster, prefix] });
    },
    onError: (e: Error) => toast.error(e.message),
  });

  if (!cluster) {
    return <div className="panel p-10 text-center muted">Pick a cluster first.</div>;
  }

  const entries = list.data?.entries ?? [];
  const holder = list.data?.holder ?? null;

  return (
    <div className="space-y-5">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight flex items-center gap-2">
          <Lock className="w-5 h-5 text-accent-500" /> Distributed locks
        </h1>
        <p className="muted mt-1 max-w-3xl">
          Acquire and release named locks against this etcd cluster. Backed by
          real etcd primitives — a lease keyed under the prefix; smallest
          CreateRevision wins, others wait. Crash-safe: a holder whose
          process vanishes loses the lock when the lease TTL elapses.
        </p>
      </div>

      <div className="panel p-4 sm:p-5 space-y-3">
        <div className="grid grid-cols-1 sm:grid-cols-[1fr_220px_140px_auto] gap-2 items-end">
          <Field label="Prefix">
            <input
              value={prefix}
              onChange={(e) => setPrefix(e.target.value)}
              placeholder="/locks/leader-election/"
              className="w-full h-9 px-3 rounded-lg text-sm font-mono"
              style={{ background: "rgb(var(--panel-2))", border: "1px solid rgb(var(--line))" }}
            />
          </Field>
          <Field label="Holder tag (optional)">
            <input
              value={holderTag}
              onChange={(e) => setHolderTag(e.target.value)}
              placeholder="alice@laptop"
              className="w-full h-9 px-3 rounded-lg text-sm font-mono"
              style={{ background: "rgb(var(--panel-2))", border: "1px solid rgb(var(--line))" }}
            />
          </Field>
          <Field label="TTL (seconds)">
            <input
              type="number"
              min={1}
              max={3600}
              value={ttl}
              onChange={(e) => setTtl(Math.max(1, Math.min(3600, Number(e.target.value) || 60)))}
              className="w-full h-9 px-3 rounded-lg text-sm font-mono"
              style={{ background: "rgb(var(--panel-2))", border: "1px solid rgb(var(--line))" }}
            />
          </Field>
          <button
            onClick={() => acquire.mutate()}
            disabled={acquire.isPending || !prefix.trim()}
            className="btn btn-primary h-9"
          >
            <Plus className="w-4 h-4" /> {acquire.isPending ? "Acquiring…" : "Acquire"}
          </button>
        </div>
      </div>

      <div className="panel p-4 sm:p-5">
        <div className="flex items-center gap-2 mb-3 flex-wrap">
          <div className="text-sm font-medium">
            {entries.length} entr{entries.length === 1 ? "y" : "ies"} under{" "}
            <code className="kbd">{prefix}</code>
          </div>
          <button
            onClick={() => list.refetch()}
            className="btn btn-ghost text-xs ml-auto"
            aria-label="Refresh locks"
          >
            <RefreshCw className={cn("w-3.5 h-3.5", list.isFetching && "animate-spin")} />
          </button>
        </div>

        {entries.length === 0 ? (
          <div className="text-sm muted py-6 text-center">
            No active locks under this prefix yet. Acquire one above to claim it.
          </div>
        ) : (
          <ul className="space-y-2">
            {entries.map((e) => (
              <LockRow
                key={e.key}
                entry={e}
                mine={mine.has(e.leaseId)}
                onRelease={() => release.mutate(e.leaseId)}
                releasing={release.isPending && release.variables === e.leaseId}
              />
            ))}
          </ul>
        )}

        {holder && (
          <div className="mt-4 text-xs muted">
            <Crown className="w-3 h-3 inline -mt-0.5 text-accent-500" /> Holder:{" "}
            <span className="font-mono">{holder.key.split("/").pop()}</span>
            {" — "}
            {holder.ttlSeconds > 0
              ? `${holder.ttlSeconds}s until auto-release`
              : "lease expired — auto-cleaning"}
          </div>
        )}
      </div>

      <Cheatsheet />
    </div>
  );
}

function LockRow({
  entry,
  mine,
  onRelease,
  releasing,
}: {
  entry: LockEntry;
  mine: boolean;
  onRelease: () => void;
  releasing: boolean;
}) {
  const leaseHex = entry.leaseId.toString(16);
  const tone = entry.holder ? "accent" : "warn";
  return (
    <li
      className="rounded-lg p-3 border flex items-start gap-3"
      style={{ borderColor: entry.holder ? "color-mix(in srgb, rgb(var(--accent-500)) 50%, transparent)" : "rgb(var(--line))" }}
    >
      <div
        className={cn(
          "w-8 h-8 rounded-md flex items-center justify-center shrink-0",
          entry.holder ? "bg-accent/15" : "bg-white/[0.04]",
        )}
      >
        {entry.holder ? <Crown className="w-4 h-4 text-accent-500" /> : <Hourglass className="w-4 h-4 muted" />}
      </div>
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-1.5 flex-wrap">
          <span className={cn("pill !text-[10px]", entry.holder && "!text-accent-500 !border-accent-500/40")}>
            {entry.holder ? "holder" : "waiter"}
          </span>
          {mine && <span className="pill !text-[10px] !text-warn !border-warn/40">yours</span>}
          <span className="font-mono text-xs truncate" title={entry.key}>
            {entry.key}
          </span>
        </div>
        <div className="text-[11px] muted font-mono mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5">
          <span>lease 0x{leaseHex}</span>
          <span>rev {entry.createRevision}</span>
          {entry.ttlSeconds > 0 ? (
            <span className={cn(tone === "warn" && entry.ttlSeconds < 5 && "text-warn")}>
              ttl {entry.ttlSeconds}s
              {entry.grantedTtl > 0 && entry.grantedTtl !== entry.ttlSeconds && (
                <span className="opacity-70"> / {entry.grantedTtl}s</span>
              )}
            </span>
          ) : (
            <span className="text-warn">
              <AlertTriangle className="w-3 h-3 inline -mt-0.5" /> {entry.note || "no lease"}
            </span>
          )}
        </div>
      </div>
      <button
        onClick={onRelease}
        disabled={releasing}
        className="btn btn-ghost text-xs"
        title={mine ? "Release this lock (revoke the lease)" : "Force-release somebody else's lock"}
      >
        <Unlock className="w-3.5 h-3.5" /> {releasing ? "…" : "release"}
      </button>
    </li>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="block">
      <span className="text-[11px] uppercase tracking-wider muted mb-1 block">{label}</span>
      {children}
    </label>
  );
}

function Cheatsheet() {
  return (
    <div className="panel p-4 text-xs muted space-y-2">
      <div className="font-medium" style={{ color: "rgb(var(--fg))" }}>
        How this maps to production code
      </div>
      <pre className="panel-2 p-3 rounded-lg overflow-x-auto text-[11px] font-mono leading-relaxed">{
`sess, _ := concurrency.NewSession(cli, concurrency.WithTTL(60))
mu := concurrency.NewMutex(sess, "/locks/leader-election/")
mu.Lock(ctx)      // blocks until smallest CreateRev under prefix
defer mu.Unlock(ctx)   // revokes the lease, smallest waiter takes over`
      }</pre>
      <p>
        Open this page in a second tab against the same prefix to watch fair
        queuing — the second acquirer joins as a waiter and gets the lock the
        moment you release the first one (or its TTL elapses).
      </p>
    </div>
  );
}
