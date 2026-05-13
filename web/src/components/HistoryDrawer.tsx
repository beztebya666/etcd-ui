import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, type KV } from "../lib/api";
import { X, History, Copy, GitCompareArrows, AlertTriangle, Braces } from "lucide-react";
import { motion, AnimatePresence } from "framer-motion";
import { toast } from "./Toast";
import { smartDiff, prettyJSON, groupHunks } from "../lib/diff";
import { cn } from "../lib/cn";
import { useFocusTrap } from "../lib/focusTrap";
import { looksBinary } from "./CodeEditor";
import { copyToClipboard } from "../lib/clipboard";

export function HistoryDrawer({
  open,
  onClose,
  cluster,
  k,
  onRestore,
}: {
  open: boolean;
  onClose: () => void;
  cluster: string | null;
  k: string | null;
  onRestore?: (v: KV) => void;
}) {
  const q = useQuery({
    queryKey: ["history", cluster, k],
    enabled: !!(open && cluster && k),
    queryFn: () => api.history(cluster!, k!),
  });
  const [compareWith, setCompareWith] = useState<number | null>(null);
  const head = q.data?.versions[0];
  const target = q.data?.versions.find((v) => v.modRevision === compareWith);
  const trapRef = useFocusTrap<HTMLDivElement>(open);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  return (
    <AnimatePresence>
      {open && (
        <motion.div
          key="bg"
          initial={{ opacity: 0 }}
          animate={{ opacity: 1 }}
          exit={{ opacity: 0 }}
          className="fixed inset-0 z-40 bg-black/65"
          onMouseDown={onClose}
        >
          <motion.aside
            ref={trapRef}
            role="dialog"
            aria-modal="true"
            aria-label="Key history"
            tabIndex={-1}
            initial={{ x: "100%" }}
            animate={{ x: 0 }}
            exit={{ x: "100%" }}
            transition={{ type: "spring", stiffness: 350, damping: 40 }}
            onMouseDown={(e) => e.stopPropagation()}
            className="absolute right-0 top-0 bottom-0 w-[min(820px,100vw)] flex flex-col"
            style={{ background: "rgb(var(--panel))", borderLeft: "1px solid rgb(var(--line))" }}
          >
            <header
              className="flex items-center gap-3 px-5 py-4 border-b"
              style={{ borderColor: "rgb(var(--line))" }}
            >
              <History className="w-4 h-4 text-accent-500" />
              <div className="min-w-0">
                <div className="text-sm font-semibold truncate">History</div>
                <div className="font-mono text-xs muted truncate">{k}</div>
              </div>
              <div className="ml-auto" />
              <button onClick={onClose} className="btn btn-ghost">
                <X className="w-4 h-4" />
              </button>
            </header>

            <div className="p-2 overflow-y-auto flex-1">
              {q.isLoading && <div className="p-6 text-sm muted">Loading history…</div>}
              {q.error && <div className="p-6 text-sm text-danger">{(q.error as Error).message}</div>}
              {q.data?.versions.length === 0 && (
                <div className="p-6 text-sm muted">No history available (compaction may have removed older versions).</div>
              )}
              {q.data?.versions.map((v, i) => (
                <VersionRow
                  key={v.modRevision}
                  v={v}
                  isHead={i === 0}
                  compareSet={compareWith === v.modRevision}
                  onToggleCompare={() =>
                    setCompareWith(compareWith === v.modRevision ? null : v.modRevision)
                  }
                  onRestore={onRestore ? () => onRestore(v) : undefined}
                />
              ))}
              {head && target && (
                <DiffPanel left={target} right={head} />
              )}
              {q.data?.truncatedAt && <CompactedNotice err={q.data.truncatedAt} />}
            </div>
          </motion.aside>
        </motion.div>
      )}
    </AnimatePresence>
  );
}

