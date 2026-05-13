# Terminal — real `etcdctl` inside the UI

The Terminal page shells out to the actual `etcdctl` binary baked into the
Docker image. It is **not** a re-implementation — every flag, every output
format, every quirk is whatever `etcdctl v3.5.x` does on the command line.

## Why a terminal at all?

A handful of cluster ops have no good UI primitive: `move-leader`,
`endpoint hashkv`, `check perf`, `auth status`. Rather than build half a dozen
half-baked screens we expose the real CLI, with safety rails.

## How it works

```text
Browser ──▶ POST /api/clusters/:id/etcdctl
              { command: "member list -w table" }
                       │
                       ▼
           ops service shells out:
           etcdctl --endpoints=https://… --cacert=… --cert=… --key=…
                   --user=… member list -w table
                       │
                       ▼
           captures stdout, stderr, exitCode, durationMs
                       │
                       ▼
           returns JSON; UI renders preformatted
```

Connection flags (`--endpoints`, `--cacert`, `--cert`, `--key`, `--user`) are
**injected by the server** from the cluster's pool entry. The user types only
the subcommand. Direct `--endpoints` or `--cert` in the user input is
rejected.

## Security

`etcdctl` is **off by default**. Enable explicitly:

```bash
ETCD_UI_ETCDCTL=on
```

Without that env, every call returns HTTP 403.

Allowlist of accepted subcommands:

```
get put del txn member endpoint alarm lease auth user role
snapshot defrag compaction move-leader check version help
```

Anything else (shell, exec, bash, env, etc.) is rejected before exec.

Sensitive flags are scrubbed before display:

- `--password=…` → `--password=•••`
- `--user=user:password` → masked in copy-as-command

## Features

- **History** — last 50 commands per cluster, ↑/↓ to scroll.
- **Suggestions** — chips for `member list`, `endpoint status -w table`,
  `move-leader <hex>`, `alarm list`, `auth status`.
- **Inline helpers** — `help` shows the allowlist; `clear` empties the buffer
  without round-tripping the server.
- **Copy as command** — full reproduceable `etcdctl …` for paste into a shell
  on a member (password masked).

## What it's not

- **Not a shell** — no pipes, no `|`, no `&&`, no env vars.
- **Not interactive** — no prompts. `auth enable` non-interactively works
  because we inject `--user`.
- **Not an editor** — for big payloads use the Browser page, where Save/Delete
  flows through audit.

## Common recipes

```
# Cluster health
endpoint health -w table

# Find the leader
endpoint status -w table

# Move leader to a specific node (hex member id)
move-leader 8211f1d0f64f3269

# Per-node hashkv (for divergence troubleshooting)
endpoint hashkv -w table

# Disarm a NOSPACE alarm after compact + defrag
alarm disarm
```
