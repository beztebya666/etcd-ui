import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { api, type TxnCondition, type TxnOp, type TxnResult } from "../lib/api";
import { useStore } from "../lib/store";
import { Plus, Workflow, Trash2, CheckCircle2, XCircle } from "lucide-react";
import { Dropdown } from "../components/Dropdown";

export function TxnPage() {
  const cluster = useStore((s) => s.selectedCluster);
  const [conds, setConds] = useState<TxnCondition[]>([
    { key: "/example/key", field: "value", op: "equal", target: "" },
  ]);
  const [success, setSuccess] = useState<TxnOp[]>([{ type: "put", key: "/example/key", value: "" }]);
  const [failure, setFailure] = useState<TxnOp[]>([]);

  const run = useMutation<TxnResult, Error, void>({
    mutationFn: () =>
      api.txn(cluster!, { conditions: conds, onSuccess: success, onFailure: failure }),
  });

  if (!cluster) return <div className="panel p-10 text-center muted">Pick a cluster first.</div>;

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight flex items-center gap-2">
          <Workflow className="w-5 h-5 text-accent-500" /> Transactions
        </h1>
        <p className="muted mt-1">
          Build a conditional etcd transaction visually. <em>If</em> all conditions hold, <em>then</em> ops run;
          otherwise the <em>else</em> branch runs.
        </p>
      </div>

      <Section title="If" subtitle="all of these must be true">
        {conds.map((c, i) => (
          <CondRow
            key={i}
            value={c}
            onChange={(v) => setConds(conds.map((x, j) => (j === i ? v : x)))}
            onRemove={() => setConds(conds.filter((_, j) => j !== i))}
          />
        ))}
        <button
          onClick={() => setConds([...conds, { key: "", field: "value", op: "equal", target: "" }])}
          className="btn"
        >
          <Plus className="w-4 h-4" /> Add condition
        </button>
      </Section>

      <Section title="Then" subtitle="run these on success">
        {success.map((op, i) => (
          <OpRow
            key={i}
            value={op}
            onChange={(v) => setSuccess(success.map((x, j) => (j === i ? v : x)))}
            onRemove={() => setSuccess(success.filter((_, j) => j !== i))}
          />
        ))}
        <div className="flex gap-2">
          <button onClick={() => setSuccess([...success, { type: "put", key: "", value: "" }])} className="btn">
            <Plus className="w-4 h-4" /> Put
          </button>
          <button onClick={() => setSuccess([...success, { type: "delete", key: "" }])} className="btn">
            <Plus className="w-4 h-4" /> Delete
          </button>
          <button onClick={() => setSuccess([...success, { type: "get", key: "" }])} className="btn">
            <Plus className="w-4 h-4" /> Get
          </button>
        </div>
      </Section>

      <Section title="Else" subtitle="run these on failure">
        {failure.map((op, i) => (
          <OpRow
            key={i}
            value={op}
            onChange={(v) => setFailure(failure.map((x, j) => (j === i ? v : x)))}
            onRemove={() => setFailure(failure.filter((_, j) => j !== i))}
          />
        ))}
        <button onClick={() => setFailure([...failure, { type: "put", key: "", value: "" }])} className="btn">
          <Plus className="w-4 h-4" /> Add op
        </button>
      </Section>

      <div className="flex items-center gap-3">
        <button onClick={() => run.mutate()} className="btn btn-primary" disabled={run.isPending}>
          {run.isPending ? "Committing…" : "Commit transaction"}
        </button>
        {run.data && (
          <span className={"tag " + (run.data.succeeded ? "tag-success" : "tag-warn")}>
            {run.data.succeeded ? (
              <>
                <CheckCircle2 className="w-4 h-4" /> succeeded · rev {run.data.revision}
              </>
            ) : (
              <>
                <XCircle className="w-4 h-4" /> else branch ran · rev {run.data.revision}
              </>
            )}
          </span>
        )}
        {run.error && <span className="text-danger text-sm">{run.error.message}</span>}
      </div>
    </div>
  );
}

