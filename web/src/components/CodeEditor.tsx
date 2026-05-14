// Lightweight editor: plain textarea for text values, hex viewer for binary.
// Monaco was removed in favour of zero-dependency rendering — it relied on a
// CDN load (breaks in air-gapped networks) and forced GPU compositing on top
// of dialogs. K8s objects in etcd are protobuf anyway, so hex view is the
// right default for them.

import { useMemo, useState } from "react";
import type { KVPreview } from "../lib/api";
import Prism from "prismjs";
import "prismjs/components/prism-json";
import "../lib/prismTheme.css";
import { cn } from "../lib/cn";

export type CodeEditorProps = {
  value: string;
  onChange: (v: string) => void;
  readOnly?: boolean;
  preview?: KVPreview;
  // When set + a decoded preview is available, the editor enters K8s
  // edit mode: user edits the structured JSON, save round-trips via
  // /put-k8s back into the wire format etcd expects.
  onEditDecoded?: (decodedJson: string) => void;
  decodedDraft?: string;
};

// Heuristic: a value is "binary" if it contains the Unicode replacement char
// (U+FFFD, what Go's JSON encoder emits for invalid UTF-8) or raw control
// bytes other than \t \n \r. Scans only the first 256 chars for speed.
export function looksBinary(s: string): boolean {
  if (!s) return false;
  if (s.includes("�")) return true;
  const n = Math.min(s.length, 256);
  for (let i = 0; i < n; i++) {
    const c = s.charCodeAt(i);
    if (c < 32 && c !== 9 && c !== 10 && c !== 13) return true;
  }
  return false;
}

/** First non-control substring — useful as a short label for binary values
 *  whose payload still has a recognisable prefix (K8s protobuf starts "k8s\0"
 *  followed by an API group/kind like "apps/v1\0\0Deployment\0…"). */
export function readableHint(s: string, max = 60): string {
  let out = "";
  for (let i = 0; i < s.length && out.length < max; i++) {
    const c = s.charCodeAt(i);
    if (c >= 32 && c < 127) out += s[i];
    else if (out && !out.endsWith(" ")) out += " ";
  }
  return out.trim();
}

export function CodeEditor({ value, onChange, readOnly, preview, onEditDecoded, decodedDraft }: CodeEditorProps) {
  const binary = useMemo(() => looksBinary(value), [value]);
  const isJSON = useMemo(() => looksJSON(value), [value]);

  // Server-decoded structured JSON wins over raw binary — kube-apiserver
  // protobuf round-trips losslessly through k8s.io/api into a real Go
  // struct, then we JSON-marshal that. Operator sees the same shape as
  // `kubectl get pod -o yaml | yq -j`. When `onEditDecoded` is wired,
  // the user can edit and save round-trips via /put-k8s.
  if (binary && preview?.json) {
    return (
      <DecodedView
        decoded={preview.json}
        preview={preview}
        rawValue={value}
        onEditDecoded={onEditDecoded}
        decodedDraft={decodedDraft}
      />
    );
  }

  if (binary) {
    return <BinaryViewer value={value} preview={preview} />;
  }

  if (isJSON && !readOnly) {
    return <JSONViewer value={value} onChange={onChange} />;
  }

  return (
    <textarea
      value={value}
      onChange={(e) => onChange(e.target.value)}
      spellCheck={false}
      readOnly={readOnly}
      className="h-full w-full p-4 font-mono text-[13px] leading-relaxed resize-none outline-none bg-transparent"
    />
  );
}

