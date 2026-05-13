// Imperative confirm() that returns a Promise<boolean>. Mount <Confirmer />
// once in App and call `confirm({...})` from anywhere — including outside
// React components.

import { useEffect, useState } from "react";
import { AlertTriangle, X } from "lucide-react";
import { motion, AnimatePresence } from "framer-motion";
import { useFocusTrap } from "../lib/focusTrap";

export type ConfirmOpts = {
  title: string;
  body?: string;
  danger?: boolean;
  confirmLabel?: string;
  cancelLabel?: string;
  /** If set, the user must type this exact string to enable the confirm button. */
  typeToConfirm?: string;
};

const EV = "etcd-ui:confirm";
type Req = ConfirmOpts & { id: number; resolve: (ok: boolean) => void };

let counter = 0;

export function confirm(opts: ConfirmOpts): Promise<boolean> {
  return new Promise((resolve) => {
    window.dispatchEvent(
      new CustomEvent<Req>(EV, { detail: { ...opts, id: ++counter, resolve } }),
    );
  });
}

export function Confirmer() {
  const [req, setReq] = useState<Req | null>(null);
  const [typed, setTyped] = useState("");
  const trapRef = useFocusTrap<HTMLDivElement>(!!req);

  useEffect(() => {
    const onAsk = (e: Event) => {
      const r = (e as CustomEvent<Req>).detail;
      setTyped("");
      setReq(r);
    };
    window.addEventListener(EV, onAsk);
    return () => window.removeEventListener(EV, onAsk);
  }, []);

  const close = (ok: boolean) => {
    if (req) {
      req.resolve(ok);
      setReq(null);
    }
  };

  // Escape / Enter shortcuts
  useEffect(() => {
    if (!req) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") close(false);
      if (e.key === "Enter" && (!req.typeToConfirm || typed === req.typeToConfirm)) close(true);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [req, typed]); // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <AnimatePresence>
      {req && (
        <motion.div
          key="bg"
          initial={{ opacity: 0 }}
          animate={{ opacity: 1 }}
          exit={{ opacity: 0 }}
          className="fixed inset-0 z-[55] bg-black/75 flex items-center justify-center p-6"
          onMouseDown={(e) => e.target === e.currentTarget && close(false)}
        >
          <motion.div
            ref={trapRef}
            role="alertdialog"
            aria-modal="true"
            aria-labelledby="confirm-title"
            aria-describedby="confirm-body"
            tabIndex={-1}
            initial={{ opacity: 0, y: 8, scale: 0.98 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, y: -4, scale: 0.98 }}
            transition={{ type: "spring", duration: 0.25 }}
            className="w-[460px] max-w-full panel p-6 shadow-elev"
          >
            <div className="flex items-start gap-3">
              <div
                className={
                  "w-10 h-10 rounded-xl flex items-center justify-center shrink-0 " +
                  (req.danger ? "bg-danger/10 text-danger" : "bg-accent/10 text-accent-500")
                }
              >
                <AlertTriangle className="w-5 h-5" />
              </div>
              <div className="flex-1">
                <div id="confirm-title" className="text-base font-semibold">{req.title}</div>
                {req.body && <p id="confirm-body" className="muted text-sm mt-1">{req.body}</p>}
              </div>
              <button onClick={() => close(false)} className="muted hover:text-current">
                <X className="w-4 h-4" />
              </button>
            </div>

            {req.typeToConfirm && (
              <div className="mt-4">
                <div className="text-xs muted mb-1">
                  Type <span className="kbd">{req.typeToConfirm}</span> to confirm
                </div>
                <input
                  autoFocus
                  value={typed}
                  onChange={(e) => setTyped(e.target.value)}
                  className="w-full h-10 px-3 rounded-lg text-sm font-mono"
                  style={{ background: "rgb(var(--panel-2))", border: "1px solid rgb(var(--line))" }}
                />
              </div>
            )}

            <div className="mt-6 flex items-center gap-2">
              <button onClick={() => close(false)} className="btn btn-ghost">
                {req.cancelLabel ?? "Cancel"}
              </button>
              <div className="ml-auto" />
              <button
                onClick={() => close(true)}
                disabled={!!req.typeToConfirm && typed !== req.typeToConfirm}
                className={"btn " + (req.danger ? "" : "btn-primary")}
                style={req.danger ? { background: "#ef4444", color: "white", borderColor: "transparent" } : undefined}
              >
                {req.confirmLabel ?? "Confirm"}
              </button>
            </div>
          </motion.div>
        </motion.div>
      )}
    </AnimatePresence>
  );
}
