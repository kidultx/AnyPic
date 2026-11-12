/* The preview modal: the before/after compare slider and the beautify
   workbench. This module owns all preview-modal state; the app shell (main.ts)
   hands in its live state through initPreview, because the modal reads the
   queue (opens the right item, applies back into the auto flow) but does not
   own it. */

import { AnyPic, type FrameStyle, type Item, type Preview, type Snapshot } from "./mygo";
import { $, $$, closeModal, formatBytes, formatDimensions, openModal, pct, showError, showToast } from "./ui";
import { t } from "./i18n";

/* The app shell's live state the modal needs. Getters, not snapshots: the
   queue changes under the modal between renders. */
export interface PreviewContext {
  items(): Item[];
  /* The ids the queue is showing right now, in display order: the current
     status tab plus the name filter. Preview paging follows the same order the
     rows do, so "next" means the next row the user can see (#21). */
  visibleIds(): number[];
  demoMode(): boolean;
  batchRunning(): boolean;
  autoProcessEnabled(): boolean;
  applySnapshot(snapshot: Snapshot, includeSettings?: boolean): void;
  startAutoRun(ids: number[], fromScreenshot: boolean): void;
  renderAll(): void;
}

let ctx: PreviewContext;

export function initPreview(context: PreviewContext) {
  ctx = context;
}

/* ------------------------------------------------------------------ */
/* state                                                               */
/* ------------------------------------------------------------------ */

let selectedId: number | null = null;
let previewData: Preview | null = null;
const previewCache = new Map<number, Preview>();
let comparePosition = 50;
let compareHintSeen = false;
let lastFocused: HTMLElement | null = null;
let previewMode: "compare" | "beautify" = "compare";

/* Compare zoom, in CSS px per source px. "fit" re-derives the scale from the
   frame every time, so resizing the window never leaves the picture cropped;
   "100" pins one source pixel to one screen pixel; "custom" is the wheel's
   arbitrary level and is what turns the readout on. Pan is in screen px and
   clamped so the picture never drifts off the frame (#21). */
let zoomMode: "fit" | "100" | "custom" = "fit";
let zoomScale = 1;
let panX = 0;
let panY = 0;
const MAX_ZOOM = 8;

/* Beautify workbench state: which item is open, the style being tuned, and
   the rendered results cached per style so switching back is instant. */
let beautifyId: number | null = null;
let frameStyle: FrameStyle = defaultFrameStyle();
const frameCache = new Map<string, string>();
let beautifyRenderSeq = 0;

/* The modal's export button acts on the item the modal is showing. */
export function previewSelectedId(): number | null {
  return selectedId;
}

/* The compare preview is cached per item; the queue invalidates it when an
   item's data changes or the item leaves the queue. */
export function invalidatePreview(id: number) {
  previewCache.delete(id);
}

/* ------------------------------------------------------------------ */
/* compare                                                             */
/* ------------------------------------------------------------------ */