// DecodedView renders a server-decoded K8s object as Prism-highlighted
// JSON, with toggles for (a) raw protobuf bytes and (b) edit mode where
// the user types directly into the JSON. Saved edits round-trip via
// /put-k8s — server re-encodes back to the original wire format.
function DecodedView({
  decoded,
  preview,
  rawValue,
  onEditDecoded,
  decodedDraft,
}: {
  decoded: string;
  preview: KVPreview;
  rawValue: string;
  onEditDecoded?: (decodedJson: string) => void;
  decodedDraft?: string;
}) {
  const [showRaw, setShowRaw] = useState(false);
  const [editing, setEditing] = useState(false);
  const current = decodedDraft ?? decoded;
  const html = useMemo(() => highlightJSON(current), [current]);
  const isDirty = decodedDraft != null && decodedDraft !== decoded;
  const parseError = useMemo(() => {
    if (!editing || !decodedDraft) return null;
    try {
      JSON.parse(decodedDraft);
      return null;
    } catch (e) {
      return (e as Error).message;
    }
  }, [editing, decodedDraft]);

  if (showRaw) {
    return (
      <div className="h-full flex flex-col">
        <ChipsBar preview={preview} extra={
          <button onClick={() => setShowRaw(false)} className="btn btn-ghost text-xs !h-6 ml-auto">
            ← Decoded view
          </button>
        }/>
        <div className="flex-1 overflow-auto">
          <BinaryViewer value={rawValue} preview={preview} hidePreview />
        </div>
      </div>
    );
  }
  return (
    <div className="h-full flex flex-col">
      <ChipsBar preview={preview} extra={
        <div className="ml-auto flex items-center gap-1.5">
          {parseError && (
            <span className="text-[10px] text-danger font-mono" title={parseError}>
              JSON error
            </span>
          )}
          {isDirty && !parseError && (
            <span className="text-[10px] text-accent-500">edited</span>
          )}
          {onEditDecoded && (
            <button
              onClick={() => setEditing((v) => !v)}
              className={cn("btn btn-ghost text-xs !h-6", editing && "text-accent-500")}
              title={editing ? "Stop editing (keeps changes)" : "Edit this object as JSON"}
            >
              {editing ? "Done editing" : "Edit"}
            </button>
          )}
          <button onClick={() => setShowRaw(true)} className="btn btn-ghost text-xs !h-6" title="View raw protobuf bytes">
            Raw protobuf
          </button>
        </div>
      }/>
      {editing && onEditDecoded ? (
        <textarea
          value={current}
          onChange={(e) => onEditDecoded(e.target.value)}
          spellCheck={false}
          autoFocus
          className="flex-1 w-full p-4 font-mono text-[13px] leading-relaxed resize-none outline-none bg-transparent"
          // Tab → 2 spaces inside textarea so structure stays consistent
          // with what Decode emits.
          onKeyDown={(e) => {
            if (e.key === "Tab") {
              e.preventDefault();
              const t = e.currentTarget;
              const s = t.selectionStart;
              const newVal = current.slice(0, s) + "  " + current.slice(t.selectionEnd);
              onEditDecoded(newVal);
              requestAnimationFrame(() => { t.selectionStart = t.selectionEnd = s + 2; });
            }
          }}
        />
      ) : (
        <pre
          // whitespace-pre-wrap so deeply-nested K8s JSON (especially the
          // `kubectl.kubernetes.io/last-applied-configuration` annotation
          // which embeds a multi-KB JSON string on a single line) wraps
          // instead of forcing horizontal scroll. break-all is too
          // aggressive (splits identifiers); break-words lets the
          // browser pick word boundaries.
          className="flex-1 overflow-auto p-4 font-mono text-[13px] leading-relaxed whitespace-pre-wrap break-words"
          dangerouslySetInnerHTML={{ __html: html }}
        />
      )}
    </div>
  );
}

function ChipsBar({ preview, extra }: { preview: KVPreview; extra?: React.ReactNode }) {
  return (
    <div className="flex items-center gap-2 px-3 py-2 border-b flex-wrap" style={{ borderColor: "rgb(var(--line))" }}>
      <span className="pill !text-[10px] !text-accent-500 !border-accent-500/40 uppercase tracking-wider">
        {preview.format}
      </span>
      {preview.apiVersion && preview.kind && (
        <span className="text-xs font-semibold">
          {preview.apiVersion} <span className="muted">·</span> {preview.kind}
        </span>
      )}
      {(preview.namespace || preview.name) && (
        <span className="text-xs muted font-mono">
          {preview.namespace && <>{preview.namespace} <span className="opacity-50">/</span> </>}
          <span style={{ color: "rgb(var(--fg))" }}>{preview.name}</span>
        </span>
      )}
      {extra}
    </div>
  );
}

function looksJSON(s: string): boolean {
  if (!s) return false;
  const t = s.trimStart();
  if (t.length < 2) return false;
  const first = t[0];
  if (first !== "{" && first !== "[") return false;
  // Don't pay for a full parse on every render — only when it looks
  // promising. JSON.parse on a 200KB string is sub-ms anyway.
  try {
    JSON.parse(t);
    return true;
  } catch {
    return false;
  }
}

