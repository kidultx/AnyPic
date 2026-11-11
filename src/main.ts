import { AnyPic, events, type Batch, type Item, type Preview, type Settings, type Snapshot, type UpdateInfo } from "./mygo";
import { isMyGo, onFileDrop, runtime } from "mygo-runtime";
import { $, $$, closeModal, esc, formatBytes, formatDimensions, icon, openModal, pct, showError, shortenPath, showToast } from "./ui";
import { bindPreview, closePreview, initPreview, invalidatePreview, openPreview, previewSelectedId } from "./preview";
import { DEFAULT_SHORTCUT, acceleratorFromEvent, formatShortcut, isReservedShortcut, setShortcutPlatform } from "./shortcuts";

function signature(settings: Settings): string {
  return JSON.stringify([settings.format, settings.quality, settings.maxDimension, settings.suffix]);
}

const wait = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

function setUseIcon(useId: string, name: string) {
  const el = document.getElementById(useId);
  if (el) el.setAttribute("href", `#i-${name}`);
}


/* ------------------------------------------------------------------ */
/* state                                                               */
/* ------------------------------------------------------------------ */

let settings: Settings = defaultSettings();
let items: Item[] = [];
let batch: Batch = { running: false, total: 0, done: 0, failed: 0, canceled: false };
let filter: "all" | "done" | "error" = "all";
let view: "list" | "grid" = "list";
/* Selected queue rows, by id; surviving across filter changes but pruned
   when their images leave the queue. */
const selected = new Set<number>();
let nameFilter = "";
let dirty = false;
let compressedSignature = "";
let demoMode = false;
let demoCanceled = false;
let raf = 0;
let saveTimer: number | undefined;
let savePending = false;
let isMac = navigator.platform.toLowerCase().includes("mac");
let recordingShortcut = false;
/* An automatic pass spans one or more batches: images added while it runs
   join the same pass and are saved together when the queue settles. */
let autoRun: { ids: Set<number>; copyIDs: number[] } | null = null;
/* Guards the async gap between deciding to compress and the backend call. */
let compressStarting = false;
/* Where the last automatic pass saved its results, for the footer summary.
   percent is signed: negative when the pass produced larger files. */
let lastSaved: { count: number; target: string; percent: number; path: string } | null = null;

const rowCache = new Map<number, HTMLElement>();
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
    autoProcess: "on",
    screenshotShortcut: DEFAULT_SHORTCUT,
    screenshotClipboard: "on",
    hideDockWhenTray: "on",
    openAtLogin: "off",
  };
}

function upsertItem(item: Item) {
  const index = items.findIndex((existing) => existing.id === item.id);
  if (index === -1) items.push(item);
  else items[index] = item;
  invalidatePreview(item.id);
}

/* ------------------------------------------------------------------ */
/* boot                                                                */
/* ------------------------------------------------------------------ */