function VersionRow({
  v,
  isHead,
  compareSet,
  onToggleCompare,
  onRestore,
}: {
  v: KV;
  isHead: boolean;
  compareSet: boolean;
  onToggleCompare: () => void;
  onRestore?: () => void;
}) {
  const binary = useMemo(() => looksBinary(v.value), [v.value]);
  // Default: pretty-format if it's JSON. Operators want readable diffs,
  // and kube-apiserver stores most resources as single-line JSON.
  const [pretty, setPretty] = useState(true);
  const display = useMemo(() => (pretty && !binary ? prettyJSON(v.value) : v.value), [v.value, pretty, binary]);
  const canFormat = useMemo(() => {
    if (binary) return false;
    try {
      JSON.parse(v.value);
      return true;
    } catch {
      return false;
    }
  }, [v.value, binary]);

  return (
    <div className="rounded-lg p-3 mb-1 soft-hover">
      <div className="flex items-center gap-2 text-xs flex-wrap">
        <span className="pill">rev {v.modRevision}</span>
        <span className="pill">v{v.version}</span>
        {isHead && <span className="pill text-accent-500 border-accent-500/40">current</span>}
        {v.preview && (
          <span className="pill !text-[10px] !text-accent-500 !border-accent-500/40 uppercase tracking-wider">
            {v.preview.format} · {v.preview.kind || "?"}
          </span>
        )}
        <div className="ml-auto flex items-center gap-1">
          {canFormat && (
            <button
              onClick={() => setPretty((p) => !p)}
              className={cn("btn btn-ghost text-xs", pretty && "text-accent-500")}
              title={pretty ? "Show compact JSON" : "Pretty-print JSON"}
            >
              <Braces className="w-3.5 h-3.5" />
            </button>
          )}
          <button
            onClick={async () => {
              if (await copyToClipboard(display)) toast.success("Copied");
              else toast.error("Couldn't copy — select and ⌘C manually");
            }}
            className="btn btn-ghost text-xs"
            title="Copy value"
          >
            <Copy className="w-3.5 h-3.5" />
          </button>
          {!isHead && (
            <button
              onClick={onToggleCompare}
              className={cn("btn btn-ghost text-xs", compareSet && "soft-active")}
              title="Compare with current"
            >
              <GitCompareArrows className="w-3.5 h-3.5" />
            </button>
          )}
          {!isHead && onRestore && (
            <button onClick={onRestore} className="btn btn-ghost text-xs">
              Restore
            </button>
          )}
        </div>
      </div>
      {binary ? (
        <div className="mt-2 panel-2 p-2 rounded-md text-xs muted">
          binary · {v.value.length} B
          {v.preview && (
            <>
              {" · "}
              <span style={{ color: "rgb(var(--fg))" }}>
                {v.preview.kind}
                {v.preview.namespace && ` · ${v.preview.namespace}`}
                {v.preview.name && ` / ${v.preview.name}`}
              </span>
            </>
          )}
        </div>
      ) : (
        <pre className="mt-2 font-mono text-xs whitespace-pre panel-2 p-2 rounded-md max-h-48 overflow-auto">
          {display || <span className="muted">(empty)</span>}
        </pre>
      )}
    </div>
  );
}

