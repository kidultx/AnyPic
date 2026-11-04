import { AnyPic, events, type Batch, type Item, type Preview, type Settings, type Snapshot } from "./mygo";
import { isMyGo, onFileDrop, runtime } from "mygo-runtime";

/* ------------------------------------------------------------------ */
/* helpers                                                             */
/* ------------------------------------------------------------------ */

const $ = <T extends HTMLElement = HTMLElement>(selector: string): T => {
  const el = document.querySelector<T>(selector);
  if (!el) throw new Error(`missing element: ${selector}`);
  return el;
};

const $$ = <T extends Element = HTMLElement>(selector: string): T[] =>
  Array.from(document.querySelectorAll<T>(selector));

const icon = (name: string) => `<svg class="icon" aria-hidden="true"><use href="#i-${name}"></use></svg>`;

const esc = (value: string) =>
  value.replace(/[&<>"']/g, (char) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[char] ?? char);

function formatBytes(bytes: number): string {
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

const pct = (value: number) => `${Math.round(Math.max(0, value))}%`;

function formatDimensions(width: number, height: number): string {
  if (!width || !height) return "未知尺寸";
  return `${width} × ${height}`;
}

function shortenPath(path: string): string {
  if (!path) return "";
  const parts = path.split(/[\\/]/).filter(Boolean);
  if (parts.length <= 2) return path;
  return `…/${parts.slice(-2).join("/")}`;
}

function signature(settings: Settings): string {
  return JSON.stringify([settings.format, settings.quality, settings.maxDimension, settings.suffix]);
}

const wait = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

function setUseIcon(useId: string, name: string) {
  const el = document.getElementById(useId);
  if (el) el.setAttribute("href", `#i-${name}`);
}

const DEFAULT_SHORTCUT = "CmdOrCtrl+Alt+S";

/* ------------------------------------------------------------------ */
/* state                                                               */
/* ------------------------------------------------------------------ */

let settings: Settings = defaultSettings();
let items: Item[] = [];
let batch: Batch = { running: false, total: 0, done: 0, failed: 0, canceled: false };
let filter: "all" | "done" | "error" = "all";
let view: "list" | "grid" = "list";
let dirty = false;
let compressedSignature = "";
let demoMode = false;
let demoCanceled = false;
let selectedId: number | null = null;
let previewData: Preview | null = null;
let comparePosition = 50;
let compareHintSeen = false;
let raf = 0;
let saveTimer: number | undefined;
let savePending = false;
let isMac = navigator.platform.toLowerCase().includes("mac");
let lastFocused: HTMLElement | null = null;
let recordingShortcut = false;
/* Live beautify previews keyed by background id; rendered by the backend
   against the newest queued screenshot (or a synthetic window). */
let beautifySwatches: Map<string, string> | null = null;
let beautifySwatchesLoading = false;
let beautifySwatchesStale = true;
/* Padding the current previews were rendered with, so changing the padding
   control refreshes them. */
let beautifyPreviewPadding = -1;

const rowCache = new Map<number, HTMLElement>();
const previewCache = new Map<number, Preview>();
let confirmResolve: ((value: boolean) => void) | null = null;

function defaultSettings(): Settings {
  return {
    format: "auto",
    quality: 82,
    maxDimension: 0,
    outputMode: "alongside",
    outputFolder: "",
    suffix: "-anypic",
    theme: "system",
    screenshotShortcut: DEFAULT_SHORTCUT,
    screenshotClipboard: "on",
    hideDockWhenTray: "on",
    openAtLogin: "off",
    screenshotStyle: { mode: "off", background: "auto", padding: 10 },
  };
}

const BEAUTIFY_BACKGROUNDS: Record<string, string> = {
  auto: "自动取色",
  breeze: "Breeze",
  candy: "Candy",
  crimson: "Crimson",
  falcon: "Falcon",
  meadow: "Meadow",
  midnight: "Midnight",
  raindrop: "Raindrop",
  sunset: "Sunset",
  ink: "靛蓝",
  graphite: "石墨",
  paper: "纸白",
};

/* Fallback chip colors for the swatch grid; once the backend's live previews
   arrive, the real rendered thumbnails take over. */
const BEAUTIFY_FALLBACKS: Record<string, string> = {
  auto: "linear-gradient(135deg, #a8edea, #d3a8f3 45%, #ff9d6f)",
  breeze: "linear-gradient(135deg, #CF2F98, #6A3DEC)",
  candy: "linear-gradient(135deg, #A58EFB, #E9BFF8)",
  crimson: "linear-gradient(135deg, #FF6363, #733434)",
  falcon: "linear-gradient(135deg, #BDE3EC, #363654)",
  meadow: "linear-gradient(135deg, #59D499, #A0872D)",
  midnight: "linear-gradient(135deg, #4CC8C8, #202033)",
  raindrop: "linear-gradient(135deg, #8EC7FB, #1C55AA)",
  sunset: "linear-gradient(135deg, #FFCF73, #FF7A2F)",
  ink: "#4F46E5",
  graphite: "#1F2937",
  paper: "#F4F1EA",
};

function newestScreenshotID(): number | null {
  for (let i = items.length - 1; i >= 0; i--) {
    if (items[i]!.screenshot) return items[i]!.id;
  }
  return null;
}

function upsertItem(item: Item) {
  const index = items.findIndex((existing) => existing.id === item.id);
  const isNew = index === -1;
  if (isNew) items.push(item);
  else items[index] = item;
  previewCache.delete(item.id);
  // A fresh capture becomes the sample the beautifier previews render
  // against; status updates for an existing item must not re-render them.
  if (isNew && item.screenshot) {
    beautifySwatchesStale = true;
    if (!$("#beautifyOptions").hidden) void refreshBeautifySwatches();
  }
}

/* ------------------------------------------------------------------ */
/* boot                                                                */
/* ------------------------------------------------------------------ */

async function init() {
  detectPlatform();
  bindEvents();
  loadViewPreference();
  loadSidebarWidth();
  bindSidebarResize();
  applyTheme(settings.theme);

  if (isMyGo()) {
    try {
      const snapshot = await AnyPic.state();
      applySnapshot(snapshot);
      events.anypicItem.on((item) => {
        upsertItem(item);
        scheduleRender();
      });
      events.anypicBatch.on((next) => handleBatch(next));
      events.anypicCommand.on((command) => void handleCommand(command));
      onFileDrop(({ paths }) => void addPaths(paths));
      void AnyPic.info()
        .then((info) => {
          $("#aboutVersion").textContent = `${info.version}，${info.platform}/${info.arch}，MyGo`;
          if (!info.screenshotActive) {
            showToast(`截图快捷键 ${formatShortcut(info.screenshotShortcut)} 未生效，可能已被其他应用占用`, "error");
          }
        })
        .catch(() => {});
    } catch (error) {
      showError(error, "无法连接压缩引擎");
      applyDemoState();
    }
  } else {
    applyDemoState();
  }

  renderAll();
}

function detectPlatform() {
  try {
    if (isMyGo()) isMac = runtime().platform === "darwin";
  } catch {
    /* the dev server opened in a browser has no runtime */
  }
  document.documentElement.classList.toggle("is-mac", isMac);
  $("#screenPermissionBtn").hidden = !(isMac && isMyGo());
  $("#windowSettings").hidden = !isMac;
  $$<HTMLElement>("[data-shortcut]").forEach((el) => {
    const shortcut = el.dataset.shortcut;
    if (shortcut === "open") el.textContent = isMac ? "⌘ O" : "Ctrl O";
    else if (shortcut === "compress") el.textContent = isMac ? "⌘ ↵" : "Ctrl ↵";
  });
}

function loadViewPreference() {
  try {
    const stored = localStorage.getItem("anypic-view");
    if (stored === "grid" || stored === "list") view = stored;
  } catch {
    /* storage can be unavailable */
  }
}

/* The sidebar's width: dragged by its edge, remembered across launches. */
const SIDE_MIN = 200;
const SIDE_MAX = 400;

function applySidebarWidth(px: number) {
  const width = Math.min(SIDE_MAX, Math.max(SIDE_MIN, Math.round(px)));
  document.documentElement.style.setProperty("--side-w", `${width}px`);
  return width;
}

function loadSidebarWidth() {
  try {
    const stored = Number(localStorage.getItem("anypic-side-w"));
    if (stored >= SIDE_MIN && stored <= SIDE_MAX) applySidebarWidth(stored);
  } catch {
    /* storage can be unavailable */
  }
}

function bindSidebarResize() {
  const edge = $("#sideEdge");
  let dragging = false;
  edge.addEventListener("pointerdown", (event) => {
    dragging = true;
    edge.setPointerCapture(event.pointerId);
    document.body.classList.add("side-drag");
  });
  edge.addEventListener("pointermove", (event) => {
    if (!dragging) return;
    applySidebarWidth(event.clientX);
  });
  const stop = () => {
    if (!dragging) return;
    dragging = false;
    document.body.classList.remove("side-drag");
    const width = applySidebarWidth(parseFloat(getComputedStyle(document.documentElement).getPropertyValue("--side-w")));
    try {
      localStorage.setItem("anypic-side-w", String(width));
    } catch {
      /* storage can be unavailable */
    }
  };
  edge.addEventListener("pointerup", stop);
  edge.addEventListener("pointercancel", stop);
  // A keyboard path, so the split stays adjustable without a pointer.
  edge.addEventListener("keydown", (event) => {
    const step = event.shiftKey ? 32 : 8;
    if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
    event.preventDefault();
    const current = parseFloat(getComputedStyle(document.documentElement).getPropertyValue("--side-w")) || 248;
    const width = applySidebarWidth(current + (event.key === "ArrowRight" ? step : -step));
    try {
      localStorage.setItem("anypic-side-w", String(width));
    } catch {
      /* storage can be unavailable */
    }
  });
}

function applySnapshot(snapshot: Snapshot, includeSettings = true) {
  // A thumbnail may have arrived before the method's response. Keep it when
  // the queue-only snapshot was taken just before the thumbnail finished.
  const previousThumbs = new Map(items.map((item) => [item.id, item.thumb]));
  const previousSample = newestScreenshotID();
  items = snapshot.items.map((item) => {
    if (item.thumb) return item;
    const thumb = previousThumbs.get(item.id);
    return thumb ? { ...item, thumb } : item;
  });
  // The beautify previews render against the newest queued screenshot.
  if (newestScreenshotID() !== previousSample) beautifySwatchesStale = true;
  if (includeSettings) {
    settings = { ...defaultSettings(), ...snapshot.settings };
    applyTheme(settings.theme);
  }
  batch = snapshot.batch;
  if (includeSettings) {
    if (!snapshot.batch.running && items.some((item) => item.status === "done")) {
      compressedSignature = signature(settings);
    }
    dirty = compressedSignature !== "" && signature(settings) !== compressedSignature && items.some((item) => item.status === "done");
  }
  syncSettingsToUI();
}

function applyDemoState() {
  demoMode = true;
  settings = defaultSettings();
  const palettes = [
    ["#ffd5a6", "#ff9963", "#d85a67", "#6b3b65"],
    ["#cfece7", "#7bc9cb", "#438ea9", "#2d4f7f"],
    ["#ffd9dc", "#f5a1a7", "#b96087", "#503562"],
    ["#d9d4f7", "#9c8fe0", "#6761b7", "#3e3a73"],
  ];
  const names = [
    ["sunrise-over-dolomites.jpg", 4_982_144, 6016, 4016, 1_124_352, "WEBP", 77.4],
    ["studio-portrait.png", 2_448_000, 3200, 4000, 893_440, "WEBP", 63.5],
    ["product-shot-01.jpg", 1_834_112, 2400, 1600, 412_672, "WEBP", 77.5],
    ["mountain-lake.tiff", 12_042_240, 7680, 4320, 2_482_176, "JPEG", 79.4],
  ] as const;
  items = names.map((entry, index) => {
    const [name, original, width, height, compressed, outputFormat, saved] = entry;
    const ready = index === 3;
    const thumb = demoThumb(index, palettes[index] ?? palettes[0]!);
    return {
      id: index + 1,
      name,
      path: `/Users/demo/Pictures/${name}`,
      dir: "/Users/demo/Pictures",
      format: name.endsWith(".png") ? "PNG" : name.endsWith(".tiff") ? "TIFF" : "JPEG",
      width,
      height,
      originalSize: original,
      compressedSize: ready ? 0 : compressed,
      status: ready ? "ready" : "done",
      outputFormat: ready ? "" : outputFormat,
      outputName: ready ? "" : name.replace(/\.(jpg|png|tiff)$/i, "-anypic.webp"),
      thumb,
      screenshot: false,
      hasAlpha: name.endsWith(".png"),
      passedThrough: false,
      savedPercent: ready ? 0 : saved,
    };
  });
  batch = { running: false, total: items.length, done: 3, failed: 0, canceled: false };
  compressedSignature = signature(settings);
  syncSettingsToUI();
  applyTheme("system");
  $("#aboutVersion").textContent = "0.1.0，浏览器预览";
  window.setTimeout(() => showToast("浏览器预览模式。请在 MyGo 桌面应用中启用全部功能", "info"), 700);
}

function demoThumb(index: number, palette: readonly string[]): string {
  const [sky1, sky2, hill, deep] = palette;
  const shift = (index * 37) % 120;
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="720" height="540" viewBox="0 0 720 540">
    <defs>
      <linearGradient id="sky" x1="0" y1="0" x2="0.4" y2="1">
        <stop stop-color="${sky1}"/><stop offset=".46" stop-color="${sky2}"/><stop offset="1" stop-color="${hill}"/>
      </linearGradient>
      <linearGradient id="m" x1="0" y1="0" x2="0" y2="1">
        <stop stop-color="${deep}" stop-opacity=".42"/><stop offset="1" stop-color="${deep}"/>
      </linearGradient>
    </defs>
    <rect width="720" height="540" fill="url(#sky)"/>
    <circle cx="${520 - shift}" cy="${150 + shift / 4}" r="58" fill="#fff3d7" opacity=".86"/>
    <path d="M0 378 L104 288 L186 344 L286 226 L394 348 L474 292 L590 368 L720 276 L720 540 L0 540Z" fill="url(#m)" opacity=".7"/>
    <path d="M0 468 L146 350 L248 438 L372 292 L500 430 L616 356 L720 432 L720 540 L0 540Z" fill="${deep}" opacity=".74"/>
    <path d="M0 494 C120 456 242 512 372 478 C512 442 598 496 720 464 L720 540 L0 540Z" fill="${deep}" opacity=".9"/>
  </svg>`;
  return `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`;
}

/* ------------------------------------------------------------------ */
/* rendering                                                           */
/* ------------------------------------------------------------------ */

function scheduleRender() {
  if (raf) return;
  raf = requestAnimationFrame(() => {
    raf = 0;
    renderAll();
  });
}

function renderAll() {
  document.body.classList.toggle("has-items", items.length > 0);
  renderIntro();
  renderSettings();
  renderStage();
  renderStats();
  renderQueue();
  renderFooter();
  renderActions();
}

function renderIntro() {
  $("#countBadge").textContent = String(items.length);
}

function renderSettings() {
  const format = settings.format;
  $$<HTMLButtonElement>("#formatSeg button").forEach((button) => {
    const active = button.dataset.format === format;
    button.classList.toggle("active", active);
    button.setAttribute("aria-pressed", String(active));
  });

  const quality = $<HTMLInputElement>("#quality");
  if (document.activeElement !== quality) quality.value = String(settings.quality);
  $("#qualityValue").textContent = String(settings.quality);
  const fill = ((settings.quality - 30) / 70) * 100;
  $("#qualityFill").style.width = `${Math.max(0, Math.min(100, fill))}%`;
  $("#qualityLabel").textContent = qualityLabel(settings.quality);

  const qualityField = quality.closest(".quality-field");
  qualityField?.classList.toggle("disabled", format === "png");
  quality.disabled = format === "png" || batch.running;

  const maxDim = $<HTMLSelectElement>("#maxDim");
  if (document.activeElement !== maxDim) maxDim.value = String(settings.maxDimension);

  const suffix = $<HTMLInputElement>("#suffix");
  if (document.activeElement !== suffix) suffix.value = settings.suffix;

  if (!recordingShortcut) $("#shortcutValue").textContent = formatShortcut(settings.screenshotShortcut);
  $("#screenshotBtn").title = `截取屏幕区域（${formatShortcut(settings.screenshotShortcut)}）`;

  $("#formatHint").textContent =
    format === "auto" ? "保持原格式" : format === "jpeg" ? "有损" : format === "png" ? "无损" : "有损";

  if (settings.outputMode === "folder" && settings.outputFolder) {
    $("#outputLabel").textContent = shortenPath(settings.outputFolder);
    $("#outputLabel").title = settings.outputFolder;
    $("#resetLocation").hidden = false;
  } else {
    $("#outputLabel").textContent = "原图旁边";
    $("#outputLabel").title = "";
    $("#resetLocation").hidden = true;
  }

  const tips: Record<string, [string, string]> = {
    auto: ["保持原格式", "原格式保留；其他格式转为 JPG 或 PNG。"],
    webp: ["WebP", "体积更小，支持透明。"],
    jpeg: ["JPG", "兼容性最好，适合照片。"],
    png: ["PNG", "无损压缩，质量设置不适用。"],
  };
  const [tipTitle, tipText] = tips[format] ?? tips.auto!;
  $("#tipTitle").textContent = tipTitle;
  $("#tipText").textContent = tipText;
}

function qualityLabel(quality: number): string {
  if (quality >= 92) return "几乎无损";
  if (quality >= 78) return "平衡";
  if (quality >= 60) return "更小体积";
  if (quality >= 45) return "强力压缩";
  return "极限压缩";
}

function renderStage() {
  const done = items.filter((item) => item.status === "done");

  const hasDone = done.length > 0;
  $("#staleBadge").hidden = !(dirty && hasDone);
  $("#stats").hidden = items.length === 0;
  $("#emptyState").hidden = items.length > 0;
  $("#queue").hidden = items.length === 0;
  $("#stageFooter").hidden = items.length === 0;
}

function renderStats() {
  const totalOriginal = items.reduce((sum, item) => sum + item.originalSize, 0);
  const done = items.filter((item) => item.status === "done");
  const doneOriginal = done.reduce((sum, item) => sum + item.originalSize, 0);
  const doneCompressed = done.reduce((sum, item) => sum + item.compressedSize, 0);
  const saved = Math.max(0, doneOriginal - doneCompressed);
  const savedPercent = doneOriginal > 0 ? (saved / doneOriginal) * 100 : 0;

  $("#statOriginal").textContent = formatBytes(totalOriginal);
  $("#statCompressed").textContent = done.length > 0 ? formatBytes(doneCompressed) : "暂无";
  $("#statSaved").textContent = done.length > 0 ? `${Math.round(savedPercent)}%` : "暂无";
  $("#statSavedFoot").textContent =
    done.length === 0 ? "" : saved > 0 ? `（共 ${formatBytes(saved)}）` : "（无需压缩）";
  $("#summaryDone").style.display = done.length === 0 ? "none" : "";
  $("#summarySaved").style.display = done.length === 0 ? "none" : "";
}

function renderQueue() {
  const list = $("#queueList");
  const filtered = items.filter((item) => {
    if (filter === "done") return item.status === "done";
    if (filter === "error") return item.status === "error";
    return true;
  });

  list.classList.toggle("grid", view === "grid");
  $("#queueColumns").hidden = view === "grid";
  const activeIds = new Set(items.map((item) => item.id));
  for (const [id, row] of rowCache) {
    if (!activeIds.has(id)) {
      row.remove();
      rowCache.delete(id);
    }
  }

  const visibleIds = new Set<number>();
  for (const item of filtered) {
    visibleIds.add(item.id);
    let row = rowCache.get(item.id);
    if (!row) {
      row = createRow(item.id);
      rowCache.set(item.id, row);
    }
    updateRow(row, item);
    list.appendChild(row);
  }

  for (const [id, row] of rowCache) {
    if (!visibleIds.has(id)) row.remove();
  }

  const counts = {
    all: items.length,
    done: items.filter((item) => item.status === "done").length,
    error: items.filter((item) => item.status === "error").length,
  };
  $("#tabAllCount").textContent = String(counts.all);
  $("#tabDoneCount").textContent = String(counts.done);
  $("#tabErrorCount").textContent = String(counts.error);
  $$<HTMLButtonElement>(".queue-tab").forEach((button) => {
    const active = button.dataset.filter === filter;
    button.classList.toggle("active", active);
    button.setAttribute("aria-selected", String(active));
  });
  $("#queueEmptyFilter").hidden = filtered.length > 0;
}

function createRow(id: number): HTMLElement {
  const row = document.createElement("article");
  row.className = "queue-item";
  row.dataset.id = String(id);
  row.innerHTML = `
    <div class="thumb">
      <div class="thumb-placeholder">${icon("image")}</div>
      <span class="thumb-state" hidden></span>
    </div>
    <div class="item-main">
      <p class="item-name"></p>
      <div class="item-meta"></div>
      <div class="item-error" hidden></div>
    </div>
    <div class="item-result">
      <div class="result-sizes"></div>
      <span class="saving-pill"></span>
    </div>
    <div class="item-actions">
      <button class="icon-btn" data-action="preview" title="对比预览" aria-label="对比预览">${icon("eye")}</button>
      <button class="icon-btn" data-action="export" title="导出这张" aria-label="导出这张">${icon("download")}</button>
      <button class="icon-btn" data-action="remove" title="移除" aria-label="移除">${icon("x")}</button>
    </div>`;

  row.addEventListener("click", (event) => {
    const target = event.target as HTMLElement;
    if (target.closest("button")) return;
    const item = items.find((entry) => entry.id === id);
    if (!item) return;
    if (item.status === "done") void openPreview(item.id);
    else if (item.status === "error" && item.error) showToast(item.error, "error");
  });

  row.querySelector<HTMLButtonElement>('[data-action="preview"]')?.addEventListener("click", (event) => {
    event.stopPropagation();
    void openPreview(id);
  });
  row.querySelector<HTMLButtonElement>('[data-action="export"]')?.addEventListener("click", (event) => {
    event.stopPropagation();
    void exportOne(id);
  });
  row.querySelector<HTMLButtonElement>('[data-action="remove"]')?.addEventListener("click", (event) => {
    event.stopPropagation();
    void removeItem(id);
  });
  return row;
}

function updateRow(row: HTMLElement, item: Item) {
  row.dataset.id = String(item.id);
  row.title = item.path ? `${item.name}\n${item.path}` : item.name;
  row.classList.toggle("is-clickable", item.status === "done" || item.status === "error");

  const thumb = row.querySelector<HTMLElement>(".thumb")!;
  let image = thumb.querySelector<HTMLImageElement>("img");
  if (item.thumb) {
    if (!image) {
      image = document.createElement("img");
      image.alt = "";
      image.draggable = false;
      thumb.prepend(image);
    }
    if (image.getAttribute("src") !== item.thumb) image.src = item.thumb;
    const placeholder = thumb.querySelector<HTMLElement>(".thumb-placeholder");
    if (placeholder) placeholder.hidden = true;
  } else {
    image?.remove();
    const placeholder = thumb.querySelector<HTMLElement>(".thumb-placeholder");
    if (placeholder) placeholder.hidden = false;
  }

  const state = thumb.querySelector<HTMLElement>(".thumb-state")!;
  state.className = "thumb-state";
  if (item.status === "working") {
    state.hidden = false;
    state.classList.add("working");
    state.innerHTML = icon("loader");
  } else if (item.status === "done") {
    state.hidden = false;
    state.innerHTML = icon("check");
  } else if (item.status === "error") {
    state.hidden = false;
    state.classList.add("error");
    state.innerHTML = icon("alert");
  } else {
    state.hidden = true;
    state.innerHTML = "";
  }

  const name = row.querySelector<HTMLElement>(".item-name")!;
  name.textContent = item.name;

  const meta = row.querySelector<HTMLElement>(".item-meta")!;
  const formatClass = item.format.toLowerCase();
  meta.innerHTML = `<span class="format-chip ${esc(formatClass)}">${esc(item.format || "IMG")}</span><span>${formatDimensions(
    item.width,
    item.height,
  )}</span>`;

  const error = row.querySelector<HTMLElement>(".item-error")!;
  if (item.status === "error" && item.error) {
    error.hidden = false;
    error.textContent = item.error;
  } else {
    error.hidden = true;
  }

  const sizes = row.querySelector<HTMLElement>(".result-sizes")!;
  const pill = row.querySelector<HTMLElement>(".saving-pill")!;
  pill.className = "saving-pill";

  if (item.status === "done") {
    if (item.passedThrough) {
      sizes.innerHTML = `<strong>${formatBytes(item.originalSize)}</strong><span>原图</span>`;
      pill.classList.add("neutral");
      pill.textContent = "已是最优";
    } else {
      const saved = item.originalSize - item.compressedSize;
      const savedPct = item.originalSize > 0 ? (saved / item.originalSize) * 100 : 0;
      sizes.innerHTML = `<span>${formatBytes(item.originalSize)}</span><svg class="icon arrow" aria-hidden="true"><use href="#i-arrow-right"></use></svg><strong>${formatBytes(
        item.compressedSize,
      )}</strong>`;
      if (saved >= 0) {
        pill.textContent = `${item.outputFormat || ""} −${pct(savedPct)}`.trim();
      } else {
        pill.classList.add("neutral");
        pill.textContent = `${item.outputFormat || ""} +${pct(-savedPct)}`.trim();
      }
    }
  } else if (item.status === "working") {
    sizes.innerHTML = `<span>正在优化…</span>`;
    pill.classList.add("working");
    pill.innerHTML = `${icon("loader")} 压缩中`;
  } else if (item.status === "error") {
    sizes.innerHTML = `<span>未能压缩</span>`;
    pill.classList.add("error");
    pill.textContent = "失败";
  } else {
    sizes.innerHTML = `<span>${formatBytes(item.originalSize)}</span><span>待压缩</span>`;
    pill.classList.add("neutral");
    pill.textContent = "待处理";
  }

  const previewButton = row.querySelector<HTMLButtonElement>('[data-action="preview"]')!;
  const exportButton = row.querySelector<HTMLButtonElement>('[data-action="export"]')!;
  const removeButton = row.querySelector<HTMLButtonElement>('[data-action="remove"]')!;
  previewButton.disabled = item.status !== "done";
  exportButton.disabled = item.status !== "done" || batch.running;
  removeButton.disabled = batch.running;
}

function renderFooter() {
  if (items.length === 0) return;
  const done = items.filter((item) => item.status === "done");
  const failed = items.filter((item) => item.status === "error");
  const total = items.length;
  const completed = done.length + failed.length;
  const percent = total > 0 ? Math.round((completed / total) * 100) : 0;

  let status = "准备就绪";
  if (batch.running) status = `正在压缩，${batch.done || completed} / ${batch.total || total}`;
  else if (batch.canceled) status = "压缩已取消";
  else if (done.length === total && total > 0) status = "压缩完成";
  else if (failed.length > 0 && completed === total) status = "部分图片未能压缩";
  else if (done.length > 0) status = `${done.length} 张已完成，${total - completed} 张待处理`;

  $("#batchStatus").textContent = status;
  $("#batchPercent").textContent = `${percent}%`;
  $("#progressFill").style.transform = `scaleX(${percent / 100})`;
  $<HTMLButtonElement>("#exportBtn").disabled = batch.running || done.length === 0;
  $("#exportBtn").title = done.length === 0 ? "先压缩图片" : `导出 ${done.length} 张压缩后的图片`;

}

function renderActions() {
  const running = batch.running;
  const compress = $<HTMLButtonElement>("#compressBtn");
  compress.disabled = running || items.length === 0;
  compress.classList.toggle("loading", running);
  const label = compress.querySelector<HTMLElement>(".primary-label");
  if (label) {
    const hasDone = items.some((item) => item.status === "done");
    const hasPending = items.some((item) => item.status !== "done");
    label.textContent = running
      ? "正在压缩…"
      : items.length === 0
        ? "开始压缩"
        : dirty
          ? "应用新设置"
          : hasPending
            ? "压缩全部"
            : hasDone
              ? "重新压缩"
              : "开始压缩";
  }

  $("#cancelBtn").hidden = !running;
  $<HTMLButtonElement>("#clearBtn").disabled = running || items.length === 0;
  $<HTMLButtonElement>("#addBtn").disabled = running;
  $<HTMLButtonElement>("#screenshotBtn").disabled = running;
  $<HTMLButtonElement>("#emptyAddBtn").disabled = running;
  $<HTMLButtonElement>("#outputBtn").disabled = running;
  $<HTMLButtonElement>("#resetBtn").disabled = running;
  $<HTMLButtonElement>("#suffix").disabled = running;
  $<HTMLButtonElement>("#maxDim").disabled = running;
  $$<HTMLButtonElement>("#formatSeg button").forEach((button) => {
    button.disabled = running;
  });
  $<HTMLInputElement>("#quality").disabled = running || settings.format === "png";
  $$<HTMLButtonElement>("#beautifyGrid button").forEach((button) => {
    button.disabled = running;
  });
  $$<HTMLButtonElement>("#beautifyPad button").forEach((button) => {
    button.disabled = running;
  });
}

/* ------------------------------------------------------------------ */
/* events                                                              */
/* ------------------------------------------------------------------ */

function syncViewSwitch() {
  $$<HTMLButtonElement>("#viewSwitch button").forEach((button) => {
    const active = button.dataset.view === view;
    button.classList.toggle("active", active);
    button.setAttribute("aria-pressed", String(active));
  });
}

function bindEvents() {
  $("#addBtn").addEventListener("click", () => void pickFiles());
  $("#emptyAddBtn").addEventListener("click", () => void pickFiles());
  $("#screenshotBtn").addEventListener("click", () => void captureScreenshot());
  $("#compressBtn").addEventListener("click", () => void compressAll());
  $("#cancelBtn").addEventListener("click", () => void cancelCompression());
  $("#exportBtn").addEventListener("click", () => void exportAll());
  $("#clearBtn").addEventListener("click", () => void clearQueue());
  $("#outputBtn").addEventListener("click", () => void chooseOutputFolder());
  $("#resetLocation").addEventListener("click", () => void setSettings({ outputMode: "alongside" }));
  $("#themeBtn").addEventListener("click", cycleTheme);
  $("#shortcutBtn").addEventListener("click", () => {
    if (recordingShortcut) stopShortcutRecording();
    else startShortcutRecording();
  });
  $("#clipboardToggle").addEventListener("change", (event) => {
    const on = (event.target as HTMLInputElement).checked;
    void setSettings({ screenshotClipboard: on ? "on" : "off" });
    showToast(on ? "截图后将复制到剪贴板" : "截图后不再复制到剪贴板", "info");
  });
  $("#beautifyToggle").addEventListener("change", (event) => {
    const on = (event.target as HTMLInputElement).checked;
    void setSettings({ screenshotStyle: { ...settings.screenshotStyle, mode: on ? "on" : "off" } });
    showToast(on ? "截图将在压缩时自动美化" : "截图美化已关闭", on ? "success" : "info");
  });
  $("#beautifyGrid").addEventListener("click", (event) => {
    const button = (event.target as HTMLElement).closest<HTMLButtonElement>("button[data-bg]");
    if (!button || button.disabled) return;
    const background = button.dataset.bg ?? "auto";
    void setSettings({ screenshotStyle: { ...settings.screenshotStyle, background } });
  });
  $("#beautifyPad").addEventListener("click", (event) => {
    const button = (event.target as HTMLElement).closest<HTMLButtonElement>("button[data-pad]");
    if (!button || button.disabled) return;
    const padding = Number(button.dataset.pad);
    void setSettings({ screenshotStyle: { ...settings.screenshotStyle, padding } });
  });
  $("#dockToggle").addEventListener("change", (event) => {
    const on = (event.target as HTMLInputElement).checked;
    void setSettings({ hideDockWhenTray: on ? "on" : "off" });
    showToast(on ? "关闭窗口后将隐藏 Dock 图标" : "关闭窗口后保留 Dock 图标", "info");
  });
  $("#openAtLoginToggle").addEventListener("change", (event) => {
    const on = (event.target as HTMLInputElement).checked;
    void setSettings({ openAtLogin: on ? "on" : "off" }, (next) => {
      // The backend may refuse the login item; the toggle follows the truth.
      const input = event.target as HTMLInputElement;
      input.checked = next.openAtLogin === "on";
    });
    showToast(on ? "AnyPic 将在登录时启动" : "AnyPic 不再随登录启动", on ? "success" : "info");
  });
  $("#screenPermissionBtn").addEventListener("click", () => {
    if (demoMode) return;
    void AnyPic.openScreenRecordingSettings().catch(() => {
      showToast("请手动打开「系统设置 → 隐私与安全性 → 屏幕录制」", "info");
    });
  });
  document.addEventListener(
    "keydown",
    (event) => {
      if (!recordingShortcut) return;
      event.preventDefault();
      event.stopPropagation();
      if (event.key === "Escape") {
        stopShortcutRecording();
        return;
      }
      const accelerator = acceleratorFromEvent(event);
      if (!accelerator) return;
      if (isReservedShortcut(accelerator)) {
        showToast("这个组合键被系统占用，请换一个", "error");
        return;
      }
      void applyShortcut(accelerator);
    },
    true,
  );
  document.addEventListener(
    "click",
    (event) => {
      if (!recordingShortcut) return;
      if ($("#shortcutBtn").contains(event.target as Node)) return;
      stopShortcutRecording();
    },
    true,
  );
  $("#infoBtn").addEventListener("click", openAbout);
  $("#aboutClose").addEventListener("click", closeAbout);
  $$<HTMLElement>("[data-close-about]").forEach((el) => el.addEventListener("click", closeAbout));

  $("#resetBtn").addEventListener("click", () => {
    const next: Settings = {
      ...settings,
      format: "auto",
      quality: 82,
      maxDimension: 0,
      suffix: "-anypic",
    };
    void setSettings(next);
    showToast("已恢复推荐压缩设置", "success");
  });

  $("#formatSeg").addEventListener("click", (event) => {
    const button = (event.target as HTMLElement).closest<HTMLButtonElement>("button[data-format]");
    if (!button || button.disabled) return;
    void setSettings({ format: button.dataset.format ?? "auto" });
  });

  const quality = $<HTMLInputElement>("#quality");
  quality.addEventListener("input", () => {
    const value = Number(quality.value);
    settings = { ...settings, quality: value };
    $("#qualityValue").textContent = String(value);
    $("#qualityFill").style.width = `${((value - 30) / 70) * 100}%`;
    $("#qualityLabel").textContent = qualityLabel(value);
    markDirty();
    scheduleRender();
    queueSave();
  });

  $("#maxDim").addEventListener("change", (event) => {
    void setSettings({ maxDimension: Number((event.target as HTMLSelectElement).value) });
  });

  const suffix = $<HTMLInputElement>("#suffix");
  suffix.addEventListener("input", () => {
    settings = { ...settings, suffix: suffix.value };
    markDirty();
    scheduleRender();
    queueSave();
  });
  suffix.addEventListener("blur", () => {
    if (!suffix.value.trim()) {
      void setSettings({ suffix: "-anypic" });
    }
  });

  $("#viewSwitch").addEventListener("click", (event) => {
    const button = (event.target as HTMLElement).closest<HTMLButtonElement>("button[data-view]");
    if (!button) return;
    const next = button.dataset.view === "grid" ? "grid" : "list";
    if (next === view) return;
    view = next;
    try {
      localStorage.setItem("anypic-view", view);
    } catch {
      /* storage can be unavailable */
    }
    syncViewSwitch();
    renderQueue();
  });
  syncViewSwitch();

  $(".queue-tabs").addEventListener("click", (event) => {
    const button = (event.target as HTMLElement).closest<HTMLButtonElement>("button[data-filter]");
    if (!button) return;
    filter = (button.dataset.filter as "all" | "done" | "error") ?? "all";
    renderQueue();
  });
  $(".queue-tabs").addEventListener("keydown", (event) => {
    if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
    const tabs = $$<HTMLButtonElement>("#queueTabs button[data-filter]");
    const index = tabs.findIndex((tab) => tab.dataset.filter === filter);
    const delta = event.key === "ArrowRight" ? 1 : -1;
    const next = tabs[(index + delta + tabs.length) % tabs.length];
    if (!next) return;
    event.preventDefault();
    next.click();
    next.focus();
  });

  $("#previewClose").addEventListener("click", closePreview);
  $("[data-close-modal]").addEventListener("click", closePreview);
  $("#previewExportBtn").addEventListener("click", () => {
    if (selectedId !== null) void exportOne(selectedId);
  });

  const confirmModal = $<HTMLElement>("#confirmModal");
  $("#confirmCancel").addEventListener("click", () => resolveConfirm(false));
  $("#confirmOk").addEventListener("click", () => resolveConfirm(true));
  $("[data-close-confirm]").addEventListener("click", () => resolveConfirm(false));

  const aboutModal = $<HTMLElement>("#aboutModal");
  aboutModal.addEventListener("click", (event) => {
    if (event.target === aboutModal) closeAbout();
  });
  confirmModal.addEventListener("click", (event) => {
    if (event.target === confirmModal) resolveConfirm(false);
  });

  bindCompare();

  document.addEventListener("keydown", (event) => {
    const meta = isMac ? event.metaKey : event.ctrlKey;
    if (event.key === "Escape") {
      if (confirmResolve) {
        event.preventDefault();
        resolveConfirm(false);
      } else if ($("#previewModal").classList.contains("open")) {
        event.preventDefault();
        closePreview();
      } else if (aboutModal.classList.contains("open")) {
        event.preventDefault();
        closeAbout();
      }
      return;
    }
    if (!meta) return;
    if (event.key.toLowerCase() === "o") {
      event.preventDefault();
      void pickFiles();
    } else if (event.key === "Enter") {
      event.preventDefault();
      void compressAll();
    } else if (event.key.toLowerCase() === "e") {
      event.preventDefault();
      void exportAll();
    } else if (event.key === "Backspace") {
      event.preventDefault();
      void clearQueue();
    } else if (event.shiftKey && event.key.toLowerCase() === "l") {
      event.preventDefault();
      cycleTheme();
    }
  });

  window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => {
    if (settings.theme === "system") applyTheme("system");
  });

  const hasFiles = (event: DragEvent) => Array.from(event.dataTransfer?.types ?? []).includes("Files");
  let dragDepth = 0;
  window.addEventListener("dragenter", (event) => {
    if (!hasFiles(event)) return;
    dragDepth++;
    $("#dropOverlay").classList.add("visible");
  });
  window.addEventListener("dragleave", (event) => {
    if (!hasFiles(event)) return;
    dragDepth = Math.max(0, dragDepth - 1);
    if (dragDepth === 0) $("#dropOverlay").classList.remove("visible");
  });
  window.addEventListener("dragover", (event) => {
    if (!hasFiles(event)) return;
    if (!isMyGo()) {
      event.preventDefault();
      if (event.dataTransfer) event.dataTransfer.dropEffect = "copy";
    }
  });
  window.addEventListener("drop", (event) => {
    if (!hasFiles(event)) return;
    dragDepth = 0;
    $("#dropOverlay").classList.remove("visible");
    if (!isMyGo()) {
      event.preventDefault();
      showToast("浏览器预览无法读取本地路径，请在 MyGo 桌面应用中拖入图片", "info");
    }
  });
}

/* ------------------------------------------------------------------ */
/* actions                                                             */
/* ------------------------------------------------------------------ */

async function pickFiles() {
  if (batch.running) return;
  if (demoMode) {
    showToast("浏览器预览模式：请在 MyGo 桌面应用中选择图片", "info");
    return;
  }
  try {
    const result = await AnyPic.pickFiles();
    applyAddResult(result);
  } catch (error) {
    showError(error, "添加图片失败");
  }
}

async function captureScreenshot() {
  if (batch.running) return;
  if (demoMode) {
    showToast("浏览器预览模式无法截屏，请在 MyGo 桌面应用中使用", "info");
    return;
  }
  try {
    const result = await AnyPic.captureScreenshot();
    // A fresh capture is "ready", so a done/error filter would hide it.
    if (result.added > 0) filter = "all";
    applyAddResult(result);
    if (result.copied) showToast("截图已复制到剪贴板", "info");
  } catch (error) {
    // Canceling the region selection, or a second press while the first
    // capture is still open, is not an error the user needs to see.
    if (isCaptureNotice(error)) return;
    showError(error, "截图失败");
  }
}

function isCaptureNotice(error: unknown) {
  const message = error instanceof Error ? error.message : typeof error === "string" ? error : "";
  return message.includes("已取消截图") || message.includes("正在截图中");
}

async function addPaths(paths: string[]) {
  if (batch.running || demoMode || paths.length === 0) return;
  try {
    const result = await AnyPic.addPaths(paths);
    applyAddResult(result);
  } catch (error) {
    showError(error, "添加图片失败");
  }
}

function applyAddResult(result: { snapshot: Snapshot; added: number; skipped: { name: string; reason: string }[] }) {
  applySnapshot(result.snapshot, false);
  if (result.added === 0 && result.skipped.length === 0) {
    renderAll();
    return;
  }
  if (result.added > 0) {
    showToast(`已添加 ${result.added} 张图片${result.skipped.length ? `，跳过 ${result.skipped.length} 个文件` : ""}`, "success");
    if (result.skipped.length > 0) {
      const first = result.skipped[0]!;
      showToast(`${first.name}：${first.reason}`, "info");
    }
    setTimeout(() => $("#queueScroll").scrollTo({ top: $("#queueScroll").scrollHeight, behavior: "smooth" }), 100);
  } else if (result.skipped.length > 0) {
    const first = result.skipped[0]!;
    showToast(`${first.name}：${first.reason}`, "error");
  }
  renderAll();
}

async function compressAll() {
  if (batch.running || items.length === 0) return;
  dirty = false;
  markDirty();

  if (demoMode) {
    await demoCompress();
    return;
  }

  const runSignature = signature(settings);
  items = items.map((item) => ({
    ...item,
    status: "ready",
    error: "",
    compressedSize: 0,
    savedPercent: 0,
    passedThrough: false,
    outputFormat: "",
    outputName: "",
  }));
  batch = { running: true, total: items.length, done: 0, failed: 0, canceled: false };
  scheduleRender();
  try {
    await AnyPic.compress(settings);
    compressedSignature = runSignature;
    markDirty();
  } catch (error) {
    batch = { ...batch, running: false };
    showError(error, "无法开始压缩");
    renderAll();
  }
}

async function cancelCompression() {
  if (!batch.running) return;
  if (demoMode) {
    demoCanceled = true;
    return;
  }
  try {
    await AnyPic.cancel();
  } catch (error) {
    showError(error, "取消失败");
  }
}

async function removeItem(id: number) {
  if (batch.running) return;
  if (demoMode) {
    items = items.filter((item) => item.id !== id);
    previewCache.delete(id);
    scheduleRender();
    return;
  }
  try {
    const snapshot = await AnyPic.remove([id]);
    applySnapshot(snapshot, false);
    renderAll();
  } catch (error) {
    showError(error, "移除失败");
  }
}

async function clearQueue() {
  if (batch.running || items.length === 0) return;
  const ok = await askConfirm("清空图片队列？", "已压缩的结果会从队列移除，原始图片不会被删除。", "清空队列");
  if (!ok) return;
  if (demoMode) {
    items = [];
    compressedSignature = "";
    batch = { running: false, total: 0, done: 0, failed: 0, canceled: false };
    beautifySwatchesStale = true;
    renderAll();
    return;
  }
  try {
    const snapshot = await AnyPic.clear();
    applySnapshot(snapshot, false);
    compressedSignature = "";
    dirty = false;
    renderAll();
    showToast("队列已清空", "success");
  } catch (error) {
    showError(error, "清空失败");
  }
}

async function chooseOutputFolder() {
  if (batch.running) return;
  if (demoMode) {
    showToast("浏览器预览模式：导出时会选择保存位置", "info");
    return;
  }
  try {
    const next = await AnyPic.chooseOutputFolder();
    applySettingsFromBackend(next);
  } catch (error) {
    showError(error, "无法选择文件夹");
  }
}

async function exportAll() {
  const done = items.filter((item) => item.status === "done");
  if (batch.running || done.length === 0) return;
  await flushSave();
  if (demoMode) {
    showToast(`预览模式：已模拟导出 ${done.length} 张图片`, "success");
    return;
  }
  try {
    const result = await AnyPic.exportAll();
    if (result.canceled) return;
    showToast(`已导出 ${result.count} 张图片`, "success", {
      label: "在文件夹中显示",
      onClick: () => void AnyPic.revealPath(result.folder),
    });
  } catch (error) {
    showError(error, "导出失败");
  }
}

async function exportOne(id: number) {
  const item = items.find((entry) => entry.id === id);
  if (!item || item.status !== "done" || batch.running) return;
  await flushSave();
  if (demoMode) {
    showToast(`预览模式：将导出 ${item.outputName || item.name}`, "success");
    return;
  }
  try {
    const result = await AnyPic.exportOne(id);
    if (result.canceled) return;
    const path = result.paths[0];
    showToast(`已导出 ${item.outputName || item.name}`, "success", path
      ? { label: "在文件夹中显示", onClick: () => void AnyPic.revealPath(path) }
      : undefined);
  } catch (error) {
    showError(error, "导出失败");
  }
}

/* ------------------------------------------------------------------ */
/* settings                                                            */
/* ------------------------------------------------------------------ */

function formatShortcut(accelerator: string): string {
  const value = (accelerator || DEFAULT_SHORTCUT).trim();
  const parts = value.split("+").map((part) => part.trim()).filter(Boolean);
  const key = parts.pop() ?? "";
  const mods = parts.map((part) => {
    switch (part.toLowerCase()) {
      case "cmdorctrl":
      case "commandorcontrol":
        return isMac ? "⌘" : "Ctrl";
      case "cmd":
      case "command":
      case "super":
      case "meta":
      case "win":
        return isMac ? "⌘" : "Win";
      case "ctrl":
      case "control":
        return isMac ? "⌃" : "Ctrl";
      case "alt":
      case "option":
      case "altgr":
        return isMac ? "⌥" : "Alt";
      case "shift":
        return isMac ? "⇧" : "Shift";
      default:
        return part;
    }
  });
  const label = shortcutKeyLabel(key);
  return isMac ? [...mods, label].join("") : [...mods, label].join("+");
}

function shortcutKeyLabel(key: string): string {
  const named: Record<string, string> = isMac
    ? { Space: "Space", Enter: "↩", Tab: "⇥", Backspace: "⌫", Delete: "⌦", Up: "↑", Down: "↓", Left: "←", Right: "→", Escape: "⎋", PrintScreen: "PrtSc" }
    : { Space: "Space", Enter: "Enter", Tab: "Tab", Backspace: "Backspace", Delete: "Delete", Up: "Up", Down: "Down", Left: "Left", Right: "Right", Escape: "Esc", PrintScreen: "PrtSc" };
  return named[key] ?? (key.length === 1 ? key.toUpperCase() : key);
}

function acceleratorFromEvent(event: KeyboardEvent): string | null {
  const mods: string[] = [];
  if (event.metaKey) mods.push("Cmd");
  if (event.ctrlKey) mods.push("Ctrl");
  if (event.altKey) mods.push("Alt");
  if (event.shiftKey) mods.push("Shift");
  if (mods.length === 0) return null;
  const key = acceleratorKey(event);
  if (!key) return null;
  return [...mods, key].join("+");
}

function acceleratorKey(event: KeyboardEvent): string | null {
  const code = event.code;
  if (/^Key[A-Z]$/.test(code)) return code.slice(3);
  if (/^Digit[0-9]$/.test(code)) return code.slice(5);
  if (/^F([1-9]|1[0-9]|2[0-4])$/.test(code)) return code;
  const named: Record<string, string> = {
    Space: "Space",
    Enter: "Enter",
    NumpadEnter: "Enter",
    Tab: "Tab",
    Backspace: "Backspace",
    Delete: "Delete",
    Insert: "Insert",
    ArrowUp: "Up",
    ArrowDown: "Down",
    ArrowLeft: "Left",
    ArrowRight: "Right",
    Home: "Home",
    End: "End",
    PageUp: "PageUp",
    PageDown: "PageDown",
    PrintScreen: "PrintScreen",
    Minus: "-",
    Equal: "=",
    BracketLeft: "[",
    BracketRight: "]",
    Backslash: "\\",
    Semicolon: ";",
    Quote: "'",
    Comma: ",",
    Period: ".",
    Slash: "/",
    Backquote: "`",
  };
  return named[code] ?? null;
}

function isReservedShortcut(accelerator: string): boolean {
  const reserved = isMac
    ? ["Cmd+Q", "Cmd+W", "Cmd+Tab", "Cmd+Space", "Cmd+H", "Cmd+M", "Cmd+Shift+3", "Cmd+Shift+4", "Cmd+Shift+5"]
    : ["Ctrl+Alt+Delete"];
  return reserved.includes(accelerator);
}

function startShortcutRecording() {
  if (demoMode) {
    showToast("浏览器预览模式无法修改全局快捷键", "info");
    return;
  }
  recordingShortcut = true;
  void AnyPic.setShortcutRecording(true).catch(() => {});
  $("#shortcutBtn").classList.add("recording");
  $("#shortcutValue").textContent = "按组合键";
  $("#shortcutEdit").toggleAttribute("hidden", true);
  $("#shortcutHint").hidden = false;
}

function stopShortcutRecording() {
  if (!recordingShortcut) return;
  recordingShortcut = false;
  void AnyPic.setShortcutRecording(false).catch(() => {});
  $("#shortcutBtn").classList.remove("recording");
  $("#shortcutEdit").toggleAttribute("hidden", false);
  $("#shortcutHint").hidden = true;
  $("#shortcutValue").textContent = formatShortcut(settings.screenshotShortcut);
}

async function applyShortcut(accelerator: string) {
  if (demoMode) return;
  try {
    const next = await AnyPic.setScreenshotShortcut(accelerator);
    settings = { ...settings, ...next };
    stopShortcutRecording();
    showToast(`截图快捷键已设为 ${formatShortcut(settings.screenshotShortcut)}`, "success");
  } catch (error) {
    stopShortcutRecording();
    showError(error, "无法设置快捷键");
  }
}

async function setSettings(patch: Partial<Settings>, then?: (next: Settings) => void) {
  settings = { ...settings, ...patch };
  if (patch.theme) applyTheme(settings.theme);
  markDirty();
  syncSettingsToUI();
  renderAll();
  queueSave();
  if (then && !demoMode) {
    try {
      const next = await AnyPic.saveSettings(settings);
      settings = { ...defaultSettings(), ...next };
      syncSettingsToUI();
      then(settings);
    } catch {
      /* keep the optimistic UI; the save retried on the next change */
    }
  }
}

function applySettingsFromBackend(next: Settings) {
  settings = { ...defaultSettings(), ...next };
  applyTheme(settings.theme);
  syncSettingsToUI();
  renderAll();
  queueSave();
}

/* The beautifier's panel and swatch states follow the persisted style. */
function syncBeautifyUI() {
  const style = { ...defaultSettings().screenshotStyle, ...settings.screenshotStyle };
  const on = style.mode === "on";
  const toggle = $<HTMLInputElement>("#beautifyToggle");
  if (toggle) toggle.checked = on;
  $("#beautifyOptions").hidden = !on;
  const grid = $("#beautifyGrid");
  if (!grid.childElementCount) renderBeautifyGrid();
  $$<HTMLButtonElement>("#beautifyGrid button").forEach((button) => {
    const active = button.dataset.bg === style.background;
    button.setAttribute("aria-checked", String(active));
  });
  $$<HTMLButtonElement>("#beautifyPad button").forEach((button) => {
    const active = Number(button.dataset.pad) === style.padding;
    button.classList.toggle("active", active);
    button.setAttribute("aria-pressed", String(active));
  });
  $("#beautifyHint").textContent = BEAUTIFY_BACKGROUNDS[style.background] ?? "自动取色";
  if (on && !beautifySwatchesLoading && (beautifySwatchesStale || beautifyPreviewPadding !== style.padding)) {
    void refreshBeautifySwatches();
  }
}

/* The swatch grid renders immediately from fallback colors, then swaps in
   the backend's real previews of the current screenshot when they land. */
function renderBeautifyGrid() {
  const grid = $("#beautifyGrid");
  grid.innerHTML = "";
  for (const [id, label] of Object.entries(BEAUTIFY_BACKGROUNDS)) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "beautify-swatch";
    button.dataset.bg = id;
    button.title = label;
    button.setAttribute("role", "radio");
    button.setAttribute("aria-checked", "false");
    const chip = document.createElement("span");
    chip.className = "swatch-fallback";
    chip.setAttribute("aria-hidden", "true");
    chip.style.background = BEAUTIFY_FALLBACKS[id] ?? "#888";
    const thumb = document.createElement("img");
    thumb.className = "swatch-thumb";
    thumb.hidden = true;
    thumb.alt = "";
    thumb.setAttribute("aria-hidden", "true");
    button.append(chip, thumb);
    grid.appendChild(button);
  }
  applyBeautifySwatches();
}

async function refreshBeautifySwatches() {
  if (demoMode || beautifySwatchesLoading) return;
  const style = { ...defaultSettings().screenshotStyle, ...settings.screenshotStyle };
  if (style.mode !== "on" || $("#beautifyOptions").hidden) {
    beautifySwatchesStale = true;
    return;
  }
  beautifySwatchesLoading = true;
  try {
    const swatches = await AnyPic.beautifySwatches(style);
    beautifySwatches = new Map(swatches.map((s) => [s.id, s.image]));
    beautifySwatchesStale = false;
    beautifyPreviewPadding = style.padding;
    applyBeautifySwatches();
  } catch {
    /* keep the fallback chips; previews are cosmetic */
  } finally {
    beautifySwatchesLoading = false;
  }
}

/* applyBeautifySwatches swaps fallback chips for rendered previews; ids the
   backend did not return keep their fallback. */
function applyBeautifySwatches() {
  if (!beautifySwatches) return;
  $$("#beautifyGrid .beautify-swatch").forEach((button) => {
    const image = beautifySwatches?.get(button.dataset.bg ?? "");
    if (!image) return;
    const thumb = button.querySelector<HTMLImageElement>(".swatch-thumb");
    const chip = button.querySelector<HTMLElement>(".swatch-fallback");
    if (!thumb || !chip) return;
    if (thumb.getAttribute("src") !== image) thumb.src = image;
    thumb.hidden = false;
    chip.hidden = true;
  });
}

function syncSettingsToUI() {
  const clipboardToggle = $<HTMLInputElement>("#clipboardToggle");
  if (clipboardToggle) clipboardToggle.checked = settings.screenshotClipboard !== "off";
  syncBeautifyUI();
  const dockToggle = $<HTMLInputElement>("#dockToggle");
  if (dockToggle) dockToggle.checked = settings.hideDockWhenTray !== "off";
  const openAtLoginToggle = $<HTMLInputElement>("#openAtLoginToggle");
  if (openAtLoginToggle) openAtLoginToggle.checked = settings.openAtLogin === "on";
  try {
    localStorage.setItem("anypic-theme", settings.theme);
  } catch {
    /* storage can be unavailable */
  }
}

function queueSave() {
  if (demoMode) return;
  savePending = true;
  if (saveTimer) window.clearTimeout(saveTimer);
  saveTimer = window.setTimeout(() => {
    saveTimer = undefined;
    savePending = false;
    AnyPic.saveSettings(settings).catch(() => {
      // Settings are best effort; compression still works.
    });
  }, 220);
}

async function flushSave() {
  if (demoMode || !savePending) return;
  if (saveTimer) {
    window.clearTimeout(saveTimer);
    saveTimer = undefined;
  }
  savePending = false;
  try {
    await AnyPic.saveSettings(settings);
  } catch {
    // Export still uses the settings the backend has.
  }
}

function markDirty() {
  dirty =
    compressedSignature !== "" &&
    signature(settings) !== compressedSignature &&
    items.some((item) => item.status === "done");
}

function handleBatch(next: Batch) {
  const wasRunning = batch.running;
  batch = next;
  if (wasRunning && !next.running) {
    compressedSignature = signature(settings);
    markDirty();
    const done = items.filter((item) => item.status === "done").length;
    const failed = items.filter((item) => item.status === "error").length;
    if (next.canceled) {
      showToast("压缩已取消", "info");
    } else if (failed > 0) {
      showToast(`压缩完成：${done} 张成功，${failed} 张失败`, "error");
    } else {
      const doneOriginal = items.filter((item) => item.status === "done").reduce((sum, item) => sum + item.originalSize, 0);
      const doneCompressed = items.filter((item) => item.status === "done").reduce((sum, item) => sum + item.compressedSize, 0);
      const saved = Math.max(0, doneOriginal - doneCompressed);
      showToast(`压缩完成，共省下 ${formatBytes(saved)}`, "success");
    }
  }
  scheduleRender();
}

/* ------------------------------------------------------------------ */
/* theme                                                               */
/* ------------------------------------------------------------------ */

function cycleTheme() {
  const next = settings.theme === "system" ? "dark" : settings.theme === "dark" ? "light" : "system";
  void setSettings({ theme: next });
  showToast(
    next === "system" ? "主题：跟随系统" : next === "dark" ? "主题：深色" : "主题：浅色",
    "info",
  );
}

function applyTheme(mode: string) {
  const theme = mode === "light" || mode === "dark" || mode === "system" ? mode : "system";
  settings.theme = theme;
  const resolved = theme === "system" ? (window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light") : theme;
  document.documentElement.dataset.theme = resolved;
  try {
    localStorage.setItem("anypic-theme", theme);
  } catch {
    /* storage can be unavailable */
  }
  const name = theme === "system" ? "monitor" : theme === "dark" ? "moon" : "sun";
  setUseIcon("themeIcon", name);
  const button = document.getElementById("themeBtn");
  if (button) button.title = theme === "system" ? "主题：跟随系统" : theme === "dark" ? "主题：深色" : "主题：浅色";
}

/* ------------------------------------------------------------------ */
/* preview                                                             */
/* ------------------------------------------------------------------ */

async function openPreview(id: number) {
  const item = items.find((entry) => entry.id === id);
  if (!item || item.status !== "done") return;
  selectedId = id;
  lastFocused = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  previewData = previewCache.get(id) ?? null;
  $("#previewTitle").textContent = item.name;
  $("#previewMeta").textContent = `${item.format}，${formatDimensions(item.width, item.height)}，${formatBytes(item.originalSize)}，${item.path}`;
  $("#previewOriginalSize").textContent = formatBytes(item.originalSize);
  $("#previewCompressedSize").textContent = item.passedThrough ? formatBytes(item.originalSize) : formatBytes(item.compressedSize);
  $("#previewSaved").textContent = item.passedThrough
    ? "原图已是最优"
    : item.compressedSize <= item.originalSize
      ? `−${pct(item.savedPercent)}`
      : `+${pct((item.compressedSize / Math.max(1, item.originalSize) - 1) * 100)}`;
  $("#previewDetails").textContent = `${item.outputFormat || item.format}，${item.outputName || item.name}`;
  $<HTMLButtonElement>("#previewExportBtn").disabled = item.status !== "done";
  $("#compareLoading").hidden = false;
  $("#compareFrame").hidden = true;
  compareHintSeen = false;
  $("#compareHint").classList.remove("hidden");
  setComparePosition(50);
  openModal($("#previewModal"));
  $("#previewClose").focus({ preventScroll: true });

  if (previewData) {
    paintPreview(previewData);
    return;
  }

  try {
    const data = demoMode ? demoPreview(item) : await AnyPic.preview(id);
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

function closePreview() {
  closeModal($("#previewModal"));
  selectedId = null;
  previewData = null;
  $("#previewBefore").removeAttribute("src");
  $("#previewAfter").removeAttribute("src");
  lastFocused?.focus({ preventScroll: true });
}

/* ------------------------------------------------------------------ */
/* modals                                                              */
/* ------------------------------------------------------------------ */

function openModal(modal: HTMLElement) {
  for (const sibling of document.body.children) {
    if (sibling !== modal && !sibling.classList.contains("toast-stack") && !sibling.classList.contains("svg-sprite")) {
      (sibling as HTMLElement).inert = true;
    }
  }
  modal.classList.add("open");
  modal.setAttribute("aria-hidden", "false");
}

function closeModal(modal: HTMLElement) {
  for (const sibling of document.body.children) {
    if (sibling !== modal && !sibling.classList.contains("toast-stack") && !sibling.classList.contains("svg-sprite")) {
      (sibling as HTMLElement).inert = false;
    }
  }
  modal.classList.remove("open");
  modal.setAttribute("aria-hidden", "true");
}

function askConfirm(title: string, body: string, confirmLabel: string): Promise<boolean> {
  if (confirmResolve) resolveConfirm(false);
  $("#confirmTitle").textContent = title;
  $("#confirmBody").textContent = body;
  $("#confirmOk").textContent = confirmLabel;
  openModal($("#confirmModal"));
  $("#confirmCancel").focus({ preventScroll: true });
  return new Promise<boolean>((resolve) => {
    confirmResolve = resolve;
  });
}

function resolveConfirm(value: boolean) {
  closeModal($("#confirmModal"));
  const resolve = confirmResolve;
  confirmResolve = null;
  resolve?.(value);
}

function openAbout() {
  openModal($("#aboutModal"));
  $("#aboutClose").focus({ preventScroll: true });
}

function closeAbout() {
  closeModal($("#aboutModal"));
}

/* ------------------------------------------------------------------ */
/* toasts                                                              */
/* ------------------------------------------------------------------ */

function showError(error: unknown, fallback: string) {
  const message = error instanceof Error ? error.message : typeof error === "string" ? error : fallback;
  showToast(message || fallback, "error");
}

function showToast(
  message: string,
  kind: "success" | "error" | "info" = "info",
  action?: { label: string; onClick: () => void },
) {
  const stack = $("#toasts");
  const toast = document.createElement("div");
  toast.className = `toast ${kind}`;
  const iconName = kind === "success" ? "check" : kind === "error" ? "alert" : "info";
  toast.innerHTML = `<span class="toast-icon">${icon(iconName)}</span><span>${esc(message)}</span>`;
  if (action) {
    const button = document.createElement("button");
    button.className = "toast-action";
    button.textContent = action.label;
    button.addEventListener("click", () => {
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
  const pause = () => window.clearTimeout(timer);
  const resume = () => {
    window.clearTimeout(timer);
    timer = window.setTimeout(dismiss, 1800);
  };
  toast.addEventListener("mouseenter", pause);
  toast.addEventListener("mouseleave", resume);
  toast.addEventListener("focusin", pause);
  toast.addEventListener("focusout", resume);

  function dismiss() {
    window.clearTimeout(timer);
    toast.classList.add("leaving");
    toast.addEventListener("animationend", () => toast.remove(), { once: true });
  }
}

/* ------------------------------------------------------------------ */
/* demo mode                                                           */
/* ------------------------------------------------------------------ */

async function demoCompress() {
  demoCanceled = false;
  batch = { running: true, total: items.length, done: 0, failed: 0, canceled: false };
  scheduleRender();
  for (const item of items) {
    if (demoCanceled) break;
    if (item.status === "done") {
      batch.done++;
      continue;
    }
    item.status = "working";
    scheduleRender();
    await wait(700 + Math.random() * 450);
    if (demoCanceled) break;
    item.status = "done";
    item.compressedSize = Math.round(item.originalSize * (0.19 + Math.random() * 0.12));
    item.savedPercent = ((item.originalSize - item.compressedSize) / item.originalSize) * 100;
    item.outputFormat = settings.format === "auto" ? "WEBP" : settings.format.toUpperCase();
    item.outputName = item.name.replace(/\.[^.]+$/, `${settings.suffix}.${settings.format === "png" ? "png" : settings.format === "jpeg" ? "jpg" : "webp"}`);
    batch.done++;
    scheduleRender();
  }
  batch = { ...batch, running: false, canceled: demoCanceled };
  compressedSignature = signature(settings);
  markDirty();
  if (demoCanceled) showToast("压缩已取消", "info");
  else showToast("预览模式：压缩完成", "success");
  renderAll();
}

async function handleCommand(command: string) {
  switch (command) {
    case "open":
      await pickFiles();
      break;
    case "screenshot":
      await captureScreenshot();
      break;
    case "compress":
      await compressAll();
      break;
    case "export":
      await exportAll();
      break;
    case "clear":
      await clearQueue();
      break;
    case "theme":
      cycleTheme();
      break;
  }
}

void init();
