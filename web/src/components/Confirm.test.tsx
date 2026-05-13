// @vitest-environment jsdom
import { describe, it, expect, beforeEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import { Confirmer, confirm } from "./Confirm";

describe("Confirm modal", () => {
  beforeEach(() => cleanup());

  it("resolves true when the confirm button is clicked", async () => {
    render(<Confirmer />);
    const promise = confirm({ title: "Are you sure?", confirmLabel: "Yes" });
    const btn = await screen.findByRole("button", { name: "Yes" });
    fireEvent.click(btn);
    await expect(promise).resolves.toBe(true);
  });

  it("resolves false when the cancel button is clicked", async () => {
    render(<Confirmer />);
    const promise = confirm({ title: "Are you sure?" });
    const btn = await screen.findByRole("button", { name: "Cancel" });
    fireEvent.click(btn);
    await expect(promise).resolves.toBe(false);
  });

  it("disables confirm until the user types the required token", async () => {
    render(<Confirmer />);
    const promise = confirm({
      title: "Drop the cluster",
      typeToConfirm: "PROD",
      confirmLabel: "Drop",
    });
    const drop = (await screen.findByRole("button", { name: "Drop" })) as HTMLButtonElement;
    expect(drop.disabled).toBe(true);
    const input = screen.getByDisplayValue("") as HTMLInputElement;
    fireEvent.change(input, { target: { value: "PROD" } });
    expect(drop.disabled).toBe(false);
    fireEvent.click(drop);
    await expect(promise).resolves.toBe(true);
  });

  it("Escape resolves false", async () => {
    render(<Confirmer />);
    const promise = confirm({ title: "Esc test" });
    await screen.findByText("Esc test");
    fireEvent.keyDown(window, { key: "Escape" });
    await expect(promise).resolves.toBe(false);
  });
});
