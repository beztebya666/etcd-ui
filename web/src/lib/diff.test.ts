import { describe, it, expect } from "vitest";
import { lineDiff } from "./diff";

describe("lineDiff", () => {
  it("returns all same for identical input", () => {
    const segs = lineDiff("a\nb\nc", "a\nb\nc");
    expect(segs.every((s) => s.kind === "same")).toBe(true);
    expect(segs.map((s) => s.text)).toEqual(["a", "b", "c"]);
  });

  it("reports a single addition", () => {
    const segs = lineDiff("a\nc", "a\nb\nc");
    const adds = segs.filter((s) => s.kind === "add");
    expect(adds.length).toBe(1);
    expect(adds[0].text).toBe("b");
  });

  it("reports a single deletion", () => {
    const segs = lineDiff("a\nb\nc", "a\nc");
    const dels = segs.filter((s) => s.kind === "del");
    expect(dels.length).toBe(1);
    expect(dels[0].text).toBe("b");
  });

  it("handles complete replacement", () => {
    const segs = lineDiff("a", "b");
    expect(segs.find((s) => s.kind === "del")?.text).toBe("a");
    expect(segs.find((s) => s.kind === "add")?.text).toBe("b");
  });
});
