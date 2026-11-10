//go:build darwin

package main

import "testing"

// TestIssue17DarwinScreenCaptureSymbolsResolve proves purego can find both
// CoreGraphics TCC calls and that the preflight call signature works. It runs
// the preflight once (it never prompts) and does not assert its value, which
// depends on this machine's TCC state. The request is never called: that
// would add AnyPic to the system list and show a real prompt.
func TestIssue17DarwinScreenCaptureSymbolsResolve(t *testing.T) {
	preflight, request, err := loadScreenCaptureSymbols()
	if err != nil {
		t.Fatalf("resolve CoreGraphics screen capture symbols: %v", err)
	}
	if preflight == nil {
		t.Fatal("CGPreflightScreenCaptureAccess did not resolve to a function")
	}
	if request == nil {
		t.Fatal("CGRequestScreenCaptureAccess did not resolve to a function")
	}
	if _, err := preflightScreenCaptureAccess(); err != nil {
		t.Fatalf("call CGPreflightScreenCaptureAccess: %v", err)
	}
}
