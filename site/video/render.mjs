// Renders the storyboard (src/index.html) frame by frame and encodes the intro video.
//
// Outputs, next to this file:
//   anypic-intro.mp4   H.264, via encode-mp4.swift (macOS AVFoundation)
//   anypic-intro.webm  VP8, via the ffmpeg that ships with Playwright
//   poster.jpg         a settled frame of the batch scene
//   ../assets/og.png   1200×630 share card from the title scene
//
// Playwright is kept out of the project's package.json. To regenerate:
//   mkdir -p /tmp/anypic-rec && cd /tmp/anypic-rec && npm i playwright && npx playwright install chromium ffmpeg
//   node site/video/render.mjs            (PW_DIR=<dir with node_modules/playwright> to override)
import { createRequire } from "node:module";
import { execFileSync, spawn } from "node:child_process";
import { createReadStream, mkdirSync, readdirSync, rmSync } from "node:fs";
import { homedir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const pwDir = process.env.PW_DIR ?? "/tmp/anypic-rec";
const { chromium } = createRequire(join(pwDir, "/"))("playwright");
const FPS = 30;
const POSTER_MS = 10200; // scene 2, every row finished
const OG_MS = 3000; // scene 1, settled
const frames = "/tmp/anypic-frames";

rmSync(frames, { recursive: true, force: true });
mkdirSync(frames, { recursive: true });

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1920, height: 1080 }, deviceScaleFactor: 1 });
await page.goto(pathToFileURL(join(here, "src/index.html")).href + "?render");
await page.evaluate(() => document.fonts.ready);
const total = await page.evaluate(() => window.TOTAL_MS);
const seek = (ms) => page.evaluate((t) => window.seek(t), ms);

const count = Math.round((total / 1000) * FPS);
for (let i = 0; i < count; i++) {
  await seek((i * 1000) / FPS);
  await page.screenshot({ path: join(frames, `f${String(i).padStart(5, "0")}.jpg`), type: "jpeg", quality: 95 });
  if (i % 150 === 0) console.log(`frame ${i}/${count}`);
}
await seek(POSTER_MS);
await page.screenshot({ path: join(here, "poster.jpg"), type: "jpeg", quality: 82 });
await seek(OG_MS);
const og = join(here, "../assets/og.png");
await page.screenshot({ path: og, clip: { x: 0, y: 36, width: 1920, height: 1008 } });
await browser.close();
execFileSync("sips", ["-z", "630", "1200", og], { stdio: "ignore" });

// H.264 MP4 through AVFoundation.
execFileSync("swift", [join(here, "encode-mp4.swift"), frames, join(here, "anypic-intro.mp4"), String(FPS), "2000000"], {
  stdio: "inherit",
});

// VP8 WebM through Playwright's ffmpeg (it reads MJPEG from a pipe, nothing else).
// It is only the fallback for browsers without H.264, so it gets a lower bitrate.
// VP8 carries no colour info of its own and Chrome then assumes a BT.709 matrix
// (measured: #189a58 green 152 → 137); the container tags say what the data is:
// BT.601 matrix from the JPEG frames, sRGB transfer.
const ffDir = readdirSync(join(homedir(), "Library/Caches/ms-playwright")).filter((d) => d.startsWith("ffmpeg-")).sort().pop();
const ff = join(homedir(), "Library/Caches/ms-playwright", ffDir, "ffmpeg-mac");
const enc = spawn(ff, ["-hide_banner", "-loglevel", "error", "-f", "image2pipe", "-framerate", String(FPS), "-c:v", "mjpeg", "-i", "pipe:0",
  "-c:v", "libvpx", "-b:v", "1M", "-crf", "16", "-qmin", "0", "-qmax", "42", "-deadline", "good", "-cpu-used", "1",
  "-auto-alt-ref", "1", "-lag-in-frames", "16", "-g", "120", "-threads", "8", "-pix_fmt", "yuv420p",
  "-colorspace", "bt470bg", "-color_primaries", "bt709", "-color_trc", "iec61966-2-1", "-color_range", "tv", "-an", "-y",
  join(here, "anypic-intro.webm")], { stdio: ["pipe", "inherit", "inherit"] });
for (const f of readdirSync(frames).sort()) {
  await new Promise((ok, fail) => createReadStream(join(frames, f)).on("end", ok).on("error", fail).pipe(enc.stdin, { end: false }));
}
enc.stdin.end();
await new Promise((ok, fail) => enc.on("close", (code) => (code === 0 ? ok() : fail(new Error(`ffmpeg exit ${code}`)))));
console.log("done");