export async function openPreview(id: number, mode: "compare" | "beautify" = "compare") {
  const item = ctx.items().find((entry) => entry.id === id);
  // The compare tab needs a compressed result; the beautify workbench only
  // needs a source image, so it also opens for freshly captured (ready)
  // items. A blocked open says why — silent returns made failures look like
  // the modal being broken.
  if (!item) {
    showToast(t.previewGone(), "info");
    return;
  }
  if (mode === "compare" && item.status !== "done") {
    showToast(item.status === "working" ? t.workingCompare() : t.compareNeedsDone(), "info");
    return;
  }
  if (mode === "beautify" && item.status !== "done" && item.status !== "ready") {
    showToast(item.status === "working" ? t.workingBeautify() : t.failedBeautify(), "info");
    return;
  }
  selectedId = id;
  if (beautifyId !== id) {
    // A fresh item opens the workbench neutral: neither the style nor the
    // rendered previews of a previous image may leak into this one.
    frameStyle = defaultFrameStyle();
    frameCache.clear();
  }
  beautifyId = id;
  // Paging with ←/→ must not steal focus from the row that opened the modal:
  // closing goes back to that row, not to the close button of a reopened one.
  const alreadyOpen = $("#previewModal").classList.contains("open");
  if (!alreadyOpen) {
    lastFocused = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    compareHintSeen = false;
    $("#compareHint").classList.remove("hidden");
    setComparePosition(50);
  }
  previewData = previewCache.get(id) ?? null;
  $("#previewTitle").textContent = item.name;
  $("#previewMeta").textContent = t.previewMeta(
    item.format,
    formatDimensions(item.width, item.height),
    formatBytes(item.originalSize),
    item.path,
  );
  $("#previewOriginalSize").textContent = formatBytes(item.originalSize);
  $("#previewCompressedSize").textContent = item.passedThrough ? formatBytes(item.originalSize) : formatBytes(item.compressedSize);
  $("#previewSaved").textContent = item.status === "done"
    ? item.passedThrough
      ? t.previewAlreadyOptimal()
      : item.compressedSize <= item.originalSize
        ? `−${pct(item.savedPercent)}`
        : `+${pct((item.compressedSize / Math.max(1, item.originalSize) - 1) * 100)}`
    : t.itemWaiting();
  $("#previewDetails").textContent = item.status === "done"
    ? t.previewOutput(item.outputFormat || item.format, item.outputName || item.name)
    : t.itemWaiting();
  $<HTMLButtonElement>("#previewExportBtn").disabled = item.status !== "done";
  $("#compareLoading").hidden = false;
  $("#compareFrame").hidden = true;
  // A zoom level belongs to the picture it was set on; the next image starts
  // at fit even when the modal never closed (#21).
  zoomMode = "fit";
  zoomScale = 1;
  panX = 0;
  panY = 0;
  switchPreviewMode(mode);
  openModal($("#previewModal"));
  // The compare panel may have painted while the modal was still display:none
  // (demo data arrives synchronously), so fit once the frame has a size.
  applyZoom();
  if (!alreadyOpen) $("#previewClose").focus({ preventScroll: true });
}

/* Loads the before/after data for the item on show and paints the compare.
   It runs both when the modal opens on the compare tab and whenever the tabs
   switch to it — entering through the beautify workbench must not leave the
   compare panel waiting forever (issue #1). */
async function loadCompare() {
  if (selectedId === null) return;
  if (previewData) {
    paintPreview(previewData);
    return;
  }
  const item = ctx.items().find((entry) => entry.id === selectedId);
  if (!item) return;
  if (item.status !== "done") {
    $("#compareLoading").hidden = true;
    showToast(item.status === "working" ? t.workingCompare() : t.compareNeedsDone(), "info");
    return;
  }
  try {
    const data = ctx.demoMode() ? demoPreview(item) : await AnyPic.preview(selectedId);
    previewData = data;
    previewCache.set(selectedId, data);
    paintPreview(data);
  } catch (error) {
    $("#compareLoading").hidden = true;
    showError(error, t.previewFailed());
  }
}

function demoPreview(item: Item): Preview {
  return {
    before: item.thumb ?? "",
    after: item.thumb ?? "",
    width: item.width,
    height: item.height,
    beforeWidth: item.width,
    beforeHeight: item.height,
    afterWidth: item.width,
    afterHeight: item.height,
  };
}

function paintPreview(data: Preview) {
  $("#previewBefore").setAttribute("src", data.before);
  $("#previewAfter").setAttribute("src", data.after);
  $("#compareLoading").hidden = true;
  $("#compareFrame").hidden = false;
  applyZoom();
}

function setComparePosition(position: number) {
  comparePosition = Math.max(2, Math.min(98, position));
  const frame = $("#compareFrame");
  frame.style.setProperty("--compare-pos", `${comparePosition}%`);
  const handle = $("#compareHandle");
  handle.setAttribute("aria-valuenow", String(Math.round(comparePosition)));
  handle.setAttribute("aria-valuetext", `${Math.round(comparePosition)}%`);
  // Said every time the modal opens, so the label follows the language (#32).
  handle.setAttribute("aria-label", t.compareHandle());
}

/* ---- zoom & paging -------------------------------------------------- */

/* Source-pixel size of the picture on screen. previewData carries it even
   before the <img> has decoded, so the first fit is already correct. */
function imageSize(): { w: number; h: number } {
  const data = previewData;
  return {
    w: data?.beforeWidth || data?.width || 0,
    h: data?.beforeHeight || data?.height || 0,
  };
}

/* CSS px per source px when the picture is contained in the frame. */
function fitScaleFor(width: number, height: number): number {
  const { w, h } = imageSize();
  if (w <= 0 || h <= 0 || width <= 0 || height <= 0) return 1;
  return Math.min(width / w, height / h);
}

