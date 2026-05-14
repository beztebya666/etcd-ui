import { useEffect, useState, type ReactNode } from "react";
import { Link, NavLink } from "react-router-dom";
import {
  LayoutDashboard,
  FolderTree,
  Radio,
  Server,
  Wrench,
  Settings as Cog,
  Search,
  Command,
  Boxes,
  Users,
  ScrollText,
  Workflow,
  Activity,
  GitCompareArrows,
  Terminal,
  Menu,
  X,
  Flame,
  TerminalSquare,
  Shield as ShieldIcon,
  Globe,
  Lock,
  ChevronLeft,
  ChevronRight,
} from "lucide-react";
import { cn } from "../lib/cn";
import { ClusterPicker } from "./ClusterPicker";
import { StreamStatus } from "./StreamStatus";
import { useStore } from "../lib/store";
import { useT } from "../lib/i18n";

type NavItem = { to: string; label: string; icon: React.ComponentType<{ className?: string }>; advanced?: boolean };

const NAV: NavItem[] = [
  { to: "/dashboard", label: "Dashboard", icon: LayoutDashboard },
  { to: "/browse", label: "Keys", icon: FolderTree },
  { to: "/watch", label: "Watch", icon: Radio },
  { to: "/txn", label: "Transactions", icon: Workflow, advanced: true },
  { to: "/cluster", label: "Cluster", icon: Server },
  { to: "/metrics", label: "Metrics", icon: Activity, advanced: true },
  { to: "/diff", label: "Diff", icon: GitCompareArrows, advanced: true },
  { to: "/heatmap", label: "Heatmap", icon: Flame, advanced: true },
  { to: "/terminal", label: "etcdctl", icon: TerminalSquare, advanced: true },
  { to: "/rbac", label: "Users & Roles", icon: Users, advanced: true },
  { to: "/permissions", label: "Permissions", icon: ShieldIcon, advanced: true },
  { to: "/federation", label: "Federation", icon: Globe, advanced: true },
  { to: "/audit", label: "Audit", icon: ScrollText },
  { to: "/maintenance", label: "Maintenance", icon: Wrench },
  { to: "/locks", label: "Locks", icon: Lock, advanced: true },
  { to: "/restore-recipe", label: "Snapshot recipe", icon: Terminal, advanced: true },
  { to: "/settings", label: "Settings", icon: Cog },
];

