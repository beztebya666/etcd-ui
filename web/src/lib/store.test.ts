import { describe, it, expect, beforeEach } from "vitest";
import { useStore } from "./store";

describe("store", () => {
  beforeEach(() => {
    useStore.setState({
      pinnedClusters: [],
      savedViews: {},
      diffAgainst: null,
    } as any);
  });

  it("togglePin adds and removes", () => {
    useStore.getState().togglePin("a");
    expect(useStore.getState().pinnedClusters).toEqual(["a"]);
    useStore.getState().togglePin("b");
    expect(useStore.getState().pinnedClusters).toEqual(["a", "b"]);
    useStore.getState().togglePin("a");
    expect(useStore.getState().pinnedClusters).toEqual(["b"]);
  });

  it("reorderPinned moves an item", () => {
    useStore.setState({ pinnedClusters: ["a", "b", "c"] } as any);
    useStore.getState().reorderPinned(0, 2);
    expect(useStore.getState().pinnedClusters).toEqual(["b", "c", "a"]);
  });

  it("saveView upserts by name", () => {
    useStore.getState().saveView("cluster", "v1", "/foo", "");
    useStore.getState().saveView("cluster", "v1", "/bar", "baz");
    const views = useStore.getState().savedViews.cluster;
    expect(views.length).toBe(1);
    expect(views[0]).toEqual({ name: "v1", prefix: "/bar", valueRegex: "baz" });
  });
});
