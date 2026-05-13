import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from "react";
import { useVirtualizer } from "@tanstack/react-virtual";
import type { KV } from "../lib/api";
import {
  ChevronRight,
  ChevronDown,
  Key as KeyIcon,
  FolderClosed,
} from "lucide-react";
import { cn } from "../lib/cn";
import { looksBinary } from "./CodeEditor";
import { Checkbox } from "./Checkbox";

export type TreeNode = {
  name: string;
  fullKey: string;
  isLeaf: boolean;
  kv?: KV;
  children: Map<string, TreeNode>;
};

export function buildTree(kvs: KV[]): TreeNode {
  const root: TreeNode = { name: "", fullKey: "", isLeaf: false, children: new Map() };
  for (const kv of kvs) {
    const parts = kv.key.split("/").filter(Boolean);
    let node = root;
    parts.forEach((p, i) => {
      const isLast = i === parts.length - 1;
      let child: TreeNode | undefined = node.children.get(p);
      if (!child) {
        const fresh: TreeNode = {
          name: p,
          fullKey: parts.slice(0, i + 1).join("/"),
          isLeaf: false,
          children: new Map(),
        };
        node.children.set(p, fresh);
        child = fresh;
      }
      if (isLast) {
        child.isLeaf = true;
        child.kv = kv;
      }
      node = child;
    });
  }
  return root;
}

type Row = {
  node: TreeNode;
  depth: number;
  hasChildren: boolean;
};

function flatten(node: TreeNode, depth: number, open: Set<string>, out: Row[]) {
  const sorted = Array.from(node.children.values()).sort((a, b) =>
    a.name.localeCompare(b.name),
  );
  for (const c of sorted) {
    const hasChildren = c.children.size > 0;
    out.push({ node: c, depth, hasChildren });
    if (hasChildren && open.has(c.fullKey)) {
      flatten(c, depth + 1, open, out);
    }
  }
}

