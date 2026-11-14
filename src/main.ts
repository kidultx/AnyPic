import { AnyPic, events, type Batch, type Item, type Preview, type Settings, type Snapshot, type UpdateInfo } from "./mygo";
import { isMyGo, onFileDrop, runtime } from "mygo-runtime";
import { $, $$, closeModal, esc, formatBytes, formatDimensions, icon, openModal, pct, showError, shortenPath, showToast } from "./ui";
import { errorCodes, language, onSystemLanguageChange, setLanguage, t } from "./i18n";
import { bindPreview, closePreview, initPreview, invalidatePreview, openPreview, previewSelectedId } from "./preview";
import { ensureQueueFocus, initQueueKeys, queueFocusAfterRemoval, queueRowFocus, queueRowKeys, queueRowTabIndex } from "./queue-keys";
import { DEFAULT_SHORTCUT, acceleratorFromEvent, formatShortcut, isReservedShortcut, setShortcutPlatform } from "./shortcuts";

function signature(settings: Settings): string {
  return JSON.stringify([settings.format, settings.quality, settings.maxDimension, settings.suffix, settings.targetSize]);
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
/* Where the last automatic pass saved its results, for the footer summary:
   the folder, or "" for beside the originals — data, so the footer says it
   in the language of the moment (#32). percent is signed: negative when the
   pass produced larger files. */
let lastSaved: { count: number; folder: string; percent: number; path: string } | null = null;
/* The About dialog's version line, kept as data so it can be said again in a
   new language (#32). demoVersion picks the browser-preview wording. */
let aboutVersion: { version: string; platform: string; arch: string } | null = null;
let demoVersion = false;

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
    language: "system",
    autoProcess: "on",
    screenshotShortcut: DEFAULT_SHORTCUT,
    screenshotClipboard: "on",
    screenshotPipeline: "off",
    targetSize: 0,
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
  setLanguage(settings.language);
  initPreview({
    items: () => items,
    visibleIds: () => visibleItems().map((item) => item.id),
    demoMode: () => demoMode,
    batchRunning: () => batch.running,
    autoProcessEnabled,
    applySnapshot,
    startAutoRun,
    renderAll,
  });
  initQueueKeys({
    rows: () => rowCache,
    visibleIds: () => visibleItems().map((item) => item.id),
    selected: () => selected,
    item: (id) => items.find((entry) => entry.id === id) ?? null,
    running: () => batch.running,
    syncSelection: syncRowSelection,
    activate: activateItem,
    decorate: (id) => void openPreview(id, "beautify"),
    exportOne: (id) => void exportOne(id),
    reveal: revealItem,
    removeSelected: () => void removeSelected(),
    removeOne: (id) => void removeItem(id),
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
          aboutVersion = { version: info.version, platform: info.platform, arch: info.arch };
          renderAboutVersion();
          if (!info.screenshotActive) {
            showToast(t.screenshotShortcutInactive(formatShortcut(info.screenshotShortcut)), "error");
          }
        })
        .catch(() => {});
      // Silent boot-time update check: one GitHub API hit, failures stay
      // quiet, and a newer release earns a single toast with a shortcut.
      void checkForUpdate(true);
    } catch (error) {
      showError(error, t.engineUnavailable());
      applyDemoState();
    }
  } else {
    applyDemoState();
  }

  // Following the system keeps following it: a language change on the
  // desktop re-fills the page without a relaunch (#32).
  onSystemLanguageChange(() => {
    syncSettingsToUI();
    renderAll();
  });
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
      ssim: ready ? 0 : 0.978,
      ssimMeasured: !ready,
      targetQuality: 0,
    };
  });
  batch = { running: false, total: items.length, done: 2, failed: 0, canceled: false };
  compressedSignature = signature(settings);
  syncSettingsToUI();
  applyTheme("system");
  aboutVersion = { version: "0.1.0", platform: "", arch: "" };
  demoVersion = true;
  renderAboutVersion();
  window.setTimeout(() => showToast(t.demoToast(), "info"), 700);
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
  const targetMode = targetSizeOn();
  $$<HTMLButtonElement>("#qualityPresets button").forEach((button) => {
    const active = Number(button.dataset.quality) === settings.quality;
    button.classList.toggle("active", active);
    button.setAttribute("aria-pressed", String(active));
    button.disabled = format === "png" || batch.running || targetMode;
  });
  $("#qualityState").textContent = t.custom();
  $("#qualityState").hidden = targetMode || !customQuality;

  const targetSelect = $<HTMLSelectElement>("#targetSize");
  const preset = targetPreset();
  if (document.activeElement !== targetSelect) targetSelect.value = preset;
  $("#targetCustomRow").hidden = preset !== "custom";
  const targetCustom = $<HTMLInputElement>("#targetCustomValue");
  if (document.activeElement !== targetCustom) targetCustom.value = String(targetCustomKB());
  $("#targetNote").hidden = !targetMode;
  $("#targetNote").textContent =
    format === "png" ? t.targetPngNote() : format === "auto" ? t.targetAutoQuality() : t.targetLossyNote();

  const autoToggle = $<HTMLInputElement>("#autoProcessToggle");
  if (document.activeElement !== autoToggle) autoToggle.checked = autoProcessEnabled();
  $("#autoProcessNote").textContent = autoProcessEnabled() ? t.autoProcessNoteOn() : t.autoProcessNoteOff();
  const pipelineToggle = $<HTMLInputElement>("#pipelineToggle");
  const captureCopyOn = settings.screenshotClipboard !== "off";
  pipelineToggle.disabled = !captureCopyOn;
  $("#pipelineNote").textContent = !captureCopyOn
    ? t.screenshotPipelineNeedsClipboard()
    : screenshotPipelineEnabled()
      ? t.screenshotPipelineNoteOn()
      : t.screenshotPipelineNoteOff();
  $("#emptyTitle").textContent = autoProcessEnabled() ? t.emptyTitleAuto() : t.emptyTitleManual();
  $("#emptyShotKey").textContent = formatShortcut(settings.screenshotShortcut);
  $("#outputInlineLabel").textContent =
    settings.outputMode === "folder" && settings.outputFolder ? shortenPath(settings.outputFolder) : t.alongsideOriginal();
  $("#outputInlineBtn").title = settings.outputMode === "folder" && settings.outputFolder ? settings.outputFolder : t.alongsideOriginal();

  const qualityField = quality.closest(".quality-field");
  qualityField?.classList.toggle("disabled", format === "png" || targetMode);
  $("#qualityPresets").closest(".quality-field")?.classList.toggle("disabled", format === "png" || targetMode);
  quality.disabled = format === "png" || batch.running || targetMode;

  const maxDim = $<HTMLSelectElement>("#maxDim");
  if (document.activeElement !== maxDim) maxDim.value = String(settings.maxDimension);

  const suffix = $<HTMLInputElement>("#suffix");
  if (document.activeElement !== suffix) suffix.value = settings.suffix;

  if (!recordingShortcut) $("#shortcutValue").textContent = formatShortcut(settings.screenshotShortcut);
  $("#screenshotBtn").title = t.screenshotTitleWithShortcut(formatShortcut(settings.screenshotShortcut));

  $("#formatHint").textContent =
    format === "auto" ? t.keepOriginalFormat() : format === "png" ? t.formatLossless() : t.formatLossy();

  $("#resetLocation").hidden = !(settings.outputMode === "folder" && settings.outputFolder);

  const tips: Record<string, [string, string]> = {
    auto: [t.keepOriginalFormat(), t.keepFormatTipText()],
    webp: ["WebP", t.webpTipText()],
    jpeg: ["JPG", t.jpegTipText()],
    png: ["PNG", t.pngTipText()],
  };
  const [tipTitle, tipText] = tips[format] ?? tips.auto!;
  $("#tipTitle").textContent = tipTitle;
  $("#tipText").textContent = tipText;
}

