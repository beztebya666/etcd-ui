import { useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useStore } from "../lib/store";
import {
  LayoutDashboard,
  FolderTree,
  Radio,
  Server,
  Wrench,
  Moon,
  Sun,
  Monitor,
  RefreshCw,
  Download,
  Users,
  ScrollText,
  Workflow,
  Sparkles,
  Lock,
} from "lucide-react";
import { api } from "../lib/api";
import { useQuery } from "@tanstack/react-query";
import { cn } from "../lib/cn";
import { useFocusTrap } from "../lib/focusTrap";

type Command = {
  id: string;
  label: string;
  hint?: string;
  icon: React.ComponentType<{ className?: string }>;
  run: () => void;
};

export function CommandPalette() {
  const { paletteOpen, setPaletteOpen, setTheme, selectedCluster, setSelectedCluster } = useStore();
  const navigate = useNavigate();
  const [q, setQ] = useState("");
  const [cursor, setCursor] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const trapRef = useFocusTrap<HTMLDivElement>(paletteOpen);
  const { data: clusters } = useQuery({ queryKey: ["clusters"], queryFn: api.clusters });

  useEffect(() => {
    if (paletteOpen) {
      setQ("");
      setCursor(0);
      setTimeout(() => inputRef.current?.focus(), 0);
    }
  }, [paletteOpen]);

  const commands = useMemo<Command[]>(() => {
    const goto = (path: string) => () => {
      setPaletteOpen(false);
      navigate(path);
    };
    const base: Command[] = [
      { id: "go-dash", label: "Go to Dashboard", icon: LayoutDashboard, run: goto("/dashboard") },
      { id: "go-keys", label: "Browse keys", icon: FolderTree, run: goto("/browse") },
      { id: "go-watch", label: "Open live watch", icon: Radio, run: goto("/watch") },
      { id: "go-txn", label: "Build a transaction", icon: Workflow, run: goto("/txn") },
      { id: "go-term", label: "Open etcdctl terminal", icon: Workflow, run: goto("/terminal") },
      { id: "go-cluster", label: "Cluster details", icon: Server, run: goto("/cluster") },
      { id: "go-rbac", label: "Users & Roles", icon: Users, run: goto("/rbac") },
      { id: "go-audit", label: "Audit log", icon: ScrollText, run: goto("/audit") },
      { id: "go-maint", label: "Open maintenance", icon: Wrench, run: goto("/maintenance") },
      { id: "go-locks", label: "Distributed locks", icon: Lock, run: goto("/locks") },
      { id: "theme-dark", label: "Theme · Dark", icon: Moon, run: () => setTheme("dark") },
      { id: "theme-light", label: "Theme · Light", icon: Sun, run: () => setTheme("light") },
      { id: "theme-system", label: "Theme · System", icon: Monitor, run: () => setTheme("system") },
      {
        id: "snapshot",
        label: "Download snapshot (.db) of current cluster",
        icon: Download,
        run: () => {
          if (selectedCluster) window.open(api.snapshotURL(selectedCluster), "_blank");
          setPaletteOpen(false);
        },
      },
      {
        id: "export",
        label: "Export current cluster as .json",
        icon: Download,
        run: () => {
          if (selectedCluster) window.open(api.exportURL(selectedCluster), "_blank");
          setPaletteOpen(false);
        },
      },
      {
        id: "show-tour",
        label: "Show onboarding tour",
        icon: Sparkles,
        run: () => {
          window.dispatchEvent(new Event("etcd-ui:show-tour"));
          setPaletteOpen(false);
        },
      },
      { id: "refresh", label: "Refresh clusters", icon: RefreshCw, run: () => window.location.reload() },
    ];
    const clusterCommands: Command[] =
      clusters?.map((c) => ({
        id: `cluster-${c.id}`,
        label: `Switch to cluster · ${c.name}`,
        hint: c.endpoints.join(", "),
        icon: Server,
        run: () => {
          setSelectedCluster(c.id);
          setPaletteOpen(false);
        },
      })) ?? [];
    return [...base, ...clusterCommands];
  }, [clusters, navigate, selectedCluster, setPaletteOpen, setSelectedCluster, setTheme]);

  const filtered = useMemo(() => {
    const s = q.trim().toLowerCase();
    if (!s) return commands;
    return commands.filter((c) =>
      (c.label + " " + (c.hint ?? "")).toLowerCase().includes(s),
    );
  }, [commands, q]);

  useEffect(() => {
    setCursor(0);
  }, [q]);

  if (!paletteOpen) return null;

  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center pt-[12vh] bg-black/70"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) setPaletteOpen(false);
      }}
    >
      <div
        ref={trapRef}
        role="dialog"
        aria-modal="true"
        aria-label="Command palette"
        tabIndex={-1}
        className="w-[680px] max-w-[92vw] rounded-2xl overflow-hidden shadow-elev animate-in"
        style={{ background: "rgb(var(--panel))", border: "1px solid rgb(var(--line))" }}
      >
        <input
          ref={inputRef}
          value={q}
          onChange={(e) => setQ(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Escape") setPaletteOpen(false);
            if (e.key === "ArrowDown") {
              e.preventDefault();
              setCursor((c) => Math.min(c + 1, filtered.length - 1));
            }
            if (e.key === "ArrowUp") {
              e.preventDefault();
              setCursor((c) => Math.max(c - 1, 0));
            }
            if (e.key === "Enter") {
              filtered[cursor]?.run();
            }
          }}
          placeholder="Type a command, key, cluster…"
          className="w-full bg-transparent px-5 py-4 text-[15px] outline-none border-b"
          style={{ borderColor: "rgb(var(--line))" }}
        />
        <div className="max-h-[50vh] overflow-y-auto p-1">
          {filtered.length === 0 ? (
            <div className="p-6 text-sm muted text-center">No matches.</div>
          ) : (
            filtered.map((c, i) => (
              <button
                key={c.id}
                onMouseEnter={() => setCursor(i)}
                onClick={() => c.run()}
                className={cn(
                  "w-full flex items-center gap-3 px-3 h-10 rounded-lg text-sm text-left",
                  i === cursor ? "soft-active" : "",
                )}
              >
                <c.icon className="w-4 h-4 muted" />
                <span className="flex-1 truncate">{c.label}</span>
                {c.hint && <span className="text-xs muted truncate max-w-[40%]">{c.hint}</span>}
              </button>
            ))
          )}
        </div>
        <div className="px-4 py-2 flex items-center gap-3 text-[11px] muted border-t"
             style={{ borderColor: "rgb(var(--line))" }}>
          <span><span className="kbd">↑↓</span> navigate</span>
          <span><span className="kbd">↵</span> run</span>
          <span><span className="kbd">esc</span> close</span>
        </div>
      </div>
    </div>
  );
}