async function init() {
  detectPlatform();
  initPreview({
    items: () => items,
    demoMode: () => demoMode,
    batchRunning: () => batch.running,
    autoProcessEnabled,
    applySnapshot,
    startAutoRun,
    renderAll,
  });
  bindEvents();
  loadViewPreference();
  loadSidebarWidth();
  bindSidebarResize();
  applyTheme(settings.theme);

  if (isMyGo()) {
    try {
      const snapshot = await AnyPic.state();
      applySnapshot(snapshot);
      events.anypicItem.on((item) => renderItemUpdate(item));
      events.anypicBatch.on((next) => handleBatch(next));
      events.anypicCommand.on((command) => void handleCommand(command));
      // Files opened from the file manager, a Dock drop or the command line
      // (#6): several can arrive at once, so they are merged into one add.
      events.anypicOpen.on((path) => queueOpenedPaths([path]));
      void AnyPic.takePendingOpen()
        .then((paths) => queueOpenedPaths(paths))
        .catch(() => {});
      onFileDrop(({ paths }) => void addPaths(paths));
      void AnyPic.info()
        .then((info) => {
          $("#aboutVersion").textContent = `${info.version}，${info.platform}/${info.arch}，MyGo`;
          if (!info.screenshotActive) {
            showToast(`截图快捷键 ${formatShortcut(info.screenshotShortcut)} 未生效，可能已被其他应用占用`, "error");
          }
        })
        .catch(() => {});
      // Silent boot-time update check: one GitHub API hit, failures stay
      // quiet, and a newer release earns a single toast with a shortcut.
      void checkForUpdate(true);
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
  setShortcutPlatform(isMac);
  document.documentElement.classList.toggle("is-mac", isMac);
  $("#screenPermissionBtn").hidden = !(isMac && isMyGo());
  $("#windowSection").hidden = !isMac;
  $$<HTMLElement>("[data-shortcut]").forEach((el) => {
    const shortcut = el.dataset.shortcut;
    if (shortcut === "open") el.textContent = isMac ? "⌘ O" : "Ctrl O";
    else if (shortcut === "compress") el.textContent = isMac ? "⌘ ↵" : "Ctrl ↵";
    else if (shortcut === "paste") el.textContent = isMac ? "⌘ V" : "Ctrl V";
    else if (shortcut === "prefs") el.textContent = isMac ? "⌘," : "Ctrl,";
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
  items = snapshot.items.map((item) => {
    if (item.thumb) return item;
    const thumb = previousThumbs.get(item.id);
    return thumb ? { ...item, thumb } : item;
  });
  // Selection never outlives its images.
  const alive = new Set(items.map((item) => item.id));
  for (const id of [...selected]) {
    if (!alive.has(id)) selected.delete(id);
  }
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
    // The transparent PNG starts pending so the demo can exercise the
    // JPG → PNG transparency rule when the format is switched (#15).
    const ready = index === 1 || index === 3;
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
      hasAlpha: name.endsWith(".png"),
      passedThrough: false,
      transparencyKept: false,
      savedPercent: ready ? 0 : saved,
      animated: false,
    };
  });
  batch = { running: false, total: items.length, done: 2, failed: 0, canceled: false };
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

  const presets = [60, 82, 95];
  const customQuality = !presets.includes(settings.quality);
  $$<HTMLButtonElement>("#qualityPresets button").forEach((button) => {
    const active = Number(button.dataset.quality) === settings.quality;
    button.classList.toggle("active", active);
    button.setAttribute("aria-pressed", String(active));
    button.disabled = format === "png" || batch.running;
  });
  $("#qualityState").hidden = !customQuality;

  const autoToggle = $<HTMLInputElement>("#autoProcessToggle");
  if (document.activeElement !== autoToggle) autoToggle.checked = autoProcessEnabled();
  $("#autoProcessNote").textContent = autoProcessEnabled()
    ? "添加或截图后自动压缩并保存，只需粘贴。"
    : "关闭后需手动点「压缩全部」，再点「导出」。";
  $("#emptyTitle").textContent = autoProcessEnabled() ? "拖入图片，自动压缩保存" : "拖入图片开始压缩";
  $("#emptyShotKey").textContent = formatShortcut(settings.screenshotShortcut);
  $("#outputInlineLabel").textContent =
    settings.outputMode === "folder" && settings.outputFolder ? shortenPath(settings.outputFolder) : "原图旁边";
  $("#outputInlineBtn").title = settings.outputMode === "folder" && settings.outputFolder ? settings.outputFolder : "原图旁边";

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

  $("#resetLocation").hidden = !(settings.outputMode === "folder" && settings.outputFolder);

  const tips: Record<string, [string, string]> = {
    auto: ["保持原格式", "原格式保留；其他格式转为 JPG 或 PNG。"],
    webp: ["WebP", "体积更小，支持透明。"],
    jpeg: ["JPG", "兼容性最好，适合照片；透明图片存为 PNG。"],
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

function autoProcessEnabled(): boolean {
  return settings.autoProcess !== "off";
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
  const done = items.filter((item) => item.status === "done");
  const doneOriginal = done.reduce((sum, item) => sum + item.originalSize, 0);
  const doneCompressed = done.reduce((sum, item) => sum + item.compressedSize, 0);
  const saved = Math.max(0, doneOriginal - doneCompressed);
  const savedPercent = doneOriginal > 0 ? (saved / doneOriginal) * 100 : 0;
  const pending = items.length - done.length;

  // All three numbers describe the finished images; mixing in the originals
  // still queued made the row's own arithmetic wrong (#31).
  $("#statOriginal").textContent = done.length > 0 ? formatBytes(doneOriginal) : "暂无";
  $("#statCompressed").textContent = done.length > 0 ? formatBytes(doneCompressed) : "暂无";
  $("#statSaved").textContent = done.length > 0 ? `${Math.round(savedPercent)}%` : "暂无";
  $("#statSavedFoot").textContent =
    done.length === 0 ? "" : saved > 0 ? `（共 ${formatBytes(saved)}）` : "（无需压缩）";
  $("#summaryDone").style.display = done.length === 0 ? "none" : "";
  $("#summarySaved").style.display = done.length === 0 ? "none" : "";
  $("#summaryPending").hidden = pending === 0;
  $("#summaryPending").textContent = `另有 ${pending} 张未完成`;
}

function renderQueue() {
  const list = $("#queueList");
  const query = nameFilter.trim().toLowerCase();
  const filtered = items.filter((item) => {
    if (query && !item.name.toLowerCase().includes(query)) return false;
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

  const failedCount = items.filter((item) => item.status === "error").length;
  $("#tabErrorCount").textContent = String(failedCount);
  $("#tabErrorCount").hidden = failedCount === 0;
  $$<HTMLButtonElement>(".queue-tab").forEach((button) => {
    const active = button.dataset.filter === filter;
    button.classList.toggle("active", active);
    button.setAttribute("aria-selected", String(active));
  });
  $("#queueEmptyFilter").hidden = filtered.length > 0;
  syncSelectionBar();
  syncSelectAll();
}

/* The images the current status tab and name filter both let through (#5). */
function visibleItems(): Item[] {
  const query = nameFilter.trim().toLowerCase();
  return items.filter((item) => {
    if (query && !item.name.toLowerCase().includes(query)) return false;
    return queueShows(item);
  });
}

function allVisibleSelected(): boolean {
  const visible = visibleItems();
  return visible.length > 0 && visible.every((item) => selected.has(item.id));
}

function syncSelectionBar() {
  const showing = selected.size > 0 && !batch.running;
  $("#selectionBar").hidden = !showing;
  // Selection takes the toolbar over in place (#27): an extra row would push
  // every image down at the moment the first box is ticked.
  $("#queueTabs").hidden = showing;
  $("#selectionCount").textContent = String(selected.size);
  $<HTMLButtonElement>("#removeSelectedBtn").disabled = batch.running;
}

function syncSelectAll() {
  const button = $<HTMLButtonElement>("#selectAllBtn");
  const all = allVisibleSelected();
  button.textContent = all ? "取消全选" : "全选";
  button.setAttribute("aria-pressed", String(all));
  button.disabled = visibleItems().length === 0 || batch.running;
}

function createRow(id: number): HTMLElement {
  const row = document.createElement("article");
  row.className = "queue-item";
  row.dataset.id = String(id);
  row.innerHTML = `
    <label class="item-select">
      <input type="checkbox" data-select aria-label="选择这张图片" />
    </label>
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
      <button class="icon-btn" data-action="decorate" title="按当前设置美化" aria-label="美化">${icon("sparkle")}</button>
      <button class="icon-btn" data-action="export" title="导出这张" aria-label="导出这张">${icon("download")}</button>
      <button class="icon-btn" data-action="remove" title="移除" aria-label="移除">${icon("x")}</button>
    </div>`;

  const checkbox = row.querySelector<HTMLInputElement>("input[data-select]")!;
  checkbox.addEventListener("change", () => {
    if (checkbox.checked) selected.add(id);
    else selected.delete(id);
    row.classList.toggle("is-selected", checkbox.checked);
    syncSelectionBar();
    syncSelectAll();
  });
  // The label must not turn the click into "open preview".
  checkbox.addEventListener("click", (event) => event.stopPropagation());

  row.addEventListener("click", (event) => {
    const target = event.target as HTMLElement;
    if (target.closest("button")) return;
    const item = items.find((entry) => entry.id === id);
    if (!item) return;
    if (item.status === "done" || item.status === "ready") void openPreview(item.id);
    else if (item.status === "error" && item.error) showToast(item.error, "error");
  });

  row.querySelector<HTMLButtonElement>('[data-action="preview"]')?.addEventListener("click", (event) => {
    event.stopPropagation();
    void openPreview(id);
  });
  row.querySelector<HTMLButtonElement>('[data-action="decorate"]')?.addEventListener("click", (event) => {
    event.stopPropagation();
    void openPreview(id, "beautify");
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
  row.classList.toggle("is-selected", selected.has(item.id));
  const checkbox = row.querySelector<HTMLInputElement>("input[data-select]")!;
  checkbox.checked = selected.has(item.id);
  checkbox.disabled = batch.running;

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
  name.innerHTML =
    item.status === "ready" ? `${esc(item.name)}<span class="item-state">待处理</span>` : esc(item.name);

  const meta = row.querySelector<HTMLElement>(".item-meta")!;
  const formatClass = item.format.toLowerCase();
  const animated = item.animated ? `<span class="animated-badge">动图</span>` : "";
  const output = item.status === "done" && item.outputFormat
    ? item.transparencyKept
      ? `<span class="output-format" title="JPG 不支持透明，已改存为 PNG（无损，质量设置不适用）">→ PNG · 保留透明</span>`
      : `<span class="output-format">→ ${esc(item.outputFormat)}</span>`
    : "";
  meta.innerHTML = `<span class="format-chip ${esc(formatClass)}">${esc(item.format || "IMG")}</span><span>${formatDimensions(
    item.width,
    item.height,
  )}</span>${animated}${output}`;

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
  pill.hidden = false;

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
        pill.textContent = `−${pct(savedPct)}`;
      } else {
        pill.classList.add("neutral");
        pill.textContent = `+${pct(-savedPct)}`;
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
    // A waiting image has no result yet; the faint "待处理" beside its name
    // says so without looking like a result.
    sizes.innerHTML = "";
    pill.hidden = true;
  }

  const previewButton = row.querySelector<HTMLButtonElement>('[data-action="preview"]')!;
  const decorateButton = row.querySelector<HTMLButtonElement>('[data-action="decorate"]')!;
  const exportButton = row.querySelector<HTMLButtonElement>('[data-action="export"]')!;
  const removeButton = row.querySelector<HTMLButtonElement>('[data-action="remove"]')!;
  previewButton.disabled = item.status !== "done";
  decorateButton.disabled = batch.running || item.animated || (item.status !== "done" && item.status !== "ready");
  decorateButton.title = item.animated ? "动图暂不支持美化" : "按当前设置美化";
  exportButton.disabled = item.status !== "done" || batch.running;
  removeButton.disabled = batch.running;
}

function renderFooter() {
  if (items.length === 0) return;
  const auto = autoProcessEnabled();
  const done = items.filter((item) => item.status === "done");
  const failed = items.filter((item) => item.status === "error");
  const pending = items.filter((item) => item.status === "ready" || item.status === "working");
  const total = items.length;
  const completed = done.length + failed.length;
  const percent = total > 0 ? Math.round((completed / total) * 100) : 0;

  let status = "准备就绪";
  let showOpen = false;
  if (batch.running) {
    // Overall queue progress: an auto pass runs a smaller batch on top of
    // already-finished items, so batch.total is not the queue size and the
    // bar above uses the same completed/total pair.
    status = `正在压缩，${completed} / ${total}`;
  } else if (auto && lastSaved) {
    const savings = lastSaved.percent >= 0
      ? `省 ${lastSaved.percent}%`
      : `增大 ${-lastSaved.percent}%`;
    status = `已保存 ${lastSaved.count} 张到 ${lastSaved.target} · ${savings}`;
    showOpen = lastSaved.path !== "";
  } else if (batch.canceled) {
    status = "压缩已取消";
  } else if (auto) {
    status = done.length > 0 && pending.length === 0 ? "压缩完成" : "准备就绪";
  } else if (done.length === total) {
    status = "压缩完成";
  } else if (failed.length > 0 && completed === total) {
    status = "部分图片未能压缩";
  } else if (done.length > 0) {
    status = `${done.length} 张已完成，${total - completed} 张待处理`;
  }

  $("#batchStatus").textContent = status;
  $("#openSavedBtn").hidden = !showOpen;
  $("#retryFailedBtn").hidden = batch.running || failed.length === 0;
  $("#retryFailedBtn").textContent = failed.length > 0 ? `重试失败项（${failed.length}）` : "重试失败项";
  $("#batchPercent").textContent = `${percent}%`;
  $("#progressFill").style.transform = `scaleX(${percent / 100})`;
  renderStageAction();
}

/* The footer carries the one action that matches the mode: capturing in the
   automatic flow, compressing then exporting in the manual one. */
function renderStageAction() {
  const button = $<HTMLButtonElement>("#stageActionBtn");
  const label = $("#stageActionLabel");
  const auto = autoProcessEnabled();
  const done = items.filter((item) => item.status === "done").length;
  const pending = items.some((item) => item.status === "ready" || item.status === "working");

  let action: "screenshot" | "compress" | "export";
  if (auto) action = "screenshot";
  else if (!batch.running && done > 0 && !pending && !dirty) action = "export";
  else action = "compress";
  button.dataset.action = action;

  if (action === "screenshot") {
    setUseIcon("stageActionIcon", "camera");
    label.textContent = "截图";
    button.title = `截取屏幕区域（${formatShortcut(settings.screenshotShortcut)}）`;
    button.disabled = false;
  } else if (action === "export") {
    setUseIcon("stageActionIcon", "download");
    label.textContent = "导出";
    button.title = `导出 ${done} 张压缩后的图片`;
    button.disabled = false;
  } else {
    setUseIcon("stageActionIcon", "refresh");
    label.textContent = batch.running ? "压缩中…" : dirty ? "重新压缩" : "压缩全部";
    button.title = batch.running ? "正在压缩" : "压缩队列中的图片";
    button.disabled = batch.running || items.length === 0;
  }
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

  // The automatic flow has no use for a second, large compress button; the
  // footer owns the action. Hiding the whole block leaves no empty gap.
  $("#sidebarActions").hidden = autoProcessEnabled();
  $("#cancelBtn").hidden = !running;
  $<HTMLButtonElement>("#clearBtn").disabled = running || items.length === 0;
  $<HTMLButtonElement>("#addBtn").disabled = running;
  // In the automatic flow the footer owns the capture action, so the toolbar
  // copy steps aside — except on an empty queue, where the footer is hidden.
  $<HTMLButtonElement>("#screenshotBtn").hidden = autoProcessEnabled() && items.length > 0;
  $<HTMLButtonElement>("#screenshotBtn").disabled = running;
  $<HTMLButtonElement>("#emptyAddBtn").disabled = running;
  $<HTMLButtonElement>("#resetBtn").disabled = running;
  $<HTMLButtonElement>("#suffix").disabled = running;
  $<HTMLButtonElement>("#maxDim").disabled = running;
  $$<HTMLButtonElement>("#formatSeg button").forEach((button) => {
    button.disabled = running;
  });
  $<HTMLInputElement>("#quality").disabled = running || settings.format === "png";
  $$<HTMLButtonElement>("#qualityPresets button").forEach((button) => {
    button.disabled = running || settings.format === "png";
  });
  // Selection follows the removal rules: nothing selectable or removable
  // while a pass owns the queue.
  $<HTMLButtonElement>("#selectAllBtn").disabled = running || visibleItems().length === 0;
  $<HTMLInputElement>("#queueFilter").disabled = running;
  syncSelectionBar();
  syncSelectAll();
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
  $("#retryFailedBtn").addEventListener("click", () => void retryFailed());
  $("#stageActionBtn").addEventListener("click", () => {
    const action = $<HTMLButtonElement>("#stageActionBtn").dataset.action;
    if (action === "screenshot") void captureScreenshot();
    else if (action === "export") void exportAll();
    else void compressAll();
  });
  $("#openSavedBtn").addEventListener("click", () => {
    if (lastSaved) void AnyPic.revealPath(lastSaved.path);
  });
  $("#clearBtn").addEventListener("click", () => void clearQueue());
  $("#outputInlineBtn").addEventListener("click", () => void chooseOutputFolder());
  $("#resetLocation").addEventListener("click", () => void setSettings({ outputMode: "alongside" }));
  $("#prefsBtn").addEventListener("click", openPrefs);
  $("#prefsClose").addEventListener("click", closePrefs);
  $("#prefsDone").addEventListener("click", closePrefs);
  $$<HTMLElement>("[data-close-prefs]").forEach((el) => el.addEventListener("click", closePrefs));
  $("#themeSeg").addEventListener("click", (event) => {
    const button = (event.target as HTMLElement).closest<HTMLButtonElement>("button[data-theme]");
    if (!button) return;
    void setSettings({ theme: button.dataset.theme ?? "system" });
  });
  $("#shortcutBtn").addEventListener("click", () => {
    if (recordingShortcut) stopShortcutRecording();
    else startShortcutRecording();
  });
  function bindToggle(id: string, key: "screenshotClipboard" | "autoProcess" | "hideDockWhenTray", failure: string) {
    $(id).addEventListener("change", (event) => {
      const on = (event.target as HTMLInputElement).checked;
      void setSettings({ [key]: on ? "on" : "off" } as Partial<Settings>, undefined, (error) => showError(error, failure));
    });
  }
  bindToggle("#clipboardToggle", "screenshotClipboard", "无法保存截图剪贴板设置");
  bindToggle("#autoProcessToggle", "autoProcess", "无法保存自动处理设置");
  bindToggle("#dockToggle", "hideDockWhenTray", "无法保存 Dock 图标设置");
  $("#openAtLoginToggle").addEventListener("change", (event) => {
    const input = event.target as HTMLInputElement;
    const on = input.checked;
    void setSettings(
      { openAtLogin: on ? "on" : "off" },
      (next) => {
        // The backend may refuse the login item; the toggle follows the truth.
        input.checked = next.openAtLogin === "on";
        if (input.checked !== on) showToast("系统拒绝了这次更改，开关已恢复原状", "error");
      },
      (error) => {
        input.checked = !on;
        settings = { ...settings, openAtLogin: on ? "off" : "on" };
        showError(error, "无法更改「开机时启动」");
      },
    );
  });
  $("#screenPermissionBtn").addEventListener("click", () => {
    if (demoMode) return;
    void AnyPic.openScreenRecordingSettings().catch(() => {
      showToast("请手动打开「系统设置 → 隐私与安全性 → 屏幕与系统录音」", "info");
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
  $("#updateCheckBtn").addEventListener("click", () => void checkForUpdate(false));
  $("#updateDownloadBtn").addEventListener("click", () => void downloadUpdate());
  $("#updatePageBtn").addEventListener("click", () => {
    void AnyPic.openReleasePage().catch((error) => showError(error, "无法打开发布页"));
  });

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

  $("#qualityPresets").addEventListener("click", (event) => {
    const button = (event.target as HTMLElement).closest<HTMLButtonElement>("button[data-quality]");
    if (!button || button.disabled) return;
    void setSettings({ quality: Number(button.dataset.quality) });
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

  const queueFilter = $<HTMLInputElement>("#queueFilter");
  queueFilter.addEventListener("input", () => {
    nameFilter = queueFilter.value;
    renderQueue();
  });
  queueFilter.addEventListener("keydown", (event) => {
    if (event.key !== "Escape") return;
    event.preventDefault();
    event.stopPropagation();
    queueFilter.value = "";
    nameFilter = "";
    renderQueue();
  });
  $("#selectAllBtn").addEventListener("click", () => {
    const visible = visibleItems();
    if (allVisibleSelected()) {
      for (const item of visible) selected.delete(item.id);
    } else {
      for (const item of visible) selected.add(item.id);
    }
    renderQueue();
  });
  $("#removeSelectedBtn").addEventListener("click", () => void removeSelected());
  $("#clearSelectionBtn").addEventListener("click", () => {
    selected.clear();
    renderQueue();
  });

  $("#previewClose").addEventListener("click", closePreview);
  $("[data-close-modal]").addEventListener("click", closePreview);
  $("#previewExportBtn").addEventListener("click", () => {
    const selected = previewSelectedId();
    if (selected !== null) void exportOne(selected);
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

  bindPreview();

  document.addEventListener("keydown", (event) => {
    const meta = isMac ? event.metaKey : event.ctrlKey;
    if (event.key === "Escape") {
      if (confirmResolve) {
        event.preventDefault();
        resolveConfirm(false);
      } else if ($("#previewModal").classList.contains("open")) {
        event.preventDefault();
        closePreview();
      } else if ($("#prefsModal").classList.contains("open")) {
        event.preventDefault();
        closePrefs();
      } else if (aboutModal.classList.contains("open")) {
        event.preventDefault();
        closeAbout();
      }
      return;
    }
    if (!meta) return;
    if (event.key.toLowerCase() === "v") {
      // Paste is the quickest way in: an image copied anywhere on the machine
      // lands in the queue. Text fields keep the native paste.
      const target = event.target as HTMLElement | null;
      if (target?.closest("input, textarea, select") || target?.isContentEditable) return;
      event.preventDefault();
      void pasteImage();
    } else if (event.key === ",") {
      event.preventDefault();
      openPrefs();
    } else if (event.key.toLowerCase() === "o") {
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
  if (demoMode) {
    showToast("浏览器预览模式无法截屏，请在 MyGo 桌面应用中使用", "info");
    return;
  }
  try {
    // The backend decides whether to copy and whether to stay in the
    // background from its own settings; make sure a just-toggled switch has
    // reached it before the capture starts.
    await flushSave();
    const result = await AnyPic.captureScreenshot();
    // A fresh capture is "ready", so a done/error filter would hide it.
    if (result.added > 0) filter = "all";
    applyAddResult(result, "clipboard");
    if (result.copied) showToast("截图已复制到剪贴板", "info");
  } catch (error) {
    // Canceling the region selection, or a second press while the first
    // capture is still open, is not an error the user needs to see.
    if (isCaptureNotice(error)) return;
    // macOS refused the capture: explain the permission and offer the
    // settings instead of a toast that fades away (#17).
    if (isScreenCapturePermission(error)) {
      await showScreenCapturePermission();
      return;
    }
    showError(error, "截图失败");
  }
}

function isCaptureNotice(error: unknown) {
  const message = error instanceof Error ? error.message : typeof error === "string" ? error : "";
  return message.includes("已取消截图") || message.includes("正在截图中");
}

function isScreenCapturePermission(error: unknown) {
  const message = error instanceof Error ? error.message : typeof error === "string" ? error : "";
  return message.includes("需要屏幕录制权限");
}

async function showScreenCapturePermission() {
  const openSettings = await askConfirm(
    "AnyPic 需要屏幕录制权限",
    "去「系统设置 → 隐私与安全性 → 屏幕与系统录音」允许 AnyPic（旧系统叫「屏幕录制」）。如果 AnyPic 的开关已经打开，却还是出现这个提示，先选中它点「−」删掉，再回来截图、重新允许。允许后要退出并重新打开 AnyPic。",
    "打开系统设置",
    "稍后",
  );
  if (!openSettings) return;
  await AnyPic.openScreenRecordingSettings().catch(() => {
    showToast("请手动打开「系统设置 → 隐私与安全性 → 屏幕与系统录音」", "info");
  });
}

async function pasteImage() {
  if (demoMode) {
    showToast("浏览器预览模式无法读取剪贴板，请在 MyGo 桌面应用中使用", "info");
    return;
  }
  try {
    const result = await AnyPic.pasteImage();
    if (result.added > 0) filter = "all";
    applyAddResult(result, "clipboard");
  } catch (error) {
    // Pasting while the clipboard holds text is not an error worth a red
    // toast; the app simply has nothing to add.
    const message = error instanceof Error ? error.message : typeof error === "string" ? error : "";
    if (message.includes("剪贴板里没有图片")) {
      showToast("剪贴板里没有图片", "info");
      return;
    }
    showError(error, "粘贴失败");
  }
}

type AddSource = "files" | "clipboard";

async function addPaths(paths: string[]) {
  if (demoMode || paths.length === 0) return;
  try {
    const result = await AnyPic.addPaths(paths);
    applyAddResult(result);
  } catch (error) {
    showError(error, "添加图片失败");
  }
}

/* Opened-file paths are merged on a short timer so a multi-file open adds
   once instead of once per file. */
let openedPaths: string[] = [];
let openedTimer: number | undefined;

function queueOpenedPaths(paths: string[]) {
  if (paths.length === 0) return;
  openedPaths.push(...paths);
  if (openedTimer) window.clearTimeout(openedTimer);
  openedTimer = window.setTimeout(() => {
    const batch = openedPaths;
    openedPaths = [];
    openedTimer = undefined;
    void addPaths(batch);
  }, 60);
}

function applyAddResult(
  result: { snapshot: Snapshot; added: number; skipped: { name: string; reason: string }[] },
  source: AddSource = "files",
): void {
  const known = new Set(items.map((item) => item.id));
  applySnapshot(result.snapshot, false);
  const newIDs = items.filter((item) => !known.has(item.id)).map((item) => item.id);
  const auto = autoProcessEnabled() && !demoMode && result.added > 0;

  if (result.added > 0) {
    // The automatic flow folds the addition into one merged completion
    // toast; a separate "added" toast on top would just be noise.
    if (!auto) {
      showToast(`已添加 ${result.added} 张图片${result.skipped.length ? `，跳过 ${result.skipped.length} 个文件` : ""}`, "success");
    }
    if (result.skipped.length > 0) {
      const first = result.skipped[0]!;
      showToast(`${first.name}：${first.reason}`, "info");
    }
    if (!auto) {
      setTimeout(() => $("#queueScroll").scrollTo({ top: $("#queueScroll").scrollHeight, behavior: "smooth" }), 100);
    }
  } else if (result.skipped.length > 0) {
    const first = result.skipped[0]!;
    showToast(`${first.name}：${first.reason}`, "error");
  }

  renderAll();
  if (auto) startAutoRun(newIDs, source === "clipboard");
}

function startAutoRun(ids: number[], fromClipboard: boolean) {
  if (ids.length === 0) return;
  autoRun ??= { ids: new Set<number>(), copyIDs: [] };
  for (const id of ids) autoRun.ids.add(id);
  if (fromClipboard) autoRun.copyIDs.push(ids[ids.length - 1]!);
  // A pass in progress picks the new images up when it ends; the start
  // guard covers the async gap before the backend call.
  if (batch.running || compressStarting) return;
  void compressAll(true);
}

async function compressAll(auto = false) {
  if (batch.running || compressStarting || items.length === 0) return;

  const ready = items.filter((item) => item.status === "ready");
  if (auto && ready.length === 0) return;
  if (auto && !autoRun) autoRun = { ids: new Set<number>(), copyIDs: [] };
  if (auto) for (const item of ready) autoRun!.ids.add(item.id);

  compressStarting = true;
  dirty = false;
  lastSaved = null;
  markDirty();

  if (demoMode) {
    compressStarting = false;
    await demoCompress();
    return;
  }

  // Auto-process saves to the configured folder as soon as the pass ends;
  // persist a just-changed destination before the backend reads it.
  await flushSave();

  const runSignature = signature(settings);
  if (!auto) {
    items = items.map((item) => ({
      ...item,
      status: "ready",
      error: "",
      compressedSize: 0,
      savedPercent: 0,
      passedThrough: false,
      transparencyKept: false,
      outputFormat: "",
      outputName: "",
    }));
  }
  const total = auto ? items.filter((item) => item.status === "ready").length : items.length;
  if (total === 0) {
    compressStarting = false;
    autoRun = null;
    return;
  }
  batch = { running: true, total, done: 0, failed: 0, canceled: false };
  scheduleRender();
  try {
    if (auto) await AnyPic.compressReady(settings);
    else await AnyPic.compress(settings);
    compressedSignature = runSignature;
    markDirty();
  } catch (error) {
    batch = { ...batch, running: false };
    autoRun = null;
    showError(error, auto ? "自动处理失败" : "无法开始压缩");
    renderAll();
  } finally {
    compressStarting = false;
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

async function retryFailed() {
  if (batch.running || compressStarting) return;
  const failed = items.filter((item) => item.status === "error");
  if (failed.length === 0) return;
  if (demoMode) {
    showToast("浏览器预览模式无法重试，请在 MyGo 桌面应用中使用", "info");
    return;
  }
  await flushSave();
  // A retry in the automatic flow saves its results like any other pass.
  if (autoProcessEnabled()) {
    autoRun ??= { ids: new Set<number>(), copyIDs: [] };
    for (const item of failed) autoRun.ids.add(item.id);
  }
  compressStarting = true;
  try {
    await AnyPic.compressFailed(settings);
  } catch (error) {
    autoRun = null;
    showError(error, "重试失败");
  } finally {
    compressStarting = false;
  }
}

async function removeSelected() {
  if (batch.running || selected.size === 0) return;
  const ids = [...selected];
  const removed = items.filter((item) => ids.includes(item.id));
  const ok = await askConfirm(`移除所选 ${ids.length} 张图片？`, "原始图片不会被删除。", "移除");
  if (!ok) return;
  if (demoMode) {
    for (const item of removed) invalidatePreview(item.id);
    items = items.filter((item) => !selected.has(item.id));
    selected.clear();
    renderAll();
    return;
  }
  try {
    const snapshot = await AnyPic.remove(ids);
    selected.clear();
    applySnapshot(snapshot, false);
    renderAll();
    // Selection can be undone the same way a single removal is: the files
    // were never touched, so re-adding their paths restores the entries.
    showToast(`已移除 ${ids.length} 张图片`, "info", {
      label: "撤销",
      onClick: () => void addPaths(removed.map((item) => item.path)),
    });
  } catch (error) {
    showError(error, "移除失败");
  }
}

async function removeItem(id: number) {
  if (batch.running) return;
  const item = items.find((entry) => entry.id === id);
  if (demoMode) {
    items = items.filter((entry) => entry.id !== id);
    invalidatePreview(id);
    scheduleRender();
    if (item) {
      showToast(`已移除 ${item.name}`, "info", {
        label: "撤销",
        onClick: () => {
          items.push(item);
          scheduleRender();
        },
      });
    }
    return;
  }
  try {
    const snapshot = await AnyPic.remove([id]);
    applySnapshot(snapshot, false);
    renderAll();
    if (item) {
      // The original file was never touched, so re-adding it restores the
      // entry; the automatic flow picks it up like a fresh addition.
      showToast(`已移除 ${item.name}`, "info", {
        label: "撤销",
        onClick: () => void addPaths([item.path]),
      });
    }
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
    lastSaved = null;
    selected.clear();
    batch = { running: false, total: 0, done: 0, failed: 0, canceled: false };
    renderAll();
    return;
  }
  try {
    const snapshot = await AnyPic.clear();
    applySnapshot(snapshot, false);
    compressedSignature = "";
    dirty = false;
    lastSaved = null;
    selected.clear();
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

async function setSettings(patch: Partial<Settings>, then?: (next: Settings) => void, onError?: (error: unknown) => void) {
  settings = { ...settings, ...patch };
  if (patch.theme) applyTheme(settings.theme);
  markDirty();
  syncSettingsToUI();
  renderAll();
  queueSave();
  if (!demoMode && (then || onError)) {
    try {
      const next = await AnyPic.saveSettings(settings);
      settings = { ...defaultSettings(), ...next };
      syncSettingsToUI();
      then?.(settings);
    } catch (error) {
      // Keep the optimistic UI; the caller decides whether the failure needs
      // a message (toggles report it, silent preference changes do not).
      onError?.(error);
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

function syncSettingsToUI() {
  const autoToggle = $<HTMLInputElement>("#autoProcessToggle");
  if (autoToggle) autoToggle.checked = autoProcessEnabled();
  const clipboardToggle = $<HTMLInputElement>("#clipboardToggle");
  if (clipboardToggle) clipboardToggle.checked = settings.screenshotClipboard !== "off";
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
    if (autoRun) {
      if (next.canceled) {
        autoRun = null;
        showToast("压缩已取消", "info");
      } else if (autoProcessEnabled() && items.some((item) => item.status === "ready")) {
        // Images arrived while the pass ran; fold them into the same
        // automatic run and save everything once it settles.
        void compressAll(true);
      } else {
        void finishAutoRun();
      }
    } else {
      showBatchResult(next);
    }
  }
  // Progress ticks only move the footer; a full render for every finished
  // image rewrites every row of a large queue for nothing (#9). The running
  // edge still gets one, because action buttons and labels change with it.
  if (wasRunning !== next.running) scheduleRender();
  else renderFooter();
}

/* One image changed mid-batch (status, sizes, thumbnail): repaint its row and
   the aggregates instead of every row of the queue (#9). Rows outside the
   active filter are not cached, so those fall back to a full render. */
function renderItemUpdate(item: Item) {
  upsertItem(item);
  const row = rowCache.get(item.id);
  if (!row || !queueShows(item)) {
    scheduleRender();
    return;
  }
  updateRow(row, item);
  renderStats();
  renderFooter();
  const failed = items.filter((entry) => entry.status === "error").length;
  $("#tabErrorCount").textContent = String(failed);
  $("#tabErrorCount").hidden = failed === 0;
}

function queueShows(item: Item): boolean {
  if (filter === "done") return item.status === "done";
  if (filter === "error") return item.status === "error";
  return true;
}

function showBatchResult(next: Batch) {
  const done = items.filter((item) => item.status === "done");
  const failed = items.filter((item) => item.status === "error").length;
  if (next.canceled) {
    showToast("压缩已取消", "info");
    return;
  }
  if (failed > 0) {
    showToast(`压缩完成：${done.length} 张成功，${failed} 张失败`, "error");
    return;
  }
  const original = done.reduce((sum, item) => sum + item.originalSize, 0);
  const compressed = done.reduce((sum, item) => sum + item.compressedSize, 0);
  const saved = Math.max(0, original - compressed);
  showToast(`压缩完成，共省下 ${formatBytes(saved)}`, "success");
}

/* Ends an automatic run: copies the capture (when there was one), saves the
   results without a dialog, and reports everything in one toast. */
async function finishAutoRun() {
  const run = autoRun;
  autoRun = null;
  if (!run) return;
  if (!autoProcessEnabled()) {
    showBatchResult(batch);
    return;
  }

  const ids = [...run.ids];
  const idSet = new Set(ids);
  const done = items.filter((item) => idSet.has(item.id) && item.status === "done");
  const failed = items.filter((item) => idSet.has(item.id) && item.status === "error");

  let copied = false;
  if (run.copyIDs.length > 0 && settings.screenshotClipboard !== "off") {
    try {
      await AnyPic.copyItemToClipboard(run.copyIDs[run.copyIDs.length - 1]!);
      copied = true;
    } catch (error) {
      showError(error, "无法复制到剪贴板");
    }
  }

  if (done.length === 0) {
    if (failed.length > 0) showToast(`${failed.length} 张图片未能处理`, "error");
    return;
  }

  const original = done.reduce((sum, item) => sum + item.originalSize, 0);
  const compressed = done.reduce((sum, item) => sum + item.compressedSize, 0);
  // Signed on purpose: a pass that made files larger must reach the footer
  // as "增大 N%" instead of being clamped into a misleading "省 0%".
  const saved = original - compressed;
  const percent = original > 0 ? Math.round((saved / original) * 100) : 0;

  try {
    const result = await AnyPic.exportProcessed(ids);
    const first = result.paths[0] ?? "";
    lastSaved = {
      count: result.paths.length || done.length,
      target: result.folder ? shortenPath(result.folder) : "原图旁边",
      percent,
      path: result.folder || first,
    };
    if (failed.length > 0) {
      showToast(`${failed.length} 张图片未能处理`, "error");
    } else if (copied) {
      // The footer already reports the save; the copy still earns a nudge.
      showToast("已复制到剪贴板", "info");
    }
    scheduleRender();
  } catch (error) {
    lastSaved = null;
    showError(error, "自动保存失败");
  }
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
  $$<HTMLButtonElement>("#themeSeg button").forEach((button) => {
    const active = button.dataset.theme === theme;
    button.classList.toggle("active", active);
    button.setAttribute("aria-pressed", String(active));
  });
}

/* ------------------------------------------------------------------ */
/* modals                                                              */
/* ------------------------------------------------------------------ */

function askConfirm(title: string, body: string, confirmLabel: string, cancelLabel = "取消"): Promise<boolean> {
  if (confirmResolve) resolveConfirm(false);
  $("#confirmTitle").textContent = title;
  $("#confirmBody").textContent = body;
  $("#confirmOk").textContent = confirmLabel;
  $("#confirmCancel").textContent = cancelLabel;
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
  renderUpdateStatus();
  openModal($("#aboutModal"));
  $("#aboutClose").focus({ preventScroll: true });
}

function closeAbout() {
  closeModal($("#aboutModal"));
}

function openPrefs() {
  openModal($("#prefsModal"));
  $("#prefsClose").focus({ preventScroll: true });
}

function closePrefs() {
  closeModal($("#prefsModal"));
}

/* ------------------------------------------------------------------ */
/* update check                                                        */
/* ------------------------------------------------------------------ */

let updateInfo: UpdateInfo | null = null;
let updateBusy = false;

function renderUpdateStatus(checking = false) {
  const status = $("#aboutUpdateStatus");
  const download = $<HTMLButtonElement>("#updateDownloadBtn");
  const page = $<HTMLButtonElement>("#updatePageBtn");
  $<HTMLButtonElement>("#updateCheckBtn").disabled = updateBusy;
  if (checking) {
    status.textContent = "正在检查更新…";
    return;
  }
  if (updateInfo?.hasUpdate) {
    status.innerHTML = `新版本 <strong>v${esc(updateInfo.latest)}</strong> 可用（当前 v${esc(updateInfo.current)}）`;
    download.hidden = false;
    page.hidden = false;
    return;
  }
  if (updateInfo) {
    status.textContent = `已是最新版本（v${esc(updateInfo.current)}）`;
  }
  download.hidden = true;
  page.hidden = true;
}

/* silent: the boot-time check keeps failures to itself and only speaks when
   a newer release exists; the About dialog's button always reports. */
async function checkForUpdate(silent = false) {
  if (demoMode) {
    if (!silent) showToast("浏览器预览模式无法检查更新，请在 MyGo 桌面应用中使用", "info");
    return;
  }
  if (updateBusy) return;
  updateBusy = true;
  renderUpdateStatus(true);
  try {
    updateInfo = await AnyPic.checkForUpdate();
  } catch (error) {
    updateInfo = null;
    if (!silent) showError(error, "无法检查更新");
    renderUpdateStatus();
    return;
  } finally {
    updateBusy = false;
  }
  renderUpdateStatus();
  if (silent && updateInfo.hasUpdate) {
    showToast(`新版本 v${updateInfo.latest} 可用，在「关于」里可以更新`, "info", {
      label: "查看更新",
      onClick: () => openAbout(),
    });
  }
}

async function downloadUpdate() {
  if (demoMode) {
    showToast("浏览器预览模式无法下载更新，请在 MyGo 桌面应用中使用", "info");
    return;
  }
  if (!updateInfo?.hasUpdate || updateBusy) return;
  updateBusy = true;
  renderUpdateStatus();
  const label = $("#updateDownloadLabel");
  const stopProgress = events.anypicDownload.on((progress) => {
    label.textContent = progress.total > 0
      ? `下载中 ${Math.round((progress.received / progress.total) * 100)}%`
      : `下载中 ${formatBytes(progress.received)}`;
  });
  try {
    const download = await AnyPic.downloadUpdate();
    if (!download.needsQuit) {
      showToast(`安装包已下载并打开：${shortenPath(download.path)}`, "success");
      return;
    }
    // The installer replaces the running app (NSIS cannot overwrite
    // AnyPic.exe; Finder will not replace a running .app), so the page asks
    // before quitting (#14). The About dialog closes so the confirmation is
    // the only modal open.
    closeAbout();
    let unsavedNote = "";
    try {
      const unsaved = await AnyPic.unsavedResults();
      if (unsaved > 0) {
        unsavedNote = `队列里还有 ${unsaved} 张已压缩但未保存的图片，退出后会丢失。`;
      }
    } catch {
      unsavedNote = "如果队列里还有未保存的压缩结果，退出后会丢失。";
    }
    const install = await askConfirm(
      "安装新版本需要先退出 AnyPic",
      unsavedNote ? `AnyPic 会退出并打开安装程序。${unsavedNote}` : "AnyPic 会退出并打开安装程序。",
      "退出并安装",
      "稍后",
    );
    if (!install) {
      void AnyPic.revealPath(download.path);
      showToast(`安装包已下载到 ${download.path}。安装前请先从托盘退出 AnyPic，再运行安装包。`, "info", {
        label: "在文件夹中显示",
        onClick: () => void AnyPic.revealPath(download.path),
      });
      return;
    }
    try {
      await AnyPic.installUpdate(download.path);
    } catch (error) {
      showError(error, "无法安装更新");
    }
  } catch (error) {
    showError(error, "下载失败");
  } finally {
    updateBusy = false;
    stopProgress();
    label.textContent = "下载并安装";
    renderUpdateStatus();
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
    if (settings.format === "jpeg" && item.hasAlpha) {
      // JPG cannot carry transparency; the demo mirrors the backend, which
      // saves a transparent image as lossless PNG instead (#15).
      item.outputFormat = "PNG";
      item.transparencyKept = true;
      item.outputName = item.name.replace(/\.[^.]+$/, `${settings.suffix}.png`);
    } else {
      item.outputFormat = settings.format === "auto" ? "WEBP" : settings.format.toUpperCase();
      item.transparencyKept = false;
      item.outputName = item.name.replace(/\.[^.]+$/, `${settings.suffix}.${settings.format === "png" ? "png" : settings.format === "jpeg" ? "jpg" : "webp"}`);
    }
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
    case "prefs":
      openPrefs();
      break;
  }
}

void init();
