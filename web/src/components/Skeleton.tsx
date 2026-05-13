// Tiny skeleton primitives — opt-in for any page that wants nicer loading UX.

import type { CSSProperties } from "react";
import { cn } from "../lib/cn";

export function Skeleton({ className, style }: { className?: string; style?: CSSProperties }) {
  return (
    <div
      className={cn("animate-pulse rounded-md", className)}
      style={{ background: "rgb(var(--panel-2))", ...style }}
    />
  );
}

const PSEUDO_RANDOM = [78, 92, 55, 84, 70, 62, 88, 50, 95, 66];

export function SkeletonText({ lines = 3 }: { lines?: number }) {
  return (
    <div className="space-y-2">
      {Array.from({ length: lines }).map((_, i) => (
        <Skeleton key={i} className="h-3" style={{ width: `${PSEUDO_RANDOM[i % PSEUDO_RANDOM.length]}%` }} />
      ))}
    </div>
  );
}

export function SkeletonTable({ rows = 6, cols = 4 }: { rows?: number; cols?: number }) {
  return (
    <div className="space-y-2">
      {Array.from({ length: rows }).map((_, r) => (
        <div key={r} className="grid gap-3" style={{ gridTemplateColumns: `repeat(${cols}, 1fr)` }}>
          {Array.from({ length: cols }).map((_, c) => (
            <Skeleton key={c} className="h-4" />
          ))}
        </div>
      ))}
    </div>
  );
}

export function SkeletonCard({ rows = 3 }: { rows?: number }) {
  return (
    <div className="panel p-5">
      <Skeleton className="h-4 w-1/3" />
      <div className="mt-3 space-y-2">
        {Array.from({ length: rows }).map((_, i) => (
          <Skeleton key={i} className="h-3" style={{ width: `${PSEUDO_RANDOM[i % PSEUDO_RANDOM.length]}%` }} />
        ))}
      </div>
    </div>
  );
}
