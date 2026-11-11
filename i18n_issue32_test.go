package main

// Guards for #32: the interface is translated. Every word the user reads —
// markup, page scripts, native menus, dialogs and errors — lives in the
// catalogs in locales/, and the app speaks the language its settings choose.
//
// The catalogs are the source of truth; src/locales.ts and
// locales/catalog_gen.go are generated from them by `mygo generate`, so a new
// string is a catalog entry, never a literal in a component.

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/egoist/mygo/i18n"
)

const (
	catalogZH = "locales/zh-Hans.json"
	catalogEN = "locales/en.json"
)

// hasCJK reports whether s holds a Han character or CJK/full-width
// punctuation: text that needs a catalog rather than a literal.
func hasCJK(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) ||
			(r >= 0x3000 && r <= 0x303f) ||
			(r >= 0xff00 && r <= 0xffef) {
			return true
		}
	}
	return false
}

// readCatalog reads a catalog file, without its @metadata entries.
func readCatalog(t *testing.T, path string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v — the app is translated through the catalogs (#32)", path, err)
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		t.Fatalf("%s: %v (#32)", path, err)
	}
	out := map[string]string{}
	for key, raw := range all {
		if strings.HasPrefix(key, "@") {
			continue // @@locale and @key metadata, not messages
		}
		var message string
		if err := json.Unmarshal(raw, &message); err != nil {
			t.Fatalf("%s: %q is not a string message: %v (#32)", path, key, err)
		}
		out[key] = message
	}
	return out
}

// messageArgs returns the names of a message's arguments, in order of first
// appearance. Cases of plural/select ({one {...}}) are not arguments: they
// are followed by a brace, not by a comma or a closing brace.
var messageArg = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\s*[,}]`)

func messageArgs(message string) []string {
	var names []string
	seen := map[string]bool{}
	for _, m := range messageArg.FindAllStringSubmatch(message, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			names = append(names, m[1])
		}
	}
	sort.Strings(names)
	return names
}

// TestIssue32CatalogsHoldEveryLanguage guards the catalogs themselves: both
// languages exist, carry exactly the same keys, parse as ICU messages, take
// the same arguments, and only the source language is written in Chinese.
func TestIssue32CatalogsHoldEveryLanguage(t *testing.T) {
	config, err := os.ReadFile("mygo.config.ts")
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(config)
	if !regexp.MustCompile(`(?m)^\s*i18n:\s*\{`).MatchString(cfg) {
		t.Errorf("mygo.config.ts: no i18n block — mygo generate writes no accessors, so the app ships no translation (#32)")
	}
	if !strings.Contains(cfg, `source: "zh-Hans"`) {
		t.Errorf("mygo.config.ts: the catalog source is not zh-Hans; the app is written in Chinese first (#32)")
	}

	zh := readCatalog(t, catalogZH)
	en := readCatalog(t, catalogEN)
	if len(zh) < 40 {
		t.Errorf("%s holds %d messages; a translated interface has room for every screen (#32)", catalogZH, len(zh))
	}
	for key := range zh {
		if _, ok := en[key]; !ok {
			t.Errorf("%s: missing %q, which %s has — a key must be translated or the page falls back mid-sentence (#32)", catalogEN, key, catalogZH)
		}
	}
	for key := range en {
		if _, ok := zh[key]; !ok {
			t.Errorf("%s: has %q, which %s lacks — the source language must hold every key (#32)", catalogEN, key, catalogZH)
		}
	}

	for key, message := range zh {
		if _, err := i18n.ParseMessage(message); err != nil {
			t.Errorf("%s: %q does not parse: %v (#32)", catalogZH, key, err)
		}
	}
	for key, message := range en {
		if _, err := i18n.ParseMessage(message); err != nil {
			t.Errorf("%s: %q does not parse: %v (#32)", catalogEN, key, err)
		}
		if strings.TrimSpace(message) == "" {
			t.Errorf("%s: %q is empty (#32)", catalogEN, key)
		}
		if hasCJK(message) {
			t.Errorf("%s: %q is still Chinese: %q — the English interface cannot fall back to the source (#32)", catalogEN, key, message)
		}
	}
	for key, message := range zh {
		other, ok := en[key]
		if !ok {
			continue
		}
		want := strings.Join(messageArgs(message), ",")
		got := strings.Join(messageArgs(other), ",")
		if want != got {
			t.Errorf("%q takes {%s} in %s but {%s} in %s — a translated message must fill the same arguments (#32)", key, want, catalogZH, got, catalogEN)
		}
	}
}

// TestIssue32MarkupUsesTheCatalog guards the markup half: index.html carries
// keys, never copy. A key that no catalog entry answers leaves an empty
// element; a Chinese literal bypasses the English interface entirely.
func TestIssue32MarkupUsesTheCatalog(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	if hasCJK(html) {
		for i, line := range strings.Split(html, "\n") {
			if hasCJK(line) {
				t.Errorf("index.html:%d: Chinese literal %q — put the words in %s and a data-i18n key in the markup (#32)", i+1, strings.TrimSpace(line), catalogZH)
			}
		}
	}

	zh := readCatalog(t, catalogZH)
	en := readCatalog(t, catalogEN)
	attrs := regexp.MustCompile(`data-i18n(?:-title|-aria|-placeholder|-content)?="([^"]+)"`)
	seen := 0
	for _, m := range attrs.FindAllStringSubmatch(html, -1) {
		seen++
		key := m[1]
		if _, ok := zh[key]; !ok {
			t.Errorf("index.html: data-i18n key %q is not in %s — it renders empty (#32)", key, catalogZH)
		}
		if _, ok := en[key]; !ok {
			t.Errorf("index.html: data-i18n key %q is not in %s — it renders empty in English (#32)", key, catalogEN)
		}
	}
	if seen < 50 {
		t.Errorf("index.html: only %d data-i18n keys; the whole interface is translated, not a few labels (#32)", seen)
	}

	if !strings.Contains(html, `id="languageSeg"`) {
		t.Errorf("index.html: no #languageSeg — the language cannot be overridden in the preferences (#32)")
	}
	for _, want := range []string{`data-language="system"`, `data-language="zh-Hans"`, `data-language="en"`} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html: #languageSeg has no %s button — follow the system or pick either shipped language (#32)", want)
		}
	}
}

