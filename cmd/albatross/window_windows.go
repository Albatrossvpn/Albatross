//go:build windows

package main

import (
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unsafe"

	webview2 "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
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
	badge.mu.Lock()
	badge.w = w
	tier := badge.tier
	badge.mu.Unlock()
	defer func() {
		badge.mu.Lock()
		badge.w = nil
		badge.mu.Unlock()
	}()
	if tier != "" {
		// Runs in the window's loop, once the window is up.
		w.Dispatch(func() { applyBadge(w, tier) })
	}
	go func() {
		<-done
		w.Dispatch(w.Terminate)
	}()
	// Explorer makes the taskbar button a while after the window shows,
	// and an overlay set before it is lost. Windows says when the button
	// is there (TaskbarButtonCreated), but not to an elevated window, so
	// the badge is put on again a little later.
	go func() {
		for _, d := range []time.Duration{time.Second, 5 * time.Second} {
			select {
			case <-time.After(d):
			case <-done:
				return
			}
			badge.mu.Lock()
			open, tier := badge.w == w, badge.tier
			badge.mu.Unlock()
			if !open {
				return
			}
			if tier != "" {
				w.Dispatch(func() { applyBadge(w, tier) })
			}
		}
	}()
	w.Navigate(url)
	w.Run()
	return true
}

// The plan's badge on the window: the plan in the title, and on the
// taskbar button an overlay icon (ITaskbarList3) from the resources
// (build/windows/winres.json, icons 2 to 5).
var badge struct {
	mu   sync.Mutex
	w    webview2.WebView // nil while there is no window
	tier string
}

var planNames = map[string]string{"bronze": "Bronze", "silver": "Silver", "gold": "Gold", "vip": "VIP"}
var overlayIcons = map[string]uintptr{"bronze": 2, "silver": 3, "gold": 4, "vip": 5}

// setBadge is engine.Options.Badge. It may be called from any goroutine,
// before the window exists or after it closed: the plan is kept for the
// window.
func setBadge(tier string) {
	badge.mu.Lock()
	badge.tier = tier
	w := badge.w
	badge.mu.Unlock()
	if w != nil {
		w.Dispatch(func() { applyBadge(w, tier) })
	}
}

// applyBadge runs on the window's thread.
func applyBadge(w webview2.WebView, tier string) {
	title := windowTitle
	if name := planNames[tier]; name != "" {
		title += " · " + name
	}
	w.SetTitle(title)
	overlay(windows.HWND(w.Window()), tier)
}

var (
	modUser32            = windows.NewLazySystemDLL("user32.dll")
	modOle32             = windows.NewLazySystemDLL("ole32.dll")
	procLoadImageW       = modUser32.NewProc("LoadImageW")
	procGetSystemMetrics = modUser32.NewProc("GetSystemMetrics")
	procGetModuleHandleW = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetModuleHandleW")
	procCoCreateInstance = modOle32.NewProc("CoCreateInstance")

	clsidTaskbarList = windows.GUID{Data1: 0x56FDF344, Data2: 0xFD6D, Data3: 0x11D0, Data4: [8]byte{0x95, 0x8A, 0x00, 0x60, 0x97, 0xC9, 0xA0, 0x90}}
	iidTaskbarList3  = windows.GUID{Data1: 0xEA1AFB91, Data2: 0x9E28, Data3: 0x4B86, Data4: [8]byte{0x90, 0xE9, 0x9E, 0x9F, 0x8A, 0x5E, 0xEF, 0xAF}}

	// taskbar is the ITaskbarList3 object, made on first use on the
	// window's thread.
	taskbar *comObject
)

// comObject is a COM interface pointer: its first word is the vtable.
type comObject struct {
	vtbl *[21]uintptr
}

// ITaskbarList3's methods, in vtable order.
const (
	tbRelease        = 2
	tbHrInit         = 3
	tbSetOverlayIcon = 18
)

func (o *comObject) call(method int, args ...uintptr) int32 {
	hr, _, _ := syscall.SyscallN(o.vtbl[method], append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)...)
	return int32(hr)
}

// taskbarList returns the taskbar object, or nil while there is none: a
// failure (no taskbar yet, as at login) is tried again next time.
func taskbarList() *comObject {
	if taskbar != nil {
		return taskbar
	}
	// The window's thread has COM up for WebView2; this is a no-op then.
	windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED)
	var obj *comObject
	hr, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidTaskbarList)), 0, windows.CLSCTX_INPROC_SERVER,
		uintptr(unsafe.Pointer(&iidTaskbarList3)), uintptr(unsafe.Pointer(&obj)))
	if int32(hr) < 0 || obj == nil {
		return nil
	}
	if obj.call(tbHrInit) < 0 {
		obj.call(tbRelease)
		return nil
	}
	taskbar = obj
	return obj
}

// overlay puts the plan's icon on the window's taskbar button, or takes
// it off for a plan without one.
func overlay(hwnd windows.HWND, tier string) {
	tb := taskbarList()
	if tb == nil {
		return
	}
	var icon uintptr
	var desc *uint16
	if id := overlayIcons[tier]; id != 0 {
		const imageIcon, lrShared = 1, 0x8000
		const smCxSmIcon = 49
		size, _, _ := procGetSystemMetrics.Call(smCxSmIcon)
		exe, _, _ := procGetModuleHandleW.Call(0)
		icon, _, _ = procLoadImageW.Call(exe, id, imageIcon, size, size, lrShared)
		desc, _ = windows.UTF16PtrFromString(planNames[tier] + " plan")
	}
	tb.call(tbSetOverlayIcon, uintptr(hwnd), icon, uintptr(unsafe.Pointer(desc)))
}
