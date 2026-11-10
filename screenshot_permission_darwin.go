//go:build darwin

package main

import (
	"sync"

	"github.com/ebitengine/purego"
	"github.com/egoist/mygo"
)

// coreGraphicsFramework exports the two TCC calls. Every AppKit app already
// has CoreGraphics loaded; Dlopen only takes another reference.
const coreGraphicsFramework = "/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics"

// screenCaptureAccess resolves the symbols once. A resolution failure is
// kept so the caller can fall back to the old behavior (try the capture).
var screenCaptureAccess struct {
	once      sync.Once
	preflight func() uint8
	request   func() uint8
	err       error
}

// loadScreenCaptureSymbols resolves CGPreflightScreenCaptureAccess and
// CGRequestScreenCaptureAccess. It is split out so a test can prove both
// symbols exist without calling them: the request would show a TCC prompt.
func loadScreenCaptureSymbols() (preflight, request func() uint8, err error) {
	screenCaptureAccess.once.Do(func() {
		handle, err := purego.Dlopen(coreGraphicsFramework, purego.RTLD_LAZY|purego.RTLD_LOCAL)
		if err != nil {
			screenCaptureAccess.err = err
			return
		}
		preflightSym, err := purego.Dlsym(handle, "CGPreflightScreenCaptureAccess")
		if err != nil {
			screenCaptureAccess.err = err
			return
		}
		requestSym, err := purego.Dlsym(handle, "CGRequestScreenCaptureAccess")
		if err != nil {
			screenCaptureAccess.err = err
			return
		}
		purego.RegisterFunc(&screenCaptureAccess.preflight, preflightSym)
		purego.RegisterFunc(&screenCaptureAccess.request, requestSym)
	})
	return screenCaptureAccess.preflight, screenCaptureAccess.request, screenCaptureAccess.err
}

// preflightScreenCaptureAccess reports whether macOS would let this app
// capture other windows. It never prompts and may run on the IPC goroutine:
// Apple documents no main-thread requirement for the query.
func preflightScreenCaptureAccess() (bool, error) {
	preflight, _, err := loadScreenCaptureSymbols()
	if err != nil {
		return false, err
	}
	return preflight() != 0, nil
}

// requestScreenCaptureAccess asks macOS to add AnyPic to the screen recording
// list and show the system prompt on first use. The call presents system UI,
// so it goes through mygo.RunOnMain on the app's main thread.
func requestScreenCaptureAccess() {
	_, request, err := loadScreenCaptureSymbols()
	if err != nil {
		return
	}
	mygo.RunOnMain(func() { request() })
}
