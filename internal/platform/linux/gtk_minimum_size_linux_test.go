//go:build linux && vivagtk

package linux

import (
	"slices"
	"testing"

	"github.com/vivaui/viva/internal/platform"
)

func TestGTKMinimumSizeFollowsCompositorGeometry(t *testing.T) {
	for _, tc := range []struct {
		name    string
		wayland bool
		want    []uint32
	}{
		{"Wayland releases until back to normal", true, []uint32{gdkHintMinSize, 0, gdkHintMinSize}},
		{"X11 preserves minimum", false, []uint32{gdkHintMinSize}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var masks []uint32
			g := &gtkLibrary{windowSetGeometry: func(_, _ uintptr, geometry *gdkGeometry, mask uint32) {
				masks = append(masks, mask)
				if mask == gdkHintMinSize && (geometry.MinWidth != 640 || geometry.MinHeight != 400) {
					t.Errorf("minimum geometry = %dx%d, want 640x400", geometry.MinWidth, geometry.MinHeight)
				}
			}}
			w := &gtkWindow{app: &gtkApplication{wayland: tc.wayland}, g: g, ratio: 1,
				config: platform.Config{MinWidth: 640, MinHeight: 400}}
			w.setMinimumSize()
			w.stateChanged(&gdkEventWindowState{Changed: gdkWindowStateMaximized, New: gdkWindowStateMaximized})
			w.stateChanged(&gdkEventWindowState{Changed: gdkWindowStateMaximized | gdkWindowStateFullscreen, New: gdkWindowStateFullscreen})
			w.stateChanged(&gdkEventWindowState{Changed: gdkWindowStateFullscreen, New: 0})
			if !slices.Equal(masks, tc.want) {
				t.Fatalf("geometry hints = %v, want %v", masks, tc.want)
			}
		})
	}
}