function qualityLabel(quality: number): string {
  if (quality >= 92) return t.qualityNearlyLossless();
  if (quality >= 78) return t.balanced();
  if (quality >= 60) return t.qualitySmallerFiles();
  if (quality >= 45) return t.qualityStrong();
  return t.qualityExtreme();
}

function autoProcessEnabled(): boolean {
  return settings.autoProcess !== "off";
}

function screenshotPipelineEnabled(): boolean {
  // The pipeline replaces the capture AnyPic itself copied. With capture
  // copying off there is no raw copy to replace, so it must stay off too.
  return settings.screenshotPipeline === "on" && settings.screenshotClipboard !== "off";
}

/* Target-size mode is a cap in bytes; 0 leaves the quality setting in charge. */
function targetSizeOn(): boolean {
  return settings.targetSize > 0;
}

const TARGET_PRESETS: Record<string, number> = {
  "500": 500 * 1024,
  "1m": 1024 * 1024,
  "2m": 2 * 1024 * 1024,
};
const TARGET_CUSTOM_DEFAULT_KB = 750;

/* Which option the select shows for the stored byte count; anything that is
   not one of the four presets is the custom row. */
function targetPreset(): string {
  if (!targetSizeOn()) return "0";
  for (const [preset, bytes] of Object.entries(TARGET_PRESETS)) {
    if (bytes === settings.targetSize) return preset;
  }
  return "custom";
}

function targetCustomKB(): number {
  const kb = Math.round(settings.targetSize / 1024);
  return kb > 0 ? kb : TARGET_CUSTOM_DEFAULT_KB;
}

/* The quality tiers the queue shows beside the saving figure. SSIM is the
   measured structural similarity to the source, so the tier says what the
   compression actually cost instead of trusting the quality number alone.
   Thresholds are calibrated against testdata/benchmark at quality 82: real
   screenshots and the transparent logo score 0.999–0.991, real photos
   0.994–0.987, the synthetic photo 0.933 and a target-sized photo 0.942.
   0.98 keeps "nearly identical" for the first two clusters without putting a
   q82 photo on the boundary; 0.92 keeps "slight loss" for the photo cluster
   with headroom on both sides. A measured zero is a real score and still gets
   a tier; "not measured" is a separate flag (SSIMMeasured) so it is never
   hidden as zero. */
