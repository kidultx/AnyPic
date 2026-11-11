/* Shared UI helpers: DOM lookups, formatting and the toast stack. Anything
   here must not reach into the app's state modules — both main.ts and
   preview.ts import from this file, so it only depends on the catalog
   accessors for the few words it formats (#32). */

import { t } from "./i18n";

export const $ = <T extends HTMLElement = HTMLElement>(selector: string): T => {
  const el = document.querySelector<T>(selector);
  if (!el) throw new Error(`missing element: ${selector}`);
  return el;
};

export const $$ = <T extends Element = HTMLElement>(selector: string): T[] =>
  Array.from(document.querySelectorAll<T>(selector));

export const icon = (name: string) => `<svg class="icon" aria-hidden="true"><use href="#i-${name}"></use></svg>`;

export const esc = (value: string) =>
  value.replace(/[&<>"']/g, (char) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[char] ?? char);

export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  const digits = unit === 0 ? 0 : value >= 100 ? 0 : value >= 10 ? 1 : 1;
  return `${value.toFixed(digits)} ${units[unit]}`;
}

export const pct = (value: number) => `${Math.round(Math.max(0, value))}%`;

export function formatDimensions(width: number, height: number): string {
  if (!width || !height) return t.unknownDimensions();
  return `${width} × ${height}`;
}

export function shortenPath(path: string): string {
  if (!path) return "";
  const parts = path.split(/[\\/]/).filter(Boolean);
  if (parts.length <= 2) return path;
  return `…/${parts.slice(-2).join("/")}`;
}

/* Modals share one inert mechanism: everything outside the open dialog goes
   inert, so focus and assistive tech stay inside it. */
export function openModal(modal: HTMLElement) {
  for (const sibling of document.body.children) {
    if (sibling !== modal && !sibling.classList.contains("toast-stack") && !sibling.classList.contains("svg-sprite")) {
      (sibling as HTMLElement).inert = true;
    }
  }
  modal.classList.add("open");
  modal.setAttribute("aria-hidden", "false");
}

export function closeModal(modal: HTMLElement) {
  for (const sibling of document.body.children) {
    if (sibling !== modal && !sibling.classList.contains("toast-stack") && !sibling.classList.contains("svg-sprite")) {
      (sibling as HTMLElement).inert = false;
    }
  }
  modal.classList.remove("open");
  modal.setAttribute("aria-hidden", "true");
}

export function showError(error: unknown, fallback: string) {
  const message = error instanceof Error ? error.message : typeof error === "string" ? error : fallback;
  showToast(message || fallback, "error");
}

/* The exit class (.toast.leaving) fades with a transition, so an animation
   event never fires. Listen for the element's own transition end and also
   arm a timer slightly longer than the CSS fade (0.14s in style.css) as the
   fallback for reduced motion, an interrupted transition, or a missed event.
   Whichever arrives first removes the element; the other becomes a no-op. */
const exitFallbackMs = 240;

export function removeAfterExit(element: HTMLElement) {
  let removed = false;
  const remove = () => {
    if (removed) return;
    removed = true;
    element.remove();
  };
  element.addEventListener("transitionend", (event) => {
    // Only the element's own fade counts; children may transition too.
    if (event.target === element) remove();
  });
  window.setTimeout(remove, exitFallbackMs);
}

export function showToast(
  message: string,
  kind: "success" | "error" | "info" = "info",
  action?: { label: string; onClick: () => void },
) {
  const stack = $("#toasts");
  const toast = document.createElement("div");
  toast.className = `toast ${kind}`;
  const iconName = kind === "success" ? "check" : kind === "error" ? "alert" : "info";
  toast.innerHTML = `<span class="toast-icon">${icon(iconName)}</span><span>${esc(message)}</span>`;
  let dismissed = false;
  if (action) {
    const button = document.createElement("button");
    button.className = "toast-action";
    button.textContent = action.label;
    button.addEventListener("click", () => {
      // A faded toast ignores pointer events in CSS; this is the second
      // guard, for anything that still reaches the handler programmatically:
      // an action must die with its toast (issue #16).
      if (dismissed) return;
      action.onClick();
      dismiss();
    });
    toast.appendChild(button);
  }
  stack.appendChild(toast);

  if (kind === "error") {
    toast.setAttribute("role", "alert");
  }

  let timer = kind === "error" ? 0 : window.setTimeout(dismiss, 3600);
  const pause = () => {
    if (dismissed) return;
    window.clearTimeout(timer);
  };
  const resume = () => {
    // Hover/focus can outlive the dismissal (a fade is still on screen);
    // never re-arm the timer once the toast is on its way out.
    if (dismissed) return;
    window.clearTimeout(timer);
    timer = window.setTimeout(dismiss, 1800);
  };
  toast.addEventListener("mouseenter", pause);
  toast.addEventListener("mouseleave", resume);
  toast.addEventListener("focusin", pause);
  toast.addEventListener("focusout", resume);

  function dismiss() {
    if (dismissed) return;
    dismissed = true;
    window.clearTimeout(timer);
    toast.classList.add("leaving");
    removeAfterExit(toast);
  }
}
