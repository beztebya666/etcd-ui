/// <reference types="vite/client" />

// Project-specific env vars surfaced through `import.meta.env`. Vite injects
// the standard ones (DEV, PROD, MODE, BASE_URL) automatically; this file
// adds typing for the custom ones we read in the SPA.
interface ImportMetaEnv {
  readonly VITE_REFRESH_LEAD?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