function applyZoom() {
  const frame = $("#compareFrame");
  const rect = frame.getBoundingClientRect();
  if (rect.width <= 0 || rect.height <= 0) return;
  const fit = fitScaleFor(rect.width, rect.height);
  if (zoomMode === "fit") {
    // Fit follows the frame: it is re-derived on every resize, and a change
    // of frame size must never leave the picture cropped.
    zoomScale = fit;
    panX = 0;
    panY = 0;
  }
  // 100% must stay reachable even when "fit" upscales a small picture.
  const floor = Math.min(fit, 1);
  zoomScale = Math.max(floor, Math.min(MAX_ZOOM, zoomScale));

  const { w, h } = imageSize();
  const maxX = Math.max(0, (w * zoomScale - rect.width) / 2);
  const maxY = Math.max(0, (h * zoomScale - rect.height) / 2);
  panX = Math.max(-maxX, Math.min(maxX, panX));
  panY = Math.max(-maxY, Math.min(maxY, panY));

  frame.style.setProperty("--compare-zoom", String(zoomScale / fit));
  frame.style.setProperty("--compare-pan-x", `${panX}px`);
  frame.style.setProperty("--compare-pan-y", `${panY}px`);
  frame.classList.toggle("zoomed", maxX > 0.5 || maxY > 0.5);
  $("#zoomReadout").textContent = `${Math.round(zoomScale * 100)}%`;
  $$<HTMLButtonElement>("#zoomSeg button").forEach((button) => {
    const active = button.dataset.zoom === zoomMode;
    button.classList.toggle("active", active);
    button.setAttribute("aria-pressed", String(active));
  });
}

/* Wheel zoom keeps the point under the cursor fixed: (c − pan) / k is the
   source point under it; the new pan puts the same point back under c. */
function zoomAt(event: WheelEvent) {
  const frame = $("#compareFrame");
  const rect = frame.getBoundingClientRect();
  if (rect.width <= 0 || rect.height <= 0) return;
  if (!compareHintSeen) {
    // Zooming is the other half of what the hint advertises; once the user
    // has zoomed, it has done its job.
    compareHintSeen = true;
    $("#compareHint").classList.add("hidden");
  }
  const fit = fitScaleFor(rect.width, rect.height);
  const floor = Math.min(fit, 1);
  const next = Math.max(floor, Math.min(MAX_ZOOM, zoomScale * Math.exp(-event.deltaY * 0.0025)));
  if (Math.abs(next - zoomScale) < 0.0001) return;
  const k = zoomScale / fit;
  const kNext = next / fit;
  const cx = event.clientX - rect.left - rect.width / 2;
  const cy = event.clientY - rect.top - rect.height / 2;
  panX = cx - ((cx - panX) / k) * kNext;
  panY = cy - ((cy - panY) / k) * kNext;
  zoomScale = next;
  // Landing exactly on fit or on 1:1 is what the two buttons mean; showing
  // neither as active there would claim the view is somewhere else.
  zoomMode = Math.abs(next - fit) < 0.0001 ? "fit" : Math.abs(next - 1) < 0.0001 ? "100" : "custom";
  applyZoom();
}

/* The rows the preview may page through, in the queue's display order. The
   compare tab needs a compressed result; the beautify tab also takes a fresh
   capture. */
function navigableIds(): number[] {
  const byId = new Map(ctx.items().map((item) => [item.id, item]));
  return ctx.visibleIds().filter((id) => {
    const item = byId.get(id);
    if (!item) return false;
    return previewMode === "compare"
      ? item.status === "done"
      : item.status === "done" || item.status === "ready";
  });
}

function syncPreviewNav() {
  const ids = navigableIds();
  const index = selectedId === null ? -1 : ids.indexOf(selectedId);
  $("#previewNav").hidden = ids.length < 2 || index < 0;
  $("#previewCount").textContent = index >= 0 ? `${index + 1} / ${ids.length}` : "";
  $<HTMLButtonElement>("#previewPrev").disabled = index <= 0;
  $<HTMLButtonElement>("#previewNext").disabled = index < 0 || index >= ids.length - 1;
}

function stepPreview(delta: number) {
  const ids = navigableIds();
  const index = selectedId === null ? -1 : ids.indexOf(selectedId);
  const next = index + delta;
  if (index < 0 || next < 0 || next >= ids.length) return;
  const id = ids[next];
  if (id === undefined) return;
  void openPreview(id, previewMode);
}

