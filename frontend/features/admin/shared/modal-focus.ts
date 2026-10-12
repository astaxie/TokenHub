import { useEffect, useRef, type KeyboardEvent } from "react";

const modalStack: Array<{ modal: HTMLElement; previous: HTMLElement | null }> = [];

export function useModalFocus<T extends HTMLElement = HTMLDivElement>(onEscape?: () => void) {
  const ref = useRef<T>(null);
  useEffect(() => {
    const modal = ref.current;
    if (!modal) return;
    const entry = { modal, previous: document.activeElement as HTMLElement | null };
    modalStack.push(entry);
    function keepFocusInside(event: FocusEvent) {
      if (modalStack.at(-1)?.modal === modal && event.target instanceof Node && !modal.contains(event.target)) modal.focus();
    }
    document.addEventListener("focusin", keepFocusInside);
    modal.focus();
    return () => {
      document.removeEventListener("focusin", keepFocusInside);
      const wasTop = modalStack.at(-1) === entry;
      const index = modalStack.indexOf(entry);
      if (index !== -1) modalStack.splice(index, 1);
      if (!wasTop) {
        for (const remaining of modalStack) {
          if (remaining.previous && modal.contains(remaining.previous)) remaining.previous = entry.previous;
        }
        return;
      }
      const previous = entry.previous;
      const parent = modalStack.at(-1)?.modal;
      if (previous?.isConnected && (!parent || parent.contains(previous))) previous.focus();
      else parent?.focus();
    };
  }, []);
  useEffect(() => {
    const modal = ref.current;
    if (modal && modalStack.at(-1)?.modal === modal && !modal.contains(document.activeElement)) modal.focus();
  });
  function onKeyDown(event: KeyboardEvent<T>) {
    const modal = ref.current;
    if (!modal || modalStack.at(-1)?.modal !== modal) return;
    if (event.key === "Escape") { event.stopPropagation(); onEscape?.(); }
    if (event.key !== "Tab") return;
    const controls = Array.from(modal.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), summary')).filter(element => element.getClientRects().length > 0);
    if (controls.length === 0) { event.preventDefault(); modal.focus(); return; }
    const first = controls[0]; const last = controls.at(-1);
    if (event.shiftKey && (document.activeElement === first || document.activeElement === modal)) { event.preventDefault(); last?.focus(); }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
  }
  return { ref, tabIndex: -1, onKeyDown };
}
