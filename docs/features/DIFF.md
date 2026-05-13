# Diff — compare two clusters

Pairwise comparison of two etcd clusters under an optional prefix. Useful for:

- **Promotion** — staging vs prod, "what's drifted?"
- **DR validation** — after restore, does the replica match?
- **Migration** — old cluster vs new during cutover.
- **Multi-region sanity** — A vs B vs C across geos.

## How it works

```text
GET /api/clusters/{left}/diff?against={right}&prefix=/foo
```

For each side we do a full range scan under the prefix (or whole keyspace if
empty) and build an in-memory map `key → value`. We then categorise:

| Kind         | Meaning                                       |
|--------------|-----------------------------------------------|
| `only-left`  | Present in `left`, absent in `right`          |
| `only-right` | Present in `right`, absent in `left`          |
| `different`  | Present in both with different values         |
| _(equal)_    | Present in both, identical — **hidden from response** |

Equal keys are intentionally dropped to keep payloads small — a 1M-key cluster
that matches its replica returns an empty diff, not 1M `equal` rows.

## Performance

The handler streams both sides serially with a 30-second hard timeout. For
clusters > ~500k keys consider:

- Restricting the prefix (`/registry/services` vs `/`)
- Running the diff service-side via `etcdctl get --prefix … | diff` on a
  member, then re-rendering the result here

## v2 clusters

Not supported. If either side speaks the v2 HTTP API, the handler returns
`501 Not Implemented` ("cluster diff is unavailable on etcd v2 clusters").

## UI

The page is two cluster pickers + a prefix field + filter pills. Each diff
entry shows both values side-by-side with `panel-2` background; binary values
are not specially highlighted here — use the Browser page for that.

### Filter pills

- **All** — show everything (default)
- **Only left** / **Only right** — set-difference views
- **Different** — keys with diverging values

## Limitations

- **Eventual consistency**: the two ranges aren't taken at the same revision.
  In a write-heavy cluster you can see "noise" diffs that disappear on retry.
  A future revision-pinned diff is on the roadmap.
- **No three-way diff** — only A vs B. For "what changed since snapshot X"
  use the History drawer on a single key.

## Common recipes

```text
# Has prod drifted from staging?
left:  prod
right: staging
prefix: (empty)
filter: All

# Is my restore complete?
left:  primary
right: dr-replica
prefix: /
filter: Different + Only left
```