function JSONViewer({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const [editing, setEditing] = useState(false);
  const highlighted = useMemo(() => highlightJSON(value), [value]);

  if (editing) {
    return (
      <div className="h-full flex flex-col">
        <div className="flex items-center gap-2 px-3 py-2 text-[11px] border-b" style={{ borderColor: "rgb(var(--line))" }}>
          <span className="muted">editing JSON — syntax colours hidden while typing</span>
          <button onClick={() => setEditing(false)} className="btn btn-ghost text-xs ml-auto !h-6">
            Done
          </button>
        </div>
        <textarea
          value={value}
          onChange={(e) => onChange(e.target.value)}
          spellCheck={false}
          autoFocus
          className="flex-1 w-full p-4 font-mono text-[13px] leading-relaxed resize-none outline-none bg-transparent"
        />
      </div>
    );
  }
  return (
    <div className="h-full flex flex-col">
      <div className="flex items-center gap-2 px-3 py-2 text-[11px] border-b" style={{ borderColor: "rgb(var(--line))" }}>
        <span className="muted">JSON · read-only preview</span>
        <button onClick={() => setEditing(true)} className="btn btn-ghost text-xs ml-auto !h-6">
          Edit
        </button>
      </div>
      <pre
        className="flex-1 overflow-auto p-4 font-mono text-[13px] leading-relaxed whitespace-pre"
        dangerouslySetInnerHTML={{ __html: highlighted }}
      />
    </div>
  );
}

// Prism handles tokenisation. We just hand it the source + the language
// grammar. ~6 KB gzipped, MIT, no DOM tree walking — returns ready-to-
// inject HTML with class names that match prismTheme.css.
function highlightJSON(src: string): string {
  return Prism.highlight(src, Prism.languages.json, "json");
}

function BinaryViewer({ value, preview, hidePreview }: { value: string; preview?: KVPreview; hidePreview?: boolean }) {
  // Default: show first 2 KiB as hex. Click "Show all" to expand to the
  // full value — bounded by 256 KiB to keep the DOM responsive on K8s
  // CRDs that can run into multi-MB blobs.
  const [showAll, setShowAll] = useState(false);
  const cap = showAll ? 262_144 : 2048;
  const dump = useMemo(() => hexDump(value, cap), [value, cap]);
  const hint = useMemo(() => readableHint(value, 200), [value]);
  const shown = Math.min(value.length, cap);
  return (
    <div className="h-full overflow-auto p-4 text-[12px] font-mono">
      {preview && !hidePreview && (
        <div
          className="mb-3 p-3 rounded-lg flex items-center gap-2 flex-wrap"
          style={{
            background: "color-mix(in srgb, rgb(var(--accent-500)) 8%, transparent)",
            border: "1px solid color-mix(in srgb, rgb(var(--accent-500)) 30%, transparent)",
          }}
        >
          <span className="pill !text-[10px] !text-accent-500 !border-accent-500/40 uppercase tracking-wider">
            {preview.format}
          </span>
          {preview.apiVersion && preview.kind && (
            <span className="font-semibold">
              {preview.apiVersion} <span className="muted">·</span> {preview.kind}
            </span>
          )}
          {(preview.namespace || preview.name) && (
            <span className="muted">
              {preview.namespace && <>{preview.namespace} <span className="opacity-50">/</span> </>}
              <span style={{ color: "rgb(var(--fg))" }}>{preview.name}</span>
            </span>
          )}
          {preview.decodeError && (
            <span className="text-warn text-xs ml-auto" title={preview.decodeError}>
              decode failed — showing raw
            </span>
          )}
        </div>
      )}
      <div className="muted mb-2 flex items-center gap-3 flex-wrap">
        <span>
          Binary · {value.length.toLocaleString()} bytes · showing first {shown.toLocaleString()} as hex.
        </span>
        {value.length > 2048 && (
          <button
            onClick={() => setShowAll((v) => !v)}
            className="btn btn-ghost text-xs !h-6"
          >
            {showAll ? "Collapse" : `Show all (${(value.length / 1024).toFixed(1)} KiB)`}
          </button>
        )}
      </div>
      {hint && !preview && (
        <div className="mb-3 muted">
          Readable fragments: <span className="text-current">{hint}…</span>
        </div>
      )}
      <pre className="leading-relaxed whitespace-pre">{dump}</pre>
    </div>
  );
}

function hexDump(s: string, max: number): string {
  const n = Math.min(s.length, max);
  const out: string[] = [];
  for (let i = 0; i < n; i += 16) {
    const slice = s.slice(i, i + 16);
    const hex: string[] = [];
    const ascii: string[] = [];
    for (let j = 0; j < slice.length; j++) {
      const c = slice.charCodeAt(j) & 0xff;
      hex.push(c.toString(16).padStart(2, "0"));
      ascii.push(c >= 32 && c < 127 ? slice[j] : ".");
    }
    const hexStr = hex.join(" ").padEnd(16 * 3 - 1, " ");
    out.push(`${i.toString(16).padStart(8, "0")}  ${hexStr}  |${ascii.join("")}|`);
  }
  if (s.length > max) out.push(`… (${s.length - max} more bytes)`);
  return out.join("\n");
}