export function VirtualTree({
  tree,
  open,
  setOpen,
  selectedKey,
  checked,
  setChecked,
  onSelect,
  totalByPrefix,
  loadedByPrefix,
}: {
  tree: TreeNode;
  open: Set<string>;
  setOpen: (s: Set<string>) => void;
  selectedKey: string | null;
  checked: Set<string>;
  setChecked: (s: Set<string>) => void;
  onSelect: (n: TreeNode) => void;
  totalByPrefix?: Map<string, number>;
  loadedByPrefix?: Map<string, number>;
}) {
  const rows = useMemo(() => {
    const out: Row[] = [];
    flatten(tree, 0, open, out);
    return out;
  }, [tree, open]);

  const parentRef = useRef<HTMLDivElement>(null);
  const v = useVirtualizer({
    count: rows.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => 28,
    overscan: 16,
  });

  // Track which row is currently "focused" for arrow-key navigation. Starts on
  // the row matching selectedKey (so opening Browser with a selection lands
  // focus there), otherwise the first row.
  const [focusedIdx, setFocusedIdx] = useState(0);
  useEffect(() => {
    if (!selectedKey) return;
    const i = rows.findIndex((r) => r.node.kv?.key === selectedKey);
    if (i >= 0) setFocusedIdx(i);
  }, [selectedKey, rows]);

  // Find the index of `node`'s parent (the most recent row at depth-1 going up
  // from our position). Used by ArrowLeft when the current row isn't an
  // expanded folder.
  const parentIdxOf = (idx: number): number => {
    const here = rows[idx];
    if (!here) return idx;
    for (let i = idx - 1; i >= 0; i--) {
      if (rows[i].depth < here.depth) return i;
    }
    return idx;
  };

  const moveFocus = (next: number) => {
    if (next < 0 || next >= rows.length) return;
    setFocusedIdx(next);
    v.scrollToIndex(next, { align: "auto" });
  };

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (rows.length === 0) return;
    const idx = Math.min(focusedIdx, rows.length - 1);
    const row = rows[idx];
    const expanded = open.has(row.node.fullKey);
    switch (e.key) {
      case "ArrowDown":
        e.preventDefault();
        moveFocus(idx + 1);
        return;
      case "ArrowUp":
        e.preventDefault();
        moveFocus(idx - 1);
        return;
      case "Home":
        e.preventDefault();
        moveFocus(0);
        return;
      case "End":
        e.preventDefault();
        moveFocus(rows.length - 1);
        return;
      case "ArrowRight":
        e.preventDefault();
        if (row.hasChildren && !expanded) {
          const next = new Set(open);
          next.add(row.node.fullKey);
          setOpen(next);
        } else if (row.hasChildren && expanded) {
          moveFocus(idx + 1);
        }
        return;
      case "ArrowLeft":
        e.preventDefault();
        if (row.hasChildren && expanded) {
          const next = new Set(open);
          next.delete(row.node.fullKey);
          setOpen(next);
        } else {
          moveFocus(parentIdxOf(idx));
        }
        return;
      case "Enter":
        e.preventDefault();
        if (row.node.isLeaf) onSelect(row.node);
        else if (row.hasChildren) {
          const next = new Set(open);
          expanded ? next.delete(row.node.fullKey) : next.add(row.node.fullKey);
          setOpen(next);
        }
        return;
      case " ":
        if (row.node.isLeaf && row.node.kv) {
          e.preventDefault();
          const next = new Set(checked);
          next.has(row.node.kv.key) ? next.delete(row.node.kv.key) : next.add(row.node.kv.key);
          setChecked(next);
        }
        return;
    }
  };

  return (
    <div
      ref={parentRef}
      className="h-full overflow-auto outline-none"
      role="tree"
      aria-label="keys"
      tabIndex={0}
      onKeyDown={onKeyDown}
    >
      <div style={{ height: v.getTotalSize(), position: "relative" }}>
        {v.getVirtualItems().map((vi) => {
          const { node, depth, hasChildren } = rows[vi.index];
          const expanded = open.has(node.fullKey);
          const active = node.kv?.key === selectedKey;
          const focused = vi.index === focusedIdx;
          const isChecked = node.kv ? checked.has(node.kv.key) : false;
          return (
            <div
              key={vi.key}
              role="treeitem"
              aria-level={depth + 1}
              aria-expanded={hasChildren ? expanded : undefined}
              aria-selected={active}
              data-focused={focused || undefined}
              style={{
                position: "absolute",
                top: 0,
                left: 0,
                width: "100%",
                transform: `translateY(${vi.start}px)`,
                height: vi.size,
              }}
              className={cn(
                // The keyboard-focus indicator used to be a 4-sided
                // `ring-1`, which read as an unwanted border around a
                // row whenever you moved the cursor with arrow keys.
                // Switched to a thin left-edge accent that doesn't
                // shift content (no horizontal padding change) and
                // doesn't compete with the soft-active background of
                // the actually-selected row.
                "group flex items-center gap-1.5 px-1.5 rounded-md text-sm relative",
                active ? "soft-active" : "soft-hover",
              )}
            >
              {node.isLeaf ? (
                <button
                  onClick={(e) => {
                    e.stopPropagation();
                    const next = new Set(checked);
                    isChecked ? next.delete(node.kv!.key) : next.add(node.kv!.key);
                    setChecked(next);
                  }}
                  // Bigger hit area + visible hover halo so the click
                  // target doesn't blend into the row's soft-hover bg.
                  // Negative margin keeps the visual size unchanged so
                  // the icon column still aligns with folder rows.
                  className="shrink-0 cursor-pointer p-1.5 -m-1 rounded hover:bg-white/[0.08] active:bg-white/[0.12]"
                  style={{ marginLeft: depth * 14 - 4 }}
                  aria-label={isChecked ? "Unselect" : "Select"}
                  aria-pressed={isChecked}
                >
                  <Checkbox state={isChecked ? "on" : "off"} />
                </button>
              ) : (
                <span style={{ width: depth * 14 + 14 }} />
              )}
              <button
                onClick={() => {
                  if (hasChildren) {
                    const next = new Set(open);
                    expanded ? next.delete(node.fullKey) : next.add(node.fullKey);
                    setOpen(next);
                  }
                  if (node.isLeaf) onSelect(node);
                }}
                className="flex-1 grid grid-cols-[14px_14px_minmax(0,1fr)_minmax(0,1fr)] items-center gap-1.5 text-left text-[13px] font-mono leading-none"
              >
                {hasChildren ? (
                  expanded ? (
                    <ChevronDown className="w-3.5 h-3.5 muted" />
                  ) : (
                    <ChevronRight className="w-3.5 h-3.5 muted" />
                  )
                ) : (
                  <span />
                )}
                {node.isLeaf ? (
                  <KeyIcon className="w-3.5 h-3.5 text-accent-500" />
                ) : (
                  <FolderClosed className="w-3.5 h-3.5 muted" />
                )}
                <span className="truncate">{node.name}</span>
                {node.isLeaf && node.kv ? (
                  <PreviewSpan kv={node.kv} />
                ) : (
                  <FolderTruncationSpan
                    fullKey={"/" + node.fullKey}
                    totalByPrefix={totalByPrefix}
                    loadedByPrefix={loadedByPrefix}
                  />
                )}
              </button>
            </div>
          );
        })}
      </div>
    </div>
  );
}

