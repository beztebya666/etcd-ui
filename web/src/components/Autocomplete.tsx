// Production-grade autocomplete. Designed for the "I have a 500k-key
// cluster" case where you can't drop the full list into the DOM:
//
//   * Suggestions are produced by an async `query(input)` function the
//     caller supplies. We never assume the data fits in memory; the
//     query can hit the network, return only the top N matches.
//   * Only PAGE_SIZE (5 by default) results are rendered at a time.
//     "Load more" reveals the next page on demand. The dropdown never
//     becomes a scrolling wall.
//   * Keyboard: Tab inserts the highlighted suggestion (or top if none
//     is highlighted), Arrow keys move highlight, Enter commits + closes,
//     Esc closes without committing.
//   * Mouse: click to commit. Hover highlights without taking focus.
//   * The dropdown is positioned relative to the input via the parent
//     wrapper so it works inside flex/grid containers without
//     `position: fixed` z-index battles.
//
// Used by the key-prefix input (Browser, Watch, Heatmap, Diff) and the
// regex search input (Browser) — anywhere an operator might be staring
// at a 5-character substring and would benefit from being shown the 5
// closest matching keys.

import { useEffect, useMemo, useRef, useState } from "react";
import { cn } from "../lib/cn";

export type AcSuggestion = {
  // Value committed back to the input when the user picks this row.
  value: string;
  // Human-friendly label (defaults to value).
  label?: string;
  // Optional muted suffix (e.g. "1.2k keys", "binary · 540 B").
  hint?: string;
};

export type AcProvider = (input: string, limit: number) => Promise<AcSuggestion[]>;

