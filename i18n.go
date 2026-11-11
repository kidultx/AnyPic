package main

// The app speaks one language at a time (#32). The page reads the choice and
// translates its own copy; the Go side needs it for native menus, dialogs,
// skipped-file reasons and the errors shown in toasts and rows. The catalogs
// in locales/ are the only place any of those words live.
//
// The language is a setting like any other: "system" follows the desktop,
// "zh-Hans" and "en" override it. It is resolved once the app is ready (the
// system languages are not readable before that) and again whenever the
// preference changes.

import (
	"errors"
	"strings"
	"sync/atomic"

	"github.com/egoist/mygo"

	// MyGo's own strings — the labels of menu roles, dialog buttons — in
	// Traditional Chinese. mygo generate imports the translations of the
	// catalogs' languages (zh-Hans, en); zh-Hant is one of ours without a
	// catalog, so it is imported here.
	_ "github.com/egoist/mygo/locales/zh-Hant"

	"anypic/locales"
)

// appLanguages is what AnyPic tells MyGo it is written in, the fallback
// first. MyGo picks the language to show among them (App.Locale) — the page
// reads that pick (mygo.locale), so do the menus MyGo labels itself, and so
// does this file: one answer, which is why they cannot disagree (#32). The
// list is shaped to give the rule the app promises:
//
//   - English first: a desktop that reads none of them gets English, not the
//     catalogs' source language.
//   - zh-Hant is listed although only a Simplified catalog ships: a
//     Traditional-Chinese desktop then resolves to Chinese — catalogFor reads
//     it from the Simplified catalog — instead of the English such desktops
//     usually list second.
var appLanguages = []string{"en", "zh-Hans", "zh-Hant"}

// appLocale is the language MyGo shows: its pick among appLanguages for the
// desktop. A variable so tests can stand in for a desktop without a running
// app.
var appLocale = func() string { return mygo.App.Locale() }

// uiLang holds the resolved language: "zh-Hans" or "en". The source
// language is the default so a service built without a running app — the
// tests — still says its messages in one language.
var uiLang atomic.Value

// languageListener is told when the resolved language changes, so main.go can
// relabel the native menu and tray. Installed once, when the app is ready.
var languageListener func()

func init() { uiLang.Store("zh-Hans") }

// uiLanguage is the language the app shows.
func uiLanguage() string { return uiLang.Load().(string) }

// tr returns the messages in that language. Go-side copy goes through it, so
// a sentence is never written twice.
func tr() locales.Messages { return locales.In(uiLanguage()) }

// catalogFor names the catalog that says a language MyGo shows: every
// Chinese variant reads the Simplified translation until a Traditional one
// ships; anything else reads English.
func catalogFor(tag string) string {
	if strings.HasPrefix(strings.ToLower(tag), "zh") {
		return "zh-Hans"
	}
	return "en"
}

// resolveLanguage turns the stored setting into the language to show: an
// explicit language wins; "system" shows the language MyGo picked for the
// desktop, which is the one the page shows too.
func resolveLanguage(setting string) string {
	switch setting {
	case "zh-Hans", "en":
		return setting
	}
	return catalogFor(appLocale())
}

// setUILanguage resolves and stores the language to show.
func setUILanguage(setting string) string {
	lang := resolveLanguage(setting)
	uiLang.Store(lang)
	return lang
}

// resolveLanguageNow picks the language once the app is ready and returns it.
// main.go calls it before building the menus, which say their labels.
func (a *AnyPic) resolveLanguageNow() string {
	a.mu.Lock()
	setting := a.settings.Language
	a.mu.Unlock()
	return setUILanguage(setting)
}

// followDesktopLanguage re-reads the language after MyGo reports that the one
// it shows changed, as it does when the desktop's languages change while the
// app follows them. Without it the native menus, the tray, the dialogs and
// the Go-side copy keep the old language until a relaunch, while the page
// follows along.
func (a *AnyPic) followDesktopLanguage() {
	a.mu.Lock()
	setting := a.settings.Language
	a.mu.Unlock()
	before := uiLanguage()
	if setUILanguage(setting) != before {
		notifyLanguageChanged()
	}
}

// setLanguageListener installs the callback main.go uses to relabel the
// native menus when the preference changes.
func (a *AnyPic) setLanguageListener(fn func()) {
	languageListener = fn
}

func notifyLanguageChanged() {
	if languageListener != nil {
		languageListener()
	}
}

// Error codes the page recognizes. They are protocol, not copy: the page
// checks for them and says the localized message itself, so a translated
// error can never break the quiet-cancel or permission flows (#17).
const (
	captureCanceledCode  = "anypic:capture-canceled"
	captureBusyCode      = "anypic:capture-busy"
	screenPermissionCode = "anypic:screen-permission"
	noClipboardImageCode = "anypic:no-clipboard-image"
)

var (
	errCaptureCanceled         = errors.New(captureCanceledCode)
	errCaptureBusy             = errors.New(captureBusyCode)
	errScreenCapturePermission = errors.New(screenPermissionCode)
	errNoClipboardImage        = errors.New(noClipboardImageCode)
)