// Keys etcd writes itself (not user data) — surface a small marker so
// operators don't waste time wondering what they are. List drawn from
// etcd v3 source (server/etcdserver/util.go and embed/etcd.go).
const INTERNAL_KEYS = new Set([
  "compact_rev_key", // last compaction revision, written by Compact()
]);

export function isInternalEtcdKey(key: string): boolean {
  const last = key.startsWith("/") ? key.slice(1) : key;
  return INTERNAL_KEYS.has(last);
}

// FolderTruncationSpan renders `12.3k` on the right of a folder row when
// the cluster has more keys under that prefix than the current range
// loaded. Only shown for top-level (depth-2) folders — that's the only
// depth the /range/counts endpoint computes. For deeper folders we
// render nothing rather than a misleading partial total.
function FolderTruncationSpan({
  fullKey,
  totalByPrefix,
  loadedByPrefix,
}: {
  fullKey: string;
  totalByPrefix?: Map<string, number>;
  loadedByPrefix?: Map<string, number>;
}) {
  if (!totalByPrefix) return <span />;
  const total = totalByPrefix.get(fullKey);
  if (total == null) return <span />;
  const loaded = loadedByPrefix?.get(fullKey) ?? 0;
  if (loaded >= total) {
    return (
      <span className="text-[10px] muted text-right pl-2 tabular-nums">
        {fmtCount(total)}
      </span>
    );
  }
  return (
    <span
      className="text-[10px] text-warn text-right pl-2 tabular-nums"
      title={`${loaded.toLocaleString()} of ${total.toLocaleString()} keys loaded — raise the limit to see the rest`}
    >
      {fmtCount(loaded)}
      <span className="opacity-70">/{fmtCount(total)}</span>
    </span>
  );
}

function fmtCount(n: number): string {
  if (n < 1000) return String(n);
  if (n < 1_000_000) return (n / 1000).toFixed(1).replace(/\.0$/, "") + "k";
  return (n / 1_000_000).toFixed(1).replace(/\.0$/, "") + "M";
}

function PreviewSpan({ kv }: { kv: KV }) {
  if (isInternalEtcdKey(kv.key)) {
    return (
      <span className="text-[11px] muted truncate text-right pl-2 italic">
        etcd internal · last compacted revision
      </span>
    );
  }
  // Use the server-decoded preview when available — gives us a clean
  // `Pod · ns/name` for K8s objects instead of leaking raw protobuf
  // bytes (which sometimes round-trip as U+FFFD garbage). Plain values
  // fall through to flattened-text preview.
  if (kv.preview) {
    const p = kv.preview;
    return (
      <span className="text-[11px] muted truncate text-right pl-2">
        {p.kind && <span style={{ color: "rgb(var(--fg))" }}>{p.kind}</span>}
        {p.namespace && <> <span className="opacity-50">·</span> {p.namespace}</>}
        {p.name && p.kind && p.name !== kv.key.split("/").pop() && (
          <> <span className="opacity-50">/</span> {p.name}</>
        )}
      </span>
    );
  }
  if (!kv.value) return <span />;
  if (looksBinary(kv.value)) {
    return (
      <span className="text-[11px] muted truncate italic text-right pl-2">
        binary · {kv.value.length} B
      </span>
    );
  }
  const flat = kv.value.replace(/[\n\r\t]+/g, " ").slice(0, 80);
  return (
    <span className="text-[11px] muted truncate text-right pl-2">{flat}</span>
  );
}
