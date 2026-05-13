// Login page. Renders when the SPA gets a 401 from /api/auth/me. Shows Basic
// credentials form + a "Sign in with SSO" button when /api/auth/providers
// reports oidc=true. Auth state is otherwise opaque to the SPA — the gateway
// owns the cookie.

import { useEffect, useState } from "react";
import { api } from "../lib/api";
import { LogIn, ShieldCheck, AlertTriangle } from "lucide-react";

export function LoginPage() {
  const [providers, setProviders] = useState<{ basic: boolean; oidc: boolean } | null>(null);
  const [user, setUser] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api
      .authProviders()
      .then(setProviders)
      .catch(() => setProviders({ basic: true, oidc: false }));
  }, []);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      await api.login(user, password);
      // Hard navigate so React Query refetches every gated resource with the
      // freshly-issued cookie.
      window.location.assign(returnTo());
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="min-h-screen flex items-center justify-center p-6"
         style={{ background: "rgb(var(--bg))" }}>
      <div
        className="w-[420px] max-w-full panel p-7 shadow-elev"
        role="dialog"
        aria-labelledby="login-title"
      >
        <div className="flex items-center gap-2.5">
          <div className="w-10 h-10 rounded-xl bg-accent/15 text-accent-500 flex items-center justify-center">
            <ShieldCheck className="w-5 h-5" />
          </div>
          <div>
            <div id="login-title" className="text-lg font-semibold">Sign in</div>
            <div className="muted text-xs">etcd-ui — universal etcd console</div>
          </div>
        </div>

        {providers?.oidc && (
          <>
            <a
              href={api.oidcLoginURL(returnTo())}
              className="btn btn-primary w-full mt-6 justify-center"
            >
              <LogIn className="w-4 h-4" /> Sign in with SSO
            </a>
            {providers.basic && (
              <div className="my-4 flex items-center gap-2 text-[11px] muted">
                <div className="flex-1 h-px" style={{ background: "rgb(var(--line))" }} />
                or with credentials
                <div className="flex-1 h-px" style={{ background: "rgb(var(--line))" }} />
              </div>
            )}
          </>
        )}

        {providers?.basic && (
          <form onSubmit={submit} className="mt-4 space-y-3">
            <Field label="Username" value={user} onChange={setUser} autoFocus />
            <Field label="Password" value={password} onChange={setPassword} type="password" />
            {error && (
              <div className="text-sm text-danger flex items-center gap-2">
                <AlertTriangle className="w-4 h-4" /> {error}
              </div>
            )}
            <button type="submit" disabled={busy || !user || !password} className="btn btn-primary w-full justify-center">
              {busy ? "Signing in…" : "Sign in"}
            </button>
          </form>
        )}

        {providers && !providers.basic && !providers.oidc && (
          <div className="mt-6 muted text-sm text-center">
            Auth isn't configured on this deployment. Set <span className="kbd">AUTH_USERS</span> or
            an OIDC provider to enable login.
          </div>
        )}
      </div>
    </div>
  );
}

function Field({
  label,
  value,
  onChange,
  type = "text",
  autoFocus,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  type?: string;
  autoFocus?: boolean;
}) {
  return (
    <label className="block">
      <div className="text-xs muted mb-1">{label}</div>
      <input
        autoFocus={autoFocus}
        type={type}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="w-full h-10 px-3 rounded-lg text-sm"
        style={{ background: "rgb(var(--panel-2))", border: "1px solid rgb(var(--line))" }}
      />
    </label>
  );
}

function returnTo(): string {
  const r = new URLSearchParams(window.location.search).get("returnTo");
  if (r && r.startsWith("/") && !r.startsWith("//")) return r;
  return "/";
}
