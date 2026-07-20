//go:build !darwin

package main

import "unsafe"

// fixHiDPI is a no-op off macOS (the WKWebView scale-factor workaround is
// Cocoa-specific).
func fixHiDPI(unsafe.Pointer) {}
