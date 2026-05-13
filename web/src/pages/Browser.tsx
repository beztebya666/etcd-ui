import { useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, type KV } from "../lib/api";
import { useStore } from "../lib/store";
import {
  Key as KeyIcon,
  Save,
  Trash2,
  Plus,
  RefreshCw,
  Copy,
  Download,
  UploadCloud,
  Search,
  History,
  AlertTriangle,
  X,
} from "lucide-react";
import { BulkImportDialog } from "../components/BulkImportDialog";
import { BulkDeletePreview } from "../components/BulkDeletePreview";
import { Checkbox } from "../components/Checkbox";
import { ConflictResolver, type ConflictPayload } from "../components/ConflictResolver";
import { confirm } from "../components/Confirm";
import { toast } from "../components/Toast";
import { HistoryDrawer } from "../components/HistoryDrawer";
import { exportFile, type ExportFormat } from "../lib/export";
import { Skeleton } from "../components/Skeleton";
import { CodeEditor } from "../components/CodeEditor";
import { VirtualTree, buildTree } from "../components/VirtualTree";
import { Dropdown } from "../components/Dropdown";
import { Autocomplete } from "../components/Autocomplete";
import { copyToClipboard } from "../lib/clipboard";

export function Browser() {
  const cluster = useStore((s) => s.selectedCluster);
  const simpleMode = useStore((s) => s.simpleMode);
  const { savedViews, saveView, deleteView, reorderView } = useStore();
  const qc = useQueryClient();
  // Initial state synced from URL — shareable links.
  const initialParams = typeof window !== "undefined" ? new URLSearchParams(window.location.search) : new URLSearchParams();
  const [prefix, setPrefix] = useState(initialParams.get("prefix") || "/");
  const [valueRegex, setValueRegex] = useState(initialParams.get("vr") || "");

  // Reflect prefix/regex back to URL without growing history.
  useEffect(() => {
    if (typeof window === "undefined") return;
    const p = new URLSearchParams(window.location.search);
    if (prefix && prefix !== "/") p.set("prefix", prefix); else p.delete("prefix");
    if (valueRegex) p.set("vr", valueRegex); else p.delete("vr");
    const q = p.toString();
    window.history.replaceState(null, "", q ? "?" + q : window.location.pathname);
  }, [prefix, valueRegex]);
  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  const [draft, setDraft] = useState<{ key: string; value: string } | null>(null);
  // Snapshot of the modRevision at the moment the user opened the editor.
  // When background refetch shows a newer modRev for the same key, somebody
  // else (another tab, another user, an operator with etcdctl) wrote to
  // the key while you were editing — banner warns before you overwrite.
  // 0 = new key (no prior revision).
  const [draftBaseRev, setDraftBaseRev] = useState<number>(0);
  const [open, setOpen] = useState<Set<string>>(new Set([""]));
  const [checked, setChecked] = useState<Set<string>>(new Set());
  const [importOpen, setImportOpen] = useState(false);
  const [historyOpen, setHistoryOpen] = useState(false);
  const [exportMenuOpen, setExportMenuOpen] = useState(false);
  const [bulkDelOpen, setBulkDelOpen] = useState(false);
  const [conflict, setConflict] = useState<ConflictPayload | null>(null);
  const [limit, setLimit] = useState<number>(10000);
  // Resizable left pane. Persisted in localStorage so the operator's
  // preferred width survives reloads. Bounded to [280, 900] — narrower
  // than 280 hides the key icon column; wider than 900 chokes the
  // editor on a typical 1440px monitor.
  const [leftW, setLeftW] = useState<number>(() => {
    if (typeof window === "undefined") return 420;
    const v = Number(window.localStorage.getItem("etcd-ui:browser-left-w") || 420);
    return Number.isFinite(v) && v >= 280 && v <= 900 ? v : 420;
  });
  const browserRootRef = useRef<HTMLDivElement>(null);
  const dragRef = useRef<{ startX: number; startW: number; currentW: number; raf: number } | null>(null);
  useEffect(() => {
    // Drag rewrites the CSS variable directly on the root element via
    // requestAnimationFrame, bypassing React re-renders entirely. Each
    // mousemove just schedules one rAF; the grid template column reads
    // --browser-left-w live so the layout updates at display refresh
    // rate without re-running any component render or persisting to
    // localStorage on every pixel. State + persistence happen exactly
    // once, on mouseup.
    const onMove = (e: MouseEvent) => {
      const d = dragRef.current;
      if (!d) return;
      const dx = e.clientX - d.startX;
      d.currentW = Math.max(280, Math.min(900, d.startW + dx));
      if (d.raf === 0) {
        d.raf = requestAnimationFrame(() => {
          if (!dragRef.current) return;
          browserRootRef.current?.style.setProperty(
            "--browser-left-w",
            `${dragRef.current.currentW}px`,
          );
          dragRef.current.raf = 0;
        });
      }
    };
    const onUp = () => {
      const d = dragRef.current;
      if (!d) return;
      if (d.raf) cancelAnimationFrame(d.raf);
      dragRef.current = null;
      document.body.style.cursor = "";
      document.body.style.userSelect = "";
      // Commit final width to React state + localStorage. This is the
      // only time React re-runs the parent during a drag — the actual
      // resize stayed at 60+ fps because we mutated --browser-left-w
      // directly.
      setLeftW(d.currentW);
      try {
        window.localStorage.setItem("etcd-ui:browser-left-w", String(d.currentW));
      } catch {
        /* localStorage unavailable (private mode, quota) — width still
           applies for the current session, just doesn't persist. */
      }
    };
    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp);
    return () => {
      window.removeEventListener("mousemove", onMove);
      window.removeEventListener("mouseup", onUp);
    };
  }, []);
  // Custom-limit inline editor — the dropdown's last item is a "custom…"
  // sentinel that swaps the trigger for a small numeric input. Lets users
  // pick precise caps (e.g. 13_500) without leaving the keyboard.
  const [limitCustomMode, setLimitCustomMode] = useState(false);
  const [limitCustomDraft, setLimitCustomDraft] = useState("");

  // Debounce the value regex so we don't fire on every keystroke.
  const [debounced, setDebounced] = useState("");
  useEffect(() => {
    const t = window.setTimeout(() => setDebounced(valueRegex), 300);
    return () => window.clearTimeout(t);
  }, [valueRegex]);

  const list = useQuery({
    queryKey: ["range", cluster, prefix, debounced, limit],
    enabled: !!cluster,
    queryFn: () =>
      api.range(cluster!, {
        prefix: prefix === "/" ? "" : prefix,
        limit,
        keysOnly: false,
        valueRegex: debounced || undefined,
      }),
    refetchInterval: 5_000,
  });
  // Cluster-wide total. The range query only returns the count INSIDE
  // the current prefix (and capped by the limit); for the "X loaded of Y
  // total" line in the toolbar we want the real keyspace size.
  const clusterSummary = useQuery({
    queryKey: ["summary", cluster],
    enabled: !!cluster,
    queryFn: () => api.cluster(cluster!),
    refetchInterval: 10_000,
  });
  // When the range is truncated (more=true), ask the server for per-
  // folder counts so the tree can tag `▸ pods (12.3k)` on folders that
  // have more keys than we loaded. Skip the round-trip entirely when
  // nothing is truncated.
  const folderCounts = useQuery({
    queryKey: ["folder-counts", cluster, prefix],
    enabled: !!cluster && !!list.data?.more,
    queryFn: () =>
      api.rangeCounts(cluster!, {
        prefix: prefix === "/" ? "" : prefix,
        depth: 2,
      }),
    refetchInterval: 30_000,
  });
  // Map prefix → total count for quick lookup in the tree renderer.
  const totalByPrefix = useMemo(() => {
    const m = new Map<string, number>();
    for (const b of folderCounts.data?.buckets ?? []) {
      m.set(b.prefix, b.count);
    }
    return m;
  }, [folderCounts.data]);
  // Loaded count per prefix bucket (depth 2). Computed from list.data.
  const loadedByPrefix = useMemo(() => {
    const m = new Map<string, number>();
    for (const kv of list.data?.kvs ?? []) {
      const parts = kv.key.split("/").filter(Boolean);
      if (parts.length < 2) continue;
      const bucket = "/" + parts.slice(0, 2).join("/");
      m.set(bucket, (m.get(bucket) ?? 0) + 1);
    }
    return m;
  }, [list.data]);

  const tree = useMemo(() => buildTree(list.data?.kvs ?? []), [list.data]);
  const dataBytes = useMemo(() => {
    let s = 0;
    for (const k of list.data?.kvs ?? []) {
      s += k.key.length + (k.value?.length ?? 0);
    }
    return s;
  }, [list.data]);
  // Average bytes per key, measured from whatever's currently loaded.
  // Used to project the dropdown's "~N MB" hints against THIS cluster's
  // actual payload distribution instead of a 1KB hard-coded guess. Falls
  // back to 1KB before the first range fetch returns at least 5 keys.
  const avgInfo = useMemo(() => {
    const n = list.data?.kvs.length ?? 0;
    if (n < 5 || dataBytes === 0) {
      return { avg: 1024, basis: "estimate" as const, n };
    }
    return { avg: Math.max(64, Math.round(dataBytes / n)), basis: "measured" as const, n };
  }, [list.data, dataBytes]);
  const avgBytesPerKey = avgInfo.avg;
  const allKeys = useMemo(() => list.data?.kvs.map((k) => k.key) ?? [], [list.data]);
  const selected = list.data?.kvs.find((k) => k.key === selectedKey) ?? null;

  useEffect(() => {
    if (selected && (!draft || draft.key !== selected.key)) {
      setDraft({ key: selected.key, value: selected.value });
      setDraftBaseRev(selected.modRevision);
    }
  }, [selected, draft]);

  // Live conflict signal: same key, modRev moved forward server-side since
  // we started editing. Caller decides whether to show banner + which
  // resolution affordances to offer.
  const staleBase =
    !!draft && !!selected && draftBaseRev > 0 && selected.modRevision > draftBaseRev;

  // Optimistic mutations: the UI updates the cached range immediately so the
  // tree+editor reflect the change before the server round-trip completes.
  // On error we snap back to the prior cache and surface a toast.
  type RangeData = { kvs: KV[]; more: boolean; count: number };

  const putMut = useMutation({
    mutationFn: async (b: { key: string; value: string }) => {
      // Use compare-and-set + 3-way merge whenever we have a base
      // revision (i.e. we're editing an existing key the user opened).
      // For brand-new keys (baseRev=0) it degenerates to a plain put.
      return api.putCAS(cluster!, { key: b.key, value: b.value, baseRev: draftBaseRev });
    },
    onMutate: async (b) => {
      await qc.cancelQueries({ queryKey: ["range", cluster] });
      const snapshots = qc.getQueriesData<RangeData>({ queryKey: ["range", cluster] });
      qc.setQueriesData<RangeData>({ queryKey: ["range", cluster] }, (old) => {
        if (!old) return old;
        const idx = old.kvs.findIndex((k) => k.key === b.key);
        if (idx >= 0) {
          const next = old.kvs.slice();
          next[idx] = { ...next[idx], value: b.value, version: next[idx].version + 1 };
          return { ...old, kvs: next };
        }
        return {
          ...old,
          kvs: [...old.kvs, { key: b.key, value: b.value, createRevision: 0, modRevision: 0, version: 1 }],
          count: old.count + 1,
        };
      });
      return { snapshots };
    },
    onError: (e: Error, _b, ctx) => {
      ctx?.snapshots.forEach(([key, data]) => qc.setQueryData(key, data));
      toast.error(e.message);
    },
    onSuccess: (res, b, ctx) => {
      if (res.status === "conflict") {
        // Roll back optimistic update — the server didn't commit. Surface
        // the resolver. User picks a side per block, then commits via
        // `acceptConflicts:true` from the resolver's onResolve.
        ctx?.snapshots.forEach(([key, data]) => qc.setQueryData(key, data));
        setConflict({
          base: res.base ?? "",
          theirs: res.theirs ?? "",
          merged: res.merged ?? "",
          conflicts: res.conflicts ?? 0,
        });
        return;
      }
      if (res.status === "merged") {
        // Server auto-merged with the live value. Refresh local draft so
        // the editor shows the post-merge content, not our local pre-merge.
        toast.success("Saved with auto-merge — somebody else also wrote to this key");
        return;
      }
      toast.success("Saved");
    },
    onSettled: () => qc.invalidateQueries({ queryKey: ["range", cluster] }),
  });

  // Resolver "Save resolved value" hits this — re-submit with the merged
  // text and acceptConflicts so any leftover markers go through verbatim
  // (user explicitly chose them).
  const commitResolved = async (value: string) => {
    try {
      const r = await api.putCAS(cluster!, {
        key: draft!.key,
        value,
        baseRev: 0, // bypass guard — we already arbitrated
        acceptConflicts: true,
      });
      if (r.status === "conflict") {
        toast.error("Server still reports conflict — pick a side and retry");
        return;
      }
      toast.success("Conflict resolved");
      setConflict(null);
      setDraft({ key: draft!.key, value });
      qc.invalidateQueries({ queryKey: ["range", cluster] });
    } catch (e) {
      toast.error((e as Error).message);
    }
  };

  const delMut = useMutation({
    mutationFn: (key: string) => api.delete(cluster!, { key }),
    onMutate: async (key) => {
      await qc.cancelQueries({ queryKey: ["range", cluster] });
      const snapshots = qc.getQueriesData<RangeData>({ queryKey: ["range", cluster] });
      qc.setQueriesData<RangeData>({ queryKey: ["range", cluster] }, (old) => {
        if (!old) return old;
        const kvs = old.kvs.filter((k) => k.key !== key);
        return { ...old, kvs, count: Math.max(0, old.count - 1) };
      });
      return { snapshots };
    },
    onError: (e: Error, _k, ctx) => {
      ctx?.snapshots.forEach(([key, data]) => qc.setQueryData(key, data));
      toast.error(e.message);
    },
    onSuccess: () => {
      toast.success("Deleted");
      setSelectedKey(null);
      setDraft(null);
    },
    onSettled: () => qc.invalidateQueries({ queryKey: ["range", cluster] }),
  });

  const bulkDel = useMutation({
    mutationFn: (keys: string[]) => api.bulkDelete(cluster!, { keys }),
    onMutate: async (keys) => {
      await qc.cancelQueries({ queryKey: ["range", cluster] });
      const dead = new Set(keys);
      const snapshots = qc.getQueriesData<RangeData>({ queryKey: ["range", cluster] });
      qc.setQueriesData<RangeData>({ queryKey: ["range", cluster] }, (old) => {
        if (!old) return old;
        const kvs = old.kvs.filter((k) => !dead.has(k.key));
        return { ...old, kvs, count: Math.max(0, old.count - dead.size) };
      });
      return { snapshots };
    },
    onError: (e: Error, _k, ctx) => {
      ctx?.snapshots.forEach(([key, data]) => qc.setQueryData(key, data));
      toast.error(e.message);
    },
    onSuccess: (r) => {
      toast.success(`Deleted ${r.deleted} keys`);
      setChecked(new Set());
    },
    onSettled: () => qc.invalidateQueries({ queryKey: ["range", cluster] }),
  });

  if (!cluster) return <NoCluster />;

  const toggleAll = () => {
    if (checked.size === allKeys.length) setChecked(new Set());
    else setChecked(new Set(allKeys));
  };

  const exportSelected = (format: ExportFormat) => {
    const kvs = (list.data?.kvs ?? []).filter((k) => checked.has(k.key));
    exportFile(kvs, cluster ?? "selection", format);
    setExportMenuOpen(false);
  };

  return (
    <div
      ref={browserRootRef}
      className="grid grid-cols-1 lg:grid-cols-[var(--browser-left-w)_6px_1fr] gap-4 min-h-[calc(100vh-120px)] lg:h-[calc(100vh-120px)] relative"
      style={{ ["--browser-left-w" as never]: `${leftW}px` }}
    >
      <div className="panel p-3 flex flex-col min-h-0 max-h-[55vh] lg:max-h-none">
        <div className="flex items-center gap-2 mb-2">
          <Autocomplete
            value={prefix}
            onChange={setPrefix}
            placeholder="prefix"
            ariaLabel="Key prefix"
            monospace
            className="flex-1"
            query={async (input, limit) => {
              if (!cluster) return [];
              const keys = await api.suggestKeys(cluster, input, limit);
              // Surface unique prefix buckets (everything up to the
              // next "/") so the dropdown shows folder paths the user
              // can drill into, plus exact-key matches if the user
              // already typed past the final slash.
              const seen = new Set<string>();
              const out: { value: string; hint?: string }[] = [];
              const cutAt = input.lastIndexOf("/") + 1;
              for (const k of keys) {
                const slash = k.indexOf("/", cutAt);
                const v = slash >= 0 ? k.slice(0, slash + 1) : k;
                if (seen.has(v)) continue;
                seen.add(v);
                out.push({ value: v, hint: slash >= 0 ? "folder" : "key" });
              }
              return out;
            }}
          />
          <button onClick={() => list.refetch()} className="btn btn-ghost" title="Refresh">
            <RefreshCw className="w-4 h-4" />
          </button>
          <button
            onClick={() => {
              const k = `${prefix.replace(/\/$/, "")}/new-key`;
              setDraft({ key: k, value: "" });
              setSelectedKey(k);
            }}
            className="btn btn-primary"
          >
            <Plus className="w-4 h-4" /> New
          </button>
        </div>
        {!simpleMode && (
          <div className="flex items-center gap-2 mb-2">
            <Search className="w-4 h-4 muted" />
            <input
              value={valueRegex}
              onChange={(e) => setValueRegex(e.target.value)}
              placeholder="search inside values (regex)…"
              className="flex-1 h-8 px-3 rounded-lg bg-transparent text-xs font-mono"
              style={{ background: "rgb(var(--panel-2))", border: "1px solid rgb(var(--line))" }}
            />
          </div>
        )}

        {cluster && !simpleMode && (savedViews[cluster]?.length ?? 0) > 0 && (
          <div
            className="flex items-center gap-1 mb-2 flex-wrap"
            role="toolbar"
            aria-label="Saved views"
          >
            {(savedViews[cluster] ?? []).map((v, idx) => (
              <span
                key={v.name}
                className="pill gap-1.5"
                draggable
                onDragStart={(e) => {
                  e.dataTransfer.effectAllowed = "move";
                  e.dataTransfer.setData("text/etcd-ui-view-index", String(idx));
                }}
                onDragOver={(e) => {
                  e.preventDefault();
                  e.dataTransfer.dropEffect = "move";
                }}
                onDrop={(e) => {
                  e.preventDefault();
                  const fromStr = e.dataTransfer.getData("text/etcd-ui-view-index");
                  const from = Number(fromStr);
                  if (Number.isFinite(from) && from !== idx) {
                    reorderView(cluster, from, idx);
                  }
                }}
                title="Drag to reorder"
              >
                <button onClick={() => { setPrefix(v.prefix || "/"); setValueRegex(v.valueRegex); }}>
                  {v.name}
                </button>
                <button
                  onClick={() => deleteView(cluster, v.name)}
                  className="muted hover:text-danger"
                  title="Delete view"
                  aria-label={`Delete saved view ${v.name}`}
                >×</button>
              </span>
            ))}
            <button
              onClick={() => {
                const name = window.prompt("Name this view:");
                if (name && cluster) saveView(cluster, name, prefix === "/" ? "" : prefix, valueRegex);
              }}
              className="pill muted hover:text-current"
            >
              + save view
            </button>
          </div>
        )}

        <div className="flex items-center gap-2 px-1 pb-2 text-[11px] muted flex-wrap">
          <button
            onClick={toggleAll}
            className="shrink-0 cursor-pointer p-0.5 -m-0.5 rounded"
            title="Toggle all"
            aria-label={checked.size === allKeys.length && allKeys.length > 0 ? "Deselect all keys" : "Select all keys"}
            aria-pressed={checked.size === allKeys.length && allKeys.length > 0}
          >
            <Checkbox
              state={
                checked.size === 0
                  ? "off"
                  : checked.size === allKeys.length
                    ? "on"
                    : "indeterminate"
              }
            />
          </button>
          <span
            className="font-medium"
            style={{ color: "rgb(var(--fg))" }}
            title={
              clusterSummary.data?.keyCount != null
                ? `${(list.data?.count ?? 0).toLocaleString()} in prefix · ${clusterSummary.data.keyCount.toLocaleString()} in cluster`
                : `${(list.data?.count ?? 0).toLocaleString()} keys in prefix`
            }
          >
            {compactNum(list.data?.count ?? 0)}
            {clusterSummary.data?.keyCount != null && list.data?.count !== clusterSummary.data.keyCount && (
              <span className="muted font-normal"> / {compactNum(clusterSummary.data.keyCount)}</span>
            )}
          </span>
          <span>keys</span>
          <span className="muted">·</span>
          <span
            title={
              clusterSummary.data
                ? `${dataBytes.toLocaleString()} bytes of live values in the prefix.\n` +
                  `Cluster DB: ${compactBytes(clusterSummary.data.dbSizeBytes)} on disk` +
                  (clusterSummary.data.dbSizeInUse
                    ? ` (${compactBytes(clusterSummary.data.dbSizeInUse)} in use after MVCC compaction; the rest is historical revisions waiting for the next compact).`
                    : ".")
                : `${dataBytes.toLocaleString()} bytes loaded`
            }
          >
            {compactBytes(dataBytes)}
          </span>
          {checked.size > 0 && <span>· {checked.size} sel.</span>}

          {list.data?.more && (
            <button
              onClick={() => setLimit((l) => Math.min(l * 2, MAX_LIMIT))}
              className="inline-flex items-center gap-1 text-warn hover:opacity-80 shrink-0 ml-1"
              title={`Showing first ${limit.toLocaleString()}. Click to double the limit (capped at ${MAX_LIMIT.toLocaleString()}).`}
            >
              <span className="w-1.5 h-1.5 rounded-full bg-warn" />
              load more
            </button>
          )}

          {limitCustomMode ? (
            <form
              className="ml-1 inline-flex items-center gap-1 h-8 px-2 rounded-md surface"
              onSubmit={(e) => {
                e.preventDefault();
                const n = parseLimitInput(limitCustomDraft);
                if (n != null) setLimit(n);
                setLimitCustomMode(false);
              }}
            >
              <span className="muted text-xs">≤</span>
              <input
                autoFocus
                inputMode="numeric"
                value={limitCustomDraft}
                onChange={(e) => setLimitCustomDraft(e.target.value)}
                onBlur={() => {
                  const n = parseLimitInput(limitCustomDraft);
                  if (n != null) setLimit(n);
                  setLimitCustomMode(false);
                }}
                onKeyDown={(e) => {
                  if (e.key === "Escape") {
                    e.preventDefault();
                    setLimitCustomMode(false);
                  }
                }}
                placeholder="13500 or 5k"
                aria-label="Custom limit"
                className="w-24 h-6 px-1 text-xs font-mono bg-transparent border-b outline-none"
                style={{ borderColor: "rgb(var(--line))" }}
              />
              <span className="muted text-[10px] tabular-nums font-mono">
                ~{compactBytes(Math.max(0, parseLimitInput(limitCustomDraft) ?? 0) * avgBytesPerKey)}
              </span>
            </form>
          ) : (
            <span
              className="inline-block ml-1"
              title={
                avgInfo.basis === "measured"
                  ? `≈${compactBytes(avgBytesPerKey)}/key — measured from ${avgInfo.n} loaded keys (${compactBytes(dataBytes)} total)`
                  : `≈${compactBytes(avgBytesPerKey)}/key — fallback estimate, no keys loaded yet. The number sharpens after the first fetch lands.`
              }
            >
            <Dropdown
              value={limit}
              onChange={(v) => {
                const n = Number(v);
                if (n === -1) {
                  setLimitCustomDraft(limit > 0 && !LIMIT_OPTIONS.includes(limit) ? String(limit) : "");
                  setLimitCustomMode(true);
                  return;
                }
                setLimit(n);
              }}
              label="limit"
              ariaLabel="Per-fetch key limit"
              items={[
                // Ghost row mirroring the active custom value so the
                // trigger has something to show (and to highlight). Hidden
                // visually because the same number isn't in LIMIT_OPTIONS,
                // but the Dropdown's `active = items.find(...value)`
                // resolution needs an entry here.
                ...(limit > 0 && !LIMIT_OPTIONS.includes(limit)
                  ? [{
                      value: limit,
                      label: `≤ ${compactNum(limit)}`,
                      hint: `custom · ~${compactBytes(limit * avgBytesPerKey)}`,
                    }]
                  : []),
                ...LIMIT_OPTIONS.map((n) => ({
                  value: n,
                  label: `≤ ${compactNum(n)}`,
                  hint: `~${compactBytes(n * avgBytesPerKey)}`,
                })),
                // No-limit option. The kv handler treats limit≤0 as "all keys"
                // (etcd v3 returns everything in a single range scan).
                {
                  value: 0,
                  label: "no limit",
                  hint: list.data?.count
                    ? `${compactNum(list.data.count)} loaded`
                    : "all keys",
                },
                // Sentinel: opens the inline numeric editor in place of the
                // dropdown trigger. Kept as the very last item so the
                // visual flow reads "5k → 10k → … → custom".
                {
                  value: -1,
                  label: "custom",
                  hint: "",
                },
              ]}
            />
            </span>
          )}

          <div className="ml-auto flex items-center gap-1 relative">
            <button
              onClick={() => setExportMenuOpen((v) => !v)}
              disabled={checked.size === 0}
              className="btn btn-ghost text-xs"
              title="Export selected"
            >
              <Download className="w-3.5 h-3.5" />
            </button>
            {exportMenuOpen && checked.size > 0 && (
              <div
                className="absolute right-0 top-7 z-10 panel p-1 min-w-[140px]"
                onMouseLeave={() => setExportMenuOpen(false)}
              >
                {(["json", "yaml", "env", "toml"] as ExportFormat[]).map((f) => (
                  <button
                    key={f}
                    onClick={() => exportSelected(f)}
                    className="w-full text-left px-2 py-1.5 rounded-md text-xs soft-hover font-mono"
                  >
                    .{f}
                  </button>
                ))}
              </div>
            )}
            <button
              onClick={() => setImportOpen(true)}
              className="btn btn-ghost text-xs"
              title="Bulk import"
            >
              <UploadCloud className="w-3.5 h-3.5" />
            </button>
            <button
              onClick={() => setBulkDelOpen(true)}
              disabled={checked.size === 0 || bulkDel.isPending}
              className="btn btn-ghost text-xs text-danger"
              title="Delete selected"
            >
              <Trash2 className="w-3.5 h-3.5" />
            </button>
          </div>
        </div>

        <div
          className="flex-1 overflow-y-auto"
          onDragOver={(e) => {
            e.preventDefault();
          }}
          onDrop={(e) => {
            if (e.dataTransfer.files.length > 0) {
              e.preventDefault();
              setImportOpen(true);
            }
          }}
        >
          {list.isLoading && (
            <div className="space-y-1.5 p-1.5">
              {Array.from({ length: 12 }).map((_, i) => (
                <Skeleton key={i} className="h-5" />
              ))}
            </div>
          )}
          {!list.isLoading && (
          <VirtualTree
            tree={tree}
            open={open}
            setOpen={setOpen}
            selectedKey={selectedKey}
            checked={checked}
            setChecked={setChecked}
            totalByPrefix={totalByPrefix}
            loadedByPrefix={loadedByPrefix}
            onSelect={(n) => {
              setSelectedKey(n.kv?.key ?? null);
              setDraft(n.kv ? { key: n.kv.key, value: n.kv.value } : null);
              setDraftBaseRev(n.kv?.modRevision ?? 0);
            }}
          />
          )}
        </div>
      </div>

      {/* Drag handle between panels. Hidden below lg where the layout
          stacks vertically (drag-resize is mouse-only UX). Double-click
          resets to the default 420px. */}
      <div
        role="separator"
        aria-orientation="vertical"
        aria-label="Resize key list"
        className="hidden lg:block cursor-col-resize group relative"
        onMouseDown={(e) => {
          dragRef.current = { startX: e.clientX, startW: leftW, currentW: leftW, raf: 0 };
          document.body.style.cursor = "col-resize";
          document.body.style.userSelect = "none";
        }}
        onDoubleClick={() => setLeftW(420)}
        title="Drag to resize · double-click to reset"
      >
        <div
          className="absolute inset-y-0 left-1/2 -translate-x-1/2 w-px transition-colors"
          style={{ background: "rgb(var(--line))" }}
        />
        <div
          className="absolute inset-y-0 left-1/2 -translate-x-1/2 w-[3px] opacity-0 group-hover:opacity-100 transition-opacity rounded-full"
          style={{ background: "rgb(var(--accent-500))" }}
        />
      </div>

      <div className="panel p-3 sm:p-5 flex flex-col min-h-[60vh] lg:min-h-0">
        {draft ? (
          <>
            {staleBase && selected && (
              <div
                className="mb-3 flex items-start gap-2 p-3 rounded-lg text-sm"
                style={{
                  background: "color-mix(in srgb, rgb(var(--warn, 234 179 8)) 12%, transparent)",
                  border: "1px solid color-mix(in srgb, rgb(var(--warn, 234 179 8)) 50%, transparent)",
                }}
                role="status"
                aria-live="polite"
              >
                <AlertTriangle className="w-4 h-4 mt-0.5 shrink-0 text-warn" />
                <div className="flex-1 min-w-0">
                  <div className="font-medium">This key changed on the server while you were editing.</div>
                  <div className="muted text-xs mt-0.5 font-mono">
                    your base rev {draftBaseRev} → current rev {selected.modRevision}.
                    Save will overwrite the newer version.
                  </div>
                </div>
                <button
                  onClick={() => {
                    setDraft({ key: selected.key, value: selected.value });
                    setDraftBaseRev(selected.modRevision);
                  }}
                  className="btn btn-ghost text-xs shrink-0"
                  title="Discard your edits and load the latest server value"
                >
                  Reload
                </button>
              </div>
            )}
            <div className="flex items-center gap-2 mb-3 flex-wrap">
              <input
                value={draft.key}
                onChange={(e) => setDraft({ ...draft, key: e.target.value })}
                aria-label="Key path"
                className="flex-1 min-w-[200px] h-10 px-3 rounded-lg bg-transparent text-sm font-mono"
                style={{ background: "rgb(var(--panel-2))", border: "1px solid rgb(var(--line))" }}
              />
              <button
                className="btn btn-ghost"
                onClick={async () => {
                  if (await copyToClipboard(draft.key)) toast.success("Copied");
                  else toast.error("Couldn't copy — select and ⌘C manually");
                }}
                title="Copy key"
                aria-label="Copy key to clipboard"
              >
                <Copy className="w-4 h-4" />
              </button>
              {selected && !simpleMode && (
                <button
                  className="btn"
                  onClick={() => setHistoryOpen(true)}
                  title="View history"
                  aria-label="View revision history for this key"
                >
                  <History className="w-4 h-4" />
                  {/* Hide the word at editor-pane widths < ~520px so the
                      action row stays on one line instead of wrapping
                      Save underneath. Tooltip still names the action. */}
                  <span className="hidden xl:inline">History</span>
                </button>
              )}
              <button
                className="btn"
                onClick={async () => {
                  if (!selected) return;
                  const ok = await confirm({
                    title: `Delete "${selected.key}"?`,
                    body: "This action cannot be undone.",
                    danger: true,
                    confirmLabel: "Delete",
                  });
                  if (ok) delMut.mutate(selected.key);
                }}
                disabled={!selected || delMut.isPending}
                title="Delete this key"
                aria-label={selected ? `Delete key ${selected.key}` : "Delete key"}
              >
                <Trash2 className="w-4 h-4" />
                <span className="hidden xl:inline">Delete</span>
              </button>
              <button
                className="btn btn-primary"
                onClick={() => putMut.mutate({ key: draft.key, value: draft.value })}
                disabled={putMut.isPending}
                title={putMut.isPending ? "Saving…" : "Save (Ctrl+S)"}
                aria-label={putMut.isPending ? "Saving" : "Save key"}
              >
                <Save className="w-4 h-4" />
                <span className="hidden xl:inline">{putMut.isPending ? "Saving…" : "Save"}</span>
              </button>
              {/* Cancel only makes sense on a new-key draft (no `selected`
                  cluster record yet). For existing keys, the editor IS
                  the persistent view — closing it would be confusing. */}
              {!selected && (
                <button
                  className="btn btn-ghost"
                  onClick={() => {
                    setDraft(null);
                    setSelectedKey(null);
                  }}
                  title="Cancel new key (discard draft)"
                  aria-label="Cancel new key"
                >
                  <X className="w-4 h-4" />
                  <span className="hidden xl:inline">Cancel</span>
                </button>
              )}
            </div>
            {selected && (
              <div className="flex items-center gap-2 text-xs muted mb-3 flex-wrap">
                <span className="pill">rev {selected.modRevision}</span>
                <span className="pill">v{selected.version}</span>
                {selected.lease ? <span className="pill">lease {selected.lease}</span> : null}
                {(() => {
                  const canFormat = (() => {
                    try { JSON.parse(draft.value); return true; } catch { return false; }
                  })();
                  if (!canFormat) return null;
                  return (
                    <button
                      className="ml-auto btn btn-ghost text-xs"
                      onClick={() => {
                        try {
                          const pretty = JSON.stringify(JSON.parse(draft.value), null, 2);
                          setDraft({ ...draft, value: pretty });
                        } catch {
                          /* unreachable — canFormat already validated */
                        }
                      }}
                    >
                      Format JSON
                    </button>
                  );
                })()}
              </div>
            )}
            <div
              className="flex-1 rounded-lg overflow-hidden"
              style={{ background: "rgb(var(--panel-2))", border: "1px solid rgb(var(--line))" }}
            >
              <CodeEditor
                value={draft.value}
                onChange={(v) => setDraft({ ...draft, value: v })}
                preview={selected?.preview}
              />
            </div>
            {putMut.error ? (
              <div className="mt-3 text-sm text-danger">{(putMut.error as Error).message}</div>
            ) : null}
          </>
        ) : (
          <EmptyEditor />
        )}
      </div>

      <BulkImportDialog
        open={importOpen}
        onClose={() => setImportOpen(false)}
        cluster={cluster}
        onApplied={() => qc.invalidateQueries({ queryKey: ["range"] })}
      />
      <HistoryDrawer
        open={historyOpen}
        onClose={() => setHistoryOpen(false)}
        cluster={cluster}
        k={selected?.key ?? null}
        onRestore={(v) => {
          setDraft({ key: v.key, value: v.value });
          setHistoryOpen(false);
          toast.info("Loaded into editor — press Save to apply");
        }}
      />
      <BulkDeletePreview
        open={bulkDelOpen}
        keys={Array.from(checked)}
        pending={bulkDel.isPending}
        onCancel={() => setBulkDelOpen(false)}
        onConfirm={() => {
          bulkDel.mutate(Array.from(checked), {
            onSettled: () => setBulkDelOpen(false),
          });
        }}
      />
      <ConflictResolver
        open={!!conflict}
        payload={conflict}
        ours={draft?.value ?? ""}
        onResolve={commitResolved}
        onCancel={() => setConflict(null)}
      />
    </div>
  );
}

