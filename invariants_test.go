package main

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestInvariantOneNoNetworkImports is the executable half of AGENTS.md
// invariant 1: image bytes never leave the machine. The updater is the only
// approved network path (issue #3), so every other non-test Go file must not
// import net or any of its subpackages (net/http, net/url, ...).
//
// This is deliberately an import-surface tripwire, not a proof: it cannot
// see a hand-rolled network client reached through os/exec or a dependency
// with another import path. It is the cheap guard the first-principles
// review asked for; the product promise stays the invariant itself.
func TestInvariantOneNoNetworkImports(t *testing.T) {
	fset := token.NewFileSet()
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			name := entry.Name()
			// Hidden dirs and build outputs are not source.
			if path != "." && (strings.HasPrefix(name, ".") || name == "build" || name == "dist" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || path == "updater.go" {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imported := range file.Imports {
			importPath, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				return err
			}
			if importPath == "net" || strings.HasPrefix(importPath, "net/") {
				t.Errorf("%s imports %q; only updater.go may use the network (AGENTS.md invariant 1)", path, importPath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
