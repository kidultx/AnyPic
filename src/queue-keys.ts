/* The queue's list manners: roving focus, arrow keys, Shift ranges, ⌘A,
   Delete, and the row context menu. main.ts still owns the queue state and
   rendering; this module reads it through QueueKeysContext and asks it to
   sync the rows after a selection change (#20). */

import type { Item } from "./mygo";
import { $ } from "./ui";
import { t } from "./i18n";

export interface QueueKeysContext {
  rows(): Map<number, HTMLElement>;
  /** The ids the queue is showing, in display order. */
  visibleIds(): number[];
  /** The live selection set; this module edits it in place. */
  selected(): Set<number>;
  item(id: number): Item | null;
  running(): boolean;
  /** Repaint the rows' selected state and the selection bar. */
  syncSelection(): void;
  /** The same action a click on the row runs (preview / error toast). */
  activate(id: number): void;
  decorate(id: number): void;
  exportOne(id: number): void;
  reveal(id: number): void;
  removeSelected(): void;
  removeOne(id: number): void;
}

let ctx: QueueKeysContext;
/* The roving tab stop: exactly one row is tabindex 0, so the list is one tab
   stop rather than N. anchorId is where a Shift range starts. */
let focusId: number | null = null;
let anchorId: number | null = null;
/* The row that was focused before the current press: clicking a row moves
   focus to it before the click arrives, so Shift+click must remember the
   previous row in pointerdown or the range collapses to one row (#20). */
let pressFocus: number | null = null;
let menuRowId: number | null = null;

export function initQueueKeys(context: QueueKeysContext) {
  ctx = context;
  bindQueueShortcuts();
  bindQueueMenu();
}

/* Called after the queue is filtered: keep the tab stop on a row that still
   exists (the first one when nothing was focused yet). */
export function ensureQueueFocus(visible: number[]) {
  if (visible.length === 0) {
    focusId = null;
    return;
  }
  if (focusId === null || !visible.includes(focusId)) focusId = visible[0] ?? null;
}

export function queueRowTabIndex(id: number): number {
  return id === focusId ? 0 : -1;
}

/* Delete moves focus to the nearest surviving neighbour instead of throwing
   it back to the document; called while the old order still exists. */
export function queueFocusAfterRemoval(removed: Set<number>) {
  if (focusId === null || !removed.has(focusId)) return;
  const ids = ctx.visibleIds();
  const index = ids.indexOf(focusId);
  for (let step = 1; step < ids.length; step++) {
    const after = ids[index + step];
    if (after !== undefined && !removed.has(after)) {
      focusId = after;
      return;
    }
    const before = ids[index - step];
    if (before !== undefined && !removed.has(before)) {
      focusId = before;
      return;
    }
  }
  focusId = null;
}

export function queueRowFocus(): boolean {
  if (focusId === null) return false;
  const row = ctx.rows().get(focusId);
  if (!row) return false;
  row.focus({ preventScroll: true });
  return true;
}

/* ------------------------------------------------------------------ */
/* rows                                                                */
/* ------------------------------------------------------------------ */

/* Wires one queue row. Called from createRow, so the behaviour is identical
   in the list and the grid — they render the same element. */
