//go:build linux && vivagtk

package linux

import (
	"slices"
	"testing"

	"github.com/vivaui/viva/internal/platform"
)

func TestGTKMinimumSizeFitsWaylandOutput(t *testing.T) {
	for _, tc := range []struct {
		name              string
		wayland, realized bool
		output, want      [2]int32
	}{
		{"small startup output", true, false, [2]int32{640, 360}, [2]int32{640, 360}},
		{"small current output", true, true, [2]int32{600, 360}, [2]int32{600, 360}},
		{"normal output", true, true, [2]int32{1280, 800}, [2]int32{640, 400}},
		{"unknown dimensions", true, true, [2]int32{0, 0}, [2]int32{640, 400}},
		{"X11 remains unchanged", false, true, [2]int32{600, 360}, [2]int32{640, 400}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got [2]int32
			g := &gtkLibrary{
				gdkDisplayDefault: func() uintptr { return 1 },
				gdkDisplayPrimary: func(uintptr) uintptr { return 3 },
				widgetGetWindow: func(uintptr) uintptr {
					if tc.realized {
						return 2
					}
					return 0
				},
				gdkDisplayMonitorAt: func(_, _ uintptr) uintptr { return 4 },
				gdkMonitorGeometry: func(monitor uintptr, rect *[4]int32) {
					if tc.realized && monitor != 4 || !tc.realized && monitor != 3 {
						t.Errorf("monitor = %d, realized = %v", monitor, tc.realized)
					}
					*rect = [4]int32{20, 30, tc.output[0], tc.output[1]}
				},
				windowSetGeometry: func(_, _ uintptr, geometry *gdkGeometry, mask uint32) {
					if mask != gdkHintMinSize {
						t.Errorf("geometry mask = %v", mask)
					}
					got = [2]int32{geometry.MinWidth, geometry.MinHeight}
				},
			}
			window := &gtkWindow{g: g, app: &gtkApplication{wayland: tc.wayland}, ratio: 1,
				config: platform.Config{MinWidth: 640, MinHeight: 400}}
			window.setMinimumSize()
			if got != tc.want {
				t.Fatalf("minimum geometry = %v, want %v", got, tc.want)
			}
		})
	}
}

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
			g := &gtkLibrary{gdkDisplayDefault: func() uintptr { return 0 }, windowSetGeometry: func(_, _ uintptr, geometry *gdkGeometry, mask uint32) {
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
