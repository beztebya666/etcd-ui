# 3-way merge for concurrent edits

When two operators (or two tabs from the same operator) edit the same key in
the etcd-ui browser, the second `Save` would normally overwrite the first
silently. The CRDT-lite layer makes that impossible: every save goes through
a compare-and-set with a 3-way line-level merge fallback.

## What you see

- Live banner above the editor when somebody else's write advanced the
  modRevision while your draft was open. `Reload` discards your draft and
  loads the new server value.
- "Saved with auto-merge" toast when your changes and theirs touched
  different lines — both edits are preserved.
- A 3-pane merge resolver modal when your changes and theirs touched the
  same lines. You see Base / Yours / Theirs side-by-side, pick a side per
  conflict block (or "both"), and commit.

## How it works

1. When you select a key, the editor snapshots `modRevision` as your
   `baseRev`.
2. `Save` calls `POST /api/clusters/{id}/put-cas` with `{key, value, baseRev}`.
3. The kv service runs:
   - `cli.Get(key)` — fetches current revision and live value.
   - If `live.ModRevision == baseRev`, atomic txn:
     `If ModRevision(key) == baseRev Then Put(key, value)`. No contention,
     no merge needed.
   - Otherwise, fetch the historical value at `baseRev` via
     `clientv3.WithRev(baseRev)` (gracefully treats live as base if the
     history was compacted).
   - Run the line-based 3-way merge in `internal/threewaymerge`:
     - LCS of `(base, ours)` and `(base, theirs)` → diff hunks.
     - Diff3 weave applies non-overlapping hunks cleanly.
     - Overlapping hunks emit `<<<<<<< ours / ======= / >>>>>>> theirs`
       conflict markers.
   - **Clean merge** — atomic txn applies the merged value at the current
     revision. Returns `{status:"merged"}`.
   - **Conflict** — server returns `409` with `{status:"conflict", base,
     theirs, merged, conflicts}`. The UI opens the resolver. After the
     user picks sides, the resubmit carries `acceptConflicts:true` so any
     remaining markers go through verbatim if they really want them.
4. The whole flow retries up to 3 times if the race re-occurs between
   read and atomic write.

## Limits

- Line granularity. Two edits inside the same line conflict even if they
  edit different words.
- Binary values fall through unchanged — the merge is a no-op when neither
  ours nor theirs touches lines (rare in practice for binary blobs since
  one byte changes the whole "line").
- No operational-transform log — this is stateless 3-way reconciliation,
  not a full CRDT. State lives entirely in etcd's MVCC history.

## Disabling

Plain `POST /api/clusters/{id}/put` still exists and bypasses the merge
entirely (last-write-wins). Use this for bulk imports or scripted writes
where you explicitly want overwrite semantics.
