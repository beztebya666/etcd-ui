// Clipboard helper that works on non-HTTPS origins.
//
// `navigator.clipboard.writeText` is only defined in "secure contexts"
// (https://, file://, localhost, 127.0.0.1). Many operators access
// etcd-ui via `http://10.x.x.x:8080` from another machine on the LAN —
// that's NOT a secure context, so the native API is `undefined` and
// silently calling `.writeText` throws "cannot read 'writeText' of
// undefined". This wrapper falls back to the legacy `execCommand`
// path, which works everywhere browser-side regardless of TLS.

export async function copyToClipboard(text: string): Promise<boolean> {
  // Modern path: try the async API first; only available in secure
  // contexts (HTTPS / localhost).
  if (typeof navigator !== "undefined" && navigator.clipboard && window.isSecureContext) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      // permission denied / iframe restriction — fall through.
    }
  }
  // Legacy path: stash the value in an off-screen textarea, select,
  // execCommand('copy'). Works on HTTP origins; deprecated in spec
  // but every major browser still ships it. The textarea is removed
  // synchronously so it never appears in the layout.
  try {
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.setAttribute("readonly", "");
    ta.style.position = "fixed";
    ta.style.left = "-9999px";
    ta.style.top = "0";
    document.body.appendChild(ta);
    ta.select();
    ta.setSelectionRange(0, text.length);
    const ok = document.execCommand("copy");
    document.body.removeChild(ta);
    return ok;
  } catch {
    return false;
  }
}
