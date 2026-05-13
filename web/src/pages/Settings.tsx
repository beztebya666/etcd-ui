import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../lib/api";
import { useStore } from "../lib/store";
import { Plus, Trash2, Moon, Sun, Monitor, Sparkles, ToggleLeft, ToggleRight, Languages, Activity } from "lucide-react";
import { AddClusterWizard } from "../components/AddClusterWizard";
import { confirm } from "../components/Confirm";
import { toast } from "../components/Toast";
import { useT } from "../lib/i18n";
import { StreamDebugPanel } from "../components/StreamDebugPanel";

export function Settings() {
  const qc = useQueryClient();
  const clusters = useQuery({ queryKey: ["clusters"], queryFn: api.clusters });
  const versionQ = useQuery({ queryKey: ["version"], queryFn: api.version });
  const { theme, setTheme, simpleMode, toggleSimpleMode, lang, setLang } = useStore();
  const [wizardOpen, setWizardOpen] = useState(false);
  const t = useT();

  const remove = useMutation({
    mutationFn: (id: string) => api.removeCluster(id),
    onSuccess: () => {
      toast.success(t("Deleted"));
      qc.invalidateQueries({ queryKey: ["clusters"] });
    },
    onError: (e: Error) => toast.error(e.message),
  });

  return (
    <div className="space-y-6 max-w-3xl">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Settings</h1>
        <p className="muted mt-1">Manage clusters, themes and preferences.</p>
      </div>

      <section className="panel p-5 space-y-5">
        <div>
          <div className="text-sm font-medium mb-3">{t("Theme")}</div>
          <div className="flex gap-2 flex-wrap">
            <button onClick={() => setTheme("dark")} className={btn(theme === "dark")}>
              <Moon className="w-4 h-4" /> {t("Dark")}
            </button>
            <button onClick={() => setTheme("light")} className={btn(theme === "light")}>
              <Sun className="w-4 h-4" /> {t("Light")}
            </button>
            <button onClick={() => setTheme("system")} className={btn(theme === "system")}>
              <Monitor className="w-4 h-4" /> {t("System")}
            </button>
          </div>
        </div>

        <div>
          <div className="text-sm font-medium mb-3 flex items-center gap-2">
            <Languages className="w-4 h-4" /> {t("Language")}
          </div>
          <div className="flex gap-2">
            <button onClick={() => setLang("en")} className={btn(lang === "en")}>English</button>
            <button onClick={() => setLang("ru")} className={btn(lang === "ru")}>Русский</button>
          </div>
        </div>

        <div className="flex items-center gap-3">
          <button
            onClick={toggleSimpleMode}
            className="btn"
          >
            {simpleMode ? <ToggleRight className="w-4 h-4 text-accent-500" /> : <ToggleLeft className="w-4 h-4" />}
            Simple mode
          </button>
          <div className="text-sm muted">
            Friendlier labels, advanced features hidden. Recommended for newcomers.
          </div>
        </div>

        <div className="flex items-center gap-3">
          <button
            onClick={() => window.dispatchEvent(new Event("etcd-ui:show-tour"))}
            className="btn"
          >
            <Sparkles className="w-4 h-4" /> Replay onboarding tour
          </button>
          <div className="text-sm muted">Walk through the app in 30 seconds.</div>
        </div>
      </section>

      <section className="panel p-5">
        <div className="flex items-center gap-2 mb-3 flex-wrap">
          <div className="text-sm font-medium">Clusters</div>
          <div className="ml-auto">
            <button onClick={() => setWizardOpen(true)} className="btn btn-primary">
              <Plus className="w-4 h-4" /> <span className="hidden sm:inline">Add cluster…</span>
              <span className="sm:hidden">Add</span>
            </button>
          </div>
        </div>
        <ul className="divide-y" style={{ borderColor: "rgb(var(--line))" }}>
          {clusters.data?.map((c) => (
            <li
              key={c.id}
              className="row-hover -mx-2 px-2 rounded-md py-3 grid grid-cols-[1fr_auto] gap-3 items-center"
            >
              <div className="min-w-0">
                <div className="font-medium truncate">{c.name}</div>
                <div className="text-xs muted truncate font-mono" title={c.endpoints.join(", ")}>
                  <span className="inline-block mr-1.5 pill !py-0 !text-[10px]">{c.source}</span>
                  {c.endpoints.join(", ")}
                </div>
              </div>
              {(c.source === "manual" || c.source === "env") && (
                <button
                  onClick={async () => {
                    const ok = await confirm({
                      title: `${t("Delete")} "${c.name}"?`,
                      body: t("This action cannot be undone."),
                      danger: true,
                      confirmLabel: t("Delete"),
                    });
                    if (ok) remove.mutate(c.id);
                  }}
                  className="btn btn-ghost text-danger shrink-0"
                  aria-label={`Delete ${c.name}`}
                >
                  <Trash2 className="w-4 h-4" />
                </button>
              )}
            </li>
          ))}
          {!clusters.data?.length && <li className="py-4 text-sm muted">No clusters yet — click "Add cluster…".</li>}
        </ul>
        <p className="mt-4 text-xs muted">
          Tip: clusters can also be auto-discovered. See{" "}
          <a className="underline" href="https://github.com/yourorg/etcd-ui/blob/main/docs/CONNECT.md" target="_blank">
            docs/CONNECT.md
          </a>{" "}
          for how to get etcd endpoints from Kubernetes, Patroni, Vitess, Vault, APISIX, Cilium, KubeEdge, Karmada, Talos, etc.
        </p>
      </section>

      <StreamDebugPanel />

      <footer
        className="flex items-center gap-2 pt-3 mt-2 text-[11px] font-mono muted border-t"
        style={{ borderColor: "rgb(var(--line))" }}
      >
        <span className="font-semibold tracking-tight not-italic" style={{ color: "rgb(var(--fg))" }}>
          etcd-ui
        </span>
        <span>{versionQ.data?.version ?? "v0.1"}</span>
        {versionQ.data?.build && versionQ.data.build !== "dev" && (
          <>
            <span className="opacity-50">·</span>
            <span title={versionQ.data.build}>{shortBuild(versionQ.data.build)}</span>
          </>
        )}
        <span className="ml-auto opacity-70">
          <a
            href="https://github.com/yourorg/etcd-ui"
            target="_blank"
            rel="noreferrer"
            className="hover:underline"
          >
            github
          </a>
        </span>
      </footer>

      {wizardOpen && <AddClusterWizard onClose={() => setWizardOpen(false)} />}
    </div>
  );
}

function btn(active: boolean) {
  return "btn " + (active ? "btn-primary" : "");
}

// Format the build identifier for the footer pill. A git SHA is 40 hex
// chars and conventionally shown as the first 7. A date-based stamp like
// `20260513-211048` from Makefile/CI is shorter but breaks at unfortunate
// places when blindly cut to 7 (e.g. `2026051`). Slice only when the
// input looks like a long hex SHA.
function shortBuild(s: string): string {
  if (/^[0-9a-f]{12,}$/i.test(s)) return s.slice(0, 7);
  return s;
}
