package main

// Guards for #32: the interface is translated. Every word the user reads —
// markup, page scripts, native menus, dialogs and errors — lives in the
// catalogs in locales/, and the app speaks the language its settings choose.
//
// The catalogs are the source of truth; src/locales.ts and
// locales/catalog_gen.go are generated from them by `mygo generate`, so a new
// string is a catalog entry, never a literal in a component.

import (
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

// TestIssue32LanguagesResolveFromTheDesktop guards the choice itself: an
// explicit language wins, "system" follows the desktop's first Chinese tag or
// falls back to English.
func TestIssue32LanguagesResolveFromTheDesktop(t *testing.T) {
	original := preferredLanguages
	defer func() { preferredLanguages = original }()

	cases := []struct {
		tags []string
		want string
	}{
		{[]string{"zh-Hans-CN", "en-US"}, "zh-Hans"},
		{[]string{"zh-Hant-TW"}, "zh-Hans"}, // read as Simplified until one ships
		{[]string{"en-GB", "zh-Hans"}, "en"},
		{[]string{"fr-FR"}, "en"}, // no catalog: English, not an empty page
		{nil, "en"},
	}
	for _, c := range cases {
		tags := c.tags
		preferredLanguages = func() []string { return tags }
		if got := resolveLanguage("system"); got != c.want {
			t.Errorf("resolveLanguage(system) with desktop languages %v is %q, want %q (#32)", tags, got, c.want)
		}
	}
	for _, choice := range []string{"zh-Hans", "en"} {
		preferredLanguages = func() []string { return []string{"fr-FR"} }
		if got := resolveLanguage(choice); got != choice {
			t.Errorf("resolveLanguage(%q) is %q; an explicit choice must beat the desktop (#32)", choice, got)
		}
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