export function Shell({ children }: { children: ReactNode }) {
  const setPaletteOpen = useStore((s) => s.setPaletteOpen);
  const simpleMode = useStore((s) => s.simpleMode);
  const t = useT();

  // Mobile sidebar (off-canvas under 1024px)
  const [navOpen, setNavOpen] = useState(false);
  useEffect(() => {
    const close = () => setNavOpen(false);
    window.addEventListener("hashchange", close);
    return () => window.removeEventListener("hashchange", close);
  }, []);

  // Desktop sidebar collapse — operators on 13–14" laptops, half-window
  // setups, or anyone with a wide cluster page benefit from reclaiming
  // 200px. Persisted so the choice survives reloads.
  const [navCollapsed, setNavCollapsed] = useState<boolean>(() => {
    if (typeof window === "undefined") return false;
    return window.localStorage.getItem("etcd-ui:nav-collapsed") === "1";
  });
  const toggleNav = () => {
    setNavCollapsed((v) => {
      const next = !v;
      try {
        window.localStorage.setItem("etcd-ui:nav-collapsed", next ? "1" : "0");
      } catch {
        /* ignore quota / private-mode */
      }
      return next;
    });
  };

  const items = simpleMode ? NAV.filter((n) => !n.advanced) : NAV;

  return (
    <div
      className={cn(
        "h-full grid grid-rows-[56px_1fr]",
        navCollapsed ? "lg:grid-cols-[56px_1fr]" : "lg:grid-cols-[260px_1fr]",
      )}
    >
      <header
        className="lg:col-span-2 flex items-center justify-between gap-2 sm:gap-3 px-3 sm:px-4 border-b"
        style={{ borderColor: "rgb(var(--line))" }}
      >
        <div className="flex items-center gap-2.5 min-w-0 shrink-0">
          <button
            className="lg:hidden w-7 h-7 rounded-md surface flex items-center justify-center"
            onClick={() => setNavOpen(true)}
            aria-label="Open navigation menu"
          >
            <Menu className="w-4 h-4" aria-hidden="true" />
          </button>
          {/* Clickable logo + name → /dashboard. Wrapped in a single
              Link so the click target is one continuous element. The
              "universal" pill is intentionally NOT inside the Link so
              its text isn't part of the focus outline rectangle. */}
          <Link
            to="/dashboard"
            aria-label="Go to dashboard"
            className="flex items-center gap-2.5 min-w-0 shrink-0 rounded-md -mx-1 px-1 hover:opacity-80 transition-opacity select-none"
          >
            <div className="w-7 h-7 rounded-md bg-accent flex items-center justify-center shrink-0">
              <Boxes className="w-4 h-4 text-white" strokeWidth={2.4} />
            </div>
            <div className="font-semibold tracking-tight leading-none">etcd-ui</div>
          </Link>
          <span className="pill hidden sm:inline-flex leading-none shrink-0 select-none">universal</span>
        </div>

        <button
          onClick={() => setPaletteOpen(true)}
          className={cn(
            "surface hidden md:flex items-center gap-2 h-9 px-3 rounded-lg text-sm cursor-pointer",
            // min-w-0 lets the flex item shrink past content-size so the
            // placeholder span actually truncates instead of forcing a wrap;
            // flex-1 grows up to 460px on wide screens.
            "min-w-0 flex-1 max-w-[460px]",
            "muted hover:text-current",
          )}
        >
          <Search className="w-4 h-4 shrink-0" />
          <span className="text-left grow truncate min-w-0">
            {t("Search keys, jump to cluster, run commands…")}
          </span>
          <span className="kbd hidden lg:inline-flex items-center gap-1 shrink-0">
            <Command className="w-3 h-3" /> K
          </span>
        </button>
        <button
          onClick={() => setPaletteOpen(true)}
          className="md:hidden w-9 h-9 rounded-lg surface flex items-center justify-center"
          aria-label="search"
        >
          <Search className="w-4 h-4" />
        </button>

        <div className="flex items-center gap-2 shrink-0">
          <StreamStatus />
          <ClusterPicker />
        </div>
      </header>

      {/* Desktop sidebar */}
      <aside
        className="hidden lg:flex lg:flex-col row-start-2 border-r overflow-hidden"
        style={{ borderColor: "rgb(var(--line))" }}
      >
        <div className="flex-1 overflow-y-auto">
          <Nav
            items={items}
            t={t}
            collapsed={navCollapsed}
            onPaletteOpen={() => setPaletteOpen(true)}
            onShortcutsOpen={() => window.dispatchEvent(new Event("etcd-ui:show-shortcuts"))}
          />
        </div>
        <button
          onClick={toggleNav}
          // Collapse toggle pinned to the bottom of the sidebar.
          // Mirrors what VS Code, GitLab and Grafana do — bottom-left
          // chevron, label hidden when collapsed.
          className="border-t flex items-center gap-2 px-3 py-2 text-xs muted hover:text-current transition-colors"
          style={{ borderColor: "rgb(var(--line))" }}
          aria-label={navCollapsed ? "Expand navigation" : "Collapse navigation"}
          title={navCollapsed ? "Expand (more room → keys, editor)" : "Collapse (icons only)"}
        >
          {navCollapsed ? (
            <ChevronRight className="w-4 h-4 mx-auto" />
          ) : (
            <>
              <ChevronLeft className="w-4 h-4" />
              <span>Collapse</span>
            </>
          )}
        </button>
      </aside>

      {/* Mobile off-canvas */}
      {navOpen && (
        <div className="lg:hidden fixed inset-0 z-30">
          <div className="absolute inset-0 bg-black/70" onClick={() => setNavOpen(false)} />
          <aside
            className="absolute left-0 top-0 bottom-0 w-72 max-w-[85vw] overflow-y-auto"
            style={{ background: "rgb(var(--panel))", borderRight: "1px solid rgb(var(--line))" }}
          >
            <div
              className="flex items-center justify-between px-4 h-14 border-b"
              style={{ borderColor: "rgb(var(--line))" }}
            >
              <div className="font-semibold">etcd-ui</div>
              <button className="btn btn-ghost p-1.5" onClick={() => setNavOpen(false)}>
                <X className="w-5 h-5" />
              </button>
            </div>
            <Nav
              items={items}
              t={t}
              onPick={() => setNavOpen(false)}
              onPaletteOpen={() => {
                setNavOpen(false);
                setPaletteOpen(true);
              }}
              onShortcutsOpen={() => {
                setNavOpen(false);
                window.dispatchEvent(new Event("etcd-ui:show-shortcuts"));
              }}
            />
          </aside>
        </div>
      )}

      <a
        href="#main-content"
        className="sr-only focus:not-sr-only focus:fixed focus:top-2 focus:left-2 focus:z-[100] panel px-3 py-2"
      >
        Skip to main content
      </a>
      <main id="main-content" role="main" className="row-start-2 overflow-y-auto grid-bg">
        {/* No max-width cap — wide 21:9 / 4K monitors had ~300px wasted
            margin on each side, and the Browser/Cluster/Metrics pages
            actively want that space for the tree + editor. The pages
            themselves still use sensible inner max-widths where reading
            comfort matters (Settings, About text blocks). */}
        <div className="w-full p-4 sm:p-6 animate-in">{children}</div>
      </main>
    </div>
  );
}

