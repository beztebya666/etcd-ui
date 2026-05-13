import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { useStore } from "../lib/store";
import { GitCompareArrows, ArrowRightLeft, MinusCircle, PlusCircle, CircleDotDashed } from "lucide-react";
import { cn } from "../lib/cn";
import { Dropdown } from "../components/Dropdown";

export function DiffPage() {
  const cluster = useStore((s) => s.selectedCluster);
  const { diffAgainst, setDiffAgainst } = useStore();
  const [prefix, setPrefix] = useState("");
  const [filter, setFilter] = useState<"all" | "only-left" | "only-right" | "different">("all");

  const clusters = useQuery({ queryKey: ["clusters"], queryFn: api.clusters });
  const diff = useQuery({
    queryKey: ["diff", cluster, diffAgainst, prefix],
    enabled: !!(cluster && diffAgainst),
    queryFn: () => api.diff(cluster!, diffAgainst!, prefix),
  });

  if (!cluster) return <div className="panel p-10 text-center muted">Pick a cluster first.</div>;

  const others = (clusters.data ?? []).filter((c) => c.id !== cluster);
  const visible = (diff.data?.diffs ?? []).filter((d) => filter === "all" || d.kind === filter);

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight flex items-center gap-2">
          <GitCompareArrows className="w-5 h-5 text-accent-500" /> Cluster diff
        </h1>
        <p className="muted mt-1">Pairwise compare two etcd clusters under a prefix. Identical keys are hidden.</p>
      </div>

      <div className="panel p-4 grid grid-cols-1 md:grid-cols-[1fr_auto_1fr_auto] gap-3 items-end">
        <Disabled label="Left (current)" value={cluster} />
        <ArrowRightLeft className="w-4 h-4 muted mx-auto mb-3" />
        <div>
          <div className="text-xs muted mb-1">Right (compare to)</div>
          <Dropdown<string>
            value={diffAgainst ?? ""}
            onChange={(v) => setDiffAgainst(v || null)}
            buttonClassName="w-full h-10 justify-between"
            ariaLabel="cluster to compare against"
            items={[
              { value: "", label: "— select cluster —" },
              ...others.map((c) => ({ value: c.id, label: c.name })),
            ]}
          />
        </div>
        <button onClick={() => diff.refetch()} className="btn btn-primary">Compare</button>
      </div>

      <div className="panel p-3 flex items-center gap-2">
        <input
          value={prefix}
          onChange={(e) => setPrefix(e.target.value)}
          placeholder="prefix (empty = whole keyspace)"
          className="flex-1 h-9 px-3 rounded-lg text-sm font-mono"
          style={{ background: "rgb(var(--panel-2))", border: "1px solid rgb(var(--line))" }}
        />
        <FilterPill cur={filter} val="all" set={setFilter}>All ({diff.data?.total ?? 0})</FilterPill>
        <FilterPill cur={filter} val="only-left" set={setFilter}>Only left</FilterPill>
        <FilterPill cur={filter} val="only-right" set={setFilter}>Only right</FilterPill>
        <FilterPill cur={filter} val="different" set={setFilter}>Different</FilterPill>
      </div>

      <div className="panel">
        {diff.isLoading && (
          <div className="p-10 text-center text-sm">
            <ArrowRightLeft className="w-7 h-7 mx-auto mb-2 muted animate-pulse" />
            <div className="font-medium">Computing diff…</div>
            <div className="muted mt-1">Full range scan on both clusters — large keyspaces take a moment.</div>
          </div>
        )}
        {!diff.data && !diff.isLoading && (
          <div className="p-12 text-center text-sm">
            <GitCompareArrows className="w-8 h-8 mx-auto mb-2 muted" />
            <div className="font-medium">Compare two clusters</div>
            <div className="muted mt-1 max-w-md mx-auto">
              {others.length === 0
                ? "Add a second cluster on the Dashboard before you can diff."
                : 'Pick a "Right" cluster above and press Compare. Identical keys are hidden.'}
            </div>
          </div>
        )}
        {diff.error && !diff.isLoading && (
          <div className="p-12 text-center text-sm">
            <MinusCircle className="w-8 h-8 mx-auto mb-2 text-danger" />
            <div className="font-medium">Diff failed</div>
            <div className="muted mt-1">{(diff.error as Error).message}</div>
          </div>
        )}
        {visible.length > 0 && (
          <ul className="divide-y" style={{ borderColor: "rgb(var(--line))" }}>
            {visible.map((d) => (
              <li key={d.kind + d.key} className="px-4 py-3 grid grid-cols-[20px_1fr] gap-3 text-sm">
                <Marker kind={d.kind} />
                <div className="min-w-0">
                  <div className="font-mono text-[13px] truncate">{d.key}</div>
                  <div className="mt-1 grid grid-cols-1 md:grid-cols-2 gap-2 text-xs">
                    {d.kind !== "only-right" && (
                      <pre className="panel-2 p-2 rounded-md max-h-40 overflow-auto whitespace-pre-wrap break-all">
                        {d.left || <span className="muted">(empty)</span>}
                      </pre>
                    )}
                    {d.kind !== "only-left" && (
                      <pre className="panel-2 p-2 rounded-md max-h-40 overflow-auto whitespace-pre-wrap break-all">
                        {d.right || <span className="muted">(empty)</span>}
                      </pre>
                    )}
                  </div>
                </div>
              </li>
            ))}
          </ul>
        )}
        {diff.data && visible.length === 0 && (
          <div className="p-12 text-center text-sm">
            <CircleDotDashed className="w-8 h-8 mx-auto mb-2 text-accent-500" />
            <div className="font-medium">All keys match</div>
            <div className="muted mt-1">
              {filter === "all"
                ? "Both clusters have identical key/value pairs under this prefix."
                : "No entries of this kind. Try the All filter."}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

function Disabled({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="text-xs muted mb-1">{label}</div>
      <div
        className="h-10 px-3 rounded-lg text-sm flex items-center font-mono"
        style={{ background: "rgb(var(--panel-2))", border: "1px solid rgb(var(--line))" }}
      >
        {value}
      </div>
    </div>
  );
}

function FilterPill({
  cur,
  val,
  set,
  children,
}: {
  cur: string;
  val: "all" | "only-left" | "only-right" | "different";
  set: (v: "all" | "only-left" | "only-right" | "different") => void;
  children: React.ReactNode;
}) {
  return (
    <button
      onClick={() => set(val)}
      className={cn("btn btn-ghost text-xs", cur === val && "soft-active")}
    >
      {children}
    </button>
  );
}

function Marker({ kind }: { kind: string }) {
  if (kind === "only-left") return <MinusCircle className="w-4 h-4 text-danger mt-1" />;
  if (kind === "only-right") return <PlusCircle className="w-4 h-4 text-accent-500 mt-1" />;
  return <CircleDotDashed className="w-4 h-4 text-warn mt-1" />;
}
