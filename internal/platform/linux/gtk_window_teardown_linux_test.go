//go:build linux && vivagtk

package linux

import "testing"

func TestGTKDestroyDetachesInputMethodBeforeNativeWidget(t *testing.T) {
	g := &gtkLibrary{}
	a := &gtkApplication{g: g}
	w := &gtkWindow{app: a, g: g, toplevel: 17}
	w.ime = &gtkIME{w: w, g: g, ctx: 23, active: true}
	detached, destroyed := false, 0
	g.objectRef = func(object uintptr) uintptr { return object }
	g.objectUnref = func(uintptr) {}
	g.imFocusOut = func(context uintptr) {
		if context != 23 || !w.destroying || w.ime != nil {
			t.Error("native focus-out did not keep the context and destruction guard")
		}
		w.destroy()
	}
	g.imSetClientWindow = func(context, window uintptr) {
		if context != 23 || window != 0 || destroyed != 0 {
			t.Errorf("detach context=%d window=%d after %d native destructions", context, window, destroyed)
		}
		detached = true
	}
	g.idleAddFull = func(int32, uintptr, uintptr, uintptr) uint32 { return 1 }
	g.widgetDestroy = func(window uintptr) {
		destroyed++
		if window != 17 || !detached || w.ime != nil {
			t.Error("native widget destruction preceded input-method detachment")
		}
	}
	w.destroy()
	w.destroy()
	if destroyed != 1 || !detached {
		t.Fatalf("native destruction count=%d detached=%t", destroyed, detached)
	}
}
