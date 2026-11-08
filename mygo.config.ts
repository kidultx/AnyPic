import { defineConfig } from "mygo-cli";

export default defineConfig(({ command }) => ({
  name: "AnyPic",
  identifier: "com.anypic.desktop",
  version: "0.1.6",
  // `mygo dev` runs devCommand and loads devUrl; `mygo build` runs
  // buildCommand and embeds frontendDist into the app.
  devUrl: "http://localhost:5173",
  devCommand: "bun run dev:web",
  buildCommand: "bun run build:web",
  frontendDist: "dist",
  bindings: "src/mygo.ts",
  out: "build",
  // File associations feed App.OnOpenFile (#6): the system lists AnyPic for
  // these types, and opening one hands the path to the running app. The app
  // is a compressor, not an editor, so the macOS role is Viewer.
  fileAssociations: [
    { ext: ["jpg", "jpeg"], name: "JPEG image", mimeType: "image/jpeg", role: "Viewer" },
    { ext: ["png"], name: "PNG image", mimeType: "image/png", role: "Viewer" },
    { ext: ["webp"], name: "WebP image", mimeType: "image/webp", role: "Viewer" },
    { ext: ["gif"], name: "GIF image", mimeType: "image/gif", role: "Viewer" },
    { ext: ["bmp"], name: "BMP image", mimeType: "image/bmp", role: "Viewer" },
    { ext: ["tif", "tiff"], name: "TIFF image", mimeType: "image/tiff", role: "Viewer" },
  ],
  // A stable self-signed identity keeps macOS TCC grants (screen recording)
  // across rebuilds; ad-hoc signatures change their cdhash every build.
  // For distribution, replace with a Developer ID Application certificate.
  // CI signs ad hoc: its runners have no keychain, and unsigned downloads
  // require the same right-click open as an unidentified developer.
  macos: command === "build" && process.env.CI
    ? { signingIdentity: "-" }
    : { signingIdentity: "AnyPic Local Signing" },
}));
