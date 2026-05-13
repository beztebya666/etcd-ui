import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, bytesPretty } from "../lib/api";
import { useStore } from "../lib/store";
import { Crown, Server, GitBranch, Database, AlertTriangle, ShieldCheck, Key as KeyIcon, ArrowUpCircle } from "lucide-react";
import { ProtocolBadge } from "../components/ProtocolBadge";
import { confirm } from "../components/Confirm";
import { toast } from "../components/Toast";

export function ClusterPage() {
  const cluster = useStore((s) => s.selectedCluster);
  const qc = useQueryClient();
  const summary = useQuery({
    queryKey: ["summary", cluster],
    enabled: !!cluster,
    queryFn: () => api.cluster(cluster!),
    refetchInterval: 3_000,
  });
  const members = useQuery({
    queryKey: ["members", cluster],
    enabled: !!cluster,
    queryFn: () => api.members(cluster!),
    refetchInterval: 5_000,
  });
  const moveLeader = useMutation({
    mutationFn: (memberIdStr: string) => api.moveLeader(cluster!, memberIdStr),
    onSuccess: () => {
      toast.success("Leader transferred — cluster will pick up the new leader within a second");
      qc.invalidateQueries({ queryKey: ["members", cluster] });
      qc.invalidateQueries({ queryKey: ["summary", cluster] });
    },
    onError: (e: Error) => toast.error(e.message),
  });

  if (!cluster) return <div className="panel p-10 text-center muted">Pick a cluster first.</div>;

  const s = summary.data;
  return (
    <div className="space-y-6">
      <div className="flex items-end justify-between flex-wrap gap-3">
        <div>
          <div className="flex items-center gap-2 flex-wrap">
            <h1 className="text-2xl font-semibold tracking-tight">{s?.name ?? "Cluster"}</h1>
            <ProtocolBadge api={s?.apiVersion} serverVersion={s?.serverVersion} />
            {s?.serverVersion && (
              <span className="pill !text-[10px] font-mono">{s.serverVersion}</span>
            )}
          </div>
          <p className="muted mt-1">
            {s?.source} · {s?.endpoints?.join(", ")}
          </p>
        </div>
        {s?.alarms && s.alarms.length > 0 ? (
          <span className="tag tag-warn">
            <AlertTriangle className="w-4 h-4" /> {s.alarms.join(", ")}
          </span>
        ) : (
          <span className="tag tag-success">
            <ShieldCheck className="w-4 h-4" /> healthy
          </span>
        )}
      </div>

      <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-5 gap-4">
        <Stat
          icon={Crown}
          label="leader"
          value={s?.leaderShort ?? s?.leader ?? "—"}
          title={s?.leader}
        />
        <Stat
          icon={GitBranch}
          label="raft term"
          value={s?.raftTerm?.toString() ?? "—"}
          title={
            "Raft term increments on every election round, NOT every leader change. " +
            "A failed vote (split brain, network hiccup, slow node) bumps the term " +
            "without producing a new leader, so term >> leader-change count is normal."
          }
        />
        <Stat
          icon={Database}
          label="db size"
          value={s ? bytesPretty(s.dbSizeBytes) : "—"}
          hint={
            s && s.dbSizeInUse > 0 && s.dbSizeBytes > s.dbSizeInUse * 1.2
              ? `${bytesPretty(s.dbSizeInUse)} live · rest is MVCC history`
              : undefined
          }
        />
        <Stat icon={KeyIcon} label="keys" value={s?.keyCount != null ? s.keyCount.toLocaleString() : "—"} />
        <Stat icon={Server} label="members" value={s?.memberCount?.toString() ?? "—"} />
      </div>

      <div className="panel p-5">
        <div className="text-sm font-medium mb-3">Members</div>
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="text-left muted text-[11px] uppercase tracking-wider">
              <tr>
                <th className="py-2 pr-4">Name</th>
                <th className="py-2 pr-4">ID</th>
                <th className="py-2 pr-4">Peer URLs</th>
                <th className="py-2 pr-4">Client URLs</th>
                <th className="py-2 pr-4">Role</th>
                <th className="py-2 pr-4 text-right">Action</th>
              </tr>
            </thead>
            <tbody className="divide-y" style={{ borderColor: "rgb(var(--line))" }}>
              {members.data?.map((m) => (
                <tr key={m.idStr ?? m.id} className="row-hover">
                  <td className="py-2 pr-4 font-medium">{m.name}</td>
                  <td className="py-2 pr-4 font-mono text-xs" title={m.idStr}>
                    {m.idStr ?? m.id}
                  </td>
                  <td className="py-2 pr-4 muted">{m.peerUrls.join(", ")}</td>
                  <td className="py-2 pr-4 muted">{m.clientUrls.join(", ")}</td>
                  <td className="py-2 pr-4">
                    {m.isLeader ? (
                      <span className="pill text-accent-500 border-accent-500/40">leader</span>
                    ) : m.isLearner ? (
                      <span className="pill">learner</span>
                    ) : (
                      <span className="pill">follower</span>
                    )}
                  </td>
                  <td className="py-2 pr-4 text-right">
                    {!m.isLeader && !m.isLearner && (
                      <button
                        onClick={async () => {
                          const ok = await confirm({
                            title: `Transfer leadership to ${m.name}?`,
                            body:
                              `Raft will elect ${m.name} as the new leader. This is a controlled, non-destructive ` +
                              `operation: etcd refuses the transfer if the target hasn't caught up to the current ` +
                              `leader's log, so there's no risk of data loss. Brief (~100ms) blip in write latency ` +
                              `while the term advances.`,
                            confirmLabel: "Make leader",
                            danger: false,
                          });
                          if (ok) moveLeader.mutate(m.idStr ?? String(m.id));
                        }}
                        disabled={moveLeader.isPending}
                        className="btn btn-ghost text-xs"
                        title="Transfer raft leadership to this member"
                      >
                        <ArrowUpCircle className="w-3.5 h-3.5" />
                        Make leader
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}

function Stat({
  icon: Icon,
  label,
  value,
  title,
  hint,
}: {
  icon: React.ComponentType<{ className?: string }>;
  label: string;
  value: string;
  title?: string;
  hint?: string;
}) {
  return (
    <div className="panel p-4" title={title}>
      <div className="flex items-center gap-1.5 text-[11px] uppercase tracking-wider muted">
        <Icon className="w-3 h-3" />
        {label}
      </div>
      <div className="text-lg font-medium mt-1 truncate">{value}</div>
      {hint && <div className="text-[10px] muted mt-1 truncate">{hint}</div>}
    </div>
  );
}
