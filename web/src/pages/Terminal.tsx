import { useEffect, useRef, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { api } from "../lib/api";
import { useStore } from "../lib/store";
import { Terminal as TermIcon, Play, Trash2, Copy, ChevronRight } from "lucide-react";
import { toast } from "../components/Toast";
import { copyToClipboard } from "../lib/clipboard";
import { cn } from "../lib/cn";

type Entry = {
  id: number;
  cmd: string;
  args: string[];
  stdout: string;
  stderr: string;
  exitCode: number;
  durationMs: number;
  command: string[];
  pending: boolean;
};

let nextId = 1;

// Allowed subcommands kept in sync with cmd/ops/etcdctl.go for the inline help.
const HELP = [
  ["get <key> [--prefix]", "read a key or range"],
  ["put <key> <value> [--lease=ID]", "write a value (creates lease binding if given)"],
  ["del <key> [--prefix]", "delete a key or range"],
  ["txn -i", "interactive transaction (best in a real shell)"],
  ["member list / add / remove / promote", "cluster membership"],
  ["endpoint health / status / hashkv", "per-endpoint state"],
  ["move-leader <hex-member-id>", "transfer leadership without restart"],
  ["alarm list / disarm", "NOSPACE / CORRUPT alarms"],
  ["lease grant <ttl> / revoke / timetolive / list", "lease management"],
  ["auth enable / disable / status", "RBAC toggle"],
  ["user add / del / list / get / passwd / grant-role / revoke-role", "users"],
  ["role add / del / list / get / grant-permission / revoke-permission", "roles"],
  ["snapshot save <file>", "produces an .db on the etcd-ui pod's /app/data"],
  ["defrag", "reclaim space on every endpoint"],
  ["compaction <rev>", "compact key history"],
  ["check perf / datascale", "self-tests"],
  ["version", "client + server version"],
];

const SUGGESTIONS = [
  "member list",
  "endpoint status -w table",
  "endpoint health",
  "alarm list",
  "auth status",
  "lease list",
  "move-leader <hex-member-id>",
  "snapshot save /app/data/snapshots/manual.db",
];

export function TerminalPage() {
  const cluster = useStore((s) => s.selectedCluster);
  const [input, setInput] = useState("");
  const [entries, setEntries] = useState<Entry[]>([]);
  const [historyIdx, setHistoryIdx] = useState<number>(-1);
  const scrollRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  const run = useMutation({
    mutationFn: async (args: string[]) => api.etcdctl(cluster!, args),
  });

  // auto-scroll to bottom when a new entry lands
  useEffect(() => {
    scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight, behavior: "smooth" });
  }, [entries]);

  // focus input on mount + when clicking the empty pane
  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  if (!cluster) return <div className="panel p-10 text-center muted">Pick a cluster first.</div>;

  const submit = async (raw: string) => {
    const cmd = raw.trim();
    if (!cmd) return;
    const args = tokenize(cmd);
    const id = nextId++;
    const entry: Entry = {
      id,
      cmd,
      args,
      stdout: "",
      stderr: "",
      exitCode: 0,
      durationMs: 0,
      command: [],
      pending: true,
    };
    setEntries((prev) => [...prev, entry]);
    setInput("");
    setHistoryIdx(-1);
    try {
      // Local shortcuts that don't hit the server.
      if (cmd === "help" || cmd === "?") {
        finish(id, {
          stdout: helpText(),
          stderr: "",
          exitCode: 0,
          durationMs: 0,
          command: ["help"],
        });
        return;
      }
      if (cmd === "clear" || cmd === "cls") {
        setEntries([]);
        return;
      }
      const res = await run.mutateAsync(args);
      finish(id, res);
    } catch (e: any) {
      finish(id, {
        stdout: "",
        stderr: e?.message ?? String(e),
        exitCode: -1,
        durationMs: 0,
        command: ["(error)"],
      });
    }
  };

  const finish = (id: number, res: Omit<Entry, "id" | "cmd" | "args" | "pending">) => {
    setEntries((prev) =>
      prev.map((e) => (e.id === id ? { ...e, ...res, pending: false } : e)),
    );
  };

  const history = entries.map((e) => e.cmd);

  return (
    <div className="space-y-4">
      <div className="flex items-end justify-between gap-3 flex-wrap">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight flex items-center gap-2">
            <TermIcon className="w-5 h-5 text-accent-500" />
            etcdctl
          </h1>
          <p className="muted mt-1 max-w-[820px]">
            Direct shell to the upstream <code>etcdctl</code> binary against the selected cluster.
            Endpoints, TLS certs and credentials are injected automatically — you type only the
            subcommand and args. <span className="text-warn">Read/write capable</span> — every
            command is recorded in the Audit log.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <button onClick={() => setEntries([])} className="btn" title="Clear scrollback">
            <Trash2 className="w-4 h-4" /> Clear
          </button>
        </div>
      </div>

      <div
        className="panel p-0 overflow-hidden"
        onClick={(e) => {
          // click anywhere on the terminal area → focus input
          if (e.target === e.currentTarget) inputRef.current?.focus();
        }}
      >
        {/* suggestion strip */}
        <div
          className="flex items-center gap-1.5 px-3 py-2 border-b overflow-x-auto"
          style={{ borderColor: "rgb(var(--line))" }}
        >
          <span className="text-[10px] uppercase tracking-wider muted shrink-0 mr-1">try</span>
          {SUGGESTIONS.map((s) => (
            <button
              key={s}
              onClick={() => {
                setInput(s);
                inputRef.current?.focus();
              }}
              className="pill soft-hover font-mono text-[11px] whitespace-nowrap"
            >
              {s}
            </button>
          ))}
        </div>

        {/* scrollback */}
        <div
          ref={scrollRef}
          className="font-mono text-[12.5px] leading-relaxed max-h-[calc(100vh-360px)] overflow-y-auto p-4 space-y-3"
        >
          {entries.length === 0 && (
            <div className="muted text-sm">
              Type <span className="kbd">help</span> for inline cheatsheet,{" "}
              <span className="kbd">↑ / ↓</span> to recall, <span className="kbd">Enter</span> to
              run. Output is captured for 25 s max.
            </div>
          )}
          {entries.map((e) => (
            <EntryView key={e.id} entry={e} />
          ))}
        </div>

        {/* prompt */}
        <form
          onSubmit={(ev) => {
            ev.preventDefault();
            submit(input);
          }}
          className="flex items-center gap-2 px-3 h-12 border-t"
          style={{ borderColor: "rgb(var(--line))" }}
        >
          <ChevronRight className="w-4 h-4 text-accent-500 shrink-0" />
          <span className="font-mono text-xs muted shrink-0 select-none">etcdctl</span>
          <input
            ref={inputRef}
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "ArrowUp") {
                e.preventDefault();
                const next = Math.min(historyIdx + 1, history.length - 1);
                if (history.length > 0) {
                  setHistoryIdx(next);
                  setInput(history[history.length - 1 - next] ?? "");
                }
              } else if (e.key === "ArrowDown") {
                e.preventDefault();
                const next = historyIdx - 1;
                if (next < 0) {
                  setHistoryIdx(-1);
                  setInput("");
                } else {
                  setHistoryIdx(next);
                  setInput(history[history.length - 1 - next] ?? "");
                }
              }
            }}
            placeholder="member list   /   move-leader <hex-id>   /   help"
            className="flex-1 bg-transparent outline-none text-sm font-mono"
            spellCheck={false}
            autoComplete="off"
            disabled={run.isPending}
          />
          <button type="submit" className="btn btn-primary !h-8" disabled={run.isPending}>
            <Play className="w-3.5 h-3.5" /> Run
          </button>
        </form>
      </div>
    </div>
  );
}