function Nav({
  items,
  t,
  onPick,
  onPaletteOpen,
  onShortcutsOpen,
  collapsed,
}: {
  items: NavItem[];
  t: (k: string) => string;
  onPick?: () => void;
  onPaletteOpen?: () => void;
  onShortcutsOpen?: () => void;
  // When true, render an icons-only rail. Labels move to `title`
  // attribute so hover still surfaces them. The Tips block is hidden
  // because it doesn't survive without text.
  collapsed?: boolean;
}) {
  return (
    <>
      <nav className={cn("flex flex-col gap-0.5", collapsed ? "p-2" : "p-3")}>
        {items.map(({ to, label, icon: Icon }) => (
          <NavLink
            key={to}
            to={to}
            onClick={onPick}
            title={collapsed ? t(label) : undefined}
            className={({ isActive }) =>
              cn(
                "nav-item group flex items-center h-9 rounded-lg text-sm transition cursor-pointer",
                collapsed ? "justify-center px-0" : "gap-3 px-3",
                isActive ? "nav-item-active text-current" : "muted",
              )
            }
          >
            <Icon className="w-4 h-4 shrink-0 transition-transform group-hover:scale-110" />
            {!collapsed && <span className="truncate">{t(label)}</span>}
          </NavLink>
        ))}
      </nav>

      {!collapsed && (
        <div className="mx-3 mt-6 pt-5 border-t" style={{ borderColor: "rgb(var(--line))" }}>
          <div className="px-1 pb-3 text-[11px] uppercase tracking-wider muted">Tips</div>
          <ul className="pb-6 text-xs muted space-y-1">
            {(
              [
                {
                  keys: ["⌘K", "/"],
                  label: "command palette",
                  fn: () => onPaletteOpen?.(),
                },
                {
                  keys: ["?"],
                  label: "shortcuts help",
                  fn: () => onShortcutsOpen?.(),
                },
              ] as const
            ).map(({ keys, label, fn }) => (
              <li key={label}>
                <button
                  onClick={fn}
                  className="inline-flex items-center gap-2 px-1.5 py-1 -mx-1.5 rounded-md soft-hover hover:text-current"
                >
                  <span className="inline-flex items-center gap-1">
                    {keys.map((k, i) => (
                      <span key={i} className="kbd">{k}</span>
                    ))}
                  </span>
                  <span>{label}</span>
                </button>
              </li>
            ))}
          </ul>
        </div>
      )}
    </>
  );
}