function qualityTier(ssim: number): { label: string; className: string } {
  if (ssim >= 0.98) return { label: t.qualityNearlyIdentical(), className: "" };
  if (ssim >= 0.92) return { label: t.qualitySlightLoss(), className: "slight" };
  return { label: t.qualityVisibleLoss(), className: "visible" };
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
  $("#statOriginal").textContent = done.length > 0 ? formatBytes(doneOriginal) : t.none();
  $("#statCompressed").textContent = done.length > 0 ? formatBytes(doneCompressed) : t.none();
  $("#statSaved").textContent = done.length > 0 ? `${Math.round(savedPercent)}%` : t.none();
  $("#statSavedFoot").textContent =
    done.length === 0 ? "" : saved > 0 ? t.savedTotal(formatBytes(saved)) : t.noCompressionNeeded();
  $("#summaryDone").style.display = done.length === 0 ? "none" : "";
  $("#summarySaved").style.display = done.length === 0 ? "none" : "";
  $("#summaryPending").hidden = pending === 0;
  $("#summaryPending").textContent = t.pendingCount(pending);
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
  ensureQueueFocus(filtered.map((item) => item.id));
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
  $("#selectionCount").textContent = t.selectionCount(selected.size);
  $<HTMLButtonElement>("#removeSelectedBtn").disabled = batch.running;
}

function syncSelectAll() {
  const button = $<HTMLButtonElement>("#selectAllBtn");
  const all = allVisibleSelected();
  button.textContent = all ? t.deselectAll() : t.selectAll();
  button.setAttribute("aria-pressed", String(all));
  button.disabled = visibleItems().length === 0 || batch.running;
}

/* Repaints every cached row's selected state plus the bars that follow it.
   The checkbox, Shift ranges and ⌘A all land here, so they cannot drift. */
function syncRowSelection() {
  for (const [id, row] of rowCache) {
    const on = selected.has(id);
    row.classList.toggle("is-selected", on);
    row.setAttribute("aria-selected", String(on));
    row.querySelector<HTMLInputElement>("input[data-select]")!.checked = on;
  }
  syncSelectionBar();
  syncSelectAll();
}

/* What a click on a row means; queue-keys.ts reuses it for Space and Enter. */
function activateItem(id: number) {
  const item = items.find((entry) => entry.id === id);
  if (!item) return;
  if (item.status === "done" || item.status === "ready") void openPreview(item.id);
  else if (item.status === "error" && item.error) showToast(item.error, "error");
}

/* The context menu's "show in folder"; the browser demo has no backend. */
function revealItem(id: number) {
  const item = items.find((entry) => entry.id === id);
  if (!item?.path) return;
  if (demoMode) {
    showToast(t.demoOpenFolder(), "info");
    return;
  }
  AnyPic.revealPath(item.path).catch((error) => showError(error, t.unableOpenFolder()));
}

