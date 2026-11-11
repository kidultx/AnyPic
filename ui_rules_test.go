// UI guard tests ported from magpie's GUI discipline. The rules and their
// borrowed evidence live in docs/standards.md「界面」 and
// docs/subsystems/ui.md; each test fails naming the file and line that
// breaks its rule.
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// tokenLines reports which 1-based line numbers sit inside a colour token
// block: one opened by a line starting with :root or [data-theme, closed by
// a line that is exactly "}".
func tokenLines(css string) map[int]bool {
	token := 0
	open := regexp.MustCompile(`^(:root|\[data-theme)`)
	out := map[int]bool{}
	for i, line := range strings.Split(css, "\n") {
		s := strings.TrimSpace(line)
		if open.MatchString(s) {
			token++
		}
		if s == "}" && token > 0 {
			token--
		}
		if token > 0 {
			out[i+1] = true
		}
	}
	return out
}

// TestStyleColorsLiveInTokens guards "colours are tokens": a hex literal
// outside the token blocks escapes the design system — make it a token with
// a why, not a literal in a rule (docs/standards.md「界面」). Comments are
// stripped first, so a colour named in prose is fine.
func TestStyleColorsLiveInTokens(t *testing.T) {
	raw, err := os.ReadFile("src/style.css")
	if err != nil {
		t.Fatal(err)
	}
	comment := regexp.MustCompile(`(?s)/\*.*?\*/`)
	css := comment.ReplaceAllString(string(raw), "")
	hex := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`)
	tokens := tokenLines(css)
	bad := 0
	for i, line := range strings.Split(css, "\n") {
		if tokens[i+1] {
			continue
		}
		for _, m := range hex.FindAllString(line, -1) {
			bad++
			if bad <= 10 {
				t.Errorf("src/style.css:%d: hex colour %s outside the token blocks — add a token with a why instead", i+1, m)
			}
		}
	}
	if bad > 10 {
		t.Errorf("src/style.css: %d hex colours outside the token blocks in total", bad)
	}
}

// The one native <select> allowed: maxDim, wrapped in .select-wrap. A new
// native select needs its id on this list with the owner's say-so, or its
// own control built like the app's other dropdowns.
var nativeSelectsAllowed = []string{"maxDim"}

// TestNoNewNativeSelects guards it.
func TestNoNewNativeSelects(t *testing.T) {
	for _, file := range []string{"index.html", "src/main.ts"} {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(raw), "\n") {
			sel := strings.Count(line, "<select")
			if sel == 0 {
				continue
			}
			allowed := 0
			for _, id := range nativeSelectsAllowed {
				allowed += strings.Count(line, `id="`+id+`"`)
			}
			if sel > allowed {
				t.Errorf("%s:%d: a native <select> that is not on the allowed list (%v) — build the app's own control, or get the exception named here", file, i+1, nativeSelectsAllowed)
			}
		}
	}
}

// Size budgets, as the ledger in magpie's core_budget_test.go: a change
// grows the core on purpose or not at all. Sizes as of 2026-11-08:
// main.ts 75,251 · style.css 57,246 · index.html 30,370. The 2026-11-08
// queue/entry features grew all three: paste-to-queue (#4), batch selection
// with name filter (#5), retry-failed (#11) and undo-remove (#12) in
// main.ts; the selection checkbox column, filter field and selection bar in
// style.css and index.html. #15 grew main.ts to 77,153: the JPG transparency
// sidebar note, the "→ PNG · 保留透明" queue row and the demo's JPG→PNG
// result. #14 grew main.ts to 78,627: the quit-before-install confirmation,
// its unsaved-results warning, and the "later" toast that names the
// installer. Each budget is current + ~2% headroom. Over budget: move the
// code to its own module, or raise the budget here and say what the growth
// was for. The 2026-11 walk-through's first fixes crossed main.ts's line;
// its shortcut notation (labels, accelerators, reserved keys) moved to
// src/shortcuts.ts instead, budgeted here so the split is a move with a
// ledger entry, not a second hiding place.
var uiBudgets = []struct {
	file   string
	budget int
}{
	{"src/main.ts", 80_200},
	{"src/shortcuts.ts", 4_200},
	{"src/style.css", 58_400},
	{"index.html", 31_000},
}

// TestScriptAndStyleBudgets guards them. Sizes are counted with LF line
// endings: a Windows checkout expands \n to \r\n, which is checkout noise,
// not core growth, and must not fail the ledger (release v0.1.4's first run
// tripped on exactly that).
func TestScriptAndStyleBudgets(t *testing.T) {
	for _, b := range uiBudgets {
		raw, err := os.ReadFile(b.file)
		if err != nil {
			t.Fatal(err)
		}
		size := len(bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n")))
		if size > b.budget {
			t.Errorf("%s is %d bytes (LF), over its budget of %d: move the code to its own module, or raise the budget and say why in this test's comment", b.file, size, b.budget)
		}
	}
}

// blockComment matches CSS/TS block comments. stripBlockComments replaces
// them with spaces while keeping newlines, so byte offsets and line numbers
// still point at the original source.
var blockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)

func stripBlockComments(src string) string {
	return blockComment.ReplaceAllStringFunc(src, func(m string) string {
		newlines := strings.Count(m, "\n")
		return strings.Repeat("\n", newlines) + strings.Repeat(" ", len(m)-newlines)
	})
}

// TestIssue16ExitStatesIgnorePointerEvents guards the #16 rule: an element
// that is fading out must stop accepting clicks. Every exit/close state
// class — .leaving on toasts, .hidden on the compare hint, and any future
// sibling — sets pointer-events: none itself (or inherits it from the base
// rule for the same element); display: none is safe on its own. The toast
// regression was a .leaving rule that only lowered opacity while the base
// .toast still said pointer-events: auto.
func TestIssue16ExitStatesIgnorePointerEvents(t *testing.T) {
	raw, err := os.ReadFile("src/style.css")
	if err != nil {
		t.Fatal(err)
	}
	css := stripBlockComments(string(raw))

	type rule struct {
		selector string
		body     string
		line     int
	}
	var rules []rule
	// Innermost rule blocks; at-rule preludes cannot match because their body
	// contains another brace.
	ruleRE := regexp.MustCompile(`(?s)([^{}]+)\{([^{}]*)\}`)
	for _, m := range ruleRE.FindAllStringSubmatchIndex(css, -1) {
		selector := strings.TrimSpace(css[m[2]:m[3]])
		lead := len(css[m[2]:m[3]]) - len(strings.TrimLeft(css[m[2]:m[3]], " \t\r\n"))
		rules = append(rules, rule{
			selector: selector,
			body:     css[m[4]:m[5]],
			line:     strings.Count(css[:m[2]+lead], "\n") + 1,
		})
	}

	stateRE := regexp.MustCompile(`\.(leaving|closing|exiting|dismissing|fade-out|fading-out|is-hidden|hidden)\b`)
	noPointerRE := regexp.MustCompile(`(?i)pointer-events\s*:\s*none`)
	displayNoneRE := regexp.MustCompile(`(?i)display\s*:\s*none`)

	// Selectors whose own rule already ignores pointer events, keyed by the
	// full selector: .compare-hint is the base for .compare-hint.hidden.
	safeBase := map[string]bool{}
	for _, r := range rules {
		if noPointerRE.MatchString(r.body) {
			for _, part := range strings.Split(r.selector, ",") {
				safeBase[strings.TrimSpace(part)] = true
			}
		}
	}

	leavingRules := 0
	for _, r := range rules {
		for _, part := range strings.Split(r.selector, ",") {
			part = strings.TrimSpace(part)
			state := stateRE.FindString(part)
			if state == "" {
				continue
			}
			if strings.Contains(part, ".leaving") {
				leavingRules++
			}
			if noPointerRE.MatchString(r.body) || displayNoneRE.MatchString(r.body) {
				continue
			}
			// A state-only rule may rely on the base class rule for the same
			// element: .compare-hint.hidden inherits from .compare-hint.
			if base := strings.TrimSuffix(part, state); base != "" && safeBase[base] {
				continue
			}
			t.Errorf("src/style.css:%d: exit state %q still takes pointer events — add pointer-events: none (issue #16)", r.line, part)
		}
	}
	if leavingRules == 0 {
		t.Errorf("src/style.css: no .leaving rule found — the exit state that removal waits on must exist and be guarded (issue #16)")
	}
}

// exitRemoveHelper is the one function allowed to wait out an exit and
// remove the element. Issue #16: the toast fade uses a transition, so an
// animationend listener never fired and the invisible toast stayed on top of
// the page (and its undo button stayed clickable).
const exitRemoveHelper = "removeAfterExit"

// startsRegex reports whether a "/" after the previous significant byte
// begins a regular-expression literal rather than a division.
func startsRegex(prev byte) bool {
	switch prev {
	case 0, '(', '[', '{', ',', ';', '=', ':', '!', '&', '|', '?', '+', '-', '*', '%', '<', '>', '~', '^':
		return true
	}
	return false
}

// stripTSComments blanks // and /* */ comments while preserving every byte
// offset and newline. String and regex literals are copied verbatim, so a
// URL's "//" and a character class such as /[&<>"']/ are not mistaken for
// comments.
func stripTSComments(src string) string {
	out := []byte(src)
	var prev byte // last significant byte outside comments and literals
	for i := 0; i < len(src); i++ {
		switch {
		case src[i] == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				out[i] = ' '
				i++
			}
			prev = 0
		case src[i] == '/' && i+1 < len(src) && src[i+1] == '*':
			out[i], out[i+1] = ' ', ' '
			i += 2
			for i < len(src) {
				if src[i] == '*' && i+1 < len(src) && src[i+1] == '/' {
					out[i], out[i+1] = ' ', ' '
					i++
					break
				}
				if src[i] != '\n' {
					out[i] = ' '
				}
				i++
			}
		case src[i] == '"' || src[i] == '\'' || src[i] == '`':
			quote := src[i]
			i++
			for i < len(src) {
				if src[i] == '\\' {
					i += 2
					continue
				}
				if src[i] == quote {
					break
				}
				i++
			}
			prev = quote
		case src[i] == '/' && startsRegex(prev):
			i++
			inClass := false
			for i < len(src) {
				if src[i] == '\\' {
					i += 2
					continue
				}
				if src[i] == '[' {
					inClass = true
				} else if src[i] == ']' {
					inClass = false
				} else if src[i] == '/' && !inClass {
					break
				}
				i++
			}
			prev = '/'
		case src[i] != ' ' && src[i] != '\t' && src[i] != '\r' && src[i] != '\n':
			prev = src[i]
		}
	}
	return string(out)
}

