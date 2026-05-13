// Silent OIDC re-auth via a hidden iframe. The IdP supports `prompt=none`,
// which tells it: "if the user already has an active SSO session at *your*
// origin, return them a fresh code without showing any UI; otherwise fail
// fast with login_required."
//
// We use it as the *third* line of defence behind the proactive refresh
// timer + reactive 401 handler:
//
//   1. SessionKeepalive runs `/api/auth/oidc/refresh` 60s before expiry.
//   2. If that fails (refresh token revoked), the next 401 triggers a
//      `tryRefresh()`.
//   3. If that also fails, we try `trySilentAuth()` from this file. Most
//      enterprise IdPs (Keycloak, Okta, Azure AD) keep an HTTP-only SSO
//      cookie at the IdP origin; the iframe round-trip burns one redirect
//      but the user never sees the IdP page.
//   4. Only if silent auth also fails do we redirect to /login.

let inFlight: Promise<boolean> | null = null;

export function trySilentAuth(returnTo: string): Promise<boolean> {
  if (inFlight) return inFlight;
  inFlight = doSilent(returnTo).finally(() => {
    // Avoid hammering the IdP: cooldown 5s before allowing another attempt.
    setTimeout(() => (inFlight = null), 5000);
  });
  return inFlight;
}

function doSilent(returnTo: string): Promise<boolean> {
  return new Promise((resolve) => {
    const iframe = document.createElement("iframe");
    iframe.style.display = "none";
    iframe.setAttribute("aria-hidden", "true");
    iframe.setAttribute("tabindex", "-1");

    // 8s hard ceiling. If the IdP doesn't respond by then, treat it as
    // a fail and let the caller redirect to /login.
    const timeout = window.setTimeout(() => {
      cleanup();
      resolve(false);
    }, 8000);

    let resolved = false;
    function cleanup() {
      window.clearTimeout(timeout);
      window.removeEventListener("message", onMessage);
      try {
        iframe.remove();
      } catch {
        /* already gone */
      }
    }

    // The callback page (served by the gateway) postMessage's a tiny shape
    // back to us when it finishes:
    //
    //   { type: "etcd-ui:silent-auth", ok: true | false }
    //
    // The same-origin check protects us from any other iframe posting a
    // fake "success" — the message must arrive from window.location.origin.
    function onMessage(ev: MessageEvent) {
      if (ev.origin !== window.location.origin) return;
      const data = ev.data;
      if (!data || data.type !== "etcd-ui:silent-auth") return;
      if (resolved) return;
      resolved = true;
      cleanup();
      resolve(!!data.ok);
    }
    window.addEventListener("message", onMessage);

    // Trigger the flow with prompt=none. The server-side handler
    // recognises a `silent=1` query param and renders the postMessage
    // shim instead of redirecting.
    const url =
      "/api/auth/oidc/login?silent=1&returnTo=" +
      encodeURIComponent(returnTo);
    iframe.src = url;
    document.body.appendChild(iframe);
  });
}
