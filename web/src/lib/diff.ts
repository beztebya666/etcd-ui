// Line-based diff using LCS — sufficient for inline value-version compare.
// Returns an array of segments {kind: same|add|del, text} for rendering.
//
// `lineDiff` works on raw strings; `smartDiff` pre-formats JSON / k8s
// objects so identical-on-one-line blobs (which is how etcd stores them)
// produce a useful line-by-line diff instead of "everything removed,
// everything added" — the latter being indistinguishable from a full
// rewrite and useless for code review.

export type DiffSeg = { kind: "same" | "add" | "del"; text: string };

// Try to pretty-print a JSON string. Returns the original on parse failure
// so binary / non-JSON inputs flow through unchanged.
export function prettyJSON(s: string): string {
  if (!s) return s;
  const t = s.trimStart();
  if (t[0] !== "{" && t[0] !== "[") return s;
  try {
    return JSON.stringify(JSON.parse(t), null, 2);
  } catch {
    return s;
  }
}

// Diff after pretty-printing both sides if both are JSON. The single-line
// JSON blobs stored by kube-apiserver round-trip to multi-line diffs, so
// each changed field shows as its own + / - line.
export function smartDiff(a: string, b: string): DiffSeg[] {
  const ap = prettyJSON(a);
  const bp = prettyJSON(b);
  return lineDiff(ap, bp);
}

// Group contiguous segments into hunks for compact rendering. Each hunk
// is a run of same/del/add lines. Caller can collapse long `same` runs
// to a "… 42 unchanged lines …" placeholder.
export type DiffHunk = {
  kind: "same" | "change";
  lines: DiffSeg[];
};

export function groupHunks(segs: DiffSeg[]): DiffHunk[] {
  const out: DiffHunk[] = [];
  for (const s of segs) {
    const kind: "same" | "change" = s.kind === "same" ? "same" : "change";
    if (out.length === 0 || out[out.length - 1].kind !== kind) {
      out.push({ kind, lines: [s] });
    } else {
      out[out.length - 1].lines.push(s);
    }
  }
  return out;
}

export function lineDiff(a: string, b: string): DiffSeg[] {
  const A = a.split("\n");
  const B = b.split("\n");
  const n = A.length;
  const m = B.length;
  // LCS DP — fine up to a few thousand lines, which is reasonable for an
  // etcd value. For really big payloads we degrade to full-replace.
  if (n * m > 1_000_000) {
    return [
      { kind: "del", text: a },
      { kind: "add", text: b },
    ];
  }
  const dp: number[][] = Array.from({ length: n + 1 }, () => new Array(m + 1).fill(0));
  for (let i = 1; i <= n; i++) {
    for (let j = 1; j <= m; j++) {
      dp[i][j] = A[i - 1] === B[j - 1] ? dp[i - 1][j - 1] + 1 : Math.max(dp[i - 1][j], dp[i][j - 1]);
    }
  }
  const out: DiffSeg[] = [];
  let i = n;
  let j = m;
  while (i > 0 && j > 0) {
    if (A[i - 1] === B[j - 1]) {
      out.push({ kind: "same", text: A[i - 1] });
      i--;
      j--;
    } else if (dp[i - 1][j] >= dp[i][j - 1]) {
      out.push({ kind: "del", text: A[i - 1] });
      i--;
    } else {
      out.push({ kind: "add", text: B[j - 1] });
      j--;
    }
  }
  while (i > 0) {
    out.push({ kind: "del", text: A[--i] });
  }
  while (j > 0) {
    out.push({ kind: "add", text: B[--j] });
  }
  return out.reverse();
}