// braceBodyAt returns the brace-delimited body starting at open (the index
// of "{") and the offset just past its closing brace.
func braceBodyAt(src string, open int) (body string, end int, ok bool) {
	depth := 0
	for i := open; i < len(src); i++ {
		switch src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[open : i+1], i + 1, true
			}
		}
	}
	return "", 0, false
}

// functionBody returns the brace-delimited body of a function declaration
// (top-level or nested) plus its [start, end) offsets in src.
func functionBody(src, name string) (body string, start, end int, ok bool) {
	decl := regexp.MustCompile(`(?m)^\s*(?:export\s+)?(?:async\s+)?function\s+` + regexp.QuoteMeta(name) + `\s*\(`)
	loc := decl.FindStringIndex(src)
	if loc == nil {
		return "", 0, 0, false
	}
	// Walk to the closing parenthesis of the parameter list first: parameter
	// types may contain braces (showToast takes `action?: { ... }`), which
	// must not be mistaken for the body.
	i, depth := loc[1], 1
	for ; i < len(src) && depth > 0; i++ {
		switch src[i] {
		case '(':
			depth++
		case ')':
			depth--
		}
	}
	if depth != 0 {
		return "", 0, 0, false
	}
	open := strings.Index(src[i:], "{")
	if open < 0 {
		return "", 0, 0, false
	}
	open += i
	body, end, ok = braceBodyAt(src, open)
	return body, open, end, ok
}