function bindCompare() {
  const compare = $("#compare");
  const frame = $("#compareFrame");
  const handle = $("#compareHandle");
  let dragging = false;
  let panning = false;
  let panStart = { x: 0, y: 0, panX: 0, panY: 0 };

  const update = (event: PointerEvent) => {
    const rect = compare.getBoundingClientRect();
    if (rect.width <= 0) return;
    setComparePosition(((event.clientX - rect.left) / rect.width) * 100);
    if (!compareHintSeen) {
      compareHintSeen = true;
      $("#compareHint").classList.add("hidden");
    }
  };

  compare.addEventListener("pointerdown", (event) => {
    if (event.button !== 0) return;
    const origin = event.target as HTMLElement;
    if (origin.closest("#compareZoom")) return;
    // Zoomed in, a drag on the picture pans it; the split line keeps its own
    // drag (knob or line) so both gestures stay available (#21).
    if (!handle.contains(origin) && frame.classList.contains("zoomed")) {
      panning = true;
      panStart = { x: event.clientX, y: event.clientY, panX, panY };
      frame.classList.add("panning");
      compare.setPointerCapture(event.pointerId);
      event.preventDefault();
      return;
    }
    dragging = true;
    handle.classList.add("dragging");
    compare.setPointerCapture(event.pointerId);
    update(event);
  });
  compare.addEventListener("pointermove", (event) => {
    if (panning) {
      panX = panStart.panX + (event.clientX - panStart.x);
      panY = panStart.panY + (event.clientY - panStart.y);
      applyZoom();
      return;
    }
    if (!dragging) return;
    update(event);
  });
  const end = () => {
    dragging = false;
    panning = false;
    handle.classList.remove("dragging");
    frame.classList.remove("panning");
  };
  compare.addEventListener("pointerup", end);
  compare.addEventListener("pointercancel", end);

  frame.addEventListener("wheel", (event) => {
    if ((event.target as HTMLElement).closest("#compareZoom")) return;
    // Non-passive: the preview must zoom, not scroll the page under it.
    event.preventDefault();
    zoomAt(event);
  }, { passive: false });

  $("#zoomSeg").addEventListener("click", (event) => {
    const button = (event.target as HTMLElement).closest<HTMLButtonElement>("button[data-zoom]");
    if (!button) return;
    zoomMode = button.dataset.zoom === "100" ? "100" : "fit";
    // Choosing a named level re-centres; panning afterwards keeps working
    // because applyZoom only derives pan in "fit" mode (#21).
    zoomScale = zoomMode === "100" ? 1 : zoomScale;
    panX = 0;
    panY = 0;
    applyZoom();
  });
  window.addEventListener("resize", applyZoom);

  handle.addEventListener("keydown", (event) => {
    if (event.key === "ArrowLeft" || event.key === "ArrowRight") {
      event.preventDefault();
      setComparePosition(comparePosition + (event.key === "ArrowRight" ? 4 : -4));
    } else if (event.key === "Home") {
      event.preventDefault();
      setComparePosition(2);
    } else if (event.key === "End") {
      event.preventDefault();
      setComparePosition(98);
    }
  });
  handle.tabIndex = 0;
  handle.setAttribute("role", "slider");
  handle.setAttribute("aria-valuemin", "0");
  handle.setAttribute("aria-valuemax", "100");
}

export function closePreview() {
  closeModal($("#previewModal"));
  selectedId = null;
  beautifyId = null;
  previewData = null;
  $("#previewBefore").removeAttribute("src");
  $("#previewAfter").removeAttribute("src");
  $("#beautifyImg").removeAttribute("src");
  lastFocused?.focus({ preventScroll: true });
}

/* ------------------------------------------------------------------ */
/* beautify workbench                                                  */
/* ------------------------------------------------------------------ */

/* Solid swatches offered in the workbench: neutral grounds plus soft tints
   and two darks, so a light capture always finds a stage. */
const BG_SWATCHES = ["#ffffff", "#e7e7ea", "#f8ecdc", "#dbeafe", "#dcfce7", "#fae8ff", "#1f2937", "#0a0a0a"];

/* Gradient presets mirror the backend's gradientPresets table stop for stop.
   The ids are protocol; the labels come from the catalog when the swatches
   are labelled. Keep this list and decorate.go in step — a chip that promises
   a different ramp from the render is worse than no chip. */
