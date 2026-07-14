//go:build !darwin

package notify

// newDesktopChannel is a no-op off macOS: there is no portable desktop banner,
// so New simply omits the desktop channel and any routing to it is skipped.
func newDesktopChannel() channel { return nil }