function DiffPanel({ left, right }: { left: KV; right: KV }) {
  // Argo-style: side-by-side red/green columns, with unchanged hunks
  // collapsed by default. K8s objects diff cleanly after JSON pretty-print.
  const segs = useMemo(() => smartDiff(left.value, right.value), [left.value, right.value]);
  const hunks = useMemo(() => groupHunks(segs), [segs]);
  const [expanded, setExpanded] = useState<Set<number>>(new Set());
  const counts = useMemo(() => {
    let add = 0, del = 0;
    for (const s of segs) {
      if (s.kind === "add") add++;
      else if (s.kind === "del") del++;
    }
    return { add, del };
  }, [segs]);

  return (
    <div className="mt-3 mx-1 panel p-3">
      <div className="text-xs muted mb-2 flex items-center gap-2 flex-wrap">
        <span>Diff: rev {left.modRevision} → current rev {right.modRevision}</span>
        <span className="ml-auto pill !text-[10px] !text-danger !border-danger/40">
          −{counts.del}
        </span>
        <span className="pill !text-[10px] !text-accent-500 !border-accent-500/40">
          +{counts.add}
        </span>
      </div>
      <div className="max-h-[420px] overflow-auto font-mono text-[12px] leading-relaxed">
        {hunks.map((h, hi) => {
          if (h.kind === "same") {
            const isExpanded = expanded.has(hi);
            // Show 1-2 surrounding "same" lines as context; collapse longer runs.
            const CONTEXT = 2;
            if (h.lines.length <= CONTEXT * 2 + 1 || isExpanded) {
              return h.lines.map((s, li) => (
                <DiffLine key={`${hi}-${li}`} kind="same" text={s.text} />
              ));
            }
            const head = h.lines.slice(0, CONTEXT);
            const tail = h.lines.slice(-CONTEXT);
            const hidden = h.lines.length - CONTEXT * 2;
            return (
              <div key={hi}>
                {head.map((s, li) => (
                  <DiffLine key={`${hi}-h${li}`} kind="same" text={s.text} />
                ))}
                <button
                  onClick={() => {
                    const next = new Set(expanded);
                    next.add(hi);
                    setExpanded(next);
                  }}
                  className="block w-full text-left px-2 py-1 text-[11px] muted hover:text-current"
                  style={{ background: "rgba(255,255,255,0.02)" }}
                >
                  … expand {hidden} unchanged line{hidden === 1 ? "" : "s"}
                </button>
                {tail.map((s, li) => (
                  <DiffLine key={`${hi}-t${li}`} kind="same" text={s.text} />
                ))}
              </div>
            );
          }
          // Change hunk — group del lines before add lines (already in order
          // from LCS reconstruction).
          return h.lines.map((s, li) => (
            <DiffLine key={`${hi}-${li}`} kind={s.kind} text={s.text} />
          ));
        })}
      </div>
    </div>
  );
}

function DiffLine({ kind, text }: { kind: "same" | "add" | "del"; text: string }) {
  const sigil = kind === "add" ? "+" : kind === "del" ? "−" : " ";
  return (
    <div
      className={cn(
        "grid grid-cols-[20px_1fr] gap-2 px-2 whitespace-pre-wrap break-all",
        kind === "add" && "text-accent-500",
        kind === "del" && "text-danger",
        kind === "same" && "muted",
      )}
      style={{
        background:
          kind === "add"
            ? "color-mix(in srgb, rgb(var(--accent-500)) 10%, transparent)"
            : kind === "del"
              ? "color-mix(in srgb, rgb(var(--danger, 248 113 113)) 10%, transparent)"
              : undefined,
      }}
    >
      <span className="opacity-50 select-none">{sigil}</span>
      <span>{text}</span>
    </div>
  );
}

function CompactedNotice({ err }: { err: string }) {
  // The history endpoint surfaces etcd's own error verbatim when the
  // server has compacted past the revision we asked for. Translate
  // "required revision has been compacted" into something an operator
  // can act on without grepping for the term.
  const isCompacted = /compacted/i.test(err);
  return (
    <div
      className="p-3 m-2 rounded-md text-xs flex items-start gap-2"
      style={{
        background: "color-mix(in srgb, rgb(var(--warn, 234 179 8)) 10%, transparent)",
        border: "1px solid color-mix(in srgb, rgb(var(--warn, 234 179 8)) 30%, transparent)",
      }}
    >
      <AlertTriangle className="w-3.5 h-3.5 mt-0.5 shrink-0 text-warn" />
      <div className="flex-1 min-w-0">
        <div className="text-warn font-medium mb-1">History truncated</div>
        {isCompacted ? (
          <div className="muted">
            etcd has <em>compacted</em> revisions older than the ones shown above. Compaction
            permanently deletes historical key versions to reclaim disk space; what remains is the
            full timeline since the last <code className="kbd">etcdctl compact</code> ran (or auto-compact,
            controlled by the cluster's <code className="kbd">--auto-compaction-retention</code> flag —
            commonly 1 hour or 1000 revisions on K8s control-planes). Older edits are unrecoverable
            without an etcd snapshot from before the compaction.
          </div>
        ) : (
          <div className="muted font-mono break-all">{err}</div>
        )}
      </div>
    </div>
  );
}
