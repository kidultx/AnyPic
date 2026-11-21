// Theme and language toggles. Both are remembered per browser when storage is
// available; the page works the same without it.
(() => {
  const root = document.documentElement;
  const store = {
    get: (k) => { try { return localStorage.getItem(k); } catch { return null; } },
    set: (k, v) => { try { localStorage.setItem(k, v); } catch {} },
  };

  const themeBtn = document.getElementById("theme");
  const langBtn = document.getElementById("lang");

  const savedTheme = store.get("theme");
  if (savedTheme === "light" || savedTheme === "dark") root.dataset.theme = savedTheme;
  themeBtn.addEventListener("click", () => {
    const dark = root.dataset.theme
      ? root.dataset.theme === "dark"
      : matchMedia("(prefers-color-scheme: dark)").matches;
    // Switch in one frame: without this the buttons' hover transition fades
    // their fill while their text colour has already flipped.
    root.classList.add("switching");
    root.dataset.theme = dark ? "light" : "dark";
    store.set("theme", root.dataset.theme);
    requestAnimationFrame(() => requestAnimationFrame(() => root.classList.remove("switching")));
  });

  const en = {
    "nav.video": "Video", "nav.features": "Features", "nav.formats": "Formats", "nav.faq": "FAQ",
    "hero.badge": "Images never leave your computer",
    "hero.title": "Make images smaller.<br>Hand them to no one.",
    "hero.lede": "AnyPic is a cross-platform image compressor for the desktop. Drop images in, click once, and they shrink — all on your own machine. No uploads, no accounts, no telemetry.",
    "hero.download": "Download free", "hero.source": "View source",
    "stat.batch": "images per batch", "stat.file": "per-file limit", "stat.upload": "image uploads", "stat.os": "platforms",
    "v.title": "AnyPic in 40 seconds",
    "v.note": "No sound · file sizes are illustrative",
    "f.title": "Do one thing well — honestly.",
    "f1.t": "Smart compression",
    "f1.d": "JPG / PNG / WebP keep their format; GIF, BMP and TIFF become JPG or PNG. Quality 30–100, optional long-edge resize to 3840 / 2560 / 1920 / 1280 / 800 px.",
    "f2.tag": "Not smaller",
    "f2.t": "Honest results",
    "f2.d": "When the format stays the same and no resize is requested, a re-encode ships only if it is strictly smaller. Otherwise AnyPic keeps the original and says why — this run did not shrink it, the original already fits the size cap, or a pixel-by-pixel check caught a change. Still-image format changes and resizes are exempt: the result may be larger than the original, and the row marks it “+%”. Results that can be measured are graded by SSIM (nearly identical / slight loss / visible loss); animations and other unmeasurable results say “quality not measured” instead of pretending to a score, and settings never stand in for measured quality.",
    "demo.mock": "Illustrative",
    "f8.t": "Target size",
    "f8.d": "Set a cap for every image: 500 KB, 1 MB, 2 MB or your own. JPG and WebP search for the highest quality that fits; when nothing fits, AnyPic reports the smallest size it reached and the quality it used instead of giving up silently. PNG is lossless, so the cap cannot tune its quality — it may still exceed the cap.",
    "f3.t": "Animations keep every frame",
    "f3.d": "Animated GIF / WebP become animated WebP with frames, timing, looping and transparency intact — never just the first frame.",
    "f4.t": "Transparency stays",
    "f4.d": "Transparent images are never silently flattened onto black or white.",
    "f5.t": "Hotkey screenshots + beautify",
    "f5.d": "Capture a region any time and it lands in the queue. In preview, pick a solid colour, a gradient, one of 15 patterns or one of 4 photo backdrops, then tune corners, border, layered shadow, padding and canvas ratio — one click to apply.",
    "f6.t": "Never overwrites originals",
    "f6.d": "Output goes next to the original with a suffix, or to a folder you choose. The source file is untouched.",
    "f7.t": "Privacy is the architecture, not a setting",
    "f7.d": "AnyPic never uploads images, has no accounts and no telemetry. Its only network activity is checking for updates and downloading installers — at launch or when you click, and only from GitHub Releases. It’s open source, so you can check.",
    "m.title": "Pleasant every day",
    "m1": "<b>Batch</b>: drop files or whole folders, up to 500 at once.",
    "m2": "<b>Open With</b>: pick AnyPic in Finder / Explorer, or drop onto the Dock / taskbar icon.",
    "m3": "<b>Paste to queue</b>: copy an image, press ⌘V / Ctrl+V.",
    "m4": "<b>Before / after</b>: drag the divider to compare detail.",
    "m5": "<b>Lives in the tray</b>: the screenshot hotkey works with the window closed; launch at login.",
    "m6": "<b>Retry failures</b>: finished images are not re-encoded.",
    "m7": "<b>Copy a result</b>: the row button, the context menu or ⌘C / Ctrl+C. AnyPic can only put PNG on the clipboard for now; JPEG / WebP results are re-encoded first and can end up several to ten-plus times the original file. The toast states the real format and both sizes.",
    "fm.title": "Formats & limits", "fm.in": "Input", "fm.anim": "(incl. animated)", "fm.apng": "(APNG kept as-is)", "fm.out": "Output",
    "fm.outv": "JPG · PNG · WebP; animated GIF / WebP become animated WebP; APNG and CMYK sources keep the original with a stated reason",
    "fm.limit": "Limits", "fm.limitv": "250 MB per file, 500 images per batch", "fm.no": "Not yet",
    "fm.skip": "(skipped files and the reason are reported when you drop them)",
    "fm.png": "PNG output is lossless, so the quality slider has no effect on it — a format limit we show plainly.",
    "fm.meta": "Metadata", "fm.metav": "PNG / JPG output keeps the ICC colour profile when it matches the pixel colour space. JPEG EXIF keeps all fields except GPS; Orientation is reset to 1 because the pixels were already rotated. XMP, IPTC and other metadata are dropped; WebP output does not carry ICC yet; metadata that cannot be carried is stated in the result row.",
    "q.title": "FAQ",
    "q1": "macOS says the app can’t be opened?",
    "a1": "The build isn’t notarized by Apple. Go to System Settings → Privacy & Security and click “Open Anyway”.",
    "q2": "Screenshots only show the wallpaper?",
    "a2": "On macOS, allow AnyPic under System Settings → Privacy & Security → Screen Recording. The sidebar has a shortcut.",
    "q3": "Does AnyPic go online?",
    "a3": "Only to check for updates and download installers, and only from GitHub Releases. Your images never leave your computer.",
    "q4": "What does Linux need?",
    "a4": "WebKitGTK (libwebkit2gtk-4.1) to run; one of gnome-screenshot, spectacle, or grim + slurp + wl-copy for screenshots.",
    "q5": "Is it free?", "a5": "Free and open source.",
    "end.t": "Make your images lighter today", "foot.issue": "Report an issue",
  };
  const nodes = [...document.querySelectorAll("[data-i18n]")];
  const zh = new Map(nodes.map((n) => [n, n.innerHTML]));
  const zhTitle = document.title;

  // The intro video has a Chinese and an English cut ("*.en.*") on the same
  // timeline, so switching language mid-play picks up at the same moment.
  const video = document.querySelector("#video video");
  let resume = null; // { at, playing } waiting for the other cut's metadata
  video?.addEventListener("loadedmetadata", () => {
    if (!resume) return;
    const { at, playing } = resume;
    resume = null;
    video.currentTime = at;
    if (playing) video.play().catch(() => {});
  });
  const setVideo = (isEn) => {
    if (!video) return;
    const cut = (path) => path.replace(/(\.en)?(\.\w+)$/, (isEn ? ".en" : "") + "$2");
    const poster = video.getAttribute("poster");
    if (cut(poster) === poster) return;
    const state = resume ?? { at: video.currentTime, playing: !video.paused };
    video.setAttribute("poster", cut(poster));
    for (const s of video.querySelectorAll("source")) s.setAttribute("src", cut(s.getAttribute("src")));
    video.load();
    resume = state.at > 0 || state.playing ? state : null;
  };

  const setLang = (lang) => {
    const isEn = lang === "en";
    setVideo(isEn);
    for (const n of nodes) n.innerHTML = isEn ? en[n.dataset.i18n] ?? zh.get(n) : zh.get(n);
    root.lang = isEn ? "en" : "zh-CN";
    document.title = isEn ? "AnyPic — Local image compression" : zhTitle;
    langBtn.textContent = isEn ? "中文" : "EN";
    langBtn.setAttribute("aria-label", isEn ? "切换到中文" : "Switch to English");
    themeBtn.setAttribute("aria-label", isEn ? "Toggle light / dark theme" : "切换亮/暗主题");
    store.set("lang", isEn ? "en" : "zh");
  };
  langBtn.addEventListener("click", () => setLang(root.lang === "en" ? "zh" : "en"));
  const pref = store.get("lang") ?? (navigator.language?.toLowerCase().startsWith("zh") ? "zh" : "en");
  if (pref === "en") setLang("en");
})();
