// Proactive OIDC refresh. Reads `/api/auth/me` to learn the session expiry,
// schedules a POST `/api/auth/oidc/refresh` 60s before it. On success the
// new expiry comes back in the next `me` poll, the cycle continues. On
// failure we fall back to the reactive 401-handler in api.ts (which sends
// the user to /login).
//
// Mounted once at App root. Self-disabling when:
//   - auth is off entirely (authDisabled === true)
//   - the user signed in via Basic (no refresh token to use)
//   - the page is /login (avoid double prompts)

import { useEffect, useRef } from "react";
import { api } from "../lib/api";
import { useLocation } from "react-router-dom";

// How early to refresh. 60s is comfortable for a 1h TTL; configurable via
// VITE_REFRESH_LEAD if a deployment needs different cadence.
const LEAD_MS = Number(import.meta.env.VITE_REFRESH_LEAD ?? 60_000);
// Hard floor — don't try to refresh more than once a minute even if the
// server's TTL is bizarrely small.
const MIN_INTERVAL_MS = 30_000;

export function SessionKeepalive() {
  const timer = useRef<number | null>(null);
  const loc = useLocation();

  useEffect(() => {
    if (loc.pathname.startsWith("/login")) return;

    let cancelled = false;

    const schedule = (delayMs: number) => {
      if (cancelled) return;
      if (timer.current != null) window.clearTimeout(timer.current);
      timer.current = window.setTimeout(tick, Math.max(MIN_INTERVAL_MS, delayMs));
    };

    const tick = async () => {
      if (cancelled) return;
      try {
        const me = await api.me();
        if (me.authDisabled || me.src !== "oidc" || !me.exp) {
          // Nothing to refresh — poll less often (5 min) so we still notice
          // if auth gets re-enabled at runtime.
          schedule(5 * 60_000);
          return;
        }
        const msUntilExpiry = me.exp * 1000 - Date.now();
        if (msUntilExpiry < LEAD_MS) {
          const ok = await api.oidcRefresh().then(() => true).catch(() => false);
          if (ok) {
            // Invalidate any cached WS subprotocol bearer — the next WS
            // reconnect should fetch a new short-lived ticket if/when one
            // is needed. Cookie-only auth keeps working without this.
            try {
              sessionStorage.removeItem("etcd-ui-bearer");
            } catch {
              /* private mode etc — ignore */
            }
          }
          // After refresh, re-query exp from /me to schedule next tick.
          schedule(MIN_INTERVAL_MS);
          return;
        }
        schedule(msUntilExpiry - LEAD_MS);
      } catch {
        // /me itself failed — likely network hiccup. Retry in 30s.
        schedule(MIN_INTERVAL_MS);
      }
    };

    tick();

    return () => {
      cancelled = true;
      if (timer.current != null) window.clearTimeout(timer.current);
    };
  }, [loc.pathname]);

  return null;
}
