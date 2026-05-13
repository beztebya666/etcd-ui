// Custom checkbox. Three reasons we don't use <input type="checkbox">:
//
//   1. Native styling is wildly inconsistent across browsers/themes — a
//      Chromium checkbox on light theme looks nothing like the same one
//      on macOS Safari, and neither matches our pill/tag palette.
//   2. Indeterminate ("some selected") needs custom rendering anyway.
//   3. We want the same component on a <button> (toggle-all) AND inside
//      a list row, with a clear hit area that doesn't depend on label.
//
// Visual: 14px rounded square, panel-2 background when off, accent fill +
// white check when on, dashed border when indeterminate. Focus ring uses
// the global :focus-visible rule via tabIndex/role.

import type { CSSProperties } from "react";
import { Check, Minus } from "lucide-react";
import { cn } from "../lib/cn";

export type CheckboxState = "on" | "off" | "indeterminate";

export function Checkbox({
  state,
  size = 14,
  className,
  style,
}: {
  state: CheckboxState;
  size?: number;
  className?: string;
  style?: CSSProperties;
}) {
  const on = state === "on";
  const ind = state === "indeterminate";
  return (
    <span
      role="presentation"
      data-checkbox=""
      style={{
        width: size,
        height: size,
        // The off-state needed two fixes: the background was `--panel-2`
        // which is identical-ish to `soft-hover` over `--panel`, so the
        // checkbox vanished into the hover bg of a list row. We now use
        // the darker `--panel` for the fill AND bump the border to
        // ~50% foreground so it stays visible on either bg. The
        // group-hover override in the row pushes it further to 80%.
        borderColor: on
          ? "rgb(var(--accent))"
          : "color-mix(in srgb, rgb(var(--fg)) 35%, transparent)",
        background: on ? "rgb(var(--accent))" : "rgb(var(--panel))",
        ...style,
      }}
      className={cn(
        "inline-flex items-center justify-center rounded-[4px] border-2 transition-colors shrink-0",
        ind && "border-dashed",
        className,
      )}
      aria-hidden="true"
    >
      {on && <Check className="w-3 h-3 text-white" strokeWidth={3} />}
      {ind && <Minus className="w-3 h-3 text-accent-500" strokeWidth={3} />}
    </span>
  );
}
