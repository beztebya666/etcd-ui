import { describe, it, expect } from "vitest";

// We can't call exportFile (it touches Blob/URL/DOM); instead exercise the
// pure helpers via a small re-export shim. Keep them adjacent in module so
// failures are obvious.

import { exportFile } from "./export";

describe("export module", () => {
  it("exposes exportFile", () => {
    expect(typeof exportFile).toBe("function");
  });
});

// Round-trip sanity for the gettext-style t() — verify defaults.
import { t } from "./i18n";
import { useStore } from "./store";

describe("i18n", () => {
  it("falls back to the English key if no translation exists", () => {
    useStore.setState({ lang: "en" } as any);
    expect(t("totally-unknown-key")).toBe("totally-unknown-key");
  });
  it("returns Russian for Dashboard when ru lang", () => {
    useStore.setState({ lang: "ru" } as any);
    expect(t("Dashboard")).toBe("Панель");
    useStore.setState({ lang: "en" } as any);
  });
});
