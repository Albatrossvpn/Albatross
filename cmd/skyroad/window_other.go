//go:build !windows

package main

// openWindow has no native window outside Windows; the UI opens in the
// browser instead.
func openWindow(title, url, dataDir string, done <-chan struct{}) bool { return false }
