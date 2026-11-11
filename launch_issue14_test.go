// Guards for the second half of #14. The reporter's words: 「软件可以打开多次，
// 运行着几份，应该限制同时只能运行一个软件」. Since 0.1.6 main takes MyGo's
// single-instance lock, so a second copy no longer starts — but on Windows
// and Linux the refused launch only reaches the running copy through
// OnSecondInstance (files go on to OnOpenFile; a bare relaunch carries
// none). Nothing listened, so starting AnyPic again while its window sat in
// the tray did nothing at all: the new process exited and the old one stayed
// hidden. macOS never showed it because the system activates the running
// app itself.
package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestIssue14SecondLaunchSurfacesTheRunningWindow: the handler the running
// copy registers must bring its window back for every second launch — the
// bare one (the user started AnyPic again from the shortcut) and the one
// that carries files alike — and must do nothing, rather than panic, while
// the window does not exist yet: the lock is taken before WhenReady creates
// it, so a launch can arrive in between.
func TestIssue14SecondLaunchSurfacesTheRunningWindow(t *testing.T) {
	var showWindow func()
	handler := secondLaunchHandler(&showWindow)

	// No window yet: nothing to show, and nothing to call.
	handler(nil, "")

	shown := 0
	showWindow = func() { shown++ }

	// The reporter's case: AnyPic is already running and is started again
	// with no arguments.
	handler(nil, `C:\Users\Administrator`)
	if shown != 1 {
		t.Fatalf("a bare second launch surfaced the window %d times, want 1", shown)
	}

	// A launch that hands over a file surfaces the window too; OnOpenFile
	// then queues the file.
	handler([]string{`C:\Users\Administrator\Pictures\a.png`}, `C:\Users\Administrator`)
	if shown != 2 {
		t.Fatalf("a second launch carrying a file surfaced the window %d times in total, want 2", shown)
	}
}

// TestIssue14MainTakesTheLockFirstAndListensForRelaunches reads main's
// wiring back from its source, since main itself cannot run in a test:
//
//   - the lock is still requested, and a refused launch still returns;
//   - it is requested before anything a second copy must not do — build the
//     service (NewAnyPic clears stale screenshot folders, #18, which belong
//     to the running copy), bind it, create the window, run;
//   - the running copy registers the second-launch handler before Run.
func TestIssue14MainTakesTheLockFirstAndListensForRelaunches(t *testing.T) {
	stmts := issue14MainStatements(t)

	lock := -1
	for i, stmt := range stmts {
		if issue14IsLockGuard(stmt) {
			lock = i
			break
		}
	}
	if lock < 0 {
		t.Fatal("main.go: main no longer refuses a second copy — want `if !app.RequestSingleInstanceLock() { return }` (#14: only one AnyPic may run)")
	}

	for _, name := range []string{"NewAnyPic", "Bind", "WhenReady", "Run"} {
		first := issue14FirstCall(stmts, name)
		if first < 0 {
			t.Errorf("main.go: main no longer calls %s — update this guard with the new startup order", name)
			continue
		}
		if first < lock {
			t.Errorf("main.go: main calls %s before taking the single-instance lock — a refused second copy must return before it does any of the app's work (#14)", name)
		}
	}

	listen := issue14FirstCall(stmts, "OnSecondInstance")
	if listen < 0 {
		t.Fatal("main.go: main registers no OnSecondInstance handler — starting AnyPic again while it sits in the tray would do nothing (#14)")
	}
	if !issue14Calls(stmts[listen], "secondLaunchHandler") {
		t.Error("main.go: OnSecondInstance is not given secondLaunchHandler — the handler TestIssue14SecondLaunchSurfacesTheRunningWindow checks is not the one that runs")
	}
	if listen < lock {
		t.Error("main.go: OnSecondInstance is registered before the lock is taken — only the copy that holds the lock receives second launches")
	}
	if run := issue14FirstCall(stmts, "Run"); run >= 0 && listen > run {
		t.Error("main.go: OnSecondInstance is registered after app.Run — Run blocks until the app quits, so the handler would never be registered")
	}
}

// issue14MainStatements parses main.go and returns the top-level statements
// of func main.
func issue14MainStatements(t *testing.T) []ast.Stmt {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing main.go: %v", err)
	}
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == "main" && fn.Body != nil {
			return fn.Body.List
		}
	}
	t.Fatal("main.go declares no func main")
	return nil
}

// issue14IsLockGuard reports whether stmt is
// `if !<app>.RequestSingleInstanceLock() { ...; return }`: the launch that is
// refused the lock leaves main.
func issue14IsLockGuard(stmt ast.Stmt) bool {
	guard, ok := stmt.(*ast.IfStmt)
	if !ok || guard.Init != nil || guard.Else != nil {
		return false
	}
	not, ok := guard.Cond.(*ast.UnaryExpr)
	if !ok || not.Op != token.NOT {
		return false
	}
	call, ok := not.X.(*ast.CallExpr)
	if !ok || issue14CallName(call) != "RequestSingleInstanceLock" {
		return false
	}
	if len(guard.Body.List) == 0 {
		return false
	}
	_, returns := guard.Body.List[len(guard.Body.List)-1].(*ast.ReturnStmt)
	return returns
}

// issue14FirstCall returns the index of the first statement that calls a
// function or method named name, or -1.
func issue14FirstCall(stmts []ast.Stmt, name string) int {
	for i, stmt := range stmts {
		if issue14Calls(stmt, name) {
			return i
		}
	}
	return -1
}

// issue14Calls reports whether node contains a call to a function or method
// named name: NewAnyPic(), mygo.Bind(x) and app.Run() are calls to NewAnyPic,
// Bind and Run.
func issue14Calls(node ast.Node, name string) bool {
	found := false
	ast.Inspect(node, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok && issue14CallName(call) == name {
			found = true
		}
		return !found
	})
	return found
}

func issue14CallName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return fn.Sel.Name
	}
	return ""
}
