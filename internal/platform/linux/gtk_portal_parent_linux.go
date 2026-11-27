//go:build linux && vivagtk

package linux

import (
	"sync"

	"github.com/ebitengine/purego"
)

type gtkPortalExport struct {
	serial uintptr
	window uintptr
	handle string
}

var (
	gtkPortalExportOnce     sync.Once
	gtkPortalExportCallback uintptr
)

func (a *gtkApplication) syncChooserDisplay() {
	var syncDisplay func(uintptr)
	if display := a.g.gdkDisplayDefault(); display != 0 && a.g.lookup(&syncDisplay, "gdk_display_sync") {
		syncDisplay(display)
	}
}

func (a *gtkApplication) exportPortalParent(owner *gtkWindow) (string, func(), bool) {
	if owner.closed || owner.destroying || a.portalExport.window != 0 {
		return "", nil, false
	}
	g := a.g
	var mapped, destroyed func(uintptr) int32
	var hasGlobal func(uintptr, *byte) int32
	var export func(uintptr, uintptr, uintptr, uintptr) int32
	var unexport, syncDisplay func(uintptr)
	if !g.lookup(&mapped, "gtk_widget_get_mapped") || mapped(owner.toplevel) == 0 ||
		!g.lookup(&hasGlobal, "gdk_wayland_display_query_registry") ||
		!g.lookup(&export, "gdk_wayland_window_export_handle") ||
		!g.lookup(&unexport, "gdk_wayland_window_unexport_handle") ||
		!g.lookup(&destroyed, "gdk_window_is_destroyed") ||
		!g.lookup(&syncDisplay, "gdk_display_sync") {
		return "", nil, false
	}
	display, window := g.gdkDisplayDefault(), g.widgetGetWindow(owner.toplevel)
	if window == 0 || display == 0 || hasGlobal(display, &cString("zxdg_exporter_v1")[0]) == 0 {
		return "", nil, false
	}
	gtkPortalExportOnce.Do(func() {
		gtkPortalExportCallback = purego.NewCallback(func(window, handle, serial uintptr) uintptr {
			if app := gtkRunning.Load(); app != nil && app.portalExport.serial == serial && app.portalExport.window == window {
				app.portalExport.handle = goString(handle)
			}
			return 0
		})
	})
	a.portalExport.serial++
	a.portalExport.window = window
	if export(window, gtkPortalExportCallback, a.portalExport.serial, 0) == 0 {
		a.portalExport.window = 0
		return "", nil, false
	}
	g.objectRef(window)
	cleanup := func() {
		if destroyed(window) == 0 {
			unexport(window)
		}
		g.objectUnref(window)
	}
	// A newly exported handle is delivered by the compositor in this round trip.
	// Do not enter a nested GTK loop or keep a handle from a later callback.
	syncDisplay(display)
	handle := a.portalExport.handle
	a.portalExport.window, a.portalExport.handle = 0, ""
	if handle == "" || owner.closed || owner.destroying {
		cleanup()
		return "", nil, true
	}
	return "wayland:" + handle, cleanup, true
}
