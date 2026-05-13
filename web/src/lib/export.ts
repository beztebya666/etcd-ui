// Multi-format export for the Browser page. All formats are produced on the
// client to keep the backend simple and the network payload tiny.

import type { KV } from "./api";

export type ExportFormat = "json" | "yaml" | "env" | "toml";

export function exportFile(kvs: { key: string; value: string }[], cluster: string, format: ExportFormat) {
  const body = format === "json" ? toJSON(kvs, cluster) :
               format === "yaml" ? toYAML(kvs) :
               format === "env"  ? toEnv(kvs) :
                                   toTOML(kvs);
  const blob = new Blob([body], {
    type: format === "json" ? "application/json" : "text/plain;charset=utf-8",
  });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = `etcd-${cluster}-${new Date().toISOString().replace(/[:.]/g, "-")}.${format}`;
  a.click();
  URL.revokeObjectURL(url);
}

function toJSON(kvs: { key: string; value: string }[], cluster: string): string {
  return JSON.stringify({ cluster, exportedAt: new Date().toISOString(), kvs }, null, 2);
}

function toYAML(kvs: { key: string; value: string }[]): string {
  // Flat map only — enough for round-tripping in CI scripts, doesn't try to
  // nest by '/' separators since etcd keys aren't real paths.
  const lines = ["# etcd-ui export — flat key/value map", "kvs:"];
  for (const { key, value } of kvs) {
    lines.push(`  - key: ${yamlScalar(key)}`);
    lines.push(`    value: ${yamlScalar(value)}`);
  }
  return lines.join("\n") + "\n";
}

function toEnv(kvs: { key: string; value: string }[]): string {
  // Convert /foo/bar-baz → FOO_BAR_BAZ. Best effort; comment out illegal keys.
  return kvs
    .map(({ key, value }) => {
      const envName = key
        .replace(/^\/+/, "")
        .replace(/[\/.\-]+/g, "_")
        .toUpperCase();
      if (!/^[A-Z_][A-Z0-9_]*$/.test(envName)) {
        return `# skipped: ${key}`;
      }
      return `${envName}=${envQuote(value)}`;
    })
    .join("\n") + "\n";
}

function toTOML(kvs: { key: string; value: string }[]): string {
  return ["# etcd-ui export", ""]
    .concat(
      kvs.map(({ key, value }) =>
        `[[kvs]]\nkey = ${tomlString(key)}\nvalue = ${tomlString(value)}\n`,
      ),
    )
    .join("\n");
}

function yamlScalar(s: string): string {
  if (s === "") return '""';
  // quote if contains special chars or starts/ends with whitespace
  if (/[:#\-\[\]{}&*!|>'"%@`?\n]/.test(s) || /^\s|\s$/.test(s)) {
    return JSON.stringify(s); // double-quoted YAML scalar is also valid JSON
  }
  return s;
}

function envQuote(s: string): string {
  if (/[\s"'\\$]/.test(s)) return JSON.stringify(s);
  return s;
}

function tomlString(s: string): string {
  // basic strings only; escape backslashes and quotes
  return '"' + s.replace(/\\/g, "\\\\").replace(/"/g, '\\"').replace(/\n/g, "\\n") + '"';
}

// Re-export type for callers that want it.
export type _KV = KV;