const GRADIENT_PRESETS: { id: string; css: string }[] = [
  { id: "indigo", css: "linear-gradient(135deg, #c3cbfd, #4f46e5)" },
  { id: "dusk", css: "linear-gradient(135deg, #a5b2f0, #3b358c)" },
  { id: "ocean", css: "linear-gradient(135deg, #a9e4f2, #2c5fd1)" },
  { id: "mint", css: "linear-gradient(135deg, #b2e8d1, #14867f)" },
  { id: "rose", css: "linear-gradient(135deg, #f6c2c6, #c72a5b)" },
  { id: "sunset", css: "linear-gradient(135deg, #f7ddb4, #d96a3c)" },
  { id: "slate", css: "linear-gradient(135deg, #f6f7f9, #a8afba)" },
  { id: "charcoal", css: "linear-gradient(135deg, #454b57, #141820)" },
];

/* Pattern chips mirror the backend's day-card pattern set (patterns.go). The
   chips are CSS approximations of the same motif — tiny, but enough that the
   choice is made on the chip; the stage shows the real render. */
const PATTERN_PRESETS: { id: string; css: string }[] = [
  {
    id: "teal",
    css: "linear-gradient(90deg, rgba(255,255,255,0), rgba(255,255,255,.4)), linear-gradient(135deg, #1dd8d1, #92ecc6)",
  },
  {
    id: "gingham",
    css: "repeating-linear-gradient(0deg, rgba(214,72,56,.42) 0 8px, transparent 8px 16px), repeating-linear-gradient(90deg, rgba(214,72,56,.42) 0 8px, transparent 8px 16px), #fbf6ef",
  },
  {
    id: "grid",
    css: "repeating-linear-gradient(0deg, rgba(60,110,180,.25) 0 1px, transparent 1px 7px), repeating-linear-gradient(90deg, rgba(60,110,180,.25) 0 1px, transparent 1px 7px), #f3f0e6",
  },
  {
    id: "kraft",
    css: "radial-gradient(rgba(80,52,24,.35) .7px, transparent .9px) 0 0/4px 4px, radial-gradient(rgba(255,255,255,.3) .6px, transparent .8px) 2px 1px/5px 5px, #c4a274",
  },
  {
    id: "cyan",
    css: "radial-gradient(rgba(255,255,255,.3) .8px, transparent 1px) 0 0/4px 4px, #1b3a7a",
  },
  {
    id: "dots",
    css: "radial-gradient(#3d3d3d 1.4px, transparent 1.6px) 0 0/7px 7px, #151515",
  },
  {
    id: "deck",
    css: "repeating-linear-gradient(90deg, #2f5d50 0 8px, #f2ead8 8px 16px)",
  },
  {
    id: "linen",
    css: "repeating-linear-gradient(0deg, rgba(255,255,255,.07) 0 1px, transparent 1px 3px), repeating-linear-gradient(90deg, rgba(0,0,0,.1) 0 1px, transparent 1px 4px), #6f7a52",
  },
  {
    id: "seigaiha",
    css: "radial-gradient(circle at 50% 100%, transparent 5px, rgba(240,232,214,.45) 5px 6px, transparent 6px 7px) 0 0/12px 6px, #24506a",
  },
  {
    id: "xuan",
    css: "repeating-linear-gradient(115deg, rgba(120,98,66,.14) 0 1px, transparent 1px 6px), #efe9db",
  },
  {
    id: "zigzag",
    css: "linear-gradient(135deg, #ebdfc8 25%, transparent 25%) -4px 0/8px 8px, linear-gradient(225deg, #ebdfc8 25%, transparent 25%) -4px 0/8px 8px, #d6c4a2",
  },
  {
    id: "terracotta",
    css: "radial-gradient(rgba(255,236,214,.35) .8px, transparent 1px) 0 0/5px 5px, #bf6748",
  },
  {
    id: "lilac",
    css: "repeating-linear-gradient(45deg, rgba(78,60,120,.18) 0 1px, transparent 1px 6px), #d9d3e6",
  },
  {
    id: "velvet",
    css: "radial-gradient(circle at 30% 15%, rgba(80,140,110,.5), rgba(80,140,110,0) 70%), #17322a",
  },
  {
    id: "polka",
    css: "radial-gradient(#e0795a 2.2px, transparent 2.4px) 0 0/9px 9px, #f4e4cf",
  },
];

