import { create } from "zustand";
import { persist } from "zustand/middleware";

type Theme = "dark" | "light" | "system";
type Lang = "en" | "ru";

type State = {
  selectedCluster: string | null;
  setSelectedCluster: (id: string | null) => void;
  theme: Theme;
  setTheme: (t: Theme) => void;
  lang: Lang;
  setLang: (l: Lang) => void;
  paletteOpen: boolean;
  setPaletteOpen: (v: boolean) => void;
  density: "comfortable" | "compact";
  toggleDensity: () => void;
  /** Has the user seen the onboarding tour? */
  tourCompleted: boolean;
  setTourCompleted: (v: boolean) => void;
  /** "Simple mode" hides advanced UI (txn builder, raw editor, etc.) */
  simpleMode: boolean;
  toggleSimpleMode: () => void;
  /** Pinned clusters surface first in the picker and dashboard. */
  pinnedClusters: string[];
  togglePin: (id: string) => void;
  reorderPinned: (from: number, to: number) => void;
  /** Compare-pair for the Diff page. */
  diffAgainst: string | null;
  setDiffAgainst: (id: string | null) => void;
  /** Saved views: named { prefix, valueRegex } presets per cluster. */
  savedViews: Record<string, { name: string; prefix: string; valueRegex: string }[]>;
  saveView: (cluster: string, name: string, prefix: string, valueRegex: string) => void;
  deleteView: (cluster: string, name: string) => void;
  reorderView: (cluster: string, from: number, to: number) => void;
};

export const useStore = create<State>()(
  persist(
    (set) => ({
      selectedCluster: null,
      setSelectedCluster: (id) => set({ selectedCluster: id }),
      theme: "dark",
      setTheme: (theme) => {
        const root = document.documentElement;
        const wantLight =
          theme === "light" ||
          (theme === "system" && window.matchMedia("(prefers-color-scheme: light)").matches);
        root.classList.toggle("light", wantLight);
        root.classList.toggle("dark", !wantLight);
        set({ theme });
      },
      paletteOpen: false,
      setPaletteOpen: (paletteOpen) => set({ paletteOpen }),
      density: "comfortable",
      toggleDensity: () =>
        set((s) => ({ density: s.density === "comfortable" ? "compact" : "comfortable" })),
      tourCompleted: false,
      setTourCompleted: (tourCompleted) => set({ tourCompleted }),
      simpleMode: false,
      toggleSimpleMode: () => set((s) => ({ simpleMode: !s.simpleMode })),
      lang: "en",
      setLang: (lang) => set({ lang }),
      pinnedClusters: [],
      togglePin: (id) =>
        set((s) => ({
          pinnedClusters: s.pinnedClusters.includes(id)
            ? s.pinnedClusters.filter((x) => x !== id)
            : [...s.pinnedClusters, id],
        })),
      reorderPinned: (from, to) =>
        set((s) => {
          const arr = s.pinnedClusters.slice();
          const [m] = arr.splice(from, 1);
          arr.splice(to, 0, m);
          return { pinnedClusters: arr };
        }),
      diffAgainst: null,
      setDiffAgainst: (diffAgainst) => set({ diffAgainst }),
      savedViews: {},
      saveView: (cluster, name, prefix, valueRegex) =>
        set((s) => {
          const cur = s.savedViews[cluster] ?? [];
          const next = cur.filter((v) => v.name !== name).concat({ name, prefix, valueRegex });
          return { savedViews: { ...s.savedViews, [cluster]: next } };
        }),
      deleteView: (cluster, name) =>
        set((s) => ({
          savedViews: {
            ...s.savedViews,
            [cluster]: (s.savedViews[cluster] ?? []).filter((v) => v.name !== name),
          },
        })),
      reorderView: (cluster, from, to) =>
        set((s) => {
          const cur = (s.savedViews[cluster] ?? []).slice();
          if (from < 0 || from >= cur.length || to < 0 || to >= cur.length || from === to) {
            return s;
          }
          const [m] = cur.splice(from, 1);
          cur.splice(to, 0, m);
          return { savedViews: { ...s.savedViews, [cluster]: cur } };
        }),
    }),
    { name: "etcd-ui-state" },
  ),
);
