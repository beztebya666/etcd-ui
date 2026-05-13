import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../lib/api";
import {
  Boxes,
  Database,
  Network,
  Server,
  Layers,
  Cable,
  Shield,
  Workflow,
  Globe,
  Sparkles,
  Plus,
  Info,
} from "lucide-react";
import { cn } from "../lib/cn";
import { useFocusTrap } from "../lib/focusTrap";

type Preset = {
  id: string;
  label: string;
  icon: React.ComponentType<{ className?: string }>;
  defaults: Partial<{
    id: string;
    name: string;
    endpoints: string;
    username: string;
    password: string;
  }>;
  hint: string;
  howTo: string;
};

const PRESETS: Preset[] = [
  {
    id: "manual",
    label: "Custom (manual)",
    icon: Sparkles,
    defaults: {},
    hint: "I know my endpoints — let me type them.",
    howTo:
      "Any reachable etcd works. Format: scheme://host:port, comma-separated. Use https:// if your cluster requires TLS, and fill in cert paths via env on container start.",
  },
  {
    id: "k8s-cp",
    label: "Kubernetes control plane",
    icon: Boxes,
    defaults: { id: "k8s-cp", name: "kubernetes:control-plane", endpoints: "" },
    hint: "Stacked etcd inside kube-system. Usually mTLS-only.",
    howTo:
      "Run inside the cluster (preferred): the auto-discovery will find the pods. Otherwise:\n\nkubectl -n kube-system get pods -l component=etcd -o jsonpath='{.items[*].status.podIP}'\n\nClient certs live on the master at /etc/kubernetes/pki/etcd/{ca.crt, healthcheck-client.crt, healthcheck-client.key}. Mount them to /certs and set ETCD_CA_FILE / ETCD_CERT_FILE / ETCD_KEY_FILE.",
  },
  {
    id: "patroni",
    label: "Patroni (Postgres HA)",
    icon: Database,
    defaults: { id: "patroni-pg", name: "patroni:dcs", endpoints: "" },
    hint: "Patroni's distributed configuration store (when configured with etcd).",
    howTo:
      "On any Patroni host:\n\n  patronictl -c /etc/patroni.yml dsn   # shows postgres dsn\n  cat /etc/patroni.yml | grep -A3 etcd: # shows etcd hosts\n\nOr automatically: set PATRONI_URLS=http://pg-1:8008,http://pg-2:8008 and we'll pull the cluster scope and DCS endpoints from the Patroni REST API.",
  },
  {
    id: "vitess",
    label: "Vitess (TopoServer)",
    icon: Layers,
    defaults: { id: "vitess-topo", name: "vitess:topo", endpoints: "" },
    hint: "Vitess uses etcd as its topology service.",
    howTo:
      "Look at vtctld / vtgate flags:\n\n  --topo_global_server_address=etcd-global-1:2379,etcd-global-2:2379\n  --topo_global_root=/vitess/global\n\nIn k8s: kubectl get pods -l planetscale.com/component=etcd",
  },
  {
    id: "vault",
    label: "HashiCorp Vault (etcd backend)",
    icon: Shield,
    defaults: { id: "vault-storage", name: "vault:storage", endpoints: "" },
    hint: "Only when Vault is configured with `storage \"etcd\"`.",
    howTo:
      "Check Vault config (vault.hcl):\n\n  storage \"etcd\" {\n    address = \"https://etcd-1:2379,https://etcd-2:2379\"\n    path    = \"vault/\"\n  }\n\nUse the same `address` and credentials here. Vault's Raft storage does NOT use etcd — this preset is only for the legacy etcd backend.",
  },
  {
    id: "apisix",
    label: "Apache APISIX",
    icon: Network,
    defaults: { id: "apisix-conf", name: "apisix:config", endpoints: "" },
    hint: "APISIX keeps its config in an etcd cluster.",
    howTo:
      "Look at /usr/local/apisix/conf/config.yaml:\n\n  deployment:\n    etcd:\n      host:\n        - \"http://etcd:2379\"\n      prefix: \"/apisix\"\n\nOr the Admin API: GET /apisix/admin/services (your auth header) — admin host hits etcd transparently.",
  },
  {
    id: "cilium",
    label: "Cilium (KV store)",
    icon: Cable,
    defaults: { id: "cilium-kv", name: "cilium:kvstore", endpoints: "" },
    hint: "Cilium can use etcd as its KV store.",
    howTo:
      "Check the cilium-config ConfigMap:\n\n  kubectl -n kube-system get cm cilium-config -o jsonpath='{.data.kvstore-opt}'\n\nor look for kvstore-opt with `etcd.config` referring to a Secret holding endpoints.",
  },
  {
    id: "kubeedge",
    label: "KubeEdge",
    icon: Workflow,
    defaults: { id: "kubeedge", name: "kubeedge:etcd", endpoints: "" },
    hint: "Edge-K8s control plane; usually has its own etcd.",
    howTo:
      "kubectl get pods -A -l k8s-app=kubeedge-etcd\nor look in the CloudCore deployment env: KUBE_CONFIG_PATH and etcd flags.",
  },
  {
    id: "karmada",
    label: "Karmada",
    icon: Globe,
    defaults: { id: "karmada", name: "karmada:etcd", endpoints: "" },
    hint: "Multicluster control plane; runs its own etcd in karmada-system.",
    howTo:
      "kubectl -n karmada-system get pods -l app=etcd\nClient certs come from the karmada-cert secret.",
  },
  {
    id: "talos",
    label: "Talos Linux",
    icon: Server,
    defaults: { id: "talos", name: "talos:etcd", endpoints: "" },
    hint: "Talos runs etcd for kubelet/k8s control plane.",
    howTo:
      "Use the Talos CLI:\n\n  talosctl -n <node> etcd members\n  talosctl -n <node> etcd snapshot /tmp/etcd.db\n\nFor live etcd access expose --enable-discovery (not common in prod). Easier: run etcd-ui inside the cluster and let it auto-discover the control-plane etcd.",
  },
];