function gradientLabel(id: string): string {
  switch (id) {
    case "indigo": return t.gradientIndigo();
    case "dusk": return t.gradientDusk();
    case "ocean": return t.gradientOcean();
    case "mint": return t.gradientMint();
    case "rose": return t.gradientRose();
    case "sunset": return t.gradientSunset();
    case "slate": return t.gradientSlate();
    default: return t.gradientCharcoal();
  }
}

function patternLabel(id: string): string {
  switch (id) {
    case "teal": return t.patternTeal();
    case "gingham": return t.patternGingham();
    case "grid": return t.patternGrid();
    case "kraft": return t.patternKraft();
    case "cyan": return t.patternCyan();
    case "dots": return t.patternDots();
    case "deck": return t.patternDeck();
    case "linen": return t.patternLinen();
    case "seigaiha": return t.patternSeigaiha();
    case "xuan": return t.patternXuan();
    case "zigzag": return t.patternZigzag();
    case "terracotta": return t.patternTerracotta();
    case "lilac": return t.patternLilac();
    case "velvet": return t.patternVelvet();
    default: return t.patternPolka();
  }
}

function defaultFrameStyle(): FrameStyle {
  return {
    background: "gradient",
    backgroundColor: "#e7e7ea",
    backgroundGradient: "indigo",
    backgroundPattern: "teal",
    radius: "medium",
    ring: true,
    shadow: "soft",
    padding: "normal",
    ratio: "origin",
  };
}

/* The workbench style lives only for the item being tuned: it starts at the
   documented default each time a new image opens, and nothing persists to
   localStorage — a style set on one image must never leak into another. */
function populateBeautifyControls() {
  const swatches = $("#beautifySwatches");
  for (const hex of BG_SWATCHES) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "swatch";
    button.style.backgroundColor = hex;
    button.title = hex;
    button.dataset.color = hex;
    swatches.appendChild(button);
  }
  const picker = document.createElement("input");
  picker.type = "color";
  picker.id = "beautifyColor";
  picker.value = "#e7e7ea";
  swatches.appendChild(picker);

  const gradients = $("#beautifyGradients");
  for (const preset of GRADIENT_PRESETS) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "swatch gradient-chip";
    button.style.background = preset.css;
    button.dataset.gradient = preset.id;
    gradients.appendChild(button);
  }

  const patterns = $("#beautifyPatterns");
  for (const preset of PATTERN_PRESETS) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "swatch pattern-chip";
    button.style.background = preset.css;
    button.dataset.pattern = preset.id;
    patterns.appendChild(button);
  }
}

/* Keeps the workbench controls in sync with the frameStyle being tuned. */
/* Labels are set on every sync so a language change reaches swatches that were
   built once, at boot (#32). */
function labelSwatches() {
  $$<HTMLButtonElement>("#beautifySwatches .swatch").forEach((button) => {
    const hex = button.dataset.color ?? "";
    if (!hex) return;
    button.setAttribute("aria-label", t.solidSwatch(hex));
  });
  $<HTMLInputElement>("#beautifyColor").title = t.customColor();
  $<HTMLInputElement>("#beautifyColor").setAttribute("aria-label", t.customColor());
  $$<HTMLButtonElement>("#beautifyGradients .swatch").forEach((button) => {
    const id = button.dataset.gradient ?? "";
    const label = gradientLabel(id);
    button.title = label;
    button.setAttribute("aria-label", t.gradientSwatch(label));
  });
  $$<HTMLButtonElement>("#beautifyPatterns .swatch").forEach((button) => {
    const id = button.dataset.pattern ?? "";
    const label = patternLabel(id);
    button.title = label;
    button.setAttribute("aria-label", t.patternSwatch(label));
  });
}

