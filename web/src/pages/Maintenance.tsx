import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../lib/api";
import { useStore } from "../lib/store";
import {
  Download,
  Hammer,
  Zap,
  ShieldAlert,
  ClipboardCheck,
  Trash2,
  UploadCloud,
  TrendingUp,
  AlertTriangle,
  FileCheck2,
} from "lucide-react";
import { BulkImportDialog } from "../components/BulkImportDialog";
import { recordDBSize, forecast, formatBytes, formatDuration } from "../lib/dbForecast";

export function Maintenance() {
  const cluster = useStore((s) => s.selectedCluster);
  const qc = useQueryClient();
  const [restoreOpen, setRestoreOpen] = useState(false);

  const leases = useQuery({
    queryKey: ["leases", cluster],
    enabled: !!cluster,
    queryFn: () => api.leases(cluster!),
    refetchInterval: 5_000,
  });

  // poll cluster summary every 5s for the growth forecast.
  const summary = useQuery({
    queryKey: ["summary", cluster],
    enabled: !!cluster,
    queryFn: () => api.cluster(cluster!),
    refetchInterval: 5_000,
  });

  useEffect(() => {
    if (cluster && summary.data?.dbSizeBytes) {
      recordDBSize(cluster, summary.data.dbSizeBytes);
    }
  }, [cluster, summary.data?.dbSizeBytes, summary.dataUpdatedAt]);

  const fc = cluster ? forecast(cluster) : null;

  const compact = useMutation({
    mutationFn: () => api.compact(cluster!),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["summary"] }),
  });
  const defrag = useMutation({ mutationFn: () => api.defrag(cluster!) });
  const disarm = useMutation({ mutationFn: () => api.disarm(cluster!) });
  const revoke = useMutation({
    mutationFn: (id: number) => api.revokeLease(cluster!, id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["leases"] }),
  });

  if (!cluster) return <div className="panel p-10 text-center muted">Pick a cluster first.</div>;

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Maintenance</h1>
        <p className="muted mt-1">Backups, compaction, defrag, alarms and leases — all in one place.</p>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
        <Card
          icon={Download}
          title="Snapshot &amp; export"
          desc="Get a copy of your cluster you can keep safe. .db is the raw etcd snapshot; .json is a portable export that can be re-imported here."
        >
          <div className="flex flex-wrap gap-2">
            <a href={api.snapshotURL(cluster)} className="btn btn-primary">
              <Download className="w-4 h-4" /> Snapshot (.db)
            </a>
            <a href={api.exportURL(cluster)} className="btn">
              <Download className="w-4 h-4" /> Export (.json)
            </a>
          </div>
        </Card>

        <Card
          icon={UploadCloud}
          title="Restore from .json"
          desc="Upload a .json export. We re-create every key/value in this cluster. Optionally wipe a prefix first."
        >
          <button onClick={() => setRestoreOpen(true)} className="btn">
            <UploadCloud className="w-4 h-4" /> Choose file…
          </button>
          <BulkImportDialog
            open={restoreOpen}
            onClose={() => setRestoreOpen(false)}
            cluster={cluster}
            onApplied={() => qc.invalidateQueries({ queryKey: ["range"] })}
          />
        </Card>

        <Card
          icon={FileCheck2}
          title="Validate snapshot (etcdutl)"
          desc="Verify a .db snapshot offline before restoring. Reports hash, revision, total keys, and size."
        >
          <SnapshotValidator cluster={cluster} />
        </Card>

        <Card
          icon={Hammer}
          title="Defragment"
          desc="Reclaims space on every endpoint after large deletions. Runs serially per endpoint."
        >
          <button onClick={() => defrag.mutate()} className="btn" disabled={defrag.isPending}>
            {defrag.isPending ? "Defragmenting…" : "Defragment all"}
          </button>
          {defrag.data && (
            <pre className="mt-3 text-xs panel-2 p-3 rounded-lg overflow-x-auto">
              {JSON.stringify(defrag.data, null, 2)}
            </pre>
          )}
        </Card>

        <Card icon={Zap} title="Compact history" desc="Compact key history up to the current revision.">
          <button onClick={() => compact.mutate()} className="btn" disabled={compact.isPending}>
            {compact.isPending ? "Compacting…" : "Compact now"}
          </button>
          {compact.data && (
            <div className="text-xs muted mt-2">
              Compacted up to revision <span className="font-mono">{compact.data.compactedRevision}</span>
            </div>
          )}
        </Card>

        <Card icon={ShieldAlert} title="Disarm alarms" desc="Clear all NOSPACE / CORRUPT alarms across the cluster.">
          <button onClick={() => disarm.mutate()} className="btn" disabled={disarm.isPending}>
            <ClipboardCheck className="w-4 h-4" /> Disarm
          </button>
        </Card>

        <Card
          icon={TrendingUp}
          title="DB size · growth forecast"
          desc="Linear extrapolation of recent samples against the default 2 GiB quota. Open this page for ~5 minutes for a stable estimate."
        >
          {!fc ? (
            <div className="text-sm muted">
              Collecting samples — leave this tab open to populate the forecast.
            </div>
          ) : (
            <div className="grid grid-cols-2 gap-3 text-sm">
              <Stat label="Current" value={formatBytes(fc.currentBytes)} />
              <Stat
                label="Growth"
                value={
                  fc.bytesPerMs > 0
                    ? `+${formatBytes(fc.bytesPerMs * 3_600_000)} / h`
                    : fc.bytesPerMs < 0
                      ? `-${formatBytes(-fc.bytesPerMs * 3_600_000)} / h`
                      : "flat"
                }
              />
              <Stat label="Samples" value={`${fc.samples} · ${formatDuration(fc.windowMs)} window`} />
              <Stat
                label="At 2 GiB quota"
                value={fc.msUntilQuota ? `in ~${formatDuration(fc.msUntilQuota)}` : "not in this window"}
                warn={!!fc.msUntilQuota && fc.msUntilQuota < 7 * 24 * 60 * 60 * 1000}
              />
              {fc.msUntilQuota && fc.msUntilQuota < 24 * 60 * 60 * 1000 && (
                <div className="col-span-2 mt-1 flex items-start gap-2 p-2 rounded-md text-warn"
                     style={{ background: "rgb(var(--panel-2))" }}>
                  <AlertTriangle className="w-4 h-4 mt-0.5 shrink-0" />
                  <span>Less than a day at current rate — schedule compaction or raise quota now.</span>
                </div>
              )}
            </div>
          )}
        </Card>
      </div>

      <div className="panel p-5">
        <div className="text-sm font-medium mb-1">Leases ({leases.data?.length ?? 0})</div>
        <p className="muted text-xs mb-4 max-w-[920px]">
          A <strong>lease</strong> is a TTL-bound token. Keys can be attached to a lease — when
          the lease expires without a keep-alive, every attached key is auto-deleted. etcd
          uses this for <em>leader election</em> (Kubernetes controller-manager, scheduler,
          kubelet node heartbeats), distributed locks, and service discovery (Vitess, Cilium,
          KubeEdge, …).
          <br />
          <span className="text-warn">Revoking a lease immediately deletes its keys</span> — for
          K8s leases this <em>will</em> demote a leader / mark a node not-ready. Only revoke
          orphaned leases you control.
        </p>
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="text-left muted text-[11px] uppercase tracking-wider">
              <tr>
                <th className="py-2 pr-4">ID</th>
                <th className="py-2 pr-4">Role / Holder</th>
                <th className="py-2 pr-4">TTL</th>
                <th className="py-2 pr-4">Renewed</th>
                <th className="py-2 pr-4">Keys</th>
                <th className="py-2 pr-4 text-right">Action</th>
              </tr>
            </thead>
            <tbody className="divide-y" style={{ borderColor: "rgb(var(--line))" }}>
              {leases.data?.map((l) => (
                <tr key={l.id} className="row-hover align-top">
                  <td className="py-2 pr-4 font-mono text-xs">{l.id}</td>
                  <td className="py-2 pr-4 min-w-[260px]">
                    {l.holderKind ? (
                      <div>
                        <div className="font-medium text-xs">{l.holderKind}</div>
                        {l.holderIdentity && (
                          <div className="muted text-[11px] font-mono truncate max-w-[360px]" title={l.holderIdentity}>
                            {l.holderIdentity}
                          </div>
                        )}
                      </div>
                    ) : (
                      <span className="muted">—</span>
                    )}
                  </td>
                  <td className="py-2 pr-4">
                    <div>{l.ttl}s</div>
                    <div className="muted text-[11px]">of {l.grantedTtl}s</div>
                  </td>
                  <td className="py-2 pr-4 text-[11px]">
                    {l.renewedAt ? (
                      <span title={`acquired ${l.acquiredAt || "?"}`}>
                        {relativeTime(l.renewedAt)}
                      </span>
                    ) : (
                      <span className="muted">—</span>
                    )}
                  </td>
                  <td className="py-2 pr-4 muted text-xs">
                    {(l.attachedCount ?? l.attachedKeys?.length ?? 0) === 0 ? (
                      <span className="muted">—</span>
                    ) : (
                      <details>
                        <summary className="cursor-pointer">
                          {l.attachedCount ?? l.attachedKeys?.length} key
                          {(l.attachedCount ?? l.attachedKeys?.length ?? 0) === 1 ? "" : "s"}
                        </summary>
                        <ul className="mt-1 font-mono text-[10px] space-y-0.5 max-w-[400px]">
                          {l.attachedKeys?.map((k) => (
                            <li key={k} className="truncate" title={k}>{k}</li>
                          ))}
                          {(l.attachedCount ?? 0) > (l.attachedKeys?.length ?? 0) && (
                            <li className="muted">
                              … +{(l.attachedCount ?? 0) - (l.attachedKeys?.length ?? 0)} more
                            </li>
                          )}
                        </ul>
                      </details>
                    )}
                  </td>
                  <td className="py-2 pr-4 text-right">
                    <button onClick={() => revoke.mutate(l.id)} className="btn btn-ghost">
                      <Trash2 className="w-4 h-4" /> Revoke
                    </button>
                  </td>
                </tr>
              ))}
              {!leases.data?.length && (
                <tr>
                  <td colSpan={6} className="py-6 text-center muted">
                    No active leases.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}

function Card({
  icon: Icon,
  title,
  desc,
  children,
}: {
  icon: React.ComponentType<{ className?: string }>;
  title: string;
  desc: string;
  children: React.ReactNode;
}) {
  return (
    <div className="panel p-5">
      <div className="flex items-center gap-2 text-sm font-medium">
        <Icon className="w-4 h-4 text-accent-500" />
        {title}
      </div>
      <p className="muted text-sm mt-1">{desc}</p>
      <div className="mt-4">{children}</div>
    </div>
  );
}

function Stat({ label, value, warn }: { label: string; value: string; warn?: boolean }) {
  return (
    <div>
      <div className="text-[11px] uppercase tracking-wider muted">{label}</div>
      <div className={"mt-0.5 font-medium " + (warn ? "text-warn" : "")}>{value}</div>
    </div>
  );
}

// "2026-05-13T22:30:11Z" → "3s ago" / "12m ago" / "1h ago". Returns
// raw input on parse failure so we never lose information.
function relativeTime(ts: string): string {
  const t = Date.parse(ts);
  if (!Number.isFinite(t)) return ts;
  const dSec = (Date.now() - t) / 1000;
  if (dSec < 0) return "in " + formatSec(-dSec);
  return formatSec(dSec) + " ago";
}
function formatSec(s: number): string {
  if (s < 60) return `${Math.round(s)}s`;
  if (s < 3600) return `${Math.round(s / 60)}m`;
  if (s < 86400) return `${Math.round(s / 3600)}h`;
  return `${Math.round(s / 86400)}d`;
}

function SnapshotValidator({ cluster }: { cluster: string }) {
  const [file, setFile] = useState<File | null>(null);
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<Awaited<ReturnType<typeof api.etcdutlSnapshotStatus>> | null>(null);
  const [err, setErr] = useState<string | null>(null);

  const run = async () => {
    if (!file) return;
    setBusy(true);
    setErr(null);
    setResult(null);
    try {
      const r = await api.etcdutlSnapshotStatus(cluster, file);
      setResult(r);
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2 flex-wrap">
        <label className="btn cursor-pointer">
          <UploadCloud className="w-4 h-4" />
          {file ? file.name : "Choose .db"}
          <input
            type="file"
            accept=".db,application/octet-stream"
            className="hidden"
            onChange={(e) => {
              setFile(e.target.files?.[0] ?? null);
              setResult(null);
              setErr(null);
            }}
          />
        </label>
        <button
          className="btn btn-primary"
          disabled={!file || busy}
          onClick={run}
        >
          {busy ? "Validating…" : "Validate"}
        </button>
        {file && (
          <span className="text-xs muted font-mono">
            {(file.size / 1024 / 1024).toFixed(1)} MB
          </span>
        )}
      </div>
      {err && <div className="text-xs text-danger">{err}</div>}
      {result && (
        <div className="text-xs">
          {result.ok && result.status ? (
            <div className="grid grid-cols-2 gap-2">
              <Stat label="Hash" value={`0x${result.status.hash.toString(16)}`} />
              <Stat label="Revision" value={result.status.revision.toLocaleString()} />
              <Stat label="Total keys" value={result.status.totalKey.toLocaleString()} />
              <Stat label="On-disk size" value={`${(result.status.totalSize / 1024 / 1024).toFixed(1)} MB`} />
            </div>
          ) : (
            <pre className="panel-2 p-3 rounded-lg overflow-x-auto whitespace-pre-wrap text-warn">
              {result.stderr || result.stdout || `exit ${result.exitCode}`}
            </pre>
          )}
          <div className="muted mt-2">
            {result.ok ? "Snapshot looks healthy — safe to restore." : "Snapshot is corrupt or wrong format."} ({result.durationMs}ms)
          </div>
        </div>
      )}
    </div>
  );
}
