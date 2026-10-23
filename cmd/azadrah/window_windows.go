//go:build windows

package main

import (
	"path/filepath"

	webview2 "github.com/jchv/go-webview2"
)

// openWindow shows the UI in a native WebView2 window. It returns false
// when WebView2 is unavailable, otherwise it blocks until the window is
// closed or done is closed.
func openWindow(title, url, dataDir string, done <-chan struct{}) bool {
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		AutoFocus: true,
		DataPath:  filepath.Join(dataDir, "webview"),
		WindowOptions: webview2.WindowOptions{
			Title:  title,
			Width:  460,
			Height: 820,
			IconId: 1,
			Center: true,
		},
	})
	if w == nil {
		return false
	}
	defer w.Destroy()
	w.SetSize(400, 640, webview2.HintMin)
	go func() {
		<-done
		w.Dispatch(w.Terminate)
	}()
	w.Navigate(url)
	w.Run()
	return true
}
