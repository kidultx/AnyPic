/* The preview modal: the before/after compare slider and the beautify
   workbench. This module owns all preview-modal state; the app shell (main.ts)
   hands in its live state through initPreview, because the modal reads the
   queue (opens the right item, applies back into the auto flow) but does not
   own it. */

import { AnyPic, type FrameStyle, type Item, type Preview, type Snapshot } from "./mygo";
import { $, $$, closeModal, formatBytes, formatDimensions, openModal, pct, showError, showToast } from "./ui";

/* The app shell's live state the modal needs. Getters, not snapshots: the
   queue changes under the modal between renders. */
export interface PreviewContext {
  items(): Item[];
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
  // items.
  if (!item) return;
  if (mode === "compare" && item.status !== "done") return;
  if (mode === "beautify" && item.status !== "done" && item.status !== "ready") return;
  selectedId = id;
  beautifyId = id;
  lastFocused = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  previewData = previewCache.get(id) ?? null;
  $("#previewTitle").textContent = item.name;
  $("#previewMeta").textContent = `${item.format}，${formatDimensions(item.width, item.height)}，${formatBytes(item.originalSize)}，${item.path}`;
  $("#previewOriginalSize").textContent = formatBytes(item.originalSize);
  $("#previewCompressedSize").textContent = item.passedThrough ? formatBytes(item.originalSize) : formatBytes(item.compressedSize);
  $("#previewSaved").textContent = item.status === "done"
    ? item.passedThrough
      ? "原图已是最优"
      : item.compressedSize <= item.originalSize
        ? `−${pct(item.savedPercent)}`
        : `+${pct((item.compressedSize / Math.max(1, item.originalSize) - 1) * 100)}`
    : "待处理";
  $("#previewDetails").textContent = item.status === "done"
    ? `${item.outputFormat || item.format}，${item.outputName || item.name}`
    : "待处理";
  $<HTMLButtonElement>("#previewExportBtn").disabled = item.status !== "done";
  $("#compareLoading").hidden = false;
  $("#compareFrame").hidden = true;
  compareHintSeen = false;
  $("#compareHint").classList.remove("hidden");
  setComparePosition(50);
  switchPreviewMode(mode);
  openModal($("#previewModal"));
  $("#previewClose").focus({ preventScroll: true });

  if (mode === "compare") {
    if (previewData) {
      paintPreview(previewData);
      return;
    }
    try {
      const data = ctx.demoMode() ? demoPreview(item) : await AnyPic.preview(id);
      previewData = data;
      previewCache.set(id, data);
      if (selectedId === id) paintPreview(data);
    } catch (error) {
      if (selectedId === id) {
        closePreview();
        showError(error, "无法生成预览");
      }
    }
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
  setComparePosition(50);
}

function setComparePosition(position: number) {
  comparePosition = Math.max(2, Math.min(98, position));
  const frame = $("#compareFrame");
  frame.style.setProperty("--compare-pos", `${comparePosition}%`);
  const handle = $("#compareHandle");
  handle.setAttribute("aria-valuenow", String(Math.round(comparePosition)));
  handle.setAttribute("aria-valuetext", `${Math.round(comparePosition)}%`);
}

function bindCompare() {
  const compare = $("#compare");
  const handle = $("#compareHandle");
  let dragging = false;

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
    dragging = true;
    handle.classList.add("dragging");
    compare.setPointerCapture(event.pointerId);
    update(event);
  });
  compare.addEventListener("pointermove", (event) => {
    if (!dragging) return;
    update(event);
  });
  const end = () => {
    dragging = false;
    handle.classList.remove("dragging");
  };
  compare.addEventListener("pointerup", end);
  compare.addEventListener("pointercancel", end);
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
  handle.setAttribute("aria-label", "前后对比分割线");
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