function EntryView({ entry }: { entry: Entry }) {
  const failed = !entry.pending && entry.exitCode !== 0;
  return (
    <div>
      <div className="flex items-center gap-2">
        <span className="muted">$</span>
        <span className="text-current">etcdctl {entry.cmd}</span>
        {!entry.pending && (
          <span className={cn("ml-auto text-[10px]", failed ? "text-danger" : "muted")}>
            exit {entry.exitCode} · {entry.durationMs} ms
          </span>
        )}
        {!entry.pending && (
          <button
            onClick={async () => {
              if (await copyToClipboard(entry.command.join(" "))) toast.success("Full command copied");
              else toast.error("Couldn't copy — select and ⌘C manually");
            }}
            className="btn btn-ghost !h-6 !px-1.5"
            title="Copy full etcdctl command with injected flags"
          >
            <Copy className="w-3 h-3" />
          </button>
        )}
      </div>
      {entry.pending ? (
        <div className="muted text-xs mt-1">running…</div>
      ) : (
        <>
          {entry.stdout && (
            <pre className="mt-1 panel-2 p-3 rounded-md whitespace-pre-wrap break-all overflow-x-auto">
              {entry.stdout}
            </pre>
          )}
          {entry.stderr && (
            <pre className="mt-1 panel-2 p-3 rounded-md whitespace-pre-wrap break-all overflow-x-auto text-danger">
              {entry.stderr}
            </pre>
          )}
          {!entry.stdout && !entry.stderr && entry.exitCode === 0 && (
            <div className="muted text-xs mt-1">(no output)</div>
          )}
        </>
      )}
    </div>
  );
}

// Naive token splitter — supports double/single quoted args, no escape sequences.
function tokenize(s: string): string[] {
  const out: string[] = [];
  let buf = "";
  let quote: '"' | "'" | null = null;
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (quote) {
      if (c === quote) {
        quote = null;
        continue;
      }
      buf += c;
      continue;
    }
    if (c === '"' || c === "'") {
      quote = c;
      continue;
    }
    if (c === " ") {
      if (buf) {
        out.push(buf);
        buf = "";
      }
      continue;
    }
    buf += c;
  }
  if (buf) out.push(buf);
  return out;
}

function helpText(): string {
  let max = 0;
  for (const [k] of HELP) if (k.length > max) max = k.length;
  return HELP.map(([k, v]) => `  ${k.padEnd(max + 2)}${v}`).join("\n") +
    "\n\nNotes:\n" +
    "  · `etcdctl move-leader <hex>` transfers leadership without killing a member.\n" +
    "  · Watch is NOT exposed here — use the Watch page (it streams via SSE).\n" +
    "  · Operator must set ETCD_UI_ETCDCTL=on for this terminal to work.\n";
}
