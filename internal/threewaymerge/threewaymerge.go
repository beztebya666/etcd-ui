// Package threewaymerge does line-based 3-way text merging — the kind
// `git merge` does on unrelated conflict-free changes. We use it for the
// "save while someone else also edited" path in the Browser key editor:
//
//   base   = value at the modRevision the user opened
//   ours   = the user's draft
//   theirs = the current server-side value
//
// If ours == base, nothing to merge (server wins trivially? — actually the
// user just opened and didn't change anything; caller decides).
// If theirs == base, nothing to merge (no concurrent edit happened).
// Otherwise we compute LCS between base/ours and base/theirs and weave the
// two change-sets together. Where the same base line was modified
// differently by both sides, we emit a conflict block.
//
// Why not import a CRDT (Y.js, Automerge)?
//
//   - etcd values are byte blobs, not sequences of operations. We have no
//     operational log to replay — only "value@rev=N", "value@rev=M".
//   - For human-edited config/JSON/yaml a 3-way merge feels right and is
//     deterministic. Concurrent edits to *different* lines auto-merge.
//   - Binary values bypass merge entirely (caller checks `looksBinary`).
//
// The algorithm is the well-known Myers diff + diff3 weave. ~150 lines,
// zero dependencies. Not optimised — the typical etcd value is < 4 KB.

package threewaymerge

import (
	"strings"
)

type Status int

const (
	// Clean merge: result reflects both changes, no human attention needed.
	StatusClean Status = iota
	// At least one conflict block. Result contains <<<<<<< markers.
	StatusConflict
	// Identical inputs (nothing happened). Result == ours == theirs.
	StatusNoChange
)

// Result of a merge attempt.
type Result struct {
	Status   Status
	Merged   string
	Conflicts int    // number of conflict blocks
}

// Merge runs a 3-way merge on three plain-text inputs. Lines are split on
// "\n". The result preserves original line endings (we re-join with "\n").
func Merge(base, ours, theirs string) Result {
	if ours == theirs {
		return Result{Status: StatusNoChange, Merged: ours}
	}
	if base == ours {
		// We didn't change anything; just take theirs.
		return Result{Status: StatusClean, Merged: theirs}
	}
	if base == theirs {
		// They didn't change anything; ours is the only update.
		return Result{Status: StatusClean, Merged: ours}
	}
	b := splitLines(base)
	o := splitLines(ours)
	t := splitLines(theirs)

	leftHunks := diff(b, o)
	rightHunks := diff(b, t)

	out := make([]string, 0, len(b)+8)
	conflicts := 0

	bi := 0
	li := 0
	ri := 0
	for bi <= len(b) {
		lH := nextHunk(leftHunks, li, bi)
		rH := nextHunk(rightHunks, ri, bi)
		switch {
		case lH == nil && rH == nil:
			if bi < len(b) {
				out = append(out, b[bi])
			}
			bi++
		case lH != nil && rH == nil:
			out = append(out, lH.replacement...)
			bi = lH.baseEnd
			li++
		case lH == nil && rH != nil:
			out = append(out, rH.replacement...)
			bi = rH.baseEnd
			ri++
		default:
			// Both sides touched a region that overlaps. If the touched
			// base ranges are identical AND the replacements are identical,
			// we can apply once. Otherwise it's a conflict.
			if lH.baseStart == rH.baseStart && lH.baseEnd == rH.baseEnd &&
				equalSlice(lH.replacement, rH.replacement) {
				out = append(out, lH.replacement...)
			} else {
				// Conflict block. We pick the *union* base range for clarity.
				lo := minInt(lH.baseStart, rH.baseStart)
				hi := maxInt(lH.baseEnd, rH.baseEnd)
				ourSlice := materialise(b, o, leftHunks, lo, hi)
				theirSlice := materialise(b, t, rightHunks, lo, hi)
				out = append(out, "<<<<<<< ours")
				out = append(out, ourSlice...)
				out = append(out, "=======")
				out = append(out, theirSlice...)
				out = append(out, ">>>>>>> theirs")
				conflicts++
				bi = hi
			}
			li++
			ri++
		}
	}

	merged := strings.Join(out, "\n")
	// Preserve a trailing newline only if at least one source had one.
	if (strings.HasSuffix(ours, "\n") || strings.HasSuffix(theirs, "\n")) &&
		!strings.HasSuffix(merged, "\n") {
		merged += "\n"
	}
	if conflicts > 0 {
		return Result{Status: StatusConflict, Merged: merged, Conflicts: conflicts}
	}
	return Result{Status: StatusClean, Merged: merged}
}

