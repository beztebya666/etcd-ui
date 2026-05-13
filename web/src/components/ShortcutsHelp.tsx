// Keyboard-shortcut cheat sheet. Pops up on "?". Listens for a custom
// event so any component (CommandPalette, etc.) can trigger it without
// drilling a setter through props.

import { useEffect, useState } from "react";
import { motion, AnimatePresence } from "framer-motion";
import { Keyboard, X } from "lucide-react";
import { useFocusTrap } from "../lib/focusTrap";

const EV = "etcd-ui:show-shortcuts";

export function openShortcuts() {
  window.dispatchEvent(new Event(EV));
}

type Row = { keys: string[]; label: string };

const ROWS: Row[] = [
  { keys: ["⌘", "K"], label: "open command palette" },
  { keys: ["Ctrl", "K"], label: "open command palette (non-Mac)" },
  { keys: ["/"], label: "focus search / open palette" },
  { keys: ["?"], label: "show this help" },
  { keys: ["Esc"], label: "close any open dialog or dropdown" },
  { keys: ["↑", "↓"], label: "navigate keys in the tree" },
  { keys: ["←", "→"], label: "collapse / expand a folder" },
  { keys: ["Enter"], label: "select key or toggle folder" },
  { keys: ["Space"], label: "toggle key checkbox" },
  { keys: ["Home", "End"], label: "jump to first / last key" },
];

export function ShortcutsHelp() {
  const [open, setOpen] = useState(false);
  const trapRef = useFocusTrap<HTMLDivElement>(open);

  useEffect(() => {
    const onShow = () => setOpen(true);
    window.addEventListener(EV, onShow);
    return () => window.removeEventListener(EV, onShow);
  }, []);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open]);

  return (
    <AnimatePresence>
      {open && (
        <motion.div
          key="bg"
          initial={{ opacity: 0 }}
          animate={{ opacity: 1 }}
          exit={{ opacity: 0 }}
          className="fixed inset-0 z-[55] bg-black/70 flex items-center justify-center p-6"
          onMouseDown={(e) => e.target === e.currentTarget && setOpen(false)}
        >
          <motion.div
            ref={trapRef}
            role="dialog"
            aria-modal="true"
            aria-labelledby="shortcuts-title"
            tabIndex={-1}
            initial={{ opacity: 0, y: 8, scale: 0.98 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, y: -4, scale: 0.98 }}
            className="w-[460px] max-w-full panel p-6 shadow-elev"
          >
            <div className="flex items-start gap-3 mb-4">
              <div className="w-10 h-10 rounded-xl bg-accent/15 text-accent-500 flex items-center justify-center shrink-0">
                <Keyboard className="w-5 h-5" />
              </div>
              <div className="flex-1">
                <div id="shortcuts-title" className="text-base font-semibold">
                  Keyboard shortcuts
                </div>
                <p className="muted text-sm mt-0.5">Press anywhere in the app.</p>
              </div>
              <button onClick={() => setOpen(false)} className="muted hover:text-current">
                <X className="w-4 h-4" />
              </button>
            </div>

            <ul className="space-y-2">
              {ROWS.map((r, i) => (
                <li key={i} className="grid grid-cols-[140px_1fr] items-center gap-3">
                  <div className="flex items-center gap-1.5 justify-end">
                    {r.keys.map((k, j) => (
                      <span key={j} className="kbd">{k}</span>
                    ))}
                  </div>
                  <span className="text-sm muted">{r.label}</span>
                </li>
              ))}
            </ul>
          </motion.div>
        </motion.div>
      )}
    </AnimatePresence>
  );
}
