import { Routes, Route, Navigate, useLocation } from "react-router-dom";
import { useEffect } from "react";
import { MotionConfig } from "framer-motion";
import { Shell } from "./components/Shell";
import { CommandPalette } from "./components/CommandPalette";
import { OnboardingTour } from "./components/OnboardingTour";
import { Toaster } from "./components/Toast";
import { Confirmer } from "./components/Confirm";
import { AlertWatcher } from "./components/AlertWatcher";
import { SessionKeepalive } from "./components/SessionKeepalive";
import { ShortcutsHelp, openShortcuts } from "./components/ShortcutsHelp";
import { useStore } from "./lib/store";
import { Dashboard } from "./pages/Dashboard";
import { Browser } from "./pages/Browser";
import { WatchPage } from "./pages/WatchPage";
import { ClusterPage } from "./pages/ClusterPage";
import { Maintenance } from "./pages/Maintenance";
import { Settings } from "./pages/Settings";
import { RBACPage } from "./pages/RBAC";
import { AuditPage } from "./pages/Audit";
import { TxnPage } from "./pages/Txn";
import { DiffPage } from "./pages/Diff";
import { MetricsPage } from "./pages/Metrics";
import { RestoreRecipePage } from "./pages/RestoreRecipe";
import { HeatmapPage } from "./pages/Heatmap";
import { TerminalPage } from "./pages/Terminal";
import { LoginPage } from "./pages/Login";
import { PermissionsPage } from "./pages/Permissions";
import { FederationPage } from "./pages/Federation";
import { LocksPage } from "./pages/Locks";

export default function App() {
  const { setPaletteOpen, theme, setTheme } = useStore();
  const loc = useLocation();

  useEffect(() => {
    setTheme(theme); // apply persisted theme on first load
    // Skip global shortcuts when the user is typing into an input / textarea
    // / contenteditable — otherwise `/` and `?` would clobber every form.
    const isTyping = (el: EventTarget | null) => {
      if (!el || !(el instanceof HTMLElement)) return false;
      const tag = el.tagName;
      return (
        tag === "INPUT" ||
        tag === "TEXTAREA" ||
        tag === "SELECT" ||
        el.isContentEditable
      );
    };
    const onKey = (e: KeyboardEvent) => {
      // Cmd/Ctrl+K — always open palette, even from inputs.
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setPaletteOpen(true);
        return;
      }
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      if (isTyping(e.target)) return;
      // "/" focuses search → opens the palette (same surface as Cmd+K).
      if (e.key === "/") {
        e.preventDefault();
        setPaletteOpen(true);
        return;
      }
      // "?" pops the shortcuts cheat sheet.
      if (e.key === "?") {
        e.preventDefault();
        openShortcuts();
        return;
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [setPaletteOpen, setTheme, theme]);

  useEffect(() => setPaletteOpen(false), [loc.pathname, setPaletteOpen]);

  // /login bypasses Shell so the form isn't wrapped in nav chrome the user
  // can't use yet.
  if (loc.pathname === "/login") {
    return (
      <MotionConfig reducedMotion="user">
        <Routes>
          <Route path="/login" element={<LoginPage />} />
        </Routes>
        <Toaster />
      </MotionConfig>
    );
  }

  return (
    <MotionConfig reducedMotion="user">
      <Shell>
        <Routes>
          <Route path="/" element={<Navigate to="/dashboard" replace />} />
          <Route path="/dashboard" element={<Dashboard />} />
          <Route path="/browse" element={<Browser />} />
          <Route path="/watch" element={<WatchPage />} />
          <Route path="/txn" element={<TxnPage />} />
          <Route path="/cluster" element={<ClusterPage />} />
          <Route path="/metrics" element={<MetricsPage />} />
          <Route path="/diff" element={<DiffPage />} />
          <Route path="/heatmap" element={<HeatmapPage />} />
          <Route path="/terminal" element={<TerminalPage />} />
          <Route path="/rbac" element={<RBACPage />} />
          <Route path="/permissions" element={<PermissionsPage />} />
          <Route path="/federation" element={<FederationPage />} />
          <Route path="/audit" element={<AuditPage />} />
          <Route path="/maintenance" element={<Maintenance />} />
          <Route path="/locks" element={<LocksPage />} />
          <Route path="/restore-recipe" element={<RestoreRecipePage />} />
          <Route path="/settings" element={<Settings />} />
        </Routes>
      </Shell>
      <CommandPalette />
      <OnboardingTour />
      <Toaster />
      <Confirmer />
      <AlertWatcher />
      <SessionKeepalive />
      <ShortcutsHelp />
    </MotionConfig>
  );
}