export function Autocomplete({
  value,
  onChange,
  query,
  placeholder,
  className,
  inputClassName,
  ariaLabel,
  pageSize = 5,
  maxPages = 6,
  debounceMs = 150,
  prefix,
  monospace,
  disabled,
  autoFocus,
}: {
  value: string;
  onChange: (v: string) => void;
  query: AcProvider;
  placeholder?: string;
  className?: string;
  inputClassName?: string;
  ariaLabel?: string;
  pageSize?: number;
  maxPages?: number;
  debounceMs?: number;
  // Optional left-aligned icon/text rendered inside the input wrapper
  // (used e.g. for the magnifying glass on the search input).
  prefix?: React.ReactNode;
  monospace?: boolean;
  disabled?: boolean;
  autoFocus?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const [items, setItems] = useState<AcSuggestion[]>([]);
  const [pages, setPages] = useState(1);
  const [loading, setLoading] = useState(false);
  const [cursor, setCursor] = useState(-1);
  const rootRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLUListElement>(null);
  // Track the latest in-flight query so a slow earlier call doesn't
  // overwrite a fast later one (out-of-order completion).
  const seqRef = useRef(0);

  // Debounced refetch as the user types.
  useEffect(() => {
    if (!open) return;
    const mySeq = ++seqRef.current;
    setLoading(true);
    const t = window.setTimeout(async () => {
      try {
        const limit = pages * pageSize + 1; // +1 so we know "more available"
        const r = await query(value, limit);
        if (seqRef.current !== mySeq) return;
        setItems(r);
        setCursor(-1);
      } catch {
        if (seqRef.current === mySeq) setItems([]);
      } finally {
        if (seqRef.current === mySeq) setLoading(false);
      }
    }, debounceMs);
    return () => window.clearTimeout(t);
  }, [open, value, pages, pageSize, debounceMs, query]);

  // Outside click → close.
  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    window.addEventListener("mousedown", onDown);
    return () => window.removeEventListener("mousedown", onDown);
  }, [open]);

  // Scroll active item into view on cursor move.
  useEffect(() => {
    if (!open || cursor < 0) return;
    const li = listRef.current?.children[cursor] as HTMLElement | undefined;
    li?.scrollIntoView({ block: "nearest" });
  }, [cursor, open]);

  const visible = useMemo(() => items.slice(0, pages * pageSize), [items, pages, pageSize]);
  const hasMore = items.length > visible.length && pages < maxPages;

  const commit = (it: AcSuggestion) => {
    onChange(it.value);
    setOpen(false);
    // Restore focus to the input so the user can keep typing.
    requestAnimationFrame(() => inputRef.current?.focus());
  };

  const onKeyDown: React.KeyboardEventHandler<HTMLInputElement> = (e) => {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      if (!open) setOpen(true);
      setCursor((c) => Math.min(visible.length - 1, c + 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setCursor((c) => Math.max(-1, c - 1));
    } else if (e.key === "Tab" && open) {
      // Tab inserts the highlighted (or top) match without closing the
      // dropdown — common in shell completion / Prometheus query UI.
      const pick = cursor >= 0 ? visible[cursor] : visible[0];
      if (pick) {
        e.preventDefault();
        onChange(pick.value);
      }
    } else if (e.key === "Enter") {
      if (open && cursor >= 0 && visible[cursor]) {
        e.preventDefault();
        commit(visible[cursor]);
      }
    } else if (e.key === "Escape") {
      if (open) {
        e.preventDefault();
        setOpen(false);
      }
    }
  };

  return (
    <div ref={rootRef} className={cn("relative", className)}>
      <div className="flex items-center gap-2">
        {prefix}
        <input
          ref={inputRef}
          value={value}
          onChange={(e) => {
            onChange(e.target.value);
            setOpen(true);
            setPages(1);
          }}
          onFocus={() => setOpen(true)}
          onKeyDown={onKeyDown}
          placeholder={placeholder}
          aria-label={ariaLabel}
          autoComplete="off"
          spellCheck={false}
          autoFocus={autoFocus}
          disabled={disabled}
          aria-autocomplete="list"
          aria-controls="autocomplete-list"
          aria-expanded={open}
          className={cn(
            "flex-1 min-w-0 h-9 px-3 rounded-lg bg-transparent text-sm",
            monospace && "font-mono",
            inputClassName,
          )}
          style={{ background: "rgb(var(--panel-2))", border: "1px solid rgb(var(--line))" }}
        />
      </div>
      {open && (visible.length > 0 || loading) && (
        <div
          className="absolute left-0 right-0 mt-1 z-30 panel p-1 shadow-elev animate-in"
          // Cap height so even very wide suggestion blocks don't push
          // page content downward.
          style={{ maxHeight: "320px", overflow: "auto" }}
        >
          <ul ref={listRef} role="listbox" id="autocomplete-list" className="space-y-0.5">
            {visible.map((it, i) => (
              <li
                key={it.value + ":" + i}
                role="option"
                aria-selected={i === cursor}
                onMouseEnter={() => setCursor(i)}
                onMouseDown={(e) => {
                  // Use mousedown so we commit BEFORE the input blurs;
                  // otherwise the outside-click handler closes us first.
                  e.preventDefault();
                  commit(it);
                }}
                className={cn(
                  "px-2.5 py-1.5 rounded-md text-xs cursor-pointer flex items-center gap-2",
                  i === cursor ? "soft-active" : "soft-hover",
                )}
              >
                <span className={cn("flex-1 min-w-0 truncate", monospace && "font-mono")}>
                  <HighlightMatch text={it.label ?? it.value} match={value} />
                </span>
                {it.hint && (
                  <span className="muted text-[10px] font-mono shrink-0">{it.hint}</span>
                )}
              </li>
            ))}
          </ul>
          <div className="flex items-center justify-between text-[10px] muted px-2 pt-1.5 pb-1">
            <span>
              {loading ? (
                "loading…"
              ) : (
                <>
                  <span className="kbd !text-[9px] !px-1">Tab</span> to insert ·{" "}
                  <span className="kbd !text-[9px] !px-1">↑↓</span> navigate
                </>
              )}
            </span>
            {hasMore && (
              <button
                onMouseDown={(e) => {
                  e.preventDefault();
                  setPages((p) => p + 1);
                }}
                className="text-accent-500 hover:underline"
              >
                Load more
              </button>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

// Highlight every contiguous run of `match` inside `text`. Case-insensitive
// because operators don't shift+letter when grepping for keys.
function HighlightMatch({ text, match }: { text: string; match: string }) {
  if (!match) return <>{text}</>;
  const m = match.toLowerCase();
  const t = text.toLowerCase();
  const parts: React.ReactNode[] = [];
  let i = 0;
  let cursor = 0;
  while (i < t.length) {
    const hit = t.indexOf(m, i);
    if (hit < 0) break;
    if (hit > i) parts.push(text.slice(i, hit));
    parts.push(
      <span key={hit + ":h" + cursor++} className="text-accent-500 font-medium">
        {text.slice(hit, hit + m.length)}
      </span>,
    );
    i = hit + m.length;
  }
  parts.push(text.slice(i));
  return <>{parts}</>;
}