function Section({ title, subtitle, children }: { title: string; subtitle: string; children: React.ReactNode }) {
  return (
    <section className="panel p-5">
      <div className="flex items-baseline gap-2">
        <div className="text-sm font-medium">{title}</div>
        <div className="muted text-xs">{subtitle}</div>
      </div>
      <div className="mt-3 space-y-2">{children}</div>
    </section>
  );
}

function CondRow({
  value,
  onChange,
  onRemove,
}: {
  value: TxnCondition;
  onChange: (v: TxnCondition) => void;
  onRemove: () => void;
}) {
  return (
    <div className="grid grid-cols-12 gap-2 items-center">
      <input
        className="col-span-4 h-10 px-3 rounded-lg text-sm font-mono"
        style={{ background: "rgb(var(--panel-2))", border: "1px solid rgb(var(--line))" }}
        placeholder="key"
        value={value.key}
        onChange={(e) => onChange({ ...value, key: e.target.value })}
      />
      <Dropdown<TxnCondition["field"]>
        value={value.field}
        onChange={(v) => onChange({ ...value, field: v })}
        className="col-span-2"
        buttonClassName="w-full h-10 justify-between !text-sm"
        ariaLabel="condition field"
        items={[
          { value: "value", label: "value" },
          { value: "createRevision", label: "createRev" },
          { value: "modRevision", label: "modRev" },
          { value: "version", label: "version" },
        ]}
      />
      <Dropdown<TxnCondition["op"]>
        value={value.op}
        onChange={(v) => onChange({ ...value, op: v })}
        className="col-span-2"
        buttonClassName="w-full h-10 justify-between !text-sm"
        ariaLabel="condition operator"
        items={[
          { value: "equal", label: "=" },
          { value: "not-equal", label: "≠" },
          { value: "greater", label: ">" },
          { value: "less", label: "<" },
        ]}
      />
      <input
        className="col-span-3 h-10 px-3 rounded-lg text-sm font-mono"
        style={{ background: "rgb(var(--panel-2))", border: "1px solid rgb(var(--line))" }}
        placeholder="target"
        value={value.target}
        onChange={(e) => onChange({ ...value, target: e.target.value })}
      />
      <button onClick={onRemove} className="btn btn-ghost text-danger col-span-1">
        <Trash2 className="w-4 h-4" />
      </button>
    </div>
  );
}

function OpRow({
  value,
  onChange,
  onRemove,
}: {
  value: TxnOp;
  onChange: (v: TxnOp) => void;
  onRemove: () => void;
}) {
  return (
    <div className="grid grid-cols-12 gap-2 items-center">
      <Dropdown<"put" | "delete" | "get">
        value={value.type}
        onChange={(v) =>
          onChange(
            v === "put"
              ? { type: "put", key: value.key, value: "" }
              : v === "delete"
                ? { type: "delete", key: value.key }
                : { type: "get", key: value.key },
          )
        }
        className="col-span-2"
        buttonClassName="w-full h-10 justify-between !text-sm"
        ariaLabel="operation type"
        items={[
          { value: "put", label: "put" },
          { value: "delete", label: "delete" },
          { value: "get", label: "get" },
        ]}
      />
      <input
        className="col-span-4 h-10 px-3 rounded-lg text-sm font-mono"
        style={{ background: "rgb(var(--panel-2))", border: "1px solid rgb(var(--line))" }}
        placeholder="key"
        value={value.key}
        onChange={(e) => onChange({ ...value, key: e.target.value } as TxnOp)}
      />
      {value.type === "put" ? (
        <input
          className="col-span-5 h-10 px-3 rounded-lg text-sm font-mono"
          style={{ background: "rgb(var(--panel-2))", border: "1px solid rgb(var(--line))" }}
          placeholder="value"
          value={value.value}
          onChange={(e) => onChange({ ...value, value: e.target.value })}
        />
      ) : (
        <label className="col-span-5 flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={!!(value as Extract<TxnOp, { type: "delete" | "get" }>).prefix}
            onChange={(e) => onChange({ ...value, prefix: e.target.checked } as TxnOp)}
          />
          prefix
        </label>
      )}
      <button onClick={onRemove} className="btn btn-ghost text-danger col-span-1">
        <Trash2 className="w-4 h-4" />
      </button>
    </div>
  );
}
