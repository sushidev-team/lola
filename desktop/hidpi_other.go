//go:build !darwin

package main

import "unsafe"

// The standalone Preview browser and the Retina override use WKWebView.
func configureNativeWebView(unsafe.Pointer) {}

// setPageZoom is a no-op off macOS (page zoom is a WKWebView SPI).
func setPageZoom(unsafe.Pointer, float64) {}
