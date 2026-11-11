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
// ledger entry, not a second hiding place. #21 grew style.css to 59,208 and
// index.html to 31,315: the zoom pill, pager and their over-image styling —
// preview.ts, where the zoom/pan and paging state lives, is not in this
// ledger yet. #20 grew style.css to 60,571 and index.html to 32,423: the row
// focus ring and the right-click menu's surface; its behaviour moved to
// src/queue-keys.ts rather than pushing main.ts past its line. #23 replaced
// every literal font-size and px radius with a token, so style.css carries the
// longer names (63,599): the ladder is the point, not a hidden growth.
var uiBudgets = []struct {
	file   string
	budget int
}{
	{"src/main.ts", 80_200},
	{"src/shortcuts.ts", 4_200},
	{"src/queue-keys.ts", 12_300},
	{"src/style.css", 64_800},
	{"index.html", 32_900},
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

// TestIssue31SummarySamePopulation guards #31: the three header numbers must
// describe the same images — the finished ones — and anything still pending is
// stated separately. The old rule mixed all queue originals with the finished
// output, so "20.3 MB → 2.3 MB · 74%" did not add up.
func TestIssue31SummarySamePopulation(t *testing.T) {
	stats, _, _, ok := functionBody(ruleSources(t)["src/main.ts"], "renderStats")
	if !ok {
		t.Fatalf("src/main.ts: no renderStats function (#31)")
	}
	if strings.Contains(stats, "totalOriginal") || strings.Contains(stats, "formatBytes(totalOriginal)") {
		t.Errorf("src/main.ts: renderStats still feeds the all-items original total into the header; original, compressed and saved must share the finished set (#31)")
	}
	if !strings.Contains(stats, "formatBytes(doneOriginal)") {
		t.Errorf("src/main.ts: the header's original total is not computed from the finished images (#31)")
	}
	if !strings.Contains(stats, "summaryPending") {
		t.Errorf("src/main.ts: renderStats never fills #summaryPending; unprocessed images need a separate note (#31)")
	}

	html, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), `id="summaryPending"`) {
		t.Errorf("index.html: no #summaryPending element for the images still waiting (#31)")
	}
}

// listenerBody returns the arrow-function body of the change listener opened
// by decl, e.g. `$("#dockToggle").addEventListener("change"`.
func listenerBody(t *testing.T, src, decl string) string {
	t.Helper()
	i := strings.Index(src, decl)
	if i < 0 {
		t.Fatalf("src/main.ts: no change listener %q", decl)
	}
	open := strings.Index(src[i:], "{")
	if open < 0 {
		t.Fatalf("src/main.ts: listener %q has no body", decl)
	}
	body, _, ok := braceBodyAt(src, i+open)
	if !ok {
		t.Fatalf("src/main.ts: cannot read the body of %q", decl)
	}
	return body
}

// TestIssue26ToggleToastsOnlyOnFailure guards #26: a toggle's own state is the
// feedback, so flipping one succeeds silently. A toast appears only when the
// backend refuses the change, and only after its answer. The old handlers
// announced every toggle with an info/success toast, and "开机时启动" claimed
// success before SaveSettings had answered.
func TestIssue26ToggleToastsOnlyOnFailure(t *testing.T) {
	src := ruleSources(t)["src/main.ts"]

	for _, id := range []string{"clipboardToggle", "autoProcessToggle", "dockToggle"} {
		if !strings.Contains(src, `bindToggle("#`+id+`"`) {
			t.Errorf("src/main.ts: #%s is not bound through the failure-only helper (#26)", id)
		}
	}

	bind, _, _, ok := functionBody(src, "bindToggle")
	if !ok {
		t.Errorf("src/main.ts: no bindToggle helper; every toggle needs one failure-only path (#26)")
	} else {
		if !strings.Contains(bind, "showError") {
			t.Errorf("src/main.ts: bindToggle never reports a failed save (#26)")
		}
		if strings.Contains(bind, `"success"`) || strings.Contains(bind, `"info"`) {
			t.Errorf("src/main.ts: bindToggle still reports success/info — only failures deserve a toast (#26)")
		}
		if at, backendAt := strings.Index(bind, "showError"), strings.Index(bind, "setSettings("); at >= 0 && backendAt >= 0 && at < backendAt {
			t.Errorf("src/main.ts: bindToggle reports failure before the backend answers (#26)")
		}
	}

	open := listenerBody(t, src, `$("#openAtLoginToggle").addEventListener("change"`)
	if !strings.Contains(open, `"error"`) {
		t.Errorf("src/main.ts: the 开机时启动 toggle has no failure message for a refused login item (#26)")
	}
	if at, backendAt := strings.Index(open, "showToast("), strings.Index(open, "setSettings("); at >= 0 && backendAt >= 0 && at < backendAt {
		t.Errorf("src/main.ts: 开机时启动 claims a result before SaveSettings returns (#26)")
	}
}

