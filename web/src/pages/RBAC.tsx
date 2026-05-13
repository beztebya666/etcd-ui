import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, type RBACPermission } from "../lib/api";
import { useStore } from "../lib/store";
import { Plus, Trash2, Shield, ShieldOff, UserPlus, KeyRound, Lock, Users as UsersIcon } from "lucide-react";
import { SkeletonCard } from "../components/Skeleton";
import { Dropdown } from "../components/Dropdown";

export function RBACPage() {
  const cluster = useStore((s) => s.selectedCluster);
  const qc = useQueryClient();

  const status = useQuery({
    queryKey: ["rbac-status", cluster],
    enabled: !!cluster,
    queryFn: () => api.rbacStatus(cluster!),
  });
  const users = useQuery({
    queryKey: ["rbac-users", cluster],
    enabled: !!cluster,
    queryFn: () => api.rbacUsers(cluster!),
  });
  const roles = useQuery({
    queryKey: ["rbac-roles", cluster],
    enabled: !!cluster,
    queryFn: () => api.rbacRoles(cluster!),
  });

  const enable = useMutation({
    mutationFn: () => api.rbacEnable(cluster!),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["rbac-status"] }),
  });
  const disable = useMutation({
    mutationFn: () => api.rbacDisable(cluster!),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["rbac-status"] }),
  });

  const addUser = useMutation({
    mutationFn: (b: { name: string; password: string }) => api.rbacAddUser(cluster!, b),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["rbac-users"] }),
  });
  const delUser = useMutation({
    mutationFn: (name: string) => api.rbacDeleteUser(cluster!, name),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["rbac-users"] }),
  });
  const grantRole = useMutation({
    mutationFn: ({ user, role }: { user: string; role: string }) => api.rbacGrantRole(cluster!, user, role),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["rbac-users"] }),
  });

  const addRole = useMutation({
    mutationFn: (name: string) => api.rbacAddRole(cluster!, { name }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["rbac-roles"] }),
  });
  const delRole = useMutation({
    mutationFn: (name: string) => api.rbacDeleteRole(cluster!, name),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["rbac-roles"] }),
  });
  const grantPerm = useMutation({
    mutationFn: ({ role, perm }: { role: string; perm: RBACPermission }) =>
      api.rbacGrantPermission(cluster!, role, perm),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["rbac-roles"] }),
  });

  const [newUser, setNewUser] = useState({ name: "", password: "" });
  const [newRole, setNewRole] = useState("");
  const [newPerm, setNewPerm] = useState<{ role: string; perm: RBACPermission }>({
    role: "",
    perm: { type: "readwrite", key: "/", prefix: true },
  });

  if (!cluster) return <div className="panel p-10 text-center muted">Pick a cluster first.</div>;

  // First-load skeleton until status/users/roles all settle.
  const initialLoading =
    (status.isLoading && !status.data) ||
    (users.isLoading && !users.data) ||
    (roles.isLoading && !roles.data);
  if (initialLoading) {
    return (
      <div className="space-y-5">
        <div className="flex items-end justify-between gap-3 flex-wrap">
          <div>
            <h1 className="text-2xl font-semibold tracking-tight">Users &amp; Roles</h1>
            <p className="muted mt-1">Manage etcd authentication and permissions.</p>
          </div>
        </div>
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
          <SkeletonCard rows={6} />
          <SkeletonCard rows={6} />
        </div>
      </div>
    );
  }

  if (status.data && !status.data.enabled && (users.data?.length ?? 0) === 0 && (roles.data?.length ?? 0) === 0) {
    return (
      <div className="space-y-5">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Users &amp; Roles</h1>
          <p className="muted mt-1">Manage etcd authentication and permissions.</p>
        </div>
        <div className="panel p-12 text-center">
          <UsersIcon className="w-10 h-10 mx-auto mb-3 muted" />
          <div className="font-medium">Auth is disabled on this cluster</div>
          <p className="muted text-sm mt-2 max-w-md mx-auto">
            etcd RBAC needs to be enabled before users and roles can be added.
            Start by enabling — this creates a <code className="kbd">root</code> user
            you'll need to authenticate with afterwards.
          </p>
          <button onClick={() => enable.mutate()} className="btn btn-primary mt-5" disabled={enable.isPending}>
            <Shield className="w-4 h-4" /> {enable.isPending ? "Enabling…" : "Enable RBAC"}
          </button>
          {enable.error && (
            <div className="mt-3 text-sm text-danger">{(enable.error as Error).message}</div>
          )}
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <div className="flex items-end justify-between gap-3 flex-wrap">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Users &amp; Roles</h1>
          <p className="muted mt-1">Manage etcd authentication and permissions.</p>
        </div>
        <div className="flex items-center gap-2">
          <span className={"tag " + (status.data?.enabled ? "tag-success" : "tag-warn")}>
            {status.data?.enabled ? (
              <>
                <Shield className="w-4 h-4" /> auth enabled
              </>
            ) : (
              <>
                <ShieldOff className="w-4 h-4" /> auth disabled
              </>
            )}
          </span>
          {status.data?.enabled ? (
            <button onClick={() => disable.mutate()} className="btn">
              <ShieldOff className="w-4 h-4" /> Disable
            </button>
          ) : (
            <button onClick={() => enable.mutate()} className="btn btn-primary">
              <Shield className="w-4 h-4" /> Enable auth
            </button>
          )}
        </div>
      </div>

      <section className="panel p-5">
        <div className="text-sm font-medium mb-3 flex items-center gap-2">
          <UserPlus className="w-4 h-4 text-accent-500" /> Users
        </div>
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="text-left muted text-[11px] uppercase tracking-wider">
              <tr>
                <th className="py-2 pr-4">Name</th>
                <th className="py-2 pr-4">Roles</th>
                <th className="py-2 pr-4 text-right">Action</th>
              </tr>
            </thead>
            <tbody className="divide-y" style={{ borderColor: "rgb(var(--line))" }}>
              {users.data?.map((u) => (
                <tr key={u.name}>
                  <td className="py-2 pr-4 font-medium">{u.name}</td>
                  <td className="py-2 pr-4">
                    <div className="flex flex-wrap gap-1.5">
                      {(u.roles ?? []).map((r) => (
                        <span key={r} className="pill">{r}</span>
                      ))}
                      <GrantRoleButton
                        roles={(roles.data ?? []).map((r) => r.name)}
                        existing={u.roles ?? []}
                        onGrant={(r) => grantRole.mutate({ user: u.name, role: r })}
                      />
                    </div>
                  </td>
                  <td className="py-2 pr-4 text-right">
                    <button onClick={() => delUser.mutate(u.name)} className="btn btn-ghost text-danger">
                      <Trash2 className="w-4 h-4" />
                    </button>
                  </td>
                </tr>
              ))}
              {!users.data?.length && (
                <tr><td colSpan={3} className="py-4 muted text-sm">No users yet.</td></tr>
              )}
            </tbody>
          </table>
        </div>
        <div className="mt-4 grid grid-cols-1 sm:grid-cols-3 gap-2">
          <input
            placeholder="username"
            value={newUser.name}
            onChange={(e) => setNewUser({ ...newUser, name: e.target.value })}
            className="h-10 px-3 rounded-lg text-sm"
            style={{ background: "rgb(var(--panel-2))", border: "1px solid rgb(var(--line))" }}
          />
          <input
            placeholder="password"
            type="password"
            value={newUser.password}
            onChange={(e) => setNewUser({ ...newUser, password: e.target.value })}
            className="h-10 px-3 rounded-lg text-sm"
            style={{ background: "rgb(var(--panel-2))", border: "1px solid rgb(var(--line))" }}
          />
          <button
            disabled={!newUser.name || !newUser.password || addUser.isPending}
            onClick={() => addUser.mutate(newUser)}
            className="btn btn-primary"
          >
            <Plus className="w-4 h-4" /> Add user
          </button>
        </div>
      </section>

      <section className="panel p-5">
        <div className="text-sm font-medium mb-3 flex items-center gap-2">
          <KeyRound className="w-4 h-4 text-accent-500" /> Roles
        </div>
        <ul className="divide-y" style={{ borderColor: "rgb(var(--line))" }}>
          {roles.data?.map((r) => (
            <li key={r.name} className="py-3">
              <div className="flex items-center gap-2">
                <div className="font-medium">{r.name}</div>
                <button onClick={() => delRole.mutate(r.name)} className="btn btn-ghost text-danger ml-auto">
                  <Trash2 className="w-4 h-4" />
                </button>
              </div>
              <div className="mt-2 flex flex-wrap gap-1.5 text-xs">
                {(r.permissions ?? []).length === 0 ? (
                  <span className="muted">no permissions</span>
                ) : (
                  r.permissions?.map((p, i) => (
                    <span key={i} className="pill">
                      <Lock className="w-3 h-3" /> {p.type} · {p.key}
                      {p.rangeEnd ? ` → ${p.rangeEnd}` : ""}
                    </span>
                  ))
                )}
              </div>
            </li>
          ))}
          {!roles.data?.length && <li className="py-3 muted text-sm">No roles yet.</li>}
        </ul>

        <div className="mt-4 grid grid-cols-1 sm:grid-cols-3 gap-2">
          <input
            placeholder="new role name"
            value={newRole}
            onChange={(e) => setNewRole(e.target.value)}
            className="h-10 px-3 rounded-lg text-sm sm:col-span-2"
            style={{ background: "rgb(var(--panel-2))", border: "1px solid rgb(var(--line))" }}
          />
          <button
            disabled={!newRole || addRole.isPending}
            onClick={() => {
              addRole.mutate(newRole);
              setNewRole("");
            }}
            className="btn btn-primary"
          >
            <Plus className="w-4 h-4" /> Add role
          </button>
        </div>

        <details className="mt-4">
          <summary className="cursor-pointer text-sm muted">Grant permission to role…</summary>
          <div className="mt-3 grid grid-cols-1 sm:grid-cols-5 gap-2">
            <Dropdown<string>
              value={newPerm.role}
              onChange={(v) => setNewPerm({ ...newPerm, role: v })}
              buttonClassName="h-10 w-full justify-between"
              ariaLabel="role"
              items={[
                { value: "", label: "role…" },
                ...(roles.data?.map((r) => ({ value: r.name, label: r.name })) ?? []),
              ]}
            />
            <Dropdown<RBACPermission["type"]>
              value={newPerm.perm.type}
              onChange={(v) => setNewPerm({ ...newPerm, perm: { ...newPerm.perm, type: v } })}
              buttonClassName="h-10 w-full justify-between"
              ariaLabel="permission type"
              items={[
                { value: "read", label: "read" },
                { value: "write", label: "write" },
                { value: "readwrite", label: "readwrite" },
              ]}
            />
            <input
              placeholder="key"
              value={newPerm.perm.key}
              onChange={(e) => setNewPerm({ ...newPerm, perm: { ...newPerm.perm, key: e.target.value } })}
              className="h-10 px-3 rounded-lg text-sm sm:col-span-2"
              style={{ background: "rgb(var(--panel-2))", border: "1px solid rgb(var(--line))" }}
            />
            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={!!newPerm.perm.prefix}
                onChange={(e) => setNewPerm({ ...newPerm, perm: { ...newPerm.perm, prefix: e.target.checked } })}
              />
              prefix
            </label>
            <button
              disabled={!newPerm.role || !newPerm.perm.key || grantPerm.isPending}
              onClick={() => grantPerm.mutate(newPerm)}
              className="btn btn-primary sm:col-span-5"
            >
              <Plus className="w-4 h-4" /> Grant
            </button>
          </div>
        </details>
      </section>
    </div>
  );
}

function GrantRoleButton({
  roles,
  existing,
  onGrant,
}: {
  roles: string[];
  existing: string[];
  onGrant: (r: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const avail = roles.filter((r) => !existing.includes(r));
  if (avail.length === 0) return null;
  return (
    <div className="relative inline-block">
      <button
        onClick={() => setOpen((v) => !v)}
        className="pill soft-hover"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label="Grant a role to this user"
      >
        <Plus className="w-3 h-3" /> grant
      </button>
      {open && (
        <div
          role="menu"
          aria-label="Available roles"
          onMouseLeave={() => setOpen(false)}
          className="absolute z-10 mt-1 right-0 panel p-1 min-w-[140px]"
        >
          {avail.map((r) => (
            <button
              key={r}
              role="menuitem"
              onClick={() => {
                onGrant(r);
                setOpen(false);
              }}
              className="w-full text-left px-2 py-1.5 rounded-md text-sm soft-hover"
            >
              {r}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