/* Gradient presets mirror the backend's gradientPresets table. */
const GRADIENT_PRESETS: { id: string; label: string; css: string }[] = [
  { id: "indigo", label: "靛蓝", css: "linear-gradient(135deg, #a5b4fc, #4f46e5)" },
  { id: "dusk", label: "暮色", css: "linear-gradient(135deg, #818cf8, #312e81)" },
  { id: "ocean", label: "海洋", css: "linear-gradient(135deg, #67e8f9, #2563eb)" },
  { id: "mint", label: "薄荷", css: "linear-gradient(135deg, #6ee7b7, #0d9488)" },
  { id: "rose", label: "玫瑰", css: "linear-gradient(135deg, #fda4af, #e11d48)" },
  { id: "sunset", label: "落日", css: "linear-gradient(135deg, #fcd34d, #f97316)" },
  { id: "slate", label: "石墨", css: "linear-gradient(135deg, #f3f4f6, #9ca3af)" },
  { id: "charcoal", label: "炭黑", css: "linear-gradient(135deg, #4b5563, #111827)" },
];

function defaultFrameStyle(): FrameStyle {
  return {
    background: "gradient",
    backgroundColor: "#e7e7ea",
    backgroundGradient: "indigo",
    radius: "medium",
    ring: true,
    shadow: "soft",
    padding: "normal",
    ratio: "origin",
  };
}

/* The last tuned style is remembered in localStorage, the same way the view
   preference is: it is page state, not a compression setting. */
export function loadFrameStyle() {
  try {
    const stored = localStorage.getItem("anypic-frame-style");
    if (stored) frameStyle = { ...defaultFrameStyle(), ...JSON.parse(stored) };
  } catch {
    /* storage can be unavailable */
  }
}

function saveFrameStyle() {
  try {
    localStorage.setItem("anypic-frame-style", JSON.stringify(frameStyle));
  } catch {
    /* storage can be unavailable */
  }
}

function populateBeautifyControls() {
  const swatches = $("#beautifySwatches");
  for (const hex of BG_SWATCHES) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "swatch";
    button.style.backgroundColor = hex;
    button.title = hex;
    button.dataset.color = hex;
    button.setAttribute("aria-label", `纯色 ${hex}`);
    swatches.appendChild(button);
  }
  const picker = document.createElement("input");
  picker.type = "color";
  picker.id = "beautifyColor";
  picker.value = "#e7e7ea";
  picker.title = "自定义颜色";
  picker.setAttribute("aria-label", "自定义颜色");
  swatches.appendChild(picker);

  const gradients = $("#beautifyGradients");
  for (const preset of GRADIENT_PRESETS) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "swatch gradient-chip";
    button.style.background = preset.css;
    button.title = preset.label;
    button.dataset.gradient = preset.id;
    button.setAttribute("aria-label", `渐变 ${preset.label}`);
    gradients.appendChild(button);
  }
}

