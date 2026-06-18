//go:build desktop

package main

import (
	"runtime"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// alwaysOnTopDropDelay is how long the window stays pinned keep-above after a raise on Linux
// before the pin is dropped. Long enough for GTK's async map+restack to land (dropping it on
// the next UI tick races the map and leaves the window behind — the bug this avoids).
const alwaysOnTopDropDelay = 700 * time.Millisecond

// raiseWindow shows w and brings it above other windows with focus. MUST be called on the Wails
// UI thread (callers route it through application.InvokeAsync — off-thread Wails window calls
// deadlock GTK).
//
// On Linux/GTK, Focus() maps to gtk_window_present, which the window manager's focus-stealing
// prevention can deny — it blinks the taskbar instead of raising. We pin SetAlwaysOnTop(true)
// so the WM restacks the window above others when it maps regardless of focus rules, then drop
// the pin after a short delay (so normal stacking resumes; the window stays raised+focused, just
// no longer pinned). See ADR-0013.
func raiseWindow(w application.Window) {
	w.Show()
	if runtime.GOOS == "linux" {
		w.SetAlwaysOnTop(true)
		w.Focus()
		time.AfterFunc(alwaysOnTopDropDelay, func() {
			application.InvokeAsync(func() { w.SetAlwaysOnTop(false) })
		})
		return
	}
	w.Focus()
}
