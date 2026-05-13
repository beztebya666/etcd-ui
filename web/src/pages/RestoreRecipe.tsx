import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { api } from "../lib/api";
import { useStore } from "../lib/store";
import { Terminal, Boxes, Copy, FileDown } from "lucide-react";
import { toast } from "../components/Toast";
import { copyToClipboard } from "../lib/clipboard";

export function RestoreRecipePage() {
  const cluster = useStore((s) => s.selectedCluster);
  const [mode, setMode] = useState<"etcdctl" | "k8s">("etcdctl");
  const [snapshotPath, setSnapshotPath] = useState("/var/lib/etcd/snapshot.db");
  const [dataDir, setDataDir] = useState("/var/lib/etcd/restored");
  const [token, setToken] = useState("etcd-restore-1");

  const m = useMutation({
    mutationFn: () => api.restoreRecipe(cluster!, { mode, path: snapshotPath, dataDir, token }),
  });

  if (!cluster) return <div className="panel p-10 text-center muted">Pick a cluster first.</div>;

  return (
    <div className="space-y-5 max-w-4xl">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight flex items-center gap-2">
          <Terminal className="w-5 h-5 text-accent-500" /> Snapshot (.db) restore
        </h1>
        <p className="muted mt-1">
          A native <span className="kbd">.db</span> restore can't run from this UI — it has to happen
          while etcd is stopped, with new <span className="kbd">--data-dir</span>. Generate a runbook below and apply
          it on your hosts or in your cluster.
        </p>
      </div>

      <section className="panel p-5 space-y-4">
        <div className="flex gap-2">
          <button onClick={() => setMode("etcdctl")} className={"btn " + (mode === "etcdctl" ? "btn-primary" : "")}>
            <Terminal className="w-4 h-4" /> etcdctl (host)
          </button>
          <button onClick={() => setMode("k8s")} className={"btn " + (mode === "k8s" ? "btn-primary" : "")}>
            <Boxes className="w-4 h-4" /> Kubernetes Job
          </button>
        </div>
        <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
          <Field label="Snapshot path" value={snapshotPath} onChange={setSnapshotPath} />
          <Field label="Data dir (new)" value={dataDir} onChange={setDataDir} />
          <Field label="Cluster token" value={token} onChange={setToken} />
        </div>
        <button onClick={() => m.mutate()} className="btn btn-primary" disabled={m.isPending}>
          {m.isPending ? "Generating…" : "Generate recipe"}
        </button>
      </section>

      {m.data && (
        <section className="panel p-0 overflow-hidden">
          <header
            className="flex items-center gap-2 px-4 h-12 border-b"
            style={{ borderColor: "rgb(var(--line))" }}
          >
            <div className="text-sm font-medium">{mode === "k8s" ? "k8s Job YAML" : "Shell runbook"}</div>
            <div className="ml-auto flex items-center gap-2">
              <button
                onClick={async () => {
                  if (await copyToClipboard(m.data!.body)) toast.success("Copied");
                  else toast.error("Couldn't copy — select and ⌘C manually");
                }}
                className="btn btn-ghost text-xs"
              >
                <Copy className="w-3.5 h-3.5" /> Copy
              </button>
              <button
                onClick={() => download(m.data!.body, mode === "k8s" ? "restore.yaml" : "restore.sh")}
                className="btn btn-ghost text-xs"
              >
                <FileDown className="w-3.5 h-3.5" /> Save
              </button>
            </div>
          </header>
          <pre className="font-mono text-[12.5px] leading-relaxed p-4 overflow-auto max-h-[60vh] whitespace-pre-wrap">
            {m.data.body}
          </pre>
        </section>
      )}
    </div>
  );
}

function Field({ label, value, onChange }: { label: string; value: string; onChange: (v: string) => void }) {
  return (
    <label>
      <div className="text-xs muted mb-1">{label}</div>
      <input
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="w-full h-10 px-3 rounded-lg text-sm font-mono"
        style={{ background: "rgb(var(--panel-2))", border: "1px solid rgb(var(--line))" }}
      />
    </label>
  );
}

function download(content: string, name: string) {
  const blob = new Blob([content], { type: "text/plain;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  a.click();
  URL.revokeObjectURL(url);
}