// tsString matches string and template literals, which may hold copy. The
// sources are comment-stripped first, so prose in comments is not a literal.
var tsString = regexp.MustCompile("(?s)\"(?:\\\\.|[^\"\\\\])*\"|'(?:\\\\.|[^'\\\\])*'|`(?:\\\\.|[^`\\\\])*`")

// TestIssue32NoHardcodedCopy guards the script and Go halves: words the user
// can see come from the catalog, so no source file outside the generated
// accessors may carry Chinese in a string literal.
func TestIssue32NoHardcodedCopy(t *testing.T) {
	files, err := filepath.Glob("src/*.ts")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		slash := filepath.ToSlash(path)
		if slash == "src/locales.ts" || slash == "src/mygo.ts" {
			continue // generated
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		// The generated module is excluded; the page scripts go through
		// src/i18n.ts.
		for _, m := range tsString.FindAllString(stripTSComments(string(raw)), -1) {
			if hasCJK(m) {
				t.Errorf("%s: Chinese string literal %s — say it with t.<key>() from the catalog (#32)", slash, m)
			}
		}
	}

	files, err = filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") || path == "catalog_gen.go" {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(lit.Value)
			if err == nil && hasCJK(s) {
				t.Errorf("%s:%d: Chinese string literal %q — say it with tr().<key>() from the catalog (#32)", path, fset.Position(lit.Pos()).Line, s)
			}
			return true
		})
	}
}