function createRow(id: number): HTMLElement {
  const row = document.createElement("article");
  row.className = "queue-item";
  row.dataset.id = String(id);
  row.innerHTML = `
    <label class="item-select">
      <input type="checkbox" data-select />
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
      <div class="result-pills">
        <span class="quality-pill" hidden></span>
        <span class="saving-pill"></span>
        <span class="target-badge" hidden></span>
      </div>
    </div>
    <div class="item-actions">
      <button class="icon-btn" data-action="preview">${icon("eye")}</button>
      <button class="icon-btn" data-action="decorate">${icon("sparkle")}</button>
      <button class="icon-btn" data-action="export">${icon("download")}</button>
      <button class="icon-btn" data-action="remove">${icon("x")}</button>
    </div>`;

  const checkbox = row.querySelector<HTMLInputElement>("input[data-select]")!;
  checkbox.addEventListener("change", () => {
    if (checkbox.checked) selected.add(id);
    else selected.delete(id);
    syncRowSelection();
  });
  // The label must not turn the click into "open preview".
  checkbox.addEventListener("click", (event) => event.stopPropagation());

  // Focus, arrows, Shift ranges and the context menu live in queue-keys.ts,
  // so the list and the grid get the same manners from one place (#20).
  queueRowKeys(row, id);

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

/* What a row's controls are called. A row outlives a language change, so
   updateRow re-applies these whenever the row is not in the page's language
   (#32); the decorate tooltip depends on the image and stays in updateRow. */
function labelRow(row: HTMLElement) {
  const name = (selector: string, text: string, tip = true) => {
    const control = row.querySelector<HTMLElement>(selector)!;
    control.setAttribute("aria-label", text);
    if (tip) control.title = text;
  };
  name("input[data-select]", t.selectThisImage(), false);
  name('[data-action="preview"]', t.comparePreview());
  name('[data-action="decorate"]', t.beautify(), false);
  name('[data-action="export"]', t.exportThis());
  name('[data-action="remove"]', t.remove());
}

function updateRow(row: HTMLElement, item: Item) {
  if (row.dataset.lang !== language) {
    labelRow(row);
    row.dataset.lang = language;
  }
  row.dataset.id = String(item.id);
  row.title = item.path ? `${item.name}\n${item.path}` : item.name;
  row.classList.toggle("is-clickable", item.status === "done" || item.status === "error");
  row.classList.toggle("is-selected", selected.has(item.id));
  row.setAttribute("aria-selected", String(selected.has(item.id)));
  // Roving tab stop (#20): one focused row is Tab-reachable, the rest are
  // reached with the arrow keys.
  row.tabIndex = queueRowTabIndex(item.id);
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
    item.status === "ready" ? `${esc(item.name)}<span class="item-state">${esc(t.itemWaiting())}</span>` : esc(item.name);

  const meta = row.querySelector<HTMLElement>(".item-meta")!;
  const formatClass = item.format.toLowerCase();
  const animated = item.animated ? `<span class="animated-badge">${esc(t.animatedBadge())}</span>` : "";
  const output = item.status === "done" && item.outputFormat
    ? item.transparencyKept
      ? `<span class="output-format" title="${esc(t.transparencyKeptTitle())}">${esc(t.transparencyKeptOutput())}</span>`
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
  const qualityPill = row.querySelector<HTMLElement>(".quality-pill")!;
  const targetBadge = row.querySelector<HTMLElement>(".target-badge")!;
  qualityPill.hidden = true;
  qualityPill.className = "quality-pill";
  qualityPill.textContent = "";
  qualityPill.removeAttribute("title");
  targetBadge.hidden = true;
  targetBadge.textContent = "";
  targetBadge.removeAttribute("title");
  pill.className = "saving-pill";
  pill.hidden = false;

  if (item.status === "done") {
    if (item.passedThrough) {
      sizes.innerHTML = `<strong>${formatBytes(item.originalSize)}</strong><span>${esc(t.originalWord())}</span>`;
      pill.classList.add("neutral");
      pill.textContent = t.bestAlready();
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
      // The measured quality of this result, not the quality setting: the
      // row should say what the image actually lost. Unmeasured results
      // (animations) say so instead of borrowing a tier they did not earn.
      if (item.ssimMeasured) {
        const tier = qualityTier(item.ssim);
        qualityPill.hidden = false;
        qualityPill.textContent = tier.label;
        if (tier.className) qualityPill.classList.add(tier.className);
        qualityPill.title = t.ssimTitle(item.ssim.toFixed(3));
      } else {
        qualityPill.hidden = false;
        qualityPill.textContent = t.qualityNotMeasured();
        qualityPill.title = t.qualityNotMeasuredTitle();
      }
    }
    // A result can miss its cap even when the original was kept (no smaller
    // encoding exists); the honesty badge belongs to both cases and names
    // both the bytes it reached and the quality it used.
    if (item.targetMissed && (item.targetBytes ?? 0) > 0) {
      const qualityLabel =
        item.passedThrough
          ? t.targetQualityOriginal()
          : (item.targetQuality ?? 0) > 0
            ? t.targetQualityValue(item.targetQuality ?? 0)
            : t.targetQualityLossless();
      targetBadge.hidden = false;
      targetBadge.textContent = t.targetMissedBadge(formatBytes(item.compressedSize), qualityLabel);
      targetBadge.title = t.targetMissedTitle(
        formatBytes(item.targetBytes ?? 0),
        formatBytes(item.compressedSize),
        qualityLabel,
      );
    }
  } else if (item.status === "working") {
    sizes.innerHTML = `<span>${esc(t.optimizing())}</span>`;
    pill.classList.add("working");
    pill.innerHTML = `${icon("loader")} ${esc(t.compressingState())}`;
  } else if (item.status === "error") {
    sizes.innerHTML = `<span>${esc(t.cannotCompress())}</span>`;
    pill.classList.add("error");
    pill.textContent = t.failed();
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
  decorateButton.title = item.animated ? t.animatedNoBeautify() : t.beautifyWithCurrent();
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

  let status = t.ready();
  let showOpen = false;
  if (batch.running) {
    // Overall queue progress: an auto pass runs a smaller batch on top of
    // already-finished items, so batch.total is not the queue size and the
    // bar above uses the same completed/total pair.
    status = t.compressingProgress(completed, total);
  } else if (auto && lastSaved) {
    const savings = lastSaved.percent >= 0
      ? t.savedPercent(String(lastSaved.percent))
      : t.grewPercent(String(-lastSaved.percent));
    status = lastSaved.folder
      ? t.savedTo(lastSaved.count, shortenPath(lastSaved.folder), savings)
      : t.savedAlongside(lastSaved.count, savings);
    showOpen = lastSaved.path !== "";
  } else if (batch.canceled) {
    status = t.compressionCanceled();
  } else if (auto) {
    status = done.length > 0 && pending.length === 0 ? t.compressionComplete() : t.ready();
  } else if (done.length === total) {
    status = t.compressionComplete();
  } else if (failed.length > 0 && completed === total) {
    status = t.someFailed();
  } else if (done.length > 0) {
    status = t.donePending(done.length, total - completed);
  }

  $("#batchStatus").textContent = status;
  $("#openSavedBtn").hidden = !showOpen;
  $("#retryFailedBtn").hidden = batch.running || failed.length === 0;
  $("#retryFailedBtn").textContent = failed.length > 0 ? t.retryFailedCount(failed.length) : t.retryFailed();
  $("#batchPercent").textContent = `${percent}%`;
  $("#progressFill").style.transform = `scaleX(${percent / 100})`;
  renderStageAction();
}

/* The footer carries the one primary action (#19): compress while anything is
   waiting or stale, export once every image is done. Capture lives in the
   toolbar and no longer borrows this button, so its position never moves. */
function renderStageAction() {
  const button = $<HTMLButtonElement>("#stageActionBtn");
  const label = $("#stageActionLabel");
  const done = items.filter((item) => item.status === "done").length;
  const pending = items.some((item) => item.status === "ready" || item.status === "working");

  const action: "compress" | "export" =
    !batch.running && done > 0 && !pending && !dirty ? "export" : "compress";
  button.dataset.action = action;

  if (action === "export") {
    setUseIcon("stageActionIcon", "download");
    label.textContent = t.exportLabel();
    button.title = t.exportCount(done);
    button.disabled = false;
  } else {
    setUseIcon("stageActionIcon", "refresh");
    label.textContent = batch.running ? t.compressing() : dirty ? t.recompress() : t.compressAll();
    button.title = batch.running ? t.compressingTitle() : t.compressQueuedTitle();
    button.disabled = batch.running || items.length === 0;
  }
}

function renderActions() {
  const running = batch.running;
  $("#cancelBtn").hidden = !running;
  $<HTMLButtonElement>("#clearBtn").disabled = running || items.length === 0;
  $<HTMLButtonElement>("#addBtn").disabled = running;
  $<HTMLButtonElement>("#screenshotBtn").disabled = running;
  $<HTMLButtonElement>("#emptyAddBtn").disabled = running;
  $<HTMLButtonElement>("#resetBtn").disabled = running;
  $<HTMLButtonElement>("#suffix").disabled = running;
  $<HTMLButtonElement>("#maxDim").disabled = running;
  $$<HTMLButtonElement>("#formatSeg button").forEach((button) => {
    button.disabled = running;
  });
  $<HTMLInputElement>("#quality").disabled = running || settings.format === "png" || targetSizeOn();
  $$<HTMLButtonElement>("#qualityPresets button").forEach((button) => {
    button.disabled = running || settings.format === "png" || targetSizeOn();
  });
  $<HTMLSelectElement>("#targetSize").disabled = running;
  $<HTMLInputElement>("#targetCustomValue").disabled = running;
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
  $("#languageSeg").addEventListener("click", (event) => {
    const button = (event.target as HTMLElement).closest<HTMLButtonElement>("button[data-language]");
    if (!button) return;
    const value = button.dataset.language;
    if (value !== "system" && value !== "zh-Hans" && value !== "en") return;
    void setSettings({ language: value });
  });
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
  bindToggle("#clipboardToggle", "screenshotClipboard", t.clipboardSettingsFailed());
  bindToggle("#autoProcessToggle", "autoProcess", t.autoProcessSettingsFailed());
  bindToggle("#dockToggle", "hideDockWhenTray", t.dockSettingsFailed());
  $("#openAtLoginToggle").addEventListener("change", (event) => {
    const input = event.target as HTMLInputElement;
    const on = input.checked;
    void setSettings(
      { openAtLogin: on ? "on" : "off" },
      (next) => {
        // The backend may refuse the login item; the toggle follows the truth.
        input.checked = next.openAtLogin === "on";
        if (input.checked !== on) showToast(t.systemRefused(), "error");
      },
      (error) => {
        input.checked = !on;
        settings = { ...settings, openAtLogin: on ? "off" : "on" };
        showError(error, t.loginItemFailed());
      },
    );
  });
  $("#screenPermissionBtn").addEventListener("click", () => {
    if (demoMode) return;
    void AnyPic.openScreenRecordingSettings().catch(() => {
      showToast(t.permissionManual(), "info");
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
        showToast(t.shortcutInUse(), "error");
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
    void AnyPic.openReleasePage().catch((error) => showError(error, t.openReleaseFailed()));
  });

  $("#resetBtn").addEventListener("click", () => {
    const next: Settings = {
      ...settings,
      format: "auto",
      quality: 82,
      maxDimension: 0,
      suffix: "-anypic",
      targetSize: 0,
    };
    void setSettings(next);
    showToast(t.resetDone(), "success");
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

  $("#targetSize").addEventListener("change", (event) => {
    const preset = (event.target as HTMLSelectElement).value;
    if (preset === "0") {
      void setSettings({ targetSize: 0 });
      return;
    }
    if (preset === "custom") {
      // Entering the custom row while a fixed preset is selected starts from
      // the custom default; reusing the preset value would make the select
      // snap straight back to that preset and hide the input.
      const current = targetPreset() === "custom" ? settings.targetSize : TARGET_CUSTOM_DEFAULT_KB * 1024;
      void setSettings({ targetSize: current });
      return;
    }
    const bytes = TARGET_PRESETS[preset];
    if (bytes) void setSettings({ targetSize: bytes });
  });

  const targetCustom = $<HTMLInputElement>("#targetCustomValue");
  // Commit on change (blur/Enter), not on every keystroke: "1" on the way to
  // "150" must not briefly run a 1 KB target or rewrite the field.
  targetCustom.addEventListener("change", () => {
    const kb = Math.max(10, Math.min(250000, Math.round(Number(targetCustom.value) || 0)));
    targetCustom.value = String(kb);
    void setSettings({ targetSize: kb * 1024 });
  });
  targetCustom.addEventListener("keydown", (event) => {
    if (event.key !== "Enter") return;
    event.preventDefault();
    targetCustom.blur();
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
      showToast(t.demoNoDrop(), "info");
    }
  });
}

/* ------------------------------------------------------------------ */
/* actions                                                             */
/* ------------------------------------------------------------------ */

async function pickFiles() {
  if (demoMode) {
    showToast(t.demoSelectImages(), "info");
    return;
  }
  try {
    const result = await AnyPic.pickFiles();
    applyAddResult(result);
  } catch (error) {
    showError(error, t.addFailed());
  }
}

async function captureScreenshot() {
  if (demoMode) {
    showToast(t.demoNoScreenshot(), "info");
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
    if (result.copied) showToast(t.captureCopied(), "info");
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
    showError(error, t.screenshotFailed());
  }
}

function isCaptureNotice(error: unknown) {
  const message = error instanceof Error ? error.message : typeof error === "string" ? error : "";
  return message.includes(errorCodes.captureCanceled) || message.includes(errorCodes.captureBusy);
}

function isScreenCapturePermission(error: unknown) {
  const message = error instanceof Error ? error.message : typeof error === "string" ? error : "";
  return message.includes(errorCodes.screenPermission);
}

async function showScreenCapturePermission() {
  const openSettings = await askConfirm(
    t.permissionTitle(),
    t.permissionBody(),
    t.openSystemSettings(),
    t.later(),
  );
  if (!openSettings) return;
  await AnyPic.openScreenRecordingSettings().catch(() => {
    showToast(t.permissionManual(), "info");
  });
}

async function pasteImage() {
  if (demoMode) {
    showToast(t.demoNoClipboard(), "info");
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
    if (message.includes(errorCodes.noClipboardImage)) {
      showToast(t.pasteNoImage(), "info");
      return;
    }
    showError(error, t.pasteFailed());
  }
}

type AddSource = "files" | "clipboard";

async function addPaths(paths: string[]) {
  if (demoMode || paths.length === 0) return;
  try {
    const result = await AnyPic.addPaths(paths);
    applyAddResult(result);
  } catch (error) {
    showError(error, t.addFailed());
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
      showToast(
        result.skipped.length ? t.addedWithSkipped(result.added, result.skipped.length) : t.added(result.added),
        "success",
      );
    }
    if (result.skipped.length > 0) {
      const first = result.skipped[0]!;
      showToast(t.skippedFile(first.name, first.reason), "info");
    }
    if (!auto) {
      setTimeout(() => $("#queueScroll").scrollTo({ top: $("#queueScroll").scrollHeight, behavior: "smooth" }), 100);
    }
  } else if (result.skipped.length > 0) {
    const first = result.skipped[0]!;
    showToast(t.skippedFile(first.name, first.reason), "error");
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
      ssim: 0,
      ssimMeasured: false,
      targetBytes: 0,
      targetQuality: 0,
      targetMissed: false,
    }));
  }
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
    showError(error, auto ? t.autoFailed() : t.startFailed());
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
    showError(error, t.cancelFailed());
  }
}