function syncBeautifyControls() {
  labelSwatches();
  $$<HTMLButtonElement>("#beautifyBgKind button").forEach((button) => {
    const active = button.dataset.kind === frameStyle.background;
    button.classList.toggle("active", active);
    button.setAttribute("aria-pressed", String(active));
  });
  $("#beautifySwatches").hidden = frameStyle.background !== "solid";
  $("#beautifyGradients").hidden = frameStyle.background !== "gradient";
  $("#beautifyPatterns").hidden = frameStyle.background !== "pattern";

  $$<HTMLButtonElement>("#beautifySwatches .swatch").forEach((button) => {
    button.classList.toggle("active", frameStyle.background === "solid" && button.dataset.color === frameStyle.backgroundColor);
  });
  $<HTMLInputElement>("#beautifyColor").value = /^#[0-9a-f]{6}$/.test(frameStyle.backgroundColor)
    ? frameStyle.backgroundColor
    : "#e7e7ea";
  $$<HTMLButtonElement>("#beautifyGradients .swatch").forEach((button) => {
    button.classList.toggle("active", frameStyle.background === "gradient" && button.dataset.gradient === frameStyle.backgroundGradient);
  });
  $$<HTMLButtonElement>("#beautifyPatterns .swatch").forEach((button) => {
    button.classList.toggle("active", frameStyle.background === "pattern" && button.dataset.pattern === frameStyle.backgroundPattern);
  });

  for (const [id, key] of [
    ["#beautifyRadius", "radius"],
    ["#beautifyShadow", "shadow"],
    ["#beautifyPadding", "padding"],
    ["#beautifyRatio", "ratio"],
  ] as const) {
    $$<HTMLButtonElement>(`${id} button`).forEach((button) => {
      const active = button.dataset[key] === frameStyle[key];
      button.classList.toggle("active", active);
      button.setAttribute("aria-pressed", String(active));
    });
  }
  $<HTMLInputElement>("#beautifyRing").checked = frameStyle.ring;
}

/* Renders the tuned style into the workbench stage. Results are cached per
   style key, so flipping back to an earlier look is instant; a sequence
   guard keeps stale renders from overwriting newer ones. */
async function renderBeautify() {
  if (beautifyId === null) return;
  syncBeautifyControls();
  const key = JSON.stringify(frameStyle);
  const cached = frameCache.get(key);
  if (cached) {
    $<HTMLImageElement>("#beautifyImg").src = cached;
    $("#beautifyLoading").hidden = true;
    return;
  }
  const seq = ++beautifyRenderSeq;
  $("#beautifyLoading").hidden = false;
  if (ctx.demoMode()) {
    // The browser preview has no backend renderer; the queue thumb stands in
    // so the controls can still be tried out.
    const item = ctx.items().find((entry) => entry.id === beautifyId);
    const url = item?.thumb ?? "";
    if (seq === beautifyRenderSeq) {
      if (url) $<HTMLImageElement>("#beautifyImg").src = url;
      $("#beautifyLoading").hidden = true;
    }
    return;
  }
  try {
    const url = await AnyPic.previewDecorated(beautifyId, frameStyle);
    frameCache.set(key, url);
    if (seq !== beautifyRenderSeq || beautifyId === null) return;
    $<HTMLImageElement>("#beautifyImg").src = url;
  } catch (error) {
    if (seq === beautifyRenderSeq) showError(error, t.beautifyPreviewFailed());
  } finally {
    if (seq === beautifyRenderSeq) $("#beautifyLoading").hidden = true;
  }
}

function switchPreviewMode(mode: "compare" | "beautify") {
  previewMode = mode;
  const compare = mode === "compare";
  $("#compare").hidden = !compare;
  $("#beautify").hidden = compare;
  $$<HTMLButtonElement>("#previewTabs button").forEach((button) => {
    const active = button.dataset.mode === mode;
    button.classList.toggle("active", active);
    button.setAttribute("aria-selected", String(active));
  });
  // Export works on the compressed result; the beautify tab has its own
  // apply button instead.
  $<HTMLButtonElement>("#previewExportBtn").hidden = !compare;
  // Each tab loads its own content on entry: 对比 needs the before/after
  // data (cached after the first load), 美化 renders the tuned style.
  if (compare) void loadCompare();
  else void renderBeautify();
  syncPreviewNav();
}

/* Applies the tuned style: the backend writes 原名-美化.png beside the
   original and the item restarts, so the automatic flow saves it again. */
async function applyBeautify() {
  if (beautifyId === null || ctx.batchRunning()) return;
  const id = beautifyId;
  const item = ctx.items().find((entry) => entry.id === id);
  if (ctx.demoMode()) {
    showToast(t.demoNoBeautifyApply(), "info");
    return;
  }
  try {
    const snap = await AnyPic.decorateItem(id, frameStyle);
    closePreview();
    ctx.applySnapshot(snap, false);
    ctx.renderAll();
    showToast(t.beautifyApplied(item ? item.name : ""), "success");
    if (ctx.autoProcessEnabled()) ctx.startAutoRun([id], false);
  } catch (error) {
    showError(error, t.beautifyFailed());
  }
}

