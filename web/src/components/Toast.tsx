// Tiny toast system: imperative API via window event so any component can
// `toast.success("...")` without prop-drilling a provider context.

import { useEffect, useState } from "react";
import { CheckCircle2, AlertTriangle, Info, X } from "lucide-react";
import { cn } from "../lib/cn";
import { motion, AnimatePresence } from "framer-motion";

export type ToastKind = "success" | "error" | "info";
export type ToastItem = { id: number; kind: ToastKind; message: string };

let counter = 0;
const EV = "etcd-ui:toast";

function push(kind: ToastKind, message: string) {
  window.dispatchEvent(
    new CustomEvent<ToastItem>(EV, { detail: { id: ++counter, kind, message } }),
  );
}

export const toast = {
  success: (m: string) => push("success", m),
  error: (m: string) => push("error", m),
  info: (m: string) => push("info", m),
};

export function Toaster() {
  const [items, setItems] = useState<ToastItem[]>([]);

  useEffect(() => {
    const onPush = (e: Event) => {
      const t = (e as CustomEvent<ToastItem>).detail;
      setItems((prev) => [...prev, t].slice(-5));
      window.setTimeout(() => {
        setItems((prev) => prev.filter((x) => x.id !== t.id));
      }, t.kind === "error" ? 6_000 : 3_500);
    };
    window.addEventListener(EV, onPush);
    return () => window.removeEventListener(EV, onPush);
  }, []);

  return (
    <div
      className="fixed bottom-4 right-4 z-[60] flex flex-col-reverse gap-2 max-w-[min(92vw,420px)] pointer-events-none"
      role="region"
      aria-label="notifications"
      aria-live="polite"
    >
      <AnimatePresence initial={false}>
        {items.map((t) => (
          <motion.div
            key={t.id}
            initial={{ opacity: 0, y: 12, scale: 0.96 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, scale: 0.96, transition: { duration: 0.14 } }}
            transition={{ type: "spring", duration: 0.28 }}
            className={cn(
              "pointer-events-auto panel shadow-elev p-3 flex items-start gap-2.5 text-sm",
              t.kind === "success" && "border-l-2 border-l-accent-500",
              t.kind === "error" && "border-l-2 border-l-danger",
              t.kind === "info" && "border-l-2 border-l-white/30",
            )}
          >
            <Icon kind={t.kind} />
            <span className="flex-1 min-w-0 break-words">{t.message}</span>
            <button
              onClick={() => setItems((prev) => prev.filter((x) => x.id !== t.id))}
              className="muted hover:text-current shrink-0"
              aria-label="dismiss"
            >
              <X className="w-3.5 h-3.5" />
            </button>
          </motion.div>
        ))}
      </AnimatePresence>
    </div>
  );
}

function Icon({ kind }: { kind: ToastKind }) {
  if (kind === "success") return <CheckCircle2 className="w-4 h-4 text-accent-500 mt-0.5 shrink-0" />;
  if (kind === "error") return <AlertTriangle className="w-4 h-4 text-danger mt-0.5 shrink-0" />;
  return <Info className="w-4 h-4 muted mt-0.5 shrink-0" />;
}