// --- internals ------------------------------------------------------------

type hunk struct {
	baseStart, baseEnd int
	replacement        []string
}

// splitLines on "\n" without keeping the separators. Empty input → empty
// slice (not [""]).
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.TrimRight(s, "\n") // avoid spurious trailing ""
	return strings.Split(s, "\n")
}

// diff computes hunks turning `a` into `b`. Each hunk says "replace
// a[baseStart:baseEnd] with replacement". Uses LCS via the simple
// O(N×M) DP — fine for KV values that aren't books.
func diff(a, b []string) []hunk {
	la, lb := len(a), len(b)
	if la == 0 && lb == 0 {
		return nil
	}
	// DP table of LCS lengths.
	dp := make([][]int, la+1)
	for i := range dp {
		dp[i] = make([]int, lb+1)
	}
	for i := 1; i <= la; i++ {
		for j := 1; j <= lb; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}
	// Walk back collecting edits.
	type edit struct {
		op      byte // '=' '-' '+'
		aPos    int
		bLine   string
	}
	var edits []edit
	i, j := la, lb
	for i > 0 || j > 0 {
		switch {
		case i > 0 && j > 0 && a[i-1] == b[j-1]:
			edits = append(edits, edit{op: '=', aPos: i - 1})
			i--
			j--
		case j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]):
			edits = append(edits, edit{op: '+', aPos: i, bLine: b[j-1]})
			j--
		default:
			edits = append(edits, edit{op: '-', aPos: i - 1})
			i--
		}
	}
	// Reverse.
	for l, r := 0, len(edits)-1; l < r; l, r = l+1, r-1 {
		edits[l], edits[r] = edits[r], edits[l]
	}
	// Group consecutive non-equal edits into hunks.
	var hunks []hunk
	var cur *hunk
	for _, e := range edits {
		switch e.op {
		case '=':
			if cur != nil {
				hunks = append(hunks, *cur)
				cur = nil
			}
		case '-':
			if cur == nil {
				cur = &hunk{baseStart: e.aPos, baseEnd: e.aPos + 1}
			} else if cur.baseEnd == e.aPos {
				cur.baseEnd++
			} else {
				hunks = append(hunks, *cur)
				cur = &hunk{baseStart: e.aPos, baseEnd: e.aPos + 1}
			}
		case '+':
			if cur == nil {
				cur = &hunk{baseStart: e.aPos, baseEnd: e.aPos}
			}
			cur.replacement = append(cur.replacement, e.bLine)
		}
	}
	if cur != nil {
		hunks = append(hunks, *cur)
	}
	return hunks
}

// nextHunk returns the hunk at index `i` if it starts at exactly `pos`,
// else nil. Hunks are sorted by baseStart.
func nextHunk(hs []hunk, i, pos int) *hunk {
	if i >= len(hs) {
		return nil
	}
	if hs[i].baseStart == pos {
		return &hs[i]
	}
	return nil
}

// materialise reconstructs side[lo..hi] (in base coordinates) from a
// combination of the hunk replacements and untouched base lines.
func materialise(base, side []string, hs []hunk, lo, hi int) []string {
	out := make([]string, 0, hi-lo)
	pos := lo
	for _, h := range hs {
		if h.baseEnd <= lo {
			continue
		}
		if h.baseStart >= hi {
			break
		}
		// untouched gap before this hunk
		for pos < h.baseStart && pos < hi {
			out = append(out, base[pos])
			pos++
		}
		out = append(out, h.replacement...)
		pos = h.baseEnd
	}
	for pos < hi {
		out = append(out, base[pos])
		pos++
	}
	// silence linter — side isn't actually used now; keep param for future
	_ = side
	return out
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func minInt(a, b int) int { if a < b { return a }; return b }
func maxInt(a, b int) int { if a > b { return a }; return b }