// TestIssue32LanguageIsASetting guards the wiring: the language is a stored
// setting (follow the system by default), the preferences bind it, and the
// native menu labels are read from the catalog rather than fixed at startup.
func TestIssue32LanguageIsASetting(t *testing.T) {
	compressor, err := os.ReadFile("compressor.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(compressor)
	if !regexp.MustCompile(`Language\s+string\s+` + "`json:\"language\"`").MatchString(src) {
		t.Errorf("compressor.go: Settings has no language field — the choice would not survive a launch (#32)")
	}
	if !strings.Contains(src, `Language:            "system"`) && !regexp.MustCompile(`Language:\s+"system"`).MatchString(src) {
		t.Errorf("compressor.go: the language does not default to following the system (#32)")
	}

	main, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"GoMenuFile", "GoMenuOpenImages", "GoMenuCaptureEllipsis", "GoMenuPreferences", "GoMenuToggleTheme", "GoTrayTooltip"} {
		if !strings.Contains(string(main), key) {
			t.Errorf("main.go: the native menu does not say %s from the catalog — it is fixed in one language (#32)", key)
		}
	}

	mainTS := ruleSources(t)["src/main.ts"]
	if !strings.Contains(mainTS, `#languageSeg`) {
		t.Errorf("src/main.ts: nothing binds #languageSeg, so the language control does nothing (#32)")
	}
	if !strings.Contains(mainTS, "setLanguage(") {
		t.Errorf("src/main.ts: the page never calls setLanguage(), so a saved language is not applied (#32)")
	}
}

// The tests run without an app: MyGo's language store must never be reached
// from one — it keeps the choice for the next launch, on macOS in the app's
// defaults — and the desktop is whatever a test says it is.
func init() {
	setAppLocale = func(string) {}
	appLocale = func() string { return "zh-Hans" }
}

// issue32Desktop makes MyGo "show" what it would pick for a desktop whose
// languages are tags: its own matching among appLanguages, falling back to
// the first, exactly as App.Locale does.
func issue32Desktop(t *testing.T, tags ...string) {
	t.Helper()
	original := appLocale
	t.Cleanup(func() { appLocale = original })
	appLocale = func() string {
		if match := i18n.Match(tags, appLanguages); match != "" {
			return match
		}
		return appLanguages[0]
	}
}

// TestIssue32LanguagesResolveFromTheDesktop: "system" shows the first of the
// desktop's languages the app speaks. Every Chinese variant reads the
// Simplified translation until a Traditional one ships — also when the
// desktop lists English second, as Traditional-Chinese desktops usually do —
// and a desktop that reads none of them gets English, never the catalogs'
// source language. The page reads the same answer from MyGo (mygo.locale),
// so each row is also what the page shows.
func TestIssue32LanguagesResolveFromTheDesktop(t *testing.T) {
	cases := []struct {
		tags []string
		want string
	}{
		{[]string{"zh-Hans-CN", "en-US"}, "zh-Hans"},
		{[]string{"zh-CN"}, "zh-Hans"},
		{[]string{"zh-SG"}, "zh-Hans"},
		{[]string{"zh-Hant-TW"}, "zh-Hans"}, // read as Simplified until one ships
		{[]string{"zh-Hant-TW", "en-TW"}, "zh-Hans"},
		{[]string{"zh-HK", "en-HK"}, "zh-Hans"},
		{[]string{"zh-TW"}, "zh-Hans"},
		{[]string{"en-GB", "zh-Hans"}, "en"},
		{[]string{"en-US", "zh-Hant-TW"}, "en"},
		{[]string{"fr-FR"}, "en"}, // no catalog: English, not an empty page
		{[]string{"ja-JP"}, "en"},
		{[]string{"de-DE", "en-DE"}, "en"},
		{[]string{"fr-FR", "zh-Hans-CN"}, "zh-Hans"},
		{nil, "en"},
	}
	for _, c := range cases {
		issue32Desktop(t, c.tags...)
		if got := resolveLanguage("system"); got != c.want {
			t.Errorf("resolveLanguage(system) with desktop languages %v is %q, want %q (#32)", c.tags, got, c.want)
		}
	}
	for _, choice := range []string{"zh-Hans", "en"} {
		issue32Desktop(t, "fr-FR")
		if got := resolveLanguage(choice); got != choice {
			t.Errorf("resolveLanguage(%q) is %q; an explicit choice must beat the desktop (#32)", choice, got)
		}
	}
}

