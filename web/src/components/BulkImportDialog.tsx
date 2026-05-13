import { useState, useRef } from "react";
import { UploadCloud, FileJson, AlertTriangle, CheckCircle2 } from "lucide-react";
import { useMutation } from "@tanstack/react-query";
import { api } from "../lib/api";
import { cn } from "../lib/cn";
import { useFocusTrap } from "../lib/focusTrap";

type ParsedKV = { key: string; value: string };

export function BulkImportDialog({
  open,
  onClose,
  cluster,
  onApplied,
}: {
  open: boolean;
  onClose: () => void;
  cluster: string;
  onApplied: () => void;
}) {
  const [file, setFile] = useState<File | null>(null);
  const [parsed, setParsed] = useState<ParsedKV[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [dragOver, setDragOver] = useState(false);
  const [clearPrefix, setClearPrefix] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);
  const trapRef = useFocusTrap<HTMLDivElement>(open);

  const apply = useMutation({
    mutationFn: async () => {
      if (!file) throw new Error("no file");
      // Use the restore endpoint, which accepts our JSON shape.
      return api.restore(cluster, file, { clearPrefix: clearPrefix || undefined });
    },
    onSuccess: () => {
      onApplied();
      onClose();
    },
  });

  if (!open) return null;

  const handleFile = async (f: File) => {
    setFile(f);
    setError(null);
    try {
      const text = await f.text();
      const data = JSON.parse(text);
      if (Array.isArray(data?.kvs)) {
        setParsed(data.kvs.map((k: { key: string; value: string }) => ({ key: k.key, value: k.value })));
        return;
      }
      if (Array.isArray(data)) {
        setParsed(data);
        return;
      }
      if (data && typeof data === "object") {
        // accept flat { "/a/b": "v", ... }
        setParsed(
          Object.entries(data).map(([key, value]) => ({ key, value: String(value) })),
        );
        return;
      }
      throw new Error("unrecognised file shape");
    } catch (e) {
      setError((e as Error).message);
      setParsed(null);
    }
  };

  return (
    <div
      className="fixed inset-0 z-40 bg-black/70 flex items-center justify-center p-6"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div
        ref={trapRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby="bulk-import-title"
        tabIndex={-1}
        className="w-[640px] max-w-full panel p-6 shadow-elev animate-in"
      >
        <div id="bulk-import-title" className="flex items-center gap-2 text-lg font-semibold">
          <UploadCloud className="w-5 h-5 text-accent-500" />
          Bulk import
        </div>
        <p className="muted text-sm mt-1">
          Drop a <span className="kbd">.json</span> file exported from etcd-ui, or any{" "}
          <span className="kbd">{"{ \"key\": \"value\" }"}</span> map. Keys are PUT atomically in batches.
        </p>

        <div
          onDragOver={(e) => {
            e.preventDefault();
            setDragOver(true);
          }}
          onDragLeave={() => setDragOver(false)}
          onDrop={async (e) => {
            e.preventDefault();
            setDragOver(false);
            const f = e.dataTransfer.files[0];
            if (f) await handleFile(f);
          }}
          onClick={() => inputRef.current?.click()}
          className={cn(
            "mt-4 rounded-xl p-8 text-center cursor-pointer transition border-2 border-dashed",
            dragOver ? "border-accent-500 bg-accent/5" : "border-white/10 hover:border-white/20",
          )}
        >
          <FileJson className="w-8 h-8 mx-auto mb-2 muted" />
          <div className="text-sm">{file ? file.name : "Drop a file here or click to choose"}</div>
          <input
            ref={inputRef}
            type="file"
            accept="application/json,.json,.yaml,.yml,.txt"
            hidden
            onChange={(e) => {
              const f = e.target.files?.[0];
              if (f) handleFile(f);
            }}
          />
        </div>

        {error && (
          <div className="mt-3 text-sm text-danger flex items-center gap-2">
            <AlertTriangle className="w-4 h-4" /> {error}
          </div>
        )}
        {parsed && !error && (
          <div className="mt-3 text-sm text-accent-500 flex items-center gap-2">
            <CheckCircle2 className="w-4 h-4" /> {parsed.length} keys ready to import
          </div>
        )}

        <details className="mt-4">
          <summary className="cursor-pointer text-sm muted">Advanced options</summary>
          <div className="mt-2">
            <label className="text-xs muted">Clear target prefix before import (optional)</label>
            <input
              value={clearPrefix}
              onChange={(e) => setClearPrefix(e.target.value)}
              placeholder="e.g. /app/"
              className="w-full mt-1 h-9 px-3 rounded-lg text-sm font-mono"
              style={{ background: "rgb(var(--panel-2))", border: "1px solid rgb(var(--line))" }}
            />
          </div>
        </details>

        <div className="mt-6 flex items-center gap-3">
          <button onClick={onClose} className="btn btn-ghost">Cancel</button>
          <div className="ml-auto" />
          <button
            disabled={!parsed || apply.isPending}
            onClick={() => apply.mutate()}
            className="btn btn-primary"
          >
            {apply.isPending ? "Importing…" : "Import"}
          </button>
        </div>
        {apply.error && <div className="mt-3 text-danger text-sm">{(apply.error as Error).message}</div>}
      </div>
    </div>
  );
}
