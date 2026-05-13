// Bulk-delete preview modal. Shows the actual keys about to disappear so the
// user can sanity-check before triggering an irreversible delete. Echoes the
// "type to confirm" pattern from the Confirm component for big selections.

import { useMemo, useState } from "react";
import { motion, AnimatePresence } from "framer-motion";
import { AlertTriangle, X, Trash2 } from "lucide-react";
import { useFocusTrap } from "../lib/focusTrap";

const PREVIEW_LIMIT = 50;

export function BulkDeletePreview({
  open,
  keys,
  onCancel,
  onConfirm,
  pending,
}: {
  open: boolean;
  keys: string[];
  onCancel: () => void;
  onConfirm: () => void;
  pending: boolean;
}) {
  const [typed, setTyped] = useState("");
  const trapRef = useFocusTrap<HTMLDivElement>(open);
  const needsType = keys.length > 10;
  const totalBytes = useMemo(
    () => keys.reduce((n, k) => n + k.length, 0),
    [keys],
  );
  const visible = keys.slice(0, PREVIEW_LIMIT);
  const hidden = keys.length - visible.length;

  const canConfirm = !pending && (!needsType || typed === "DELETE");

  return (
    <AnimatePresence>
      {open && (
        <motion.div
          key="bg"
          initial={{ opacity: 0 }}
          animate={{ opacity: 1 }}
          exit={{ opacity: 0 }}
          className="fixed inset-0 z-[55] bg-black/75 flex items-center justify-center p-6"
          onMouseDown={(e) => e.target === e.currentTarget && onCancel()}
        >
          <motion.div
            ref={trapRef}
            role="alertdialog"
            aria-modal="true"
            aria-labelledby="bulk-del-title"
            tabIndex={-1}
            initial={{ opacity: 0, y: 8, scale: 0.98 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, y: -4, scale: 0.98 }}
            className="w-[640px] max-w-full panel p-6 shadow-elev"
          >
            <div className="flex items-start gap-3">
              <div className="w-10 h-10 rounded-xl flex items-center justify-center shrink-0 bg-danger/10 text-danger">
                <AlertTriangle className="w-5 h-5" />
              </div>
              <div className="flex-1">
                <div id="bulk-del-title" className="text-base font-semibold">
                  Delete {keys.length} {keys.length === 1 ? "key" : "keys"}?
                </div>
                <p className="muted text-sm mt-1">
                  This action cannot be undone. Review the list — anything you
                  don't want gone, uncheck before confirming.
                </p>
              </div>
              <button onClick={onCancel} className="muted hover:text-current">
                <X className="w-4 h-4" />
              </button>
            </div>

            <div
              className="mt-4 rounded-lg p-3 max-h-[280px] overflow-y-auto font-mono text-xs"
              style={{ background: "rgb(var(--panel-2))", border: "1px solid rgb(var(--line))" }}
            >
              <ul className="space-y-1">
                {visible.map((k) => (
                  <li key={k} className="flex items-center gap-2 truncate" title={k}>
                    <Trash2 className="w-3 h-3 text-danger shrink-0" />
                    <span className="truncate">{k}</span>
                  </li>
                ))}
              </ul>
              {hidden > 0 && (
                <div className="muted mt-2 text-[11px]">
                  …and {hidden.toLocaleString()} more not shown
                </div>
              )}
            </div>

            <div className="mt-3 text-xs muted flex items-center gap-3">
              <span>
                <span className="font-medium" style={{ color: "rgb(var(--fg))" }}>
                  {keys.length.toLocaleString()}
                </span>{" "}
                keys · {totalBytes.toLocaleString()} bytes of key names
              </span>
            </div>

            {needsType && (
              <div className="mt-4">
                <div className="text-xs muted mb-1">
                  Type <span className="kbd">DELETE</span> to confirm
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
              <button onClick={onCancel} className="btn btn-ghost" disabled={pending}>
                Cancel
              </button>
              <div className="ml-auto" />
              <button
                onClick={onConfirm}
                disabled={!canConfirm}
                className="btn"
                style={{ background: "#ef4444", color: "white", borderColor: "transparent" }}
              >
                <Trash2 className="w-4 h-4" />{" "}
                {pending ? "Deleting…" : `Delete ${keys.length}`}
              </button>
            </div>
          </motion.div>
        </motion.div>
      )}
    </AnimatePresence>
  );
}