// TestIssue32OneAnswerForPageMenusAndGo: the page asks MyGo which language
// the app shows (src/i18n.ts reads mygo.locale); the Go side used to walk the
// desktop's languages by a rule of its own, and the two disagreed — on a
// Traditional-Chinese desktop the page was English while the menus, skip
// reasons and errors were Chinese; on a desktop with neither language the
// page was Chinese and the rest English. Now MyGo is told the languages the
// app speaks, shaped so that its pick is the documented rule, and both sides
// read that pick.
func TestIssue32OneAnswerForPageMenusAndGo(t *testing.T) {
	if len(appLanguages) == 0 || appLanguages[0] != "en" {
		t.Errorf("appLanguages = %v; English must come first — it is what a desktop that reads none of them gets (#32)", appLanguages)
	}
	for _, tag := range []string{"zh-Hans", "zh-Hant"} {
		found := false
		for _, have := range appLanguages {
			found = found || have == tag
		}
		if !found {
			t.Errorf("appLanguages = %v, missing %s — desktops in that script would not resolve to Chinese (#32)", appLanguages, tag)
		}
	}

	main, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(main), "app.SetLanguages(appLanguages...)") {
		t.Error("main.go: MyGo is never told appLanguages — it picks among the catalogs' languages with the source language as fallback, so a desktop with no shipped language shows Chinese (#32)")
	}

	page, err := os.ReadFile("src/i18n.ts")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "locale()") {
		t.Error("src/i18n.ts: the page no longer reads the language MyGo shows (mygo.locale) — it and the Go side would resolve the desktop separately again (#32)")
	}
	source, err := os.ReadFile("i18n.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(source), "PreferredLanguages()") {
		t.Error("i18n.go: the Go side walks the desktop's languages itself again — it must map the language MyGo shows, as the page does (#32)")
	}
}

// TestIssue32GoFollowsADesktopLanguageChange: while the app follows the
// system, a language change on the desktop reached the page (MyGo tells it)
// but not the Go side — menus, tray, dialogs and errors kept the old language
// until a relaunch. MyGo's OnLocaleChanged now re-reads it, and the menus are
// relabelled once per real change. An explicit choice does not follow.
func TestIssue32GoFollowsADesktopLanguageChange(t *testing.T) {
	original := uiLanguage()
	defer setUILanguage(original)

	service := &AnyPic{settings: defaultSettings()}
	var relabelled int
	service.setLanguageListener(func() { relabelled++ })
	defer service.setLanguageListener(nil)

	issue32Desktop(t, "zh-Hans-CN")
	if got := service.resolveLanguageNow(); got != "zh-Hans" {
		t.Fatalf("on a Chinese desktop the app starts in %q, want zh-Hans", got)
	}

	issue32Desktop(t, "en-US")
	service.followDesktopLanguage()
	if uiLanguage() != "en" {
		t.Errorf("after the desktop switched to English the Go side still says %q (#32)", uiLanguage())
	}
	if relabelled != 1 {
		t.Errorf("menus were relabelled %d times for one desktop change, want 1 (#32)", relabelled)
	}
	service.followDesktopLanguage()
	if relabelled != 1 {
		t.Errorf("menus were relabelled %d times with no further change, want 1 (#32)", relabelled)
	}

	service.settings.Language = "zh-Hans"
	setUILanguage("zh-Hans")
	relabelled = 0
	issue32Desktop(t, "en-US")
	service.followDesktopLanguage()
	if uiLanguage() != "zh-Hans" || relabelled != 0 {
		t.Errorf("an explicit 中文 followed the desktop to %q (relabelled %d times); a choice does not follow (#32)", uiLanguage(), relabelled)
	}

	main, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`app\.OnLocaleChanged\(func\([^)]*\)\s*\{\s*service\.followDesktopLanguage\(\)\s*\}\)`).Match(main) {
		t.Error("main.go: nothing calls service.followDesktopLanguage from app.OnLocaleChanged — the Go side never hears a desktop language change (#32)")
	}
}

