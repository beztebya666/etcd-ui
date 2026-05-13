// 3-way conflict resolver. The server returns {base, theirs, merged} when
// `put-cas` detects an overlapping concurrent edit it couldn't auto-merge.
// We render three columns + a final editable buffer where the user picks
// how to resolve each <<<<<<< block, then commits with
// `acceptConflicts:true` (so any leftover markers go through verbatim if
// they really want them).

import { useMemo, useState } from "react";
import { motion, AnimatePresence } from "framer-motion";
import { AlertTriangle, GitMerge, X, ChevronLeft, ChevronRight } from "lucide-react";
import { useFocusTrap } from "../lib/focusTrap";
import { cn } from "../lib/cn";

export type ConflictPayload = {
  base: string;
  theirs: string;
  merged: string;
  conflicts: number;
};

export function ConflictResolver({
  open,
  payload,
  ours,
  onResolve,
  onCancel,
}: {
  open: boolean;
  payload: ConflictPayload | null;
  ours: string;
  onResolve: (value: string) => void;
  onCancel: () => void;
}) {
  const [draft, setDraft] = useState<string>("");
  const trapRef = useFocusTrap<HTMLDivElement>(open);

  // When the payload first arrives, seed the draft with the marked-up
  // merged blob so the user can resolve inline.
  useMemo(() => {
    if (open && payload) setDraft(payload.merged);
  }, [open, payload]);

  if (!open || !payload) return null;

  const blocks = useMemo(() => splitOnMarkers(draft), [draft]);
  const unresolved = blocks.filter((b) => b.kind === "conflict").length;

  const replace = (idx: number, choice: "ours" | "theirs" | "both") => {
    const next = blocks.map((b, i) => {
      if (i !== idx || b.kind !== "conflict") return b;
      switch (choice) {
        case "ours":
          return { kind: "plain" as const, text: b.ours };
        case "theirs":
          return { kind: "plain" as const, text: b.theirs };
        case "both":
          return { kind: "plain" as const, text: b.ours + "\n" + b.theirs };
      }
    });
    setDraft(joinBlocks(next));
  };

  return (
    <AnimatePresence>
      <motion.div
        key="bg"
        initial={{ opacity: 0 }}
        animate={{ opacity: 1 }}
        exit={{ opacity: 0 }}
        className="fixed inset-0 z-[55] bg-black/75 flex items-center justify-center p-4"
        onMouseDown={(e) => e.target === e.currentTarget && onCancel()}
      >
        <motion.div
          ref={trapRef}
          role="dialog"
          aria-modal="true"
          aria-labelledby="conflict-title"
          tabIndex={-1}
          initial={{ opacity: 0, y: 8, scale: 0.98 }}
          animate={{ opacity: 1, y: 0, scale: 1 }}
          exit={{ opacity: 0, y: -4, scale: 0.98 }}
          className="w-[1100px] max-w-full max-h-[92vh] panel p-5 shadow-elev flex flex-col"
        >
          <div className="flex items-start gap-3 mb-4">
            <div className="w-10 h-10 rounded-xl bg-warn/15 text-warn flex items-center justify-center shrink-0">
              <GitMerge className="w-5 h-5" />
            </div>
            <div className="flex-1 min-w-0">
              <div id="conflict-title" className="text-base font-semibold flex items-center gap-2">
                Merge conflict
                <span className="pill !text-[10px]">
                  {payload.conflicts} block{payload.conflicts === 1 ? "" : "s"}
                </span>
                {unresolved > 0 && (
                  <span className="pill !text-[10px] text-warn border-warn/40">
                    {unresolved} unresolved
                  </span>
                )}
              </div>
              <p className="muted text-sm mt-0.5">
                Somebody wrote to this key while you were editing. Auto-merge
                couldn't reconcile overlapping changes. Pick a side per block
                or hand-edit the final value below.
              </p>
            </div>
            <button onClick={onCancel} className="muted hover:text-current">
              <X className="w-4 h-4" />
            </button>
          </div>

          <div className="grid grid-cols-3 gap-3 mb-3 min-h-0 flex-1 overflow-hidden">
            <Pane label="Base (when you opened it)" body={payload.base} />
            <Pane label="Yours (your draft)" body={ours} tone="accent" />
            <Pane label="Theirs (live on server)" body={payload.theirs} tone="warn" />
          </div>

          <div className="text-xs muted mb-1 flex items-center gap-2">
            <AlertTriangle className="w-3.5 h-3.5 text-warn" />
            Merged buffer — edit inline or use the per-block buttons:
          </div>
          <div
            className="rounded-lg overflow-auto font-mono text-xs"
            style={{ background: "rgb(var(--panel-2))", border: "1px solid rgb(var(--line))" }}
          >
            <BlockEditor blocks={blocks} onChoose={replace} onText={setDraft} text={draft} />
          </div>

          <div className="mt-4 flex items-center gap-2">
            <span className="text-xs muted">
              {unresolved > 0
                ? `${unresolved} block${unresolved === 1 ? "" : "s"} still has markers.`
                : "All blocks resolved."}
            </span>
            <div className="ml-auto flex items-center gap-2">
              <button onClick={onCancel} className="btn btn-ghost">
                Cancel
              </button>
              <button
                onClick={() => onResolve(draft)}
                className="btn btn-primary"
              >
                <GitMerge className="w-4 h-4" /> Save resolved value
              </button>
            </div>
          </div>
        </motion.div>
      </motion.div>
    </AnimatePresence>
  );
}

