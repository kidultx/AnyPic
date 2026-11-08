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
    "v.note": "No sound · on-screen text in Chinese · file sizes are illustrative",
    "f.title": "Do one thing well — honestly.",
    "f1.t": "Smart compression",
    "f1.d": "JPG / PNG / WebP keep their format; GIF, BMP and TIFF become JPG or PNG. Quality 30–100, optional long-edge resize to 3840 / 2560 / 1920 / 1280 / 800 px.",
    "f2.tag": "Already optimal",
    "f2.t": "Honest results",
    "f2.d": "If re-encoding would make a file bigger, AnyPic keeps the original and labels it “already optimal”.",
    "f3.t": "Animations keep every frame",
    "f3.d": "Animated GIF / WebP become animated WebP with frames, timing, looping and transparency intact — never just the first frame.",
    "f4.t": "Transparency stays",
    "f4.d": "Transparent images are never silently flattened onto black or white.",
    "f5.t": "Hotkey screenshots + beautify",
    "f5.d": "Capture a region any time and it lands in the queue. Add background, corners, border, shadow and canvas ratio in preview.",
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
    "fm.title": "Formats & limits", "fm.in": "Input", "fm.anim": "(incl. animated)", "fm.out": "Output",
    "fm.outv": "JPG · PNG · WebP; animated GIF / WebP become animated WebP",
    "fm.limit": "Limits", "fm.limitv": "250 MB per file, 500 images per batch", "fm.no": "Not yet",
    "fm.png": "PNG output is lossless, so the quality slider has no effect on it — a format limit we show plainly.",
    "fm.meta": "Metadata", "fm.metav": "Re-encoding strips EXIF / XMP; orientation is corrected first.",
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

  const setLang = (lang) => {
    const isEn = lang === "en";
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
