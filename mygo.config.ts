import { defineConfig } from "mygo-cli";

export default defineConfig(({ command }) => ({
  name: "AnyPic",
  identifier: "com.anypic.desktop",
  version: "0.1.3",
  // `mygo dev` runs devCommand and loads devUrl; `mygo build` runs
  // buildCommand and embeds frontendDist into the app.
  devUrl: "http://localhost:5173",
  devCommand: "bun run dev:web",
  buildCommand: "bun run build:web",
  frontendDist: "dist",
  bindings: "src/mygo.ts",
  out: "build",
  // A stable self-signed identity keeps macOS TCC grants (screen recording)
  // across rebuilds; ad-hoc signatures change their cdhash every build.
  // For distribution, replace with a Developer ID Application certificate.
  // CI signs ad hoc: its runners have no keychain, and unsigned downloads
  // require the same right-click open as an unidentified developer.
  macos: command === "build" && process.env.CI
    ? { signingIdentity: "-" }
    : { signingIdentity: "AnyPic Local Signing" },
}));