export function queueRowKeys(row: HTMLElement, id: number) {
  row.tabIndex = queueRowTabIndex(id);
  row.setAttribute("role", "option");

  row.addEventListener("focus", () => {
    if (focusId === id) return;
    focusId = id;
    syncTabIndexes();
  });

  row.addEventListener("keydown", (event) => {
    // Inner buttons and the checkbox keep their own keys.
    if (event.target !== row) return;
    if (event.key === "ArrowDown") {
      event.preventDefault();
      moveFocus(1, event);
    } else if (event.key === "ArrowUp") {
      event.preventDefault();
      moveFocus(-1, event);
    } else if (event.key === " " || event.key === "Enter") {
      event.preventDefault();
      anchorId = id;
      ctx.activate(id);
    }
  });

  row.addEventListener("pointerdown", () => {
    pressFocus = focusId;
  });

  row.addEventListener("click", (event) => {
    const target = event.target as HTMLElement;
    if (target.closest("button, input, label")) return;
    const previous = pressFocus ?? focusId;
    focusId = id;
    syncTabIndexes();
    if (event.shiftKey) {
      // Extend from where focus already was; overwriting the anchor with the
      // clicked row would collapse every Shift+click to a single row.
      event.preventDefault();
      anchorId ??= previous ?? id;
      rangeTo(id, false);
      return;
    }
    anchorId = id;
    if (event.metaKey || event.ctrlKey) {
      event.preventDefault();
      toggleSelection(id);
      return;
    }
    ctx.activate(id);
  });

  row.addEventListener("contextmenu", (event) => {
    event.preventDefault();
    openQueueMenu(id, event.clientX, event.clientY);
  });
}

function syncTabIndexes() {
  for (const [id, row] of ctx.rows()) row.tabIndex = queueRowTabIndex(id);
}

function moveFocus(delta: number, event: KeyboardEvent) {
  const ids = ctx.visibleIds();
  if (ids.length === 0) return;
  const current = focusId === null ? -1 : ids.indexOf(focusId);
  const base = current < 0 ? (delta > 0 ? -1 : ids.length) : current;
  const nextIndex = Math.max(0, Math.min(ids.length - 1, base + delta));
  const next = ids[nextIndex];
  if (next === undefined) return;
  if (event.shiftKey) anchorId ??= focusId ?? next;
  focusId = next;
  syncTabIndexes();
  ctx.rows().get(next)?.focus({ preventScroll: true });
  if (event.shiftKey) rangeTo(next, false);
}

function toggleSelection(id: number) {
  if (ctx.selected().has(id)) ctx.selected().delete(id);
  else ctx.selected().add(id);
  ctx.syncSelection();
}

/* Shift selections replace the previous range and grow from the anchor; the
   anchor moves only on a plain click or a plain arrow. */
function rangeTo(targetId: number, additive: boolean) {
  const ids = ctx.visibleIds();
  const target = ids.indexOf(targetId);
  if (target < 0) return;
  const anchor = anchorId === null ? -1 : ids.indexOf(anchorId);
  if (!additive) ctx.selected().clear();
  if (anchor < 0) {
    ctx.selected().add(targetId);
  } else {
    const from = Math.min(anchor, target);
    const to = Math.max(anchor, target);
    for (let i = from; i <= to; i++) {
      const id = ids[i];
      if (id !== undefined) ctx.selected().add(id);
    }
  }
  ctx.syncSelection();
}

/* ------------------------------------------------------------------ */
/* shortcuts                                                           */
/* ------------------------------------------------------------------ */

function bindQueueShortcuts() {
  document.addEventListener("keydown", (event) => {
    if (event.defaultPrevented) return;
    const target = event.target as HTMLElement | null;
    if (target?.closest("input, textarea, select") || target?.isContentEditable) return;
    // A dialog owns the keyboard while it is up (preview, prefs, confirms);
    // the row menu does too, so its own Escape/arrow keys are not shadowed.
    if (document.querySelector(".modal.open") || !$<HTMLElement>("#queueMenu").hidden) return;
    const meta = navigator.platform.toLowerCase().includes("mac") ? event.metaKey : event.ctrlKey;
    if (meta && (event.key === "a" || event.key === "A")) {
      const ids = ctx.visibleIds();
      if (ids.length === 0) return;
      event.preventDefault();
      anchorId = focusId;
      for (const id of ids) ctx.selected().add(id);
      ctx.syncSelection();
      return;
    }
    // ⌘⌫ keeps clearing the whole queue in main.ts; only the bare key
    // removes the selection here.
    if (meta) return;
    if (event.key !== "Delete" && event.key !== "Backspace") return;
    if (ctx.selected().size === 0) return;
    event.preventDefault();
    ctx.removeSelected();
  });
}