// TestIssue32GoSaysItsCopyInTheChosenLanguage guards the Go half at runtime:
// the same error and the same generated name change language with the choice,
// while the codes the page matches on never do.
func TestIssue32GoSaysItsCopyInTheChosenLanguage(t *testing.T) {
	original := uiLanguage()
	defer setUILanguage(original)

	setUILanguage("en")
	if msg := tr().GoErrNoClipboardImage(); msg != "The clipboard has no image" {
		t.Errorf("en GoErrNoClipboardImage = %q, want the English catalog entry (#32)", msg)
	}
	if msg := tr().GoScreenshotName("2026-11-08 10.00.00"); !strings.HasPrefix(msg, "Screenshot ") {
		t.Errorf("en GoScreenshotName = %q, want an English generated name (#32)", msg)
	}

	setUILanguage("zh-Hans")
	if !hasCJK(tr().GoErrNoClipboardImage()) {
		t.Errorf("zh-Hans GoErrNoClipboardImage = %q, want Chinese (#32)", tr().GoErrNoClipboardImage())
	}

	// The page recognizes these across languages; their values are protocol.
	for _, code := range []struct {
		err  error
		want string
	}{
		{errCaptureCanceled, "anypic:capture-canceled"},
		{errCaptureBusy, "anypic:capture-busy"},
		{errScreenCapturePermission, "anypic:screen-permission"},
		{errNoClipboardImage, "anypic:no-clipboard-image"},
	} {
		if got := code.err.Error(); got != code.want {
			t.Errorf("error code = %q, want %q — the page matches on it, so it must not translate (#32)", got, code.want)
		}
	}
}

// TestIssue32SettingsChooseTheLanguage guards the stored side: following the
// system by default, an explicit language survives normalization, and an
// unknown value falls back rather than leaving the page wordless.
func TestIssue32SettingsChooseTheLanguage(t *testing.T) {
	if got := defaultSettings().Language; got != "system" {
		t.Errorf("default settings language = %q, want system (#32)", got)
	}
	if got := normalizeSettings(Settings{Language: "en"}).Language; got != "en" {
		t.Errorf("normalizeSettings kept %q instead of en (#32)", got)
	}
	if got := normalizeSettings(Settings{Language: "klingon"}).Language; got != "system" {
		t.Errorf("normalizeSettings turned an unknown language into %q, want system (#32)", got)
	}
}

// TestIssue32SavingTheLanguageSwitchesAtOnce guards the hand-off from the
// preferences to the native side: the new language is in force before
// SaveSettings returns, and the menu listener fires once — not on every
// settings save.
func TestIssue32SavingTheLanguageSwitchesAtOnce(t *testing.T) {
	original := uiLanguage()
	defer setUILanguage(original)

	service := &AnyPic{settings: defaultSettings()}
	var calls int
	service.setLanguageListener(func() { calls++ })
	defer service.setLanguageListener(nil)
	setUILanguage("zh-Hans")
	next := service.settings
	next.Language = "en"
	saved, err := service.SaveSettings(next)
	if err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	if saved.Language != "en" || uiLanguage() != "en" {
		t.Errorf("after saving en the language is %q (saved %q), want en at once (#32)", uiLanguage(), saved.Language)
	}
	if calls != 1 {
		t.Errorf("language listener ran %d times for one change, want 1 — the menus relabel once (#32)", calls)
	}
	if _, err := service.SaveSettings(saved); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("language listener ran %d times after an unchanged save, want 1 (#32)", calls)
	}
}

