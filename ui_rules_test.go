// UI guard tests ported from magpie's GUI discipline. The rules and their
// borrowed evidence live in docs/standards.md「界面」 and
// docs/subsystems/ui.md; each test fails naming the file and line that
// breaks its rule.
package main

import (
	"bytes"
	"os"
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
// style.css and index.html. Each budget is current + ~2% headroom. Over
// budget: move the code to its own module, or raise the budget here and say
// what the growth was for.
var uiBudgets = []struct {
	file   string
	budget int
}{
	{"src/main.ts", 76_800},
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
