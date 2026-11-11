// The page speaks the language the settings choose (#32).
//
// Every word the interface shows lives in the catalogs under locales/; `mygo
// generate` writes them into ./locales.ts as typed accessors. This module
// picks the language — the stored choice, or the desktop's when it is
// "system" — and fills the markup keys (data-i18n and friends) so the static
// half of the page follows without a re-render.

import { isMyGo, locale, onLocaleChange } from "mygo-runtime";

import { messagesIn, type Messages } from "./locales";

export type LanguageSetting = "system" | "zh-Hans" | "en";

// Error codes the Go side sends for flows the page treats specially (#17,
// #32): they are protocol, never copy, so recognizing them survives any
// language.
export const errorCodes = {
  captureCanceled: "anypic:capture-canceled",
  captureBusy: "anypic:capture-busy",
  screenPermission: "anypic:screen-permission",
  noClipboardImage: "anypic:no-clipboard-image",
} as const;

let setting: LanguageSetting = "system";

// The language shown ("zh-Hans" or "en") and its messages. The source
// language is the default, so a browser preview before any snapshot still
// shows words rather than empty labels.
export let language = "zh-Hans";
export let t: Messages = messagesIn(language);

function normalize(value: string | undefined | null): LanguageSetting {
  return value === "zh-Hans" || value === "en" ? value : "system";
}

// The desktop's language: MyGo's locale inside the app, the browser's
// languages in the preview. Chinese variants read the Simplified catalog
// until a Traditional one ships.
function systemLanguage(): string {
  let tag = "";
  if (isMyGo()) {
    try {
      tag = locale();
    } catch {
      tag = "";
    }
  }
  if (!tag) {
    tag = navigator.languages?.[0] ?? navigator.language ?? "";
  }
  return /^zh/i.test(tag) ? "zh-Hans" : "en";
}

/** resolveLanguage says which catalog a stored choice picks. */
export function resolveLanguage(value: string | undefined | null): string {
  const choice = normalize(value);
  return choice === "system" ? systemLanguage() : choice;
}

function message(key: string): string {
  const accessor = (t as unknown as Record<string, unknown>)[key];
  return typeof accessor === "function" ? String((accessor as () => string).call(t)) : "";
}

/**
 * applyText fills the markup keys: text, title, aria-label, placeholder,
 * alt and content. Elements whose text is rendered from data carry no key,
 * so this never overwrites a live value.
 */
export function applyText(): void {
  for (const el of document.querySelectorAll<HTMLElement>("[data-i18n]")) {
    el.textContent = message(el.dataset.i18n ?? "");
  }
  for (const el of document.querySelectorAll<HTMLElement>("[data-i18n-title]")) {
    el.title = message(el.dataset.i18nTitle ?? "");
  }
  for (const el of document.querySelectorAll<HTMLElement>("[data-i18n-aria]")) {
    el.setAttribute("aria-label", message(el.dataset.i18nAria ?? ""));
  }
  for (const el of document.querySelectorAll<HTMLElement>("[data-i18n-placeholder]")) {
    el.setAttribute("placeholder", message(el.dataset.i18nPlaceholder ?? ""));
  }
  for (const el of document.querySelectorAll<HTMLImageElement>("[data-i18n-alt]")) {
    el.alt = message(el.dataset.i18nAlt ?? "");
  }
  for (const el of document.querySelectorAll<HTMLMetaElement>("[data-i18n-content]")) {
    el.content = message(el.dataset.i18nContent ?? "");
  }
}

/**
 * setLanguage applies a stored choice ("system", "zh-Hans" or "en"), fills
 * the static markup, and returns the language shown so callers can re-render
 * their dynamic text.
 */
export function setLanguage(value: string | undefined | null): string {
  setting = normalize(value);
  language = resolveLanguage(setting);
  t = messagesIn(language);
  document.documentElement.lang = language;
  applyText();
  return language;
}

/**
 * onSystemLanguageChange runs the handler after the desktop's language
 * changes, but only while the app follows it.
 */
export function onSystemLanguageChange(handler: (language: string) => void): void {
  if (!isMyGo()) {
    return;
  }
  onLocaleChange(() => {
    if (setting === "system") {
      handler(setLanguage(setting));
    }
  });
}