/* Keeps the workbench controls in sync with the frameStyle being tuned. */
function syncBeautifyControls() {
  $$<HTMLButtonElement>("#beautifyBgKind button").forEach((button) => {
    const active = button.dataset.kind === frameStyle.background;
    button.classList.toggle("active", active);
    button.setAttribute("aria-pressed", String(active));
  });
  $("#beautifySwatches").hidden = frameStyle.background !== "solid";
  $("#beautifyGradients").hidden = frameStyle.background !== "gradient";

  $$<HTMLButtonElement>("#beautifySwatches .swatch").forEach((button) => {
    button.classList.toggle("active", frameStyle.background === "solid" && button.dataset.color === frameStyle.backgroundColor);
  });
  $<HTMLInputElement>("#beautifyColor").value = /^#[0-9a-f]{6}$/.test(frameStyle.backgroundColor)
    ? frameStyle.backgroundColor
    : "#e7e7ea";
  $$<HTMLButtonElement>("#beautifyGradients .swatch").forEach((button) => {
    button.classList.toggle("active", frameStyle.background === "gradient" && button.dataset.gradient === frameStyle.backgroundGradient);
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
    if (seq === beautifyRenderSeq) showError(error, "无法生成美化预览");
  } finally {
    if (seq === beautifyRenderSeq) $("#beautifyLoading").hidden = true;
  }
}

function switchPreviewMode(mode: "compare" | "beautify") {
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
  if (!compare) void renderBeautify();
}

/* Applies the tuned style: the backend writes 原名-美化.png beside the
   original and the item restarts, so the automatic flow saves it again. */
async function applyBeautify() {
  if (beautifyId === null || ctx.batchRunning()) return;
  const id = beautifyId;
  const item = ctx.items().find((entry) => entry.id === id);
  if (ctx.demoMode()) {
    showToast("浏览器预览模式无法应用美化，请在 MyGo 桌面应用中使用", "info");
    return;
  }
  try {
    const snap = await AnyPic.decorateItem(id, frameStyle);
    closePreview();
    ctx.applySnapshot(snap, false);
    ctx.renderAll();
    showToast(`已应用美化：${item ? item.name : ""} 等待压缩`, "success");
    if (ctx.autoProcessEnabled()) ctx.startAutoRun([id], false);
  } catch (error) {
    showError(error, "美化失败");
  }
}

function resetBeautify() {
  frameStyle = defaultFrameStyle();
  saveFrameStyle();
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
    saveFrameStyle();
    void renderBeautify();
  });
  $("#beautifySwatches").addEventListener("click", (event) => {
    const button = (event.target as HTMLElement).closest<HTMLButtonElement>(".swatch");
    if (!button) return;
    frameStyle.background = "solid";
    frameStyle.backgroundColor = button.dataset.color ?? "#e7e7ea";
    saveFrameStyle();
    void renderBeautify();
  });
  $("#beautifyColor").addEventListener("input", (event) => {
    frameStyle.background = "solid";
    frameStyle.backgroundColor = (event.target as HTMLInputElement).value;
    saveFrameStyle();
    void renderBeautify();
  });
  $("#beautifyGradients").addEventListener("click", (event) => {
    const button = (event.target as HTMLElement).closest<HTMLButtonElement>(".swatch");
    if (!button) return;
    frameStyle.background = "gradient";
    frameStyle.backgroundGradient = button.dataset.gradient ?? "indigo";
    saveFrameStyle();
    void renderBeautify();
  });

  $("#beautifyRadius").addEventListener("click", (event) => {
    const button = (event.target as HTMLElement).closest<HTMLButtonElement>("button[data-radius]");
    if (!button) return;
    frameStyle.radius = button.dataset.radius ?? "medium";
    saveFrameStyle();
    void renderBeautify();
  });
  $("#beautifyRing").addEventListener("change", (event) => {
    frameStyle.ring = (event.target as HTMLInputElement).checked;
    saveFrameStyle();
    void renderBeautify();
  });
  $("#beautifyShadow").addEventListener("click", (event) => {
    const button = (event.target as HTMLElement).closest<HTMLButtonElement>("button[data-shadow]");
    if (!button) return;
    frameStyle.shadow = button.dataset.shadow ?? "soft";
    saveFrameStyle();
    void renderBeautify();
  });
  $("#beautifyPadding").addEventListener("click", (event) => {
    const button = (event.target as HTMLElement).closest<HTMLButtonElement>("button[data-padding]");
    if (!button) return;
    frameStyle.padding = button.dataset.padding ?? "normal";
    saveFrameStyle();
    void renderBeautify();
  });
  $("#beautifyRatio").addEventListener("click", (event) => {
    const button = (event.target as HTMLElement).closest<HTMLButtonElement>("button[data-ratio]");
    if (!button) return;
    frameStyle.ratio = button.dataset.ratio ?? "origin";
    saveFrameStyle();
    void renderBeautify();
  });

  $("#beautifyReset").addEventListener("click", resetBeautify);
  $("#beautifyApply").addEventListener("click", () => void applyBeautify());
}

/* Wires the whole modal: compare slider and beautify workbench. */
export function bindPreview() {
  bindCompare();
  bindBeautify();
}