// TestIssue25ToastsClearTheFooterButton guards #25: the toast stack used to
// sit at right: 20px / bottom: 20px, exactly over the footer's primary action.
// It now lives at the top centre, over the titlebar's non-interactive middle,
// so a live toast covers no clickable control at the minimum 940×640 window.
func TestIssue25ToastsClearTheFooterButton(t *testing.T) {
	raw, err := os.ReadFile("src/style.css")
	if err != nil {
		t.Fatal(err)
	}
	css := stripBlockComments(string(raw))
	ruleRE := regexp.MustCompile(`(?s)\.toast-stack\s*\{([^{}]*)\}`)
	m := ruleRE.FindStringSubmatch(css)
	if m == nil {
		t.Fatalf("src/style.css: no .toast-stack rule (#25)")
	}
	body := m[1]
	if regexp.MustCompile(`\b(right|bottom)\s*:`).MatchString(body) {
		t.Errorf("src/style.css: .toast-stack is still anchored to a bottom corner over the footer actions (#25):%s", body)
	}
	for _, want := range []string{"top:", "left: 50%", "translateX(-50%)"} {
		if !strings.Contains(body, want) {
			t.Errorf("src/style.css: .toast-stack is not top-centred (%q missing) (#25):%s", want, body)
		}
	}
	if !regexp.MustCompile(`(?i)pointer-events\s*:\s*none`).MatchString(body) {
		t.Errorf("src/style.css: .toast-stack must keep pointer-events: none so the gaps between toasts stay clickable (#25)")
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

// divBodyByClass returns the inner markup of the first <div> whose class list
// contains class, matched by nesting rather than by a closing-tag regex, so a
// nested toolbar inside the queue cannot end the block early.
func divBodyByClass(src, class string) (string, bool) {
	open := regexp.MustCompile(`<div[^>]*\bclass="[^"]*\b` + regexp.QuoteMeta(class) + `\b[^"]*"[^>]*>`)
	loc := open.FindStringIndex(src)
	if loc == nil {
		return "", false
	}
	tag := regexp.MustCompile(`(?i)<div\b|</div\s*>`)
	depth, i := 1, loc[1]
	for depth > 0 {
		m := tag.FindStringIndex(src[i:])
		if m == nil {
			return "", false
		}
		if strings.EqualFold(src[i+m[0]:i+m[0]+4], "<div") {
			depth++
		} else {
			depth--
		}
		i += m[1]
	}
	return src[loc[1]:i], true
}

// TestIssue27SelectionTakesNoExtraRow guards #27: the selection bar used to be
// a sibling row under the queue toolbar, so ticking the first checkbox pushed
// every row down by the bar's height and moved the row under the cursor. It
// now replaces the status tabs inside the toolbar's fixed-height row; the
// toolbar keeps 46px so surrounding content still cannot move. List and grid
// share this toolbar, so one structural check covers both.
func TestIssue27SelectionTakesNoExtraRow(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	body, ok := divBodyByClass(string(raw), "queue-toolbar")
	if !ok {
		t.Fatal("index.html: no .queue-toolbar element (#27)")
	}
	if !strings.Contains(body, `id="selectionBar"`) {
		t.Errorf("index.html: #selectionBar sits outside .queue-toolbar — selecting a row adds a row and shifts the list (#27)")
	}

	cssRaw, err := os.ReadFile("src/style.css")
	if err != nil {
		t.Fatal(err)
	}
	rule := regexp.MustCompile(`(?s)\.queue-toolbar\s*\{([^{}]*)\}`)
	m := rule.FindStringSubmatch(stripBlockComments(string(cssRaw)))
	if m == nil {
		t.Fatalf("src/style.css: no .queue-toolbar rule (#27)")
	}
	if !regexp.MustCompile(`(?m)flex:\s*0 0 \d+px`).MatchString(m[1]) {
		t.Errorf("src/style.css: .queue-toolbar lost its fixed height, so selection can still move the list (#27):%s", m[1])
	}

	bar := regexp.MustCompile(`(?s)\.selection-bar\s*\{([^{}]*)\}`)
	b := bar.FindStringSubmatch(stripBlockComments(string(cssRaw)))
	if b == nil {
		t.Fatalf("src/style.css: no .selection-bar rule (#27)")
	}
	if regexp.MustCompile(`(?m)border-bottom\s*:`).MatchString(b[1]) {
		t.Errorf("src/style.css: .selection-bar still draws a row divider — it lives inside the toolbar now (#27):%s", b[1])
	}
}

// TestIssue24MustReadCopyIsNotFaint guards #24. --text-faint sits at 3.3–3.7:1
// on the surfaces it is used on, and the token block says so: it is for quiet
// labels, while anything a user must read goes one step up. These rules carry
// explanations — where automatic results are written, what a format does, why
// a capture did not happen — so they must not paint with the faint step. The
// background sits deeper in the same rule; inheriting a faint colour is the
// same failure, so each listed rule must set its own readable colour.
func TestIssue24MustReadCopyIsNotFaint(t *testing.T) {
	raw, err := os.ReadFile("src/style.css")
	if err != nil {
		t.Fatal(err)
	}
	css := stripBlockComments(string(raw))
	rules := []string{
		`.field-note`,
		`.output-inline`,
		`.tip-card`,
		`.shortcut-hint`,
		`.empty-state p`,
		`.empty-actions`,
		`.queue-empty-filter`,
	}
	readable := regexp.MustCompile(`color:\s*var\(--(ctl-fg|text-soft|text)\)`)
	for _, sel := range rules {
		m := regexp.MustCompile(`(?s)` + regexp.QuoteMeta(sel) + `\s*\{([^{}]*)\}`).FindStringSubmatch(css)
		if m == nil {
			t.Errorf("src/style.css: no %s rule — update this list if the rule was renamed (#24)", sel)
			continue
		}
		if !readable.MatchString(m[1]) {
			t.Errorf("src/style.css: %s paints must-read copy below --ctl-fg (#24):%s", sel, m[1])
		}
	}
}


// TestIssue29HeadingsAreNotShouted guards #29: the section heads borrow an
// English pattern — uppercase plus 0.05em tracking — that does nothing to
// Chinese (there are no capitals) and only stretches the characters apart.
// Weight and colour already carry the hierarchy, so the rules must not set
// either property; per-language treatment belongs to the i18n pass (#32).
func TestIssue29HeadingsAreNotShouted(t *testing.T) {
	raw, err := os.ReadFile("src/style.css")
	if err != nil {
		t.Fatal(err)
	}
	css := stripBlockComments(string(raw))
	for _, sel := range []string{`.block-head h2`, `.more-settings-summary`} {
		m := regexp.MustCompile(`(?s)` + regexp.QuoteMeta(sel) + `\s*\{([^{}]*)\}`).FindStringSubmatch(css)
		if m == nil {
			t.Errorf("src/style.css: no %s rule — update this list if the rule was renamed (#29)", sel)
			continue
		}
		if regexp.MustCompile(`text-transform\s*:`).MatchString(m[1]) {
			t.Errorf("src/style.css: %s still sets text-transform, which does nothing to Chinese (#29):%s", sel, m[1])
		}
		if regexp.MustCompile(`letter-spacing\s*:`).MatchString(m[1]) {
			t.Errorf("src/style.css: %s still sets letter-spacing, which only stretches Chinese (#29):%s", sel, m[1])
		}
	}
}

// TestIssue30ActionsColumnFitsTheButtons guards #30. The walk-through feared
// the hover actions overflowed onto the saving pill; measurement in the page
// showed the opposite — the flex row shrank the four 28px buttons to 21px
// instead, below the hit target every other icon button in the app keeps.
// So the check is the column's arithmetic: every list grid's last track must
// hold the buttons at full size, and the count comes from the row template,
// so a fifth action added later fails here until the column grows with it.
func TestIssue30ActionsColumnFitsTheButtons(t *testing.T) {
	src, err := os.ReadFile("src/main.ts")
	if err != nil {
		t.Fatal(err)
	}
	ts := stripTSComments(string(src))
	inner := regexp.MustCompile(`(?s)<div class="item-actions">(.*?)</div>`).FindStringSubmatch(ts)
	if inner == nil {
		t.Fatal("src/main.ts: no .item-actions block in the row template (#30)")
	}
	buttons := strings.Count(inner[1], `data-action="`)
	if buttons == 0 {
		t.Fatal("src/main.ts: .item-actions has no data-action buttons (#30)")
	}

	cssRaw, err := os.ReadFile("src/style.css")
	if err != nil {
		t.Fatal(err)
	}
	css := stripBlockComments(string(cssRaw))
	px := func(rule, prop string) int {
		m := regexp.MustCompile(`(?s)` + rule + `\s*\{([^{}]*)\}`).FindStringSubmatch(css)
		if m == nil {
			t.Fatalf("src/style.css: no %s rule (#30)", rule)
		}
		v := regexp.MustCompile(`(?m)(?:^|[;\s])` + prop + `\s*:\s*(\d+)px`).FindStringSubmatch(m[1])
		if v == nil {
			t.Fatalf("src/style.css: %s has no px %s (#30):%s", rule, prop, m[1])
		}
		n := 0
		for _, c := range v[1] {
			n = n*10 + int(c-'0')
		}
		return n
	}
	size := px(`\.item-actions \.icon-btn`, "width")
	gap := px(`\.item-actions`, "gap")
	need := buttons*size + (buttons-1)*gap

	grids := regexp.MustCompile(`(?s)\.queue-(?:item|columns)\s*\{([^{}]*)\}`).FindAllStringSubmatch(css, -1)
	if len(grids) == 0 {
		t.Fatal("src/style.css: no queue list grid rule (#30)")
	}
	for _, g := range grids {
		tpl := regexp.MustCompile(`(?m)grid-template-columns\s*:\s*([^;]+);`).FindStringSubmatch(g[1])
		if tpl == nil {
			continue
		}
		tracks := strings.Fields(tpl[1])
		last := tracks[len(tracks)-1]
		v := regexp.MustCompile(`^(\d+)px$`).FindStringSubmatch(last)
		if v == nil {
			continue
		}
		width := 0
		for _, c := range v[1] {
			width = width*10 + int(c-'0')
		}
		if width < need {
			t.Errorf("src/style.css: actions column is %dpx but the row holds %d × %dpx buttons + %d of gap = %dpx, so they shrink below their size (#30)", width, buttons, size, (buttons-1)*gap, need)
		}
	}
}


// TestIssue22PreferencesLiveInADialog guards #22: the sidebar answers "how
// should this batch come out" — quality, output format, where it lands. The
// application's own preferences (theme, launch at login, the Dock, the capture
// shortcut and clipboard) moved into a preferences dialog reached by 偏好设置…
// and Cmd+,. Output format is part of the answer every time, so it sits on the
// first screen rather than behind 更多设置.
func TestIssue22PreferencesLiveInADialog(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)

	start := strings.Index(html, `<aside class="sidebar">`)
	end := strings.Index(html, `</aside>`)
	if start < 0 || end < start {
		t.Fatal("index.html: no <aside class=\"sidebar\"> block (#22)")
	}
	aside := html[start:end]
	for _, id := range []string{"openAtLoginToggle", "dockToggle", "shortcutBtn", "clipboardToggle", "themeBtn"} {
		if strings.Contains(aside, `id="`+id+`"`) {
			t.Errorf("index.html: #%s still lives in the sidebar — application preferences belong in the preferences dialog (#22)", id)
		}
	}

	if !strings.Contains(html, `id="prefsModal"`) {
		t.Errorf("index.html: no #prefsModal — 偏好设置 has nowhere to open (#22)")
	}
	for _, id := range []string{"themeSeg", "openAtLoginToggle", "dockToggle", "shortcutBtn", "clipboardToggle"} {
		if !strings.Contains(html, `id="`+id+`"`) {
			t.Errorf("index.html: #%s is missing from the preferences dialog (#22)", id)
		}
	}

	formatAt := strings.Index(html, `id="formatSeg"`)
	detailsAt := strings.Index(html, `<details class="more-settings"`)
	if formatAt < 0 || detailsAt < 0 {
		t.Fatalf("index.html: missing #formatSeg or the 更多设置 disclosure (#22)")
	}
	if formatAt > detailsAt {
		t.Errorf("index.html: 输出格式 still sits behind 更多设置 — it decides every result and belongs on the first screen (#22)")
	}

	goRaw, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	goSrc := string(goRaw)
	if !strings.Contains(goSrc, `command("prefs")`) {
		t.Errorf("main.go: no menu command for 偏好设置 (#22)")
	}
	if !strings.Contains(goSrc, `"CmdOrCtrl+,"`) {
		t.Errorf("main.go: 偏好设置 has no CmdOrCtrl+, accelerator (#22)")
	}
}

// elementBodyByClass returns the inner markup of the first <tag> whose class
// list contains class, matched by nesting depth.
func elementBodyByClass(src, tag, class string) (string, bool) {
	open := regexp.MustCompile(`<` + tag + `[^>]*\bclass="[^"]*\b` + regexp.QuoteMeta(class) + `\b[^"]*"[^>]*>`)
	loc := open.FindStringIndex(src)
	if loc == nil {
		return "", false
	}
	tags := regexp.MustCompile(`(?i)<` + tag + `\b|</` + tag + `\s*>`)
	depth, i := 1, loc[1]
	for depth > 0 {
		m := tags.FindStringIndex(src[i:])
		if m == nil {
			return "", false
		}
		if strings.EqualFold(src[i+m[0]:i+m[0]+len(tag)+1], "<"+tag) {
			depth++
		} else {
			depth--
		}
		i += m[1]
	}
	return src[loc[1]:i], true
}

// TestIssue19OnePrimaryAction guards #19: the window used to show up to three
// solid accent buttons at once (添加, the sidebar's 压缩全部, the footer), and
// the footer button changed identity between capture, compress and export.
// There is now one primary action in the shell; it lives in the footer, keeps
// its position, and 添加 is a secondary button. The empty state keeps its own
// primary because the footer is hidden when the queue is empty.
func TestIssue19OnePrimaryAction(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	if strings.Contains(html, `id="compressBtn"`) || strings.Contains(html, `id="sidebarActions"`) {
		t.Errorf("index.html: the sidebar still owns a large compress button — the footer's is the one primary action (#19)")
	}
	add := regexp.MustCompile(`<button[^>]*id="addBtn"[^>]*>`).FindString(html)
	if add == "" {
		t.Fatal("index.html: no #addBtn (#19)")
	}
	if strings.Contains(add, "primary-btn") {
		t.Errorf("index.html: 添加 is still a solid primary button next to the footer's — demote it (#19): %s", add)
	}

	src, err := os.ReadFile("src/main.ts")
	if err != nil {
		t.Fatal(err)
	}
	body, _, _, ok := functionBody(stripTSComments(string(src)), "renderStageAction")
	if !ok {
		t.Fatal("src/main.ts: no renderStageAction (#19)")
	}
	if strings.Contains(body, `"screenshot"`) {
		t.Errorf("src/main.ts: the footer button still becomes 截图 — capture has its own fixed entry now (#19)")
	}
}

// TestIssue28TitlebarCarriesTheActions guards #28: 截图 and 添加 moved into the
// 46px titlebar, so the stage head is a single line (count, summary) instead of
// a second toolbar. The brand subtitle 图片压缩 and the 本地处理 badge are gone
// — the badge's promise already lives in the About dialog.
func TestIssue28TitlebarCarriesTheActions(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	titlebar, ok := elementBodyByClass(html, "header", "titlebar")
	if !ok {
		t.Fatal("index.html: no .titlebar header (#28)")
	}
	for _, id := range []string{`id="screenshotBtn"`, `id="addBtn"`} {
		if !strings.Contains(titlebar, id) {
			t.Errorf("index.html: %s is not in the titlebar — the stage head keeps a whole toolbar row for it (#28)", id)
		}
	}
	head, ok := elementBodyByClass(html, "header", "stage-head")
	if !ok {
		t.Fatal("index.html: no .stage-head header (#28)")
	}
	for _, id := range []string{`id="screenshotBtn"`, `id="addBtn"`} {
		if strings.Contains(head, id) {
			t.Errorf("index.html: %s still sits in the stage head as well (#28)", id)
		}
	}
	for _, gone := range []string{"图片压缩", "本地处理", "window-subtitle", "titlebar-note"} {
		if strings.Contains(titlebar, gone) {
			t.Errorf("index.html: the titlebar still carries %q — it earns no row and no room (#28)", gone)
		}
	}
}

// TestIssue31PendingNoteSurvivesNarrowWindows guards a hole in #31's fix: the
// pending note borrowed .summary-foot, which the ≤1040px rule hides, so at the
// app's 940px minimum the header stopped saying that images were still
// unfinished — exactly where honesty matters most, next to a savings figure.
func TestIssue31PendingNoteSurvivesNarrowWindows(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	tag := regexp.MustCompile(`<span[^>]*id="summaryPending"[^>]*>`).FindString(string(raw))
	if tag == "" {
		t.Fatal("index.html: no #summaryPending (#31)")
	}
	if strings.Contains(tag, "summary-foot") {
		t.Errorf("index.html: #summaryPending still uses .summary-foot, which narrow windows hide (#31): %s", tag)
	}
	css, err := os.ReadFile("src/style.css")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^\.summary-pending\s*\{`).MatchString(stripBlockComments(string(css))) {
		t.Errorf("src/style.css: no .summary-pending rule for the note (#31)")
	}
}

// TestIssue21PreviewZoomsAndNavigates guards #21: the compare view could only
// fit the whole picture to the window (a 6000px photo rendered at ~1000px, so
// compression artefacts were invisible), and checking a batch meant closing
// and reopening the preview for every image. The contract:
//
//   - the zoom transform sits on the two <img> elements only; the split line
//     and the clip live in frame coordinates, so the divider stays under the
//     cursor and keeps cutting both images at the same source pixel;
//   - the wheel listener is non-passive, or the preview scrolls instead of
//     zooming;
//   - ←/→ navigate the queue, but the focused split handle keeps them for its
//     own slider (it must not be possible to page away mid-drag);
//   - the order is the queue order the user sees (current tab + name filter).
func TestIssue21PreviewZoomsAndNavigates(t *testing.T) {
	html, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(html)
	for _, want := range []string{`id="zoomFit"`, `id="zoom100"`, `id="zoomReadout"`, `id="previewPrev"`, `id="previewNext"`, `id="previewCount"`} {
		if !strings.Contains(markup, want) {
			t.Errorf("index.html: missing %s — #21 needs a fit/100%% toggle with its ratio and prev/next controls", want)
		}
	}

	cssRaw, err := os.ReadFile("src/style.css")
	if err != nil {
		t.Fatal(err)
	}
	css := stripBlockComments(string(cssRaw))
	imgRule := regexp.MustCompile(`(?s)\.compare-img\s*\{([^{}]*)\}`).FindStringSubmatch(css)
	if imgRule == nil {
		t.Fatal("src/style.css: no .compare-img rule (#21)")
	}
	if !regexp.MustCompile(`transform:[^;]*scale\(var\(--compare-zoom`).MatchString(imgRule[1]) {
		t.Errorf("src/style.css: .compare-img has no scale(var(--compare-zoom)) transform — the wheel cannot zoom the picture (#21)")
	}
	for _, sel := range []string{".compare-handle", ".compare-after-wrap"} {
		rule := regexp.MustCompile(`(?s)` + regexp.QuoteMeta(sel) + `\s*\{([^{}]*)\}`).FindStringSubmatch(css)
		if rule == nil {
			t.Fatalf("src/style.css: no %s rule (#21)", sel)
		}
		if regexp.MustCompile(`transform:[^;]*scale\(|--compare-zoom`).MatchString(rule[1]) {
			t.Errorf("src/style.css: %s scales with the image — the split line and its clip must stay in frame coordinates (#21)", sel)
		}
	}

	ts, err := os.ReadFile("src/preview.ts")
	if err != nil {
		t.Fatal(err)
	}
	src := string(ts)
	compareBody, _, _, ok := functionBody(src, "bindCompare")
	if !ok {
		t.Fatal("src/preview.ts: no bindCompare() (#21)")
	}
	wheel := strings.Index(compareBody, `addEventListener("wheel"`)
	if wheel < 0 || !strings.Contains(compareBody[wheel:], "passive: false") {
		t.Errorf("src/preview.ts: the wheel zoom listener is missing or passive — the preview would scroll instead of zooming (#21)")
	}

	keys, _, _, ok := functionBody(src, "bindPreviewKeys")
	if !ok {
		t.Fatal("src/preview.ts: no bindPreviewKeys() (#21)")
	}
	guard := strings.Index(keys, `closest("#compareHandle")`)
	step := strings.Index(keys, "stepPreview(")
	if guard < 0 || step < 0 || guard > step {
		t.Errorf("src/preview.ts: ←/→ navigation does not bow out for the focused split handle — the slider and the pager would both consume the arrow (#21)")
	}
	if !strings.Contains(keys, `"ArrowLeft"`) || !strings.Contains(keys, `"ArrowRight"`) {
		t.Errorf("src/preview.ts: bindPreviewKeys() does not handle ←/→ (#21)")
	}

	main, err := os.ReadFile("src/main.ts")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(main), "visibleIds:") {
		t.Errorf("src/main.ts: the preview context does not expose visibleIds() — prev/next would ignore the current tab and name filter (#21)")
	}
}

// TestIssue20QueueIsAKeyboardList guards #20: the queue looks like a table
// but behaves like a pile of divs — rows cannot take focus, arrows do nothing,
// there is no range selection, and the only destructive shortcut (⌘⌫) clears
// the whole queue. The contract:
//
//   - a row is a real focus stop (roving tabindex, so the list adds one tab
//     stop, not N) and ↑/↓ move between rows; Space/Enter run the same action
//     as a click;
//   - Shift+↑/↓ and Shift+click extend a range from the last focused row;
//     ⌘/Ctrl+A selects the visible rows; Delete/Backspace removes the selection
//     (⌘⌫ keeps clearing the queue, which main.ts owns);
//   - text fields keep their native keys — no shortcut fires while typing;
//   - every row has a right-click menu with the row's actions plus "show in
//     folder", in list and grid alike (both render the same row element).
func TestIssue20QueueIsAKeyboardList(t *testing.T) {
	html, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	markup := string(html)
	if !strings.Contains(markup, `id="queueMenu"`) {
		t.Fatalf("index.html: no #queueMenu — rows have no right-click actions (#20)")
	}
	menu, ok := divBodyByClass(markup, "context-menu")
	if !ok {
		t.Fatalf("index.html: #queueMenu is not .context-menu (#20)")
	}
	if !regexp.MustCompile(`role="menu"`).MatchString(markup) {
		t.Errorf("index.html: the queue menu has no role=\"menu\" (#20)")
	}
	for _, label := range []string{"对比预览", "导出", "在文件夹中显示", "移除"} {
		if !strings.Contains(menu, label) {
			t.Errorf("index.html: the queue menu is missing %q (#20)", label)
		}
	}

	ts, err := os.ReadFile("src/queue-keys.ts")
	if err != nil {
		t.Fatalf("src/queue-keys.ts missing — the queue's keyboard and menu behaviour lives there (#20): %v", err)
	}
	src := stripTSComments(string(ts))

	rowKeys, _, _, ok := functionBody(src, "queueRowKeys")
	if !ok {
		t.Fatal("src/queue-keys.ts: no queueRowKeys() (#20)")
	}
	for _, want := range []string{`"contextmenu"`, `"ArrowUp"`, `"ArrowDown"`, `"Enter"`, `" "`} {
		if !strings.Contains(rowKeys, want) {
			t.Errorf("src/queue-keys.ts: queueRowKeys() never handles %s (#20)", want)
		}
	}
	if !strings.Contains(rowKeys, "event.shiftKey") {
		t.Errorf("src/queue-keys.ts: Shift is not handled on a row — no range selection (#20)")
	}
	if !strings.Contains(rowKeys, "activate(") {
		t.Errorf("src/queue-keys.ts: Space/Enter do not reuse the row's click action (#20)")
	}
	if !strings.Contains(rowKeys, "moveFocus(") {
		t.Errorf("src/queue-keys.ts: arrows do not move focus through moveFocus() (#20)")
	}

	move, _, _, ok := functionBody(src, "moveFocus")
	if !ok {
		t.Fatal("src/queue-keys.ts: no moveFocus() (#20)")
	}
	if !regexp.MustCompile(`if\s*\(event\.shiftKey\)\s*anchorId`).MatchString(move) {
		t.Errorf("src/queue-keys.ts: moveFocus() moves without setting the Shift anchor first — the range would grow from the wrong row (#20)")
	}
	if !strings.Contains(move, "rangeTo(") {
		t.Errorf("src/queue-keys.ts: moveFocus() does not extend a Shift range (#20)")
	}

	shortcuts, _, _, ok := functionBody(src, "bindQueueShortcuts")
	if !ok {
		t.Fatal("src/queue-keys.ts: no bindQueueShortcuts() (#20)")
	}
	for _, want := range []string{`"a"`, "Delete", "Backspace", `closest("input, textarea, select")`} {
		if !strings.Contains(shortcuts, want) {
			t.Errorf("src/queue-keys.ts: bindQueueShortcuts() never mentions %s (#20)", want)
		}
	}
	if !regexp.MustCompile(`if\s*\(meta\)\s*return`).MatchString(shortcuts) {
		t.Errorf("src/queue-keys.ts: the list shortcuts do not bow out when ⌘/Ctrl is held — ⌘⌫ must keep clearing the queue (#20)")
	}

	if _, _, _, ok := functionBody(src, "openQueueMenu"); !ok {
		t.Fatal("src/queue-keys.ts: no openQueueMenu() (#20)")
	}

	cssRaw, err := os.ReadFile("src/style.css")
	if err != nil {
		t.Fatal(err)
	}
	css := stripBlockComments(string(cssRaw))
	if !regexp.MustCompile(`(?m)^\.queue-item:focus-visible\s*\{`).MatchString(css) {
		t.Errorf("src/style.css: rows have no focus-visible style — keyboard focus is invisible (#20)")
	}

	main, err := os.ReadFile("src/main.ts")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"queueRowKeys(", "queueRowTabIndex("} {
		if !strings.Contains(string(main), want) {
			t.Errorf("src/main.ts: rows are not wired to %s (#20)", want)
		}
	}
}

// TestIssue23TypeAndRadiusLadders guards #23: eleven font sizes (10 to 18,
// half-pixel steps included), nearly every integer radius from 2 to 14, and
// seven control heights. Adjacent elements differed by 0.5px of type and 1px
// of corner — hierarchy was invisible, only misalignment showed. The
// contract:
//
//   - five text steps, three corner radii (control / card / dialog) and two
//     control heights, defined once in the :root token block with a note
//     saying what each step is for;
//   - outside that block no literal px font-size or border-radius — a new
//     value is not a new step, pick the nearest token. Percentage radii
//     (circle, squircle) and `inherit` describe a shape, not a step.
func TestIssue23TypeAndRadiusLadders(t *testing.T) {
	raw, err := os.ReadFile("src/style.css")
	if err != nil {
		t.Fatal(err)
	}
	css := stripBlockComments(string(raw))
	tokens := tokenLines(css)

	fsDecl := regexp.MustCompile(`font-size:\s*([^;]+)`)
	radiusDecl := regexp.MustCompile(`border-radius:\s*([^;]+)`)
	for i, line := range strings.Split(css, "\n") {
		if tokens[i+1] {
			continue
		}
		for _, m := range fsDecl.FindAllStringSubmatch(line, -1) {
			if !strings.HasPrefix(strings.TrimSpace(m[1]), "var(--fs-") {
				t.Errorf("src/style.css:%d: font-size %q outside the ladder — use a --fs-* token (#23)", i+1, strings.TrimSpace(m[1]))
			}
		}
		for _, m := range radiusDecl.FindAllStringSubmatch(line, -1) {
			value := strings.TrimSpace(m[1])
			ok := strings.HasPrefix(value, "var(--radius-") ||
				value == "50%" || value == "23%" || value == "inherit"
			if !ok {
				t.Errorf("src/style.css:%d: border-radius %q outside the ladder — use a --radius-* token (#23)", i+1, value)
			}
		}
	}

	// A half-pixel step is the exact thing being removed, token or not.
	if decimal := regexp.MustCompile(`font-size:\s*[0-9]+\.[0-9]+px`).FindString(css); decimal != "" {
		t.Errorf("src/style.css: fractional type size %s — the ladder is whole pixels (#23)", decimal)
	}

	fontSteps := regexp.MustCompile(`--fs-[a-z]+:\s*[0-9]+px`).FindAllString(css, -1)
	if len(fontSteps) < 4 || len(fontSteps) > 5 {
		t.Errorf("src/style.css: the type ladder has %d steps, want 4–5 (#23)", len(fontSteps))
	}
	radiusSteps := regexp.MustCompile(`--radius-(control|card|dialog):\s*[0-9]+px`).FindAllString(css, -1)
	if len(radiusSteps) != 3 {
		t.Errorf("src/style.css: the radius ladder has %d size steps, want 3 — control / card / dialog (#23)", len(radiusSteps))
	}
	for _, name := range []string{"--control-sm", "--control-md"} {
		if !regexp.MustCompile(regexp.QuoteMeta(name) + `:\s*[0-9]+px`).MatchString(css) {
			t.Errorf("src/style.css: no %s token — control heights are still ad hoc (#23)", name)
		}
	}

	// The controls that set the app's rhythm must use the height tokens.
	for _, pair := range [][2]string{
		{".secondary-btn", "--control-md"},
		{".text-input", "--control-md"},
		{".segmented button", "--control-sm"},
	} {
		rule := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(pair[0]) + `\s*\{([^{}]*)\}`).FindStringSubmatch(css)
		if rule == nil {
			t.Errorf("src/style.css: no %s rule (#23)", pair[0])
			continue
		}
		if !strings.Contains(rule[1], "var("+pair[1]+")") {
			t.Errorf("src/style.css: %s does not use %s for its height (#23)", pair[0], pair[1])
		}
	}
}