async function retryFailed() {
  if (batch.running || compressStarting) return;
  const failed = items.filter((item) => item.status === "error");
  if (failed.length === 0) return;
  if (demoMode) {
    showToast(t.demoNoRetry(), "info");
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
    showError(error, t.retryFailedAction());
  } finally {
    compressStarting = false;
  }
}

async function removeSelected() {
  if (batch.running || selected.size === 0) return;
  const ids = [...selected];
  const removed = items.filter((item) => ids.includes(item.id));
  // Remembered before the confirmation steals focus: after the rows go,
  // focus belongs back in the queue, not on the disappearing dialog (#20).
  const focusInQueue = !!document.activeElement?.closest?.("#queueList");
  const ok = await askConfirm(t.removeTitle(ids.length), t.removeBody(), t.remove());
  if (!ok) return;
  queueFocusAfterRemoval(new Set(ids));
  if (demoMode) {
    for (const item of removed) invalidatePreview(item.id);
    items = items.filter((item) => !selected.has(item.id));
    selected.clear();
    renderAll();
    if (focusInQueue) queueRowFocus();
    return;
  }
  try {
    const snapshot = await AnyPic.remove(ids);
    selected.clear();
    applySnapshot(snapshot, false);
    renderAll();
    if (focusInQueue) queueRowFocus();
    // Selection can be undone the same way a single removal is: the files
    // were never touched, so re-adding their paths restores the entries.
    showToast(t.removedMany(ids.length), "info", {
      label: t.undo(),
      onClick: () => void addPaths(removed.map((item) => item.path)),
    });
  } catch (error) {
    showError(error, t.removeFailed());
  }
}

