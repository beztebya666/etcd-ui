// Minimal focus trap — confines Tab/Shift+Tab inside a container and restores
// focus to the previously-active element on close. Activate by passing a ref
// to your modal's root.

import { useEffect, useRef } from "react";

const FOCUSABLE =
  'a[href], area[href], input:not([disabled]), select:not([disabled]), ' +
  'textarea:not([disabled]), button:not([disabled]), iframe, object, embed, ' +
  '[tabindex]:not([tabindex="-1"]), [contenteditable="true"]';

export function useFocusTrap<T extends HTMLElement>(active: boolean) {
  const ref = useRef<T | null>(null);
  const lastFocus = useRef<HTMLElement | null>(null);

  useEffect(() => {
    if (!active || !ref.current) return;
    lastFocus.current = (document.activeElement as HTMLElement) ?? null;

    const root = ref.current;
    const focusables = () =>
      Array.from(root.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(
        (el) => !el.hasAttribute("aria-hidden") && el.offsetParent !== null,
      );

    // Move focus inside on activation
    const first = focusables()[0];
    if (first) first.focus();
    else root.focus();

    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Tab") return;
      const items = focusables();
      if (items.length === 0) {
        e.preventDefault();
        return;
      }
      const idx = items.indexOf(document.activeElement as HTMLElement);
      if (e.shiftKey) {
        if (idx <= 0) {
          e.preventDefault();
          items[items.length - 1].focus();
        }
      } else {
        if (idx === items.length - 1) {
          e.preventDefault();
          items[0].focus();
        }
      }
    };
    root.addEventListener("keydown", onKey);
    return () => {
      root.removeEventListener("keydown", onKey);
      lastFocus.current?.focus?.();
    };
  }, [active]);

  return ref;
}