// TestIssue32QueueRowsFollowALanguageChange: a queue row is built once and
// reused for as long as its image stays in the list, so anything createRow
// writes from the catalog keeps the language of the moment the image was
// added. The tooltips and screen-reader labels of a row's controls did
// exactly that — 「对比预览」「导出这张」「移除」 stayed on every queued image
// after switching to English. They have to be applied by updateRow, which
// runs again on every render, whenever the row's language is not the page's.
func TestIssue32QueueRowsFollowALanguageChange(t *testing.T) {
	raw, err := os.ReadFile("src/main.ts")
	if err != nil {
		t.Fatal(err)
	}
	ts := string(raw)
	labels := []string{"t.selectThisImage(", "t.comparePreview(", "t.beautify(", "t.exportThis(", "t.remove("}

	create, _, _, ok := functionBody(ts, "createRow")
	if !ok {
		t.Fatal("src/main.ts: no createRow function")
	}
	for _, label := range labels {
		if strings.Contains(create, label) {
			t.Errorf("src/main.ts: createRow writes %s) into the row once — a row outlives a language change, so the label stays in the old language (#32)", label)
		}
	}

	relabel, _, _, ok := functionBody(ts, "labelRow")
	if !ok {
		t.Fatal("src/main.ts: no labelRow function — the labels of a row's controls have nowhere to be re-applied from (#32)")
	}
	for _, label := range labels {
		if !strings.Contains(relabel, label) {
			t.Errorf("src/main.ts: labelRow does not apply %s) (#32)", label)
		}
	}

	update, _, _, ok := functionBody(ts, "updateRow")
	if !ok {
		t.Fatal("src/main.ts: no updateRow function")
	}
	if !strings.Contains(update, "labelRow(") {
		t.Error("src/main.ts: updateRow never calls labelRow — rows already in the queue keep the labels of the language they were created in (#32)")
	}
	if !regexp.MustCompile(`row\.dataset\.lang\s*!==\s*language`).MatchString(update) {
		t.Error("src/main.ts: updateRow does not compare the row's language with the page's — relabel when they differ, not on every update (#32)")
	}
}

// TestIssue32TheChoiceReachesMyGo: MyGo labels the menu items it names itself
// (About, Hide, Quit, the Edit and Window menus) and tells AppKit, WebKit and
// GTK which language to say their own words in. A choice of 中文 or English
// has to reach it, or a Chinese desktop that chose English keeps a menu bar
// of 编辑 / 窗口 / 帮助 around an English window; and going back to following
// the system has to give the app back to the desktop's language. Following
// the system leaves MyGo alone — what it shows already is that language, or
// the one the user gave AnyPic in the system's own per-app language setting.
func TestIssue32TheChoiceReachesMyGo(t *testing.T) {
	original := setAppLocale
	var told []string
	setAppLocale = func(tag string) { told = append(told, tag) }
	defer func() { setAppLocale = original }()
	originalLang := uiLanguage()
	defer setUILanguage(originalLang)
	issue32Desktop(t, "zh-Hans-CN")

	service := &AnyPic{settings: defaultSettings()}
	service.resolveLanguageNow()
	if len(told) != 0 {
		t.Errorf("following the system told MyGo %v; the desktop's language is what it already shows (#32)", told)
	}

	next := service.settings
	next.Language = "en"
	if _, err := service.SaveSettings(next); err != nil {
		t.Fatal(err)
	}
	if len(told) != 1 || told[0] != "en" {
		t.Errorf("choosing English told MyGo %v, want [en] — its own menu items would stay in the desktop's language (#32)", told)
	}

	next.Language = "system"
	if _, err := service.SaveSettings(next); err != nil {
		t.Fatal(err)
	}
	if len(told) != 2 || told[1] != "" {
		t.Errorf("going back to following the system told MyGo %v, want a final \"\" (#32)", told)
	}

	told = nil
	explicit := &AnyPic{settings: defaultSettings()}
	explicit.settings.Language = "en"
	explicit.resolveLanguageNow()
	if len(told) != 1 || told[0] != "en" {
		t.Errorf("a launch pinned to English told MyGo %v, want [en] — the choice is handed over again in case its own record is gone (#32)", told)
	}
}

