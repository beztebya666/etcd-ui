import { describe, it, expect } from "vitest";
import { looksBinary, readableHint } from "./CodeEditor";

describe("looksBinary", () => {
  it("returns false for empty + plain UTF-8", () => {
    expect(looksBinary("")).toBe(false);
    expect(looksBinary("hello world")).toBe(false);
    expect(looksBinary("привет мир 🚀")).toBe(false);
    expect(looksBinary("{\n  \"k\": \"v\"\n}")).toBe(false);
  });

  it("flags the Unicode replacement char (invalid UTF-8 from Go)", () => {
    expect(looksBinary("k8s� prefix")).toBe(true);
  });

  it("flags raw control bytes outside tab/LF/CR", () => {
    expect(looksBinary("foo\x00bar")).toBe(true);
    expect(looksBinary("foo\x07bar")).toBe(true);
  });

  it("permits tab/LF/CR (whitespace)", () => {
    expect(looksBinary("foo\tbar")).toBe(false);
    expect(looksBinary("line\nline")).toBe(false);
    expect(looksBinary("crlf\r\n")).toBe(false);
  });

  it("only scans the first 256 chars (perf)", () => {
    const ok = "a".repeat(300);
    expect(looksBinary(ok)).toBe(false);
    // 300 plain + binary byte beyond the scan window — should NOT be flagged.
    const padded = "a".repeat(300) + "\x00";
    expect(looksBinary(padded)).toBe(false);
  });
});

describe("readableHint", () => {
  it("returns trimmed printable prefix", () => {
    // K8s protobuf preamble shape.
    const value = "k8s\x00\nL\nXapps/v1\x00\x00Deployment\x00";
    const hint = readableHint(value, 60);
    expect(hint).toContain("k8s");
    expect(hint).toContain("apps/v1");
    expect(hint).toContain("Deployment");
  });

  it("collapses runs of control bytes to single spaces", () => {
    expect(readableHint("a\x00\x00\x00b", 20)).toBe("a b");
  });

  it("respects the max length cap", () => {
    const long = "x".repeat(200);
    expect(readableHint(long, 40).length).toBeLessThanOrEqual(40);
  });

  it("returns empty string for fully-binary input", () => {
    const allCtrl = "\x00\x01\x02\x03\x04";
    expect(readableHint(allCtrl)).toBe("");
  });
});
