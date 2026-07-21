//go:build !darwin

package main

import "unsafe"

// fixHiDPI is a no-op off macOS (the WKWebView scale-factor workaround is
// Cocoa-specific).
func fixHiDPI(unsafe.Pointer) {}

// setPageZoom is a no-op off macOS (page zoom is a WKWebView SPI).
func setPageZoom(unsafe.Pointer, float64) {}