// TestIssue32ADialogsButtonsKeepTheirOwnWords: applyText() fills every
// element carrying a markup key, and a dialog's buttons must not carry one —
// askConfirm writes labels of its own, and the update dialog says 稍后 /
// Later where a plain Cancel would mean something else. With the key still
// on #confirmCancel, any language change while that dialog was open (a
// system change, or settings arriving from the backend) rewrote it to
// 取消 / Cancel. The page's own rule is the same one: elements whose text is
// rendered from data carry no key.
func TestIssue32ADialogsButtonsKeepTheirOwnWords(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	for _, id := range []string{"confirmCancel", "confirmOk"} {
		tag := regexp.MustCompile(`<button[^>]*id="` + id + `"[^>]*>`).FindString(html)
		if tag == "" {
			t.Fatalf("index.html: no button #%s to check (#32)", id)
		}
		if strings.Contains(tag, "data-i18n") {
			t.Errorf("index.html: #%s carries a markup key — applyText() would overwrite the label askConfirm wrote (#32): %s", id, tag)
		}
	}
}

// TestIssue32TheFooterSaysItsSummaryInTheCurrentLanguage: the automatic pass
// remembers where it saved, so the footer can say it again later. Remembering
// the translated words instead ("…到 原图旁边") means the sentence keeps the
// language of the moment it saved in, and English cannot say "Saved 3 images
// to Next to the originals" at all — the folder is data, the sentence is
// built when it is drawn, and "beside the originals" has a sentence of its
// own.
func TestIssue32TheFooterSaysItsSummaryInTheCurrentLanguage(t *testing.T) {
	raw, err := os.ReadFile("src/main.ts")
	if err != nil {
		t.Fatal(err)
	}
	ts := string(raw)

	remember := regexp.MustCompile(`(?s)lastSaved = \{.*?\n\s*\};`).FindString(ts)
	if remember == "" {
		t.Fatal("src/main.ts: no lastSaved assignment to check (#32)")
	}
	if regexp.MustCompile(`\bt\.[A-Za-z]+\(`).MatchString(remember) {
		t.Errorf("src/main.ts: the remembered save holds translated words — the footer would repeat them in the language of that moment (#32):\n%s", remember)
	}

	footer, _, _, ok := functionBody(ts, "renderFooter")
	if !ok {
		t.Fatal("src/main.ts: no renderFooter function")
	}
	for _, call := range []string{"t.savedTo(", "t.savedAlongside("} {
		if !strings.Contains(footer, call) {
			t.Errorf("src/main.ts: renderFooter does not build the summary with %s) (#32)", call)
		}
	}

	for _, path := range []string{"locales/zh-Hans.json", "locales/en.json"} {
		catalog := readCatalog(t, path)
		if _, ok := catalog["savedAlongside"]; !ok {
			t.Errorf("%s: no savedAlongside — the footer has no sentence for results saved beside the originals (#32)", path)
		}
	}
}

// TestIssue32ACanceledImageSaysSoInWords: canceling a pass stops the images
// that were being encoded with Go's own sentinel, whose text is "context
// canceled" whatever the language — and that is what the queue row and the
// toast showed. The catalog says it instead.
func TestIssue32ACanceledImageSaysSoInWords(t *testing.T) {
	original := uiLanguage()
	defer setUILanguage(original)

	service := newTestService(t)
	src := filepath.Join(t.TempDir(), "photo.png")
	writePNG(t, src, gradientImage(64, 64, false))
	added, err := service.AddPaths([]string{src})
	if err != nil {
		t.Fatalf("AddPaths: %v", err)
	}
	if len(added.Snapshot.Items) != 1 {
		t.Fatalf("added %d items, want 1", len(added.Snapshot.Items))
	}
	id := added.Snapshot.Items[0].ID

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, c := range []struct{ language, want string }{{"en", "Canceled"}, {"zh-Hans", "已取消"}} {
		setUILanguage(c.language)
		service.compressItem(ctx, id, service.settings)
		service.mu.Lock()
		got := service.byID[id].Error
		service.mu.Unlock()
		if got != c.want {
			t.Errorf("in %s an image canceled mid-pass says %q, want %q (#32)", c.language, got, c.want)
		}
	}
}