function Pane({
  label,
  body,
  tone,
}: {
  label: string;
  body: string;
  tone?: "accent" | "warn";
}) {
  return (
    <div className="flex flex-col min-h-0">
      <div
        className={cn(
          "text-xs muted mb-1 px-1",
          tone === "accent" && "text-accent-500",
          tone === "warn" && "text-warn",
        )}
      >
        {label}
      </div>
      <pre
        className="rounded-md p-2 font-mono text-xs whitespace-pre-wrap break-all overflow-auto flex-1"
        style={{ background: "rgb(var(--panel-2))", border: "1px solid rgb(var(--line))" }}
      >
        {body || <span className="muted">(empty)</span>}
      </pre>
    </div>
  );
}

// --- block splitting ------------------------------------------------------

type Block =
  | { kind: "plain"; text: string }
  | { kind: "conflict"; ours: string; theirs: string };

function splitOnMarkers(s: string): Block[] {
  const lines = s.split("\n");
  const out: Block[] = [];
  let buf: string[] = [];
  let i = 0;
  const flushPlain = () => {
    if (buf.length > 0) {
      out.push({ kind: "plain", text: buf.join("\n") });
      buf = [];
    }
  };
  while (i < lines.length) {
    if (lines[i].startsWith("<<<<<<<")) {
      flushPlain();
      const oursLines: string[] = [];
      const theirsLines: string[] = [];
      let phase: "ours" | "theirs" = "ours";
      i++;
      while (i < lines.length && !lines[i].startsWith(">>>>>>>")) {
        if (lines[i].startsWith("=======")) {
          phase = "theirs";
        } else if (phase === "ours") {
          oursLines.push(lines[i]);
        } else {
          theirsLines.push(lines[i]);
        }
        i++;
      }
      i++; // skip >>>>>>>
      out.push({ kind: "conflict", ours: oursLines.join("\n"), theirs: theirsLines.join("\n") });
      continue;
    }
    buf.push(lines[i]);
    i++;
  }
  flushPlain();
  return out;
}

function joinBlocks(blocks: Block[]): string {
  return blocks
    .map((b) =>
      b.kind === "plain"
        ? b.text
        : "<<<<<<< ours\n" + b.ours + "\n=======\n" + b.theirs + "\n>>>>>>> theirs",
    )
    .join("\n");
}

function BlockEditor({
  blocks,
  onChoose,
  onText,
  text,
}: {
  blocks: Block[];
  onChoose: (idx: number, side: "ours" | "theirs" | "both") => void;
  onText: (s: string) => void;
  text: string;
}) {
  // We render the blocks side-by-side with picker buttons. The user can
  // also bypass the buttons and hand-edit the raw buffer in the textarea
  // at the bottom (kept in sync with `text`).
  return (
    <div className="p-2 space-y-2 max-h-[40vh] overflow-auto">
      {blocks.map((b, i) =>
        b.kind === "plain" ? (
          b.text === "" ? null : (
            <pre
              key={i}
              className="px-2 py-1.5 whitespace-pre-wrap break-all"
              style={{ color: "rgb(var(--fg))" }}
            >
              {b.text}
            </pre>
          )
        ) : (
          <div
            key={i}
            className="rounded-md p-2 border"
            style={{
              borderColor: "color-mix(in srgb, rgb(var(--warn, 234 179 8)) 50%, transparent)",
              background: "color-mix(in srgb, rgb(var(--warn, 234 179 8)) 8%, transparent)",
            }}
          >
            <div className="flex items-center gap-2 text-[11px] uppercase tracking-wider muted mb-2">
              <AlertTriangle className="w-3 h-3 text-warn" />
              conflict block #{blocks.slice(0, i + 1).filter((x) => x.kind === "conflict").length}
            </div>
            <div className="grid grid-cols-2 gap-2 mb-2">
              <pre
                className="rounded p-2 text-xs whitespace-pre-wrap break-all"
                style={{ background: "rgb(var(--panel))", border: "1px solid rgb(var(--line))" }}
              >
                <div className="text-[10px] text-accent-500 mb-1 uppercase tracking-wider">ours</div>
                {b.ours || <span className="muted">(empty)</span>}
              </pre>
              <pre
                className="rounded p-2 text-xs whitespace-pre-wrap break-all"
                style={{ background: "rgb(var(--panel))", border: "1px solid rgb(var(--line))" }}
              >
                <div className="text-[10px] text-warn mb-1 uppercase tracking-wider">theirs</div>
                {b.theirs || <span className="muted">(empty)</span>}
              </pre>
            </div>
            <div className="flex items-center gap-1.5">
              <button
                onClick={() => onChoose(i, "ours")}
                className="btn btn-ghost text-xs !h-7"
                title="Keep your version, drop theirs"
              >
                <ChevronLeft className="w-3 h-3" /> use ours
              </button>
              <button
                onClick={() => onChoose(i, "theirs")}
                className="btn btn-ghost text-xs !h-7"
                title="Take their version, drop yours"
              >
                use theirs <ChevronRight className="w-3 h-3" />
              </button>
              <button
                onClick={() => onChoose(i, "both")}
                className="btn btn-ghost text-xs !h-7"
                title="Concatenate yours + theirs"
              >
                both
              </button>
            </div>
          </div>
        ),
      )}
      <details className="mt-3 text-xs">
        <summary className="cursor-pointer muted">Raw buffer (hand-edit)</summary>
        <textarea
          value={text}
          onChange={(e) => onText(e.target.value)}
          className="w-full h-40 mt-2 p-2 rounded text-xs font-mono"
          style={{
            background: "rgb(var(--panel))",
            border: "1px solid rgb(var(--line))",
            color: "rgb(var(--fg))",
          }}
        />
      </details>
    </div>
  );
}