async function removeItem(id: number) {
  if (batch.running) return;
  const item = items.find((entry) => entry.id === id);
  const focusInQueue = !!document.activeElement?.closest?.("#queueList");
  queueFocusAfterRemoval(new Set([id]));
  if (demoMode) {
    items = items.filter((entry) => entry.id !== id);
    invalidatePreview(id);
    scheduleRender();
    if (item) {
      showToast(t.removedOne(item.name), "info", {
        label: t.undo(),
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
    if (focusInQueue) queueRowFocus();
    if (item) {
      // The original file was never touched, so re-adding it restores the
      // entry; the automatic flow picks it up like a fresh addition.
      showToast(t.removedOne(item.name), "info", {
        label: t.undo(),
        onClick: () => void addPaths([item.path]),
      });
    }
  } catch (error) {
    showError(error, t.removeFailed());
  }
}

async function clearQueue() {
  if (batch.running || items.length === 0) return;
  const ok = await askConfirm(t.clearTitle(), t.clearBody(), t.clearConfirm());
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
    showToast(t.queueCleared(), "success");
  } catch (error) {
    showError(error, t.clearFailed());
  }
}

async function chooseOutputFolder() {
  if (batch.running) return;
  if (demoMode) {
    showToast(t.demoExportLocation(), "info");
    return;
  }
  try {
    const next = await AnyPic.chooseOutputFolder();
    applySettingsFromBackend(next);
  } catch (error) {
    showError(error, t.selectFolderFailed());
  }
}

async function exportAll() {
  const done = items.filter((item) => item.status === "done");
  if (batch.running || done.length === 0) return;
  await flushSave();
  if (demoMode) {
    showToast(t.demoExported(done.length), "success");
    return;
  }
  try {
    const result = await AnyPic.exportAll();
    if (result.canceled) return;
    showToast(t.exported(result.count), "success", {
      label: t.showInFolder(),
      onClick: () => void AnyPic.revealPath(result.folder),
    });
  } catch (error) {
    showError(error, t.exportFailed());
  }
}

async function exportOne(id: number) {
  const item = items.find((entry) => entry.id === id);
  if (!item || item.status !== "done" || batch.running) return;
  await flushSave();
  if (demoMode) {
    showToast(t.demoExportOne(item.outputName || item.name), "success");
    return;
  }
  try {
    const result = await AnyPic.exportOne(id);
    if (result.canceled) return;
    const path = result.paths[0];
    showToast(t.exportedOne(item.outputName || item.name), "success", path
      ? { label: t.showInFolder(), onClick: () => void AnyPic.revealPath(path) }
      : undefined);
  } catch (error) {
    showError(error, t.exportFailed());
  }
}

/* ------------------------------------------------------------------ */
/* settings                                                            */
/* ------------------------------------------------------------------ */

function startShortcutRecording() {
  if (demoMode) {
    showToast(t.demoNoShortcut(), "info");
    return;
  }
  recordingShortcut = true;
  void AnyPic.setShortcutRecording(true).catch(() => {});
  $("#shortcutBtn").classList.add("recording");
  $("#shortcutValue").textContent = t.shortcutRecording();
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
    showToast(t.shortcutSet(formatShortcut(settings.screenshotShortcut)), "success");
  } catch (error) {
    stopShortcutRecording();
    showError(error, t.shortcutFailed());
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
  // The stored choice is the source of truth for the page too: an explicit
  // change, a saved language from another launch, or "system" all land here
  // (#32).
  setLanguage(settings.language);
  renderAboutVersion();
  $$<HTMLButtonElement>("#languageSeg button").forEach((button) => {
    const active = button.dataset.language === settings.language;
    button.classList.toggle("active", active);
    button.setAttribute("aria-pressed", String(active));
  });
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
        showToast(t.compressionCanceled(), "info");
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
    showToast(t.compressionCanceled(), "info");
    return;
  }
  if (failed > 0) {
    showToast(t.batchFailed(done.length, failed), "error");
    return;
  }
  const original = done.reduce((sum, item) => sum + item.originalSize, 0);
  const compressed = done.reduce((sum, item) => sum + item.compressedSize, 0);
  const saved = Math.max(0, original - compressed);
  showToast(t.batchSaved(formatBytes(saved)), "success");
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
      showError(error, t.copyFailed());
    }
  }

  if (done.length === 0) {
    if (failed.length > 0) showToast(t.notProcessed(failed.length), "error");
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
      folder: result.folder || "",
      percent,
      path: result.folder || first,
    };
    if (failed.length > 0) {
      showToast(t.notProcessed(failed.length), "error");
    } else if (copied) {
      // The footer already reports the save; the copy still earns a nudge.
      showToast(t.copied(), "info");
    }
    scheduleRender();
  } catch (error) {
    lastSaved = null;
    showError(error, t.autoSaveFailed());
  }
}