function EmptyEditor() {
  return (
    <div className="m-auto text-center muted text-sm max-w-sm">
      <KeyIcon className="w-6 h-6 mx-auto mb-2 muted" />
      Pick a key on the left to edit, or hit <span className="kbd">New</span> to create one.
    </div>
  );
}

function NoCluster() {
  return (
    <div className="panel p-10 text-center muted">
      Select a cluster from the top-right picker to start browsing keys.
    </div>
  );
}

function compactNum(n: number): string {
  // Show the exact integer up to 99_999 so a count like 26_978 doesn't
  // round to a misleading "27k". Above that, accept lossy compaction —
  // operators reading a number on a million-key cluster don't need the
  // last few digits, but they DO need to know "I selected 26978" maps
  // to the same on-screen number.
  if (n < 100_000) return n.toLocaleString();
  if (n >= 1_000_000) {
    const m = n / 1_000_000;
    // Always include one decimal so it's obvious the value is rounded.
    return m.toFixed(1) + "M";
  }
  return (n / 1_000).toFixed(1) + "k";
}

function compactBytes(n: number): string {
  if (n >= 1024 * 1024 * 1024) return (n / 1024 / 1024 / 1024).toFixed(1).replace(/\.0$/, "") + " GB";
  if (n >= 1024 * 1024) return (n / 1024 / 1024).toFixed(1).replace(/\.0$/, "") + " MB";
  if (n >= 1024) return (n / 1024).toFixed(1).replace(/\.0$/, "") + " KB";
  return n + " B";
}

const LIMIT_OPTIONS = [5_000, 10_000, 25_000, 50_000, 100_000, 250_000, 500_000, 1_000_000, 2_000_000];
const MAX_LIMIT = LIMIT_OPTIONS[LIMIT_OPTIONS.length - 1];

// Accept plain integers and short SI suffixes (5k, 1.5M). Negative or NaN
// returns null; the caller leaves the limit untouched. Capped at MAX_LIMIT
// so a stray "9999999" doesn't try to materialise the world.
function parseLimitInput(raw: string): number | null {
  const s = raw.trim().toLowerCase();
  if (s === "") return null;
  const m = /^(\d+(?:[.,]\d+)?)\s*([km])?$/.exec(s);
  if (!m) return null;
  const n = Number(m[1].replace(",", "."));
  if (!Number.isFinite(n) || n < 0) return null;
  const mult = m[2] === "k" ? 1_000 : m[2] === "m" ? 1_000_000 : 1;
  return Math.min(MAX_LIMIT, Math.max(1, Math.round(n * mult)));
}
