// Styled dropdown — replaces native <select> where we want to keep
// the project's look. Keyboard: Enter/Space opens, Esc closes, ↑↓ navigates,
// Enter commits. Anchored to its trigger, dismisses on outside click.

import { useEffect, useRef, useState, type ReactNode } from "react";
import { ChevronDown, Check } from "lucide-react";
import { cn } from "../lib/cn";

export type DropdownItem<T> = {
  value: T;
  label: ReactNode;
  hint?: ReactNode;
};

export function Dropdown<T extends string | number>({
  value,
  items,
  onChange,
  className,
  buttonClassName,
  align = "left",
  label,
  ariaLabel,
}: {
  value: T;
  items: DropdownItem<T>[];
  onChange: (v: T) => void;
  className?: string;
  buttonClassName?: string;
  align?: "left" | "right";
  /** Optional small prefix rendered inside the trigger before the active label. */
  label?: ReactNode;
  ariaLabel?: string;
}) {
  const [open, setOpen] = useState(false);
  const [cursor, setCursor] = useState<number>(() =>
    Math.max(0, items.findIndex((i) => i.value === value)),
  );
  const rootRef = useRef<HTMLDivElement>(null);
  const listRef = useRef<HTMLUListElement>(null);

  // outside click / escape
  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    window.addEventListener("mousedown", onDown);
    window.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("mousedown", onDown);
      window.removeEventListener("keydown", onKey);
    };
  }, [open]);

  // scroll cursor into view
  useEffect(() => {
    if (!open) return;
    const li = listRef.current?.children[cursor] as HTMLElement | undefined;
    li?.scrollIntoView({ block: "nearest" });
  }, [cursor, open]);

  // keep cursor in sync with value when opening
  useEffect(() => {
    if (open) {
      setCursor(Math.max(0, items.findIndex((i) => i.value === value)));
    }
  }, [open, value, items]);

  const active = items.find((i) => i.value === value);

  return (
    <div ref={rootRef} className={cn("relative inline-block", className)}>
      <button
        type="button"
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-label={ariaLabel}
        onClick={() => setOpen((v) => !v)}
        onKeyDown={(e) => {
          if ((e.key === "ArrowDown" || e.key === " " || e.key === "Enter") && !open) {
            e.preventDefault();
            setOpen(true);
          }
        }}
        className={cn(
          "inline-flex items-center gap-1.5 h-8 px-2.5 rounded-md text-xs surface cursor-pointer",
          buttonClassName,
        )}
      >
        {label != null && <span className="muted shrink-0">{label}</span>}
        <span className="font-medium truncate text-left flex-1 min-w-0">
          {active?.label ?? <span className="muted">…</span>}
        </span>
        <ChevronDown className={cn("w-3 h-3 muted transition-transform shrink-0", open && "rotate-180")} />
      </button>

      {open && (
        <ul
          ref={listRef}
          role="listbox"
          aria-label={ariaLabel}
          tabIndex={-1}
          onKeyDown={(e) => {
            if (e.key === "ArrowDown") {
              e.preventDefault();
              setCursor((c) => Math.min(c + 1, items.length - 1));
            } else if (e.key === "ArrowUp") {
              e.preventDefault();
              setCursor((c) => Math.max(c - 1, 0));
            } else if (e.key === "Enter") {
              e.preventDefault();
              onChange(items[cursor].value);
              setOpen(false);
            }
          }}
          className={cn(
            "absolute z-20 mt-1 min-w-full panel p-1 shadow-elev animate-in",
            align === "right" ? "right-0" : "left-0",
          )}
        >
          {items.map((it, i) => {
            const isActive = it.value === value;
            const isCursor = i === cursor;
            return (
              <li
                key={String(it.value)}
                role="option"
                aria-selected={isActive}
                onMouseEnter={() => setCursor(i)}
                onClick={() => {
                  onChange(it.value);
                  setOpen(false);
                }}
                className={cn(
                  "grid items-center gap-3 px-2.5 py-1.5 rounded-md text-xs cursor-pointer whitespace-nowrap",
                  // Three columns: label (auto-sized to widest), hint
                  // (tabular-num so digits align), check mark slot.
                  "grid-cols-[auto_1fr_12px]",
                  isCursor && "soft-active",
                  !isCursor && "soft-hover",
                )}
              >
                <span className="font-medium">{it.label}</span>
                <span
                  className="muted text-[10px] text-right tabular-nums font-mono"
                >
                  {it.hint ?? ""}
                </span>
                <span className="w-3 h-3 flex items-center justify-center">
                  {isActive && <Check className="w-3 h-3 text-accent-500" />}
                </span>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}
