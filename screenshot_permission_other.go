//go:build !darwin

package main

// Other platforms have no screen recording permission gate: capture always
// proceeds the way it did before #17.
func preflightScreenCaptureAccess() (bool, error) { return true, nil }

func requestScreenCaptureAccess() {}