export function AddClusterWizard({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient();
  const [preset, setPreset] = useState<Preset>(PRESETS[0]);
  const trapRef = useFocusTrap<HTMLDivElement>(true);
  const [form, setForm] = useState({
    id: "",
    name: "",
    endpoints: "",
    username: "",
    password: "",
  });

  const choose = (p: Preset) => {
    setPreset(p);
    setForm({
      id: p.defaults.id ?? "",
      name: p.defaults.name ?? "",
      endpoints: p.defaults.endpoints ?? "",
      username: p.defaults.username ?? "",
      password: p.defaults.password ?? "",
    });
  };

  const add = useMutation({
    mutationFn: () =>
      api.addCluster({
        id: form.id,
        name: form.name || form.id,
        endpoints: form.endpoints
          .split(",")
          .map((e) => e.trim())
          .filter(Boolean),
        username: form.username || undefined,
        password: form.password || undefined,
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["clusters"] });
      onClose();
    },
  });

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
        aria-label="Add cluster"
        tabIndex={-1}
        className="w-[920px] max-w-full panel p-0 shadow-elev animate-in overflow-hidden grid grid-cols-[260px_1fr]"
      >
        <aside
          className="overflow-y-auto p-2 border-r max-h-[78vh]"
          style={{ borderColor: "rgb(var(--line))", background: "rgb(var(--panel-2))" }}
        >
          <div className="px-3 py-2 text-[11px] uppercase tracking-wider muted">Where is etcd?</div>
          {PRESETS.map((p) => (
            <button
              key={p.id}
              onClick={() => choose(p)}
              className={cn(
                "w-full text-left flex items-center gap-2.5 px-3 py-2 rounded-lg text-sm",
                preset.id === p.id ? "soft-active" : "soft-hover",
              )}
            >
              <p.icon className="w-4 h-4 text-accent-500 shrink-0" />
              <span className="truncate">{p.label}</span>
            </button>
          ))}
        </aside>

        <div className="p-6 max-h-[78vh] overflow-y-auto">
          <div className="flex items-center gap-2 text-lg font-semibold">
            <preset.icon className="w-5 h-5 text-accent-500" /> {preset.label}
          </div>
          <p className="muted text-sm mt-1">{preset.hint}</p>

          <div className="mt-4 panel-2 rounded-xl p-4 text-sm border" style={{ borderColor: "rgb(var(--line))" }}>
            <div className="flex items-center gap-1.5 text-[11px] uppercase tracking-wider muted mb-2">
              <Info className="w-3 h-3" /> How to find the endpoints
            </div>
            <pre className="whitespace-pre-wrap font-mono text-[12.5px] leading-relaxed">{preset.howTo}</pre>
          </div>

          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3 mt-5">
            <Field label="ID (slug)" value={form.id} onChange={(v) => setForm({ ...form, id: v })} placeholder="my-cluster" />
            <Field label="Display name" value={form.name} onChange={(v) => setForm({ ...form, name: v })} placeholder="My cluster" />
            <Field
              label="Endpoints"
              value={form.endpoints}
              onChange={(v) => setForm({ ...form, endpoints: v })}
              placeholder="http://etcd-1:2379, http://etcd-2:2379"
              full
            />
            <Field label="Username (optional)" value={form.username} onChange={(v) => setForm({ ...form, username: v })} />
            <Field
              label="Password (optional)"
              value={form.password}
              onChange={(v) => setForm({ ...form, password: v })}
              type="password"
            />
          </div>

          <div className="mt-6 flex items-center gap-3">
            <button onClick={onClose} className="btn btn-ghost">Cancel</button>
            <div className="ml-auto" />
            <button
              disabled={!form.id || !form.endpoints || add.isPending}
              onClick={() => add.mutate()}
              className="btn btn-primary"
            >
              <Plus className="w-4 h-4" /> {add.isPending ? "Adding…" : "Add cluster"}
            </button>
          </div>
          {add.error && <div className="mt-3 text-danger text-sm">{(add.error as Error).message}</div>}
        </div>
      </div>
    </div>
  );
}

function Field({
  label,
  value,
  onChange,
  placeholder,
  type = "text",
  full,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  type?: string;
  full?: boolean;
}) {
  return (
    <label className={full ? "sm:col-span-2" : ""}>
      <div className="text-xs muted mb-1">{label}</div>
      <input
        type={type}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        className="w-full h-10 px-3 rounded-lg text-sm bg-transparent font-mono"
        style={{ background: "rgb(var(--panel-2))", border: "1px solid rgb(var(--line))" }}
      />
    </label>
  );
}