/* ------------------------------------------------------------------ */
/* theme                                                               */
/* ------------------------------------------------------------------ */

function cycleTheme() {
  const next = settings.theme === "system" ? "dark" : settings.theme === "dark" ? "light" : "system";
  void setSettings({ theme: next });
  showToast(
    next === "system" ? t.themeSystem() : next === "dark" ? t.themeDark() : t.themeLight(),
    "info",
  );
}

function renderAboutVersion() {
  if (!aboutVersion) return;
  $("#aboutVersion").textContent = demoVersion
    ? t.aboutVersionDemo(aboutVersion.version)
    : t.aboutVersionApp(aboutVersion.version, aboutVersion.platform, aboutVersion.arch);
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

function askConfirm(title: string, body: string, confirmLabel: string, cancelLabel = t.cancel()): Promise<boolean> {
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
  $("#updateDownloadLabel").textContent = t.downloadAndInstall();
  $<HTMLButtonElement>("#updateCheckBtn").disabled = updateBusy;
  if (checking) {
    status.textContent = t.checkingUpdate();
    return;
  }
  if (updateInfo?.hasUpdate) {
    status.textContent = t.updateAvailable(updateInfo.latest, updateInfo.current);
    download.hidden = false;
    page.hidden = false;
    return;
  }
  status.textContent = updateInfo ? t.upToDate(updateInfo.current) : t.checkUpdate();
  download.hidden = true;
  page.hidden = true;
}

/* silent: the boot-time check keeps failures to itself and only speaks when
   a newer release exists; the About dialog's button always reports. */
async function checkForUpdate(silent = false) {
  if (demoMode) {
    if (!silent) showToast(t.demoNoUpdate(), "info");
    return;
  }
  if (updateBusy) return;
  updateBusy = true;
  renderUpdateStatus(true);
  try {
    updateInfo = await AnyPic.checkForUpdate();
  } catch (error) {
    updateInfo = null;
    if (!silent) showError(error, t.checkUpdateFailed());
    renderUpdateStatus();
    return;
  } finally {
    updateBusy = false;
  }
  renderUpdateStatus();
  if (silent && updateInfo.hasUpdate) {
    showToast(t.updateAvailableToast(updateInfo.latest), "info", {
      label: t.viewUpdate(),
      onClick: () => openAbout(),
    });
  }
}

async function downloadUpdate() {
  if (demoMode) {
    showToast(t.demoNoDownload(), "info");
    return;
  }
  if (!updateInfo?.hasUpdate || updateBusy) return;
  updateBusy = true;
  renderUpdateStatus();
  const label = $("#updateDownloadLabel");
  const stopProgress = events.anypicDownload.on((progress) => {
    label.textContent = progress.total > 0
      ? t.downloadingPercent(Math.round((progress.received / progress.total) * 100))
      : t.downloadingSize(formatBytes(progress.received));
  });
  try {
    const download = await AnyPic.downloadUpdate();
    if (!download.needsQuit) {
      showToast(t.updateDownloadedOpened(shortenPath(download.path)), "success");
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
        unsavedNote = t.unsavedResults(unsaved);
      }
    } catch {
      unsavedNote = t.unsavedUnknown();
    }
    const install = await askConfirm(
      t.installTitle(),
      unsavedNote ? t.installBodyUnsaved(unsavedNote) : t.installBody(),
      t.quitAndInstall(),
      t.later(),
    );
    if (!install) {
      void AnyPic.revealPath(download.path);
      showToast(t.installerDownloaded(download.path), "info", {
        label: t.showInFolder(),
        onClick: () => void AnyPic.revealPath(download.path),
      });
      return;
    }
    try {
      await AnyPic.installUpdate(download.path);
    } catch (error) {
      showError(error, t.installFailed());
    }
  } catch (error) {
    showError(error, t.downloadFailed());
  } finally {
    updateBusy = false;
    stopProgress();
    label.textContent = t.downloadAndInstall();
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
    // Mirror the backend's target mode: fit the cap when the encoder can,
    // and say so honestly when the source is already smaller than the cap,
    // the format is lossless PNG, or even the lowest lossy quality cannot
    // reach it. The floor is what makes the last case visible in the demo:
    // without it every non-PNG cap would look reachable.
    item.targetBytes = settings.targetSize > 0 ? settings.targetSize : 0;
    item.targetMissed = false;
    const demoFloor = Math.max(1024, Math.round(item.originalSize * 0.04));
    if (settings.targetSize > 0 && item.compressedSize > settings.targetSize) {
      if (settings.format === "png" || item.originalSize <= settings.targetSize) {
        item.targetMissed = true;
      } else if (settings.targetSize < demoFloor) {
        item.targetMissed = true;
        item.compressedSize = demoFloor;
      } else {
        item.compressedSize = Math.round(settings.targetSize * (0.86 + Math.random() * 0.08));
      }
    }
    item.savedPercent = ((item.originalSize - item.compressedSize) / item.originalSize) * 100;
    // The demo has no encoder to measure, so the quality tier follows the
    // compression ratio: more aggressive passes read as more loss.
    item.ssim = Math.min(0.999, Math.max(0.8, 1 - (item.compressedSize / item.originalSize) * 0.16 - Math.random() * 0.01));
    item.ssimMeasured = true;
    // A miss that hit the floor was encoded at the lowest quality the search
    // tries; a reachable cap lands somewhere in the usual range.
    item.targetQuality =
      item.targetBytes > 0 && settings.format !== "png"
        ? item.targetMissed
          ? 10
          : Math.round(55 + Math.random() * 30)
        : 0;
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
  if (demoCanceled) showToast(t.compressionCanceled(), "info");
  else showToast(t.demoCompressDone(), "success");
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
