//go:build !windows

package main

import "context"

// overlayPlatformSupported is false off Windows: the selector is built on
// Win32 windows and GDI screen capture.
func overlayPlatformSupported() bool { return false }

// captureOverlayRegion is unreachable when overlayPlatformSupported is
// false; it exists so overlay.go compiles on every platform.
func captureOverlayRegion(context.Context) ([]byte, error) {
	return nil, errOverlayUnsupported
}