// ruleSourcePath returns the lookup form of a source path: forward slashes
// on every OS. filepath.ToSlash only converts the host OS separator, so it
// would leave a Windows-style path untouched when the tests run on macOS or
// Linux; the explicit ReplaceAll keeps the rule testable on all three
// platforms.
func ruleSourcePath(path string) string {
	return strings.ReplaceAll(path, "\\", "/")
}

// readRuleSources indexes the TypeScript files the UI rules apply to, keyed
// by the slash-normalized path the rule checks look up. readSource is
// injected so tests can feed Windows-style paths without depending on the
// host OS.
func readRuleSources(files []string, readSource func(string) ([]byte, error)) (map[string]string, error) {
	sources := make(map[string]string, len(files))
	for _, file := range files {
		key := ruleSourcePath(file)
		if key == "src/mygo.ts" {
			continue
		}
		raw, err := readSource(file)
		if err != nil {
			return nil, err
		}
		sources[key] = stripTSComments(string(raw))
	}
	return sources, nil
}

// ruleSources collects src/*.ts from the real tree, keyed by normalized path.
func ruleSources(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob("src/*.ts")
	if err != nil {
		t.Fatal(err)
	}
	sources, err := readRuleSources(files, os.ReadFile)
	if err != nil {
		t.Fatal(err)
	}
	return sources
}

