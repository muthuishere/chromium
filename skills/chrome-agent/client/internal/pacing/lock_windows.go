//go:build windows

package pacing

// lockFile is a no-op on windows; the published engines are linux and macOS only.
func lockFile(path string) (func(), error) { return func() {}, nil }