function resetBeautify() {
  frameStyle = defaultFrameStyle();
  void renderBeautify();
}

function bindBeautify() {
  populateBeautifyControls();

  $("#previewTabs").addEventListener("click", (event) => {
    const button = (event.target as HTMLElement).closest<HTMLButtonElement>("button[data-mode]");
    if (!button) return;
    switchPreviewMode(button.dataset.mode === "beautify" ? "beautify" : "compare");
  });

  $("#beautifyBgKind").addEventListener("click", (event) => {
    const button = (event.target as HTMLElement).closest<HTMLButtonElement>("button[data-kind]");
    if (!button) return;
    frameStyle.background = button.dataset.kind ?? "none";
    void renderBeautify();
  });
  $("#beautifySwatches").addEventListener("click", (event) => {
    const button = (event.target as HTMLElement).closest<HTMLButtonElement>(".swatch");
    if (!button) return;
    frameStyle.background = "solid";
    frameStyle.backgroundColor = button.dataset.color ?? "#e7e7ea";
    void renderBeautify();
  });
  $("#beautifyColor").addEventListener("input", (event) => {
    frameStyle.background = "solid";
    frameStyle.backgroundColor = (event.target as HTMLInputElement).value;
    void renderBeautify();
  });
  $("#beautifyGradients").addEventListener("click", (event) => {
    const button = (event.target as HTMLElement).closest<HTMLButtonElement>(".swatch");
    if (!button) return;
    frameStyle.background = "gradient";
    frameStyle.backgroundGradient = button.dataset.gradient ?? "indigo";
    void renderBeautify();
  });
  $("#beautifyPatterns").addEventListener("click", (event) => {
    const button = (event.target as HTMLElement).closest<HTMLButtonElement>(".swatch");
    if (!button) return;
    frameStyle.background = "pattern";
    frameStyle.backgroundPattern = button.dataset.pattern ?? "teal";
    void renderBeautify();
  });

  $("#beautifyRadius").addEventListener("click", (event) => {
    const button = (event.target as HTMLElement).closest<HTMLButtonElement>("button[data-radius]");
    if (!button) return;
    frameStyle.radius = button.dataset.radius ?? "medium";
    void renderBeautify();
  });
  $("#beautifyRing").addEventListener("change", (event) => {
    frameStyle.ring = (event.target as HTMLInputElement).checked;
    void renderBeautify();
  });
  $("#beautifyShadow").addEventListener("click", (event) => {
    const button = (event.target as HTMLElement).closest<HTMLButtonElement>("button[data-shadow]");
    if (!button) return;
    frameStyle.shadow = button.dataset.shadow ?? "soft";
    void renderBeautify();
  });
  $("#beautifyPadding").addEventListener("click", (event) => {
    const button = (event.target as HTMLElement).closest<HTMLButtonElement>("button[data-padding]");
    if (!button) return;
    frameStyle.padding = button.dataset.padding ?? "normal";
    void renderBeautify();
  });
  $("#beautifyRatio").addEventListener("click", (event) => {
    const button = (event.target as HTMLElement).closest<HTMLButtonElement>("button[data-ratio]");
    if (!button) return;
    frameStyle.ratio = button.dataset.ratio ?? "origin";
    void renderBeautify();
  });

  $("#beautifyReset").addEventListener("click", resetBeautify);
  $("#beautifyApply").addEventListener("click", () => void applyBeautify());
}

/* ←/→ page through the queue while the preview is open. The focused split
   handle keeps the arrows for its own slider, and text fields keep them for
   the cursor (#21). */
function bindPreviewKeys() {
  document.addEventListener("keydown", (event) => {
    if (!$("#previewModal").classList.contains("open")) return;
    if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
    if (event.altKey || event.ctrlKey || event.metaKey) return;
    const target = event.target as HTMLElement | null;
    if (target?.closest("#compareHandle")) return;
    if (target?.closest("input, textarea, select") || target?.isContentEditable) return;
    event.preventDefault();
    stepPreview(event.key === "ArrowRight" ? 1 : -1);
  });
  $("#previewPrev").addEventListener("click", () => stepPreview(-1));
  $("#previewNext").addEventListener("click", () => stepPreview(1));
}

/* Wires the whole modal: compare slider and beautify workbench. */
export function bindPreview() {
  bindCompare();
  bindPreviewKeys();
  bindBeautify();
}