// TestIssue16SourcePathsAreSlashNormalized guards the Windows half of the
// #16 checks: filepath.Glob returns `src\ui.ts` there, while every lookup is
// written as `src/ui.ts`. The Windows spelling is injected on every OS, so
// this test catches a regression on macOS and Linux too.
func TestIssue16SourcePathsAreSlashNormalized(t *testing.T) {
	sources, err := readRuleSources(
		[]string{`src\ui.ts`, `src\mygo.ts`},
		func(string) ([]byte, error) { return []byte("// source"), nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := sources["src/ui.ts"]; !ok {
		t.Fatalf(`src\ui.ts was not indexed as "src/ui.ts" — the #16 lookups would miss it on Windows`)
	}
	if _, ok := sources["src/mygo.ts"]; ok {
		t.Errorf(`src\mygo.ts is generated and must stay out of the rule sources`)
	}
}

// TestIssue16ExitRemovalHasTimerFallback guards the #16 rule behind the CSS
// one: removal after an exit cannot depend on an event that may never fire.
// All "wait for the exit, then remove" behavior lives in one helper in
// src/ui.ts (the generated src/mygo.ts is exempt); that helper must carry a
// setTimeout fallback and call .remove(), dismiss() must route through it,
// and the event names and on-property handlers it may use are allowed
// nowhere else. A new direct listener, a helper without a timer, or a timer
// that forgets to remove all fail here.
func TestIssue16ExitRemovalHasTimerFallback(t *testing.T) {
	stripped := ruleSources(t)

	body, helperStart, helperEnd, ok := functionBody(stripped["src/ui.ts"], exitRemoveHelper)
	if !ok {
		t.Fatalf("src/ui.ts: no %s helper — exit removal must live in one helper with a timer fallback (issue #16)", exitRemoveHelper)
	}
	if !strings.Contains(body, "setTimeout") {
		t.Errorf("src/ui.ts: %s has no setTimeout fallback — a missed exit event must not leave the element in the page (issue #16)", exitRemoveHelper)
	}
	if !strings.Contains(body, ".remove()") {
		t.Errorf("src/ui.ts: %s never calls .remove() — waiting out the exit must end in removal (issue #16)", exitRemoveHelper)
	}

	// The dismiss path must route through the helper. Check the function body
	// itself (comments already blanked) so deleting the call and mentioning
	// its name in a // note cannot satisfy the rule.
	dismiss, _, _, ok := functionBody(stripped["src/ui.ts"], "dismiss")
	if !ok {
		t.Errorf("src/ui.ts: showToast has no dismiss() — exit removal must be owned by it (issue #16)")
	} else if callRE := regexp.MustCompile(`\b` + regexp.QuoteMeta(exitRemoveHelper) + `\s*\(`); !callRE.MatchString(dismiss) {
		t.Errorf("src/ui.ts: dismiss() does not call %s — the dismiss path must route through the helper (issue #16)", exitRemoveHelper)
	}

	// Event names are allowed only inside the helper: quoted addEventListener
	// names and on-property handlers (ontransitionend, onanimationend) are
	// both ways of waiting for the same exit.
	eventRE := regexp.MustCompile("[\"'`](animationend|transitionend)[\"'`]")
	propertyRE := regexp.MustCompile(`\bon(?:animation|transition)end\b`)
	for file, src := range stripped {
		check := func(idx int, name string) {
			if file != "src/ui.ts" || idx < helperStart || idx >= helperEnd {
				line := strings.Count(src[:idx], "\n") + 1
				t.Errorf("%s:%d: %q outside %s — route exit waits through the one helper (issue #16)", file, line, name, exitRemoveHelper)
			}
		}
		for _, m := range eventRE.FindAllStringSubmatchIndex(src, -1) {
			check(m[0], src[m[2]:m[3]])
		}
		for _, m := range propertyRE.FindAllStringIndex(src, -1) {
			check(m[0], src[m[0]:m[1]])
		}
	}
}

// TestIssue16ToastActionDiesWithDismiss guards the #12 promise that an undo
// entry disappears with its toast. CSS stops a faded action from receiving
// pointer events; this checks the code-side guard in showToast's action
// click handler: an early `if (dismissed) return` before action.onClick().
func TestIssue16ToastActionDiesWithDismiss(t *testing.T) {
	raw, err := os.ReadFile("src/ui.ts")
	if err != nil {
		t.Fatal(err)
	}
	src := stripTSComments(string(raw))
	show, showStart, _, ok := functionBody(src, "showToast")
	if !ok {
		t.Fatalf("src/ui.ts: no showToast function — the toast action guard cannot be checked (issue #16)")
	}
	handlerDecl := regexp.MustCompile(`button\.addEventListener\(\s*["']click["']\s*,[^{]*\{`)
	loc := handlerDecl.FindStringIndex(show)
	if loc == nil {
		t.Fatalf("src/ui.ts: showToast has no action click handler — the dismissed guard cannot be checked (issue #16)")
	}
	handler, _, ok := braceBodyAt(show, loc[1]-1)
	if !ok {
		t.Fatalf("src/ui.ts: cannot read the action click handler body (issue #16)")
	}
	onClick := strings.Index(handler, "action.onClick(")
	if onClick < 0 {
		t.Fatalf("src/ui.ts: action click handler never calls action.onClick() (issue #16)")
	}
	guard := regexp.MustCompile(`if\s*\(\s*dismissed\s*\)\s*(?:\{\s*)?return\b`)
	guardLoc := guard.FindStringIndex(handler)
	if guardLoc == nil || guardLoc[0] > onClick {
		line := strings.Count(src[:showStart+loc[0]], "\n") + 1
		t.Errorf("src/ui.ts:%d: action.onClick() runs without an earlier `if (dismissed) return` — an action must die with its toast (issue #16)", line)
	}
}
