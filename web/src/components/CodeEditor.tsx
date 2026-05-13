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

export type CodeEditorProps = {
  value: string;
  onChange: (v: string) => void;
  readOnly?: boolean;
  preview?: KVPreview;
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

export function CodeEditor({ value, onChange, readOnly, preview }: CodeEditorProps) {
  const binary = useMemo(() => looksBinary(value), [value]);
  const isJSON = useMemo(() => looksJSON(value), [value]);

  // Server-decoded structured JSON wins over raw binary — kube-apiserver
  // protobuf round-trips losslessly through k8s.io/api into a real Go
  // struct, then we JSON-marshal that. Operator sees the same shape as
  // `kubectl get pod -o yaml | yq -j`.
  if (binary && preview?.json) {
    return <DecodedView decoded={preview.json} preview={preview} rawValue={value} />;
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
// JSON, with a toggle dropping back to the raw protobuf hex for the rare
// cases where you genuinely want to inspect the wire format.
function DecodedView({
  decoded,
  preview,
  rawValue,
}: {
  decoded: string;
  preview: KVPreview;
  rawValue: string;
}) {
  const [showRaw, setShowRaw] = useState(false);
  const html = useMemo(() => highlightJSON(decoded), [decoded]);
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
        <button onClick={() => setShowRaw(true)} className="btn btn-ghost text-xs !h-6 ml-auto" title="View raw protobuf bytes">
          Raw protobuf
        </button>
      }/>
      <pre
        className="flex-1 overflow-auto p-4 font-mono text-[13px] leading-relaxed whitespace-pre"
        dangerouslySetInnerHTML={{ __html: html }}
      />
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
