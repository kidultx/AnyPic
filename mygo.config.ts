import { defineConfig } from "mygo-cli";

export default defineConfig(({ command }) => ({
  name: "AnyPic",
  identifier: "com.anypic.desktop",
  version: "0.1.8",
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
  // A stable signing identity keeps macOS TCC grants (screen recording)
  // across rebuilds and upgrades; ad-hoc signatures pin the cdhash instead,
  // which changes every build.
  // Release builds import the fixed self-signed "AnyPic Release Signing"
  // certificate into a temporary keychain and pass its name in
  // MACOS_SIGNING_IDENTITY. Without it (local CI smoke builds) they sign ad
  // hoc. A Developer ID certificate would work here too for notarization.
  macos: command === "build" && process.env.CI
    ? { signingIdentity: process.env.MACOS_SIGNING_IDENTITY || "-" }
    : { signingIdentity: "AnyPic Local Signing" },
}));
