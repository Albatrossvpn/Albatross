//go:build !windows

package main

// setBadge is engine.Options.Badge; only Windows marks its window.
func setBadge(string) {}

// openWindow has no native window outside Windows; the UI opens in the
// browser instead.
func openWindow(title, url, dataDir string, done <-chan struct{}) bool { return false }