/* ------------------------------------------------------------------ */
/* row context menu                                                    */
/* ------------------------------------------------------------------ */

function bindQueueMenu() {
  const menu = $<HTMLElement>("#queueMenu");

  menu.addEventListener("click", (event) => {
    const button = (event.target as HTMLElement).closest<HTMLButtonElement>("button[data-menu]");
    if (!button || button.disabled) return;
    const id = menuRowId;
    closeQueueMenu();
    if (id === null) return;
    switch (button.dataset.menu) {
      case "preview":
        ctx.activate(id);
        break;
      case "decorate":
        ctx.decorate(id);
        break;
      case "export":
        ctx.exportOne(id);
        break;
      case "reveal":
        ctx.reveal(id);
        break;
      case "remove":
        if (ctx.selected().size > 1 && ctx.selected().has(id)) ctx.removeSelected();
        else ctx.removeOne(id);
        break;
    }
  });

  menu.addEventListener("keydown", (event) => {
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      const buttons = [...menu.querySelectorAll<HTMLButtonElement>("button[data-menu]:not(:disabled)")];
      if (buttons.length === 0) return;
      const index = buttons.indexOf(document.activeElement as HTMLButtonElement);
      const step = event.key === "ArrowDown" ? 1 : -1;
      buttons[(index + step + buttons.length) % buttons.length]?.focus();
    } else if (event.key === "Tab") {
      closeQueueMenu();
    } else if (event.key === "Escape") {
      event.preventDefault();
      closeQueueMenu(true);
    }
  });

  document.addEventListener("pointerdown", (event) => {
    if (menu.hidden || menu.contains(event.target as Node)) return;
    closeQueueMenu();
  }, true);
  document.addEventListener("keydown", (event) => {
    if (menu.hidden || event.key !== "Escape") return;
    event.preventDefault();
    closeQueueMenu(true);
  });
  document.addEventListener("scroll", () => closeQueueMenu(), true);
  window.addEventListener("resize", () => closeQueueMenu());
  window.addEventListener("blur", () => closeQueueMenu());
}

function openQueueMenu(id: number, x: number, y: number) {
  const item = ctx.item(id);
  if (!item) return;
  // Right-clicking an unselected row selects it; right-clicking one of
  // several selected rows keeps the set, so "remove selected" can act on it.
  if (!ctx.selected().has(id)) {
    ctx.selected().clear();
    ctx.selected().add(id);
    ctx.syncSelection();
  }
  anchorId = id;
  focusId = id;
  syncTabIndexes();
  menuRowId = id;

  const running = ctx.running();
  const enable = (name: string, on: boolean) => {
    $<HTMLButtonElement>(`#queueMenu [data-menu="${name}"]`).disabled = !on;
  };
  enable("preview", item.status === "done");
  enable("decorate", !running && !item.animated && (item.status === "done" || item.status === "ready"));
  enable("export", !running && item.status === "done");
  enable("reveal", !!item.path);
  enable("remove", !running);

  const multi = ctx.selected().size > 1 && ctx.selected().has(id);
  $("#queueMenuRemoveLabel").textContent = multi ? t.removeSelectedN(ctx.selected().size) : t.remove();

  const menu = $<HTMLElement>("#queueMenu");
  menu.hidden = false;
  const rect = menu.getBoundingClientRect();
  menu.style.left = `${Math.max(8, Math.min(x, window.innerWidth - rect.width - 8))}px`;
  menu.style.top = `${Math.max(8, Math.min(y, window.innerHeight - rect.height - 8))}px`;
  menu.querySelector<HTMLButtonElement>("button[data-menu]:not(:disabled)")?.focus({ preventScroll: true });
}

function closeQueueMenu(restoreFocus = false) {
  const menu = $<HTMLElement>("#queueMenu");
  if (menu.hidden) return;
  menu.hidden = true;
  const id = menuRowId;
  menuRowId = null;
  if (restoreFocus && id !== null) ctx.rows().get(id)?.focus({ preventScroll: true });
}
