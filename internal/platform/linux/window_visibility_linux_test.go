//go:build !vivagtk

package linux

import (
	"slices"
	"testing"
	"unsafe"

	"github.com/vivaui/viva/internal/platform"
)

type visibilityTestRenderer struct {
	windowRenderer
	fullPresents int
}

func (r *visibilityTestRenderer) presentFull() error {
	r.fullPresents++
	return nil
}

func TestHideRejectsLateMapButShowRestoresVisibility(t *testing.T) {
	previous := activeWindow.Load()
	t.Cleanup(func() { activeWindow.Store(previous) })
	var maps, unmaps int
	var visibility []bool
	renderer := &visibilityTestRenderer{}
	state := linuxWindowState{
		application: &linuxApplication{},
		win: &window{id: 7, api: &xcbAPI{
			mapWindow: func(uintptr, uint32) xcbVoidCookie {
				maps++
				return xcbVoidCookie{}
			},
			unmapWindow: func(uintptr, uint32) xcbVoidCookie {
				unmaps++
				return xcbVoidCookie{}
			},
			flush: func(uintptr) int32 { return 1 },
		}},
		config: platform.Config{
			WindowMode:   platform.WindowPanel,
			OnVisibility: func(visible bool) { visibility = append(visibility, visible) },
		},
		renderer: renderer,
		visible:  true,
		exposed:  true,
	}
	handle := func(event unsafe.Pointer) {
		t.Helper()
		if quit, err := state.handleEvent(event); err != nil || quit {
			t.Fatalf("handle event: quit=%v err=%v", quit, err)
		}
	}
	command := func(kind uint32) {
		t.Helper()
		if err := state.command(kind, nil); err != nil {
			t.Fatal(err)
		}
	}
	mapEvent := xcbMapEvent{ResponseType: xcbMapNotify, Window: state.win.id}
	command(commandHide)
	handle(unsafe.Pointer(&mapEvent))
	if unmaps != 2 || !slices.Equal(visibility, []bool{false}) {
		t.Fatalf("late MapNotify: unmaps=%d visibility=%v, want two unmaps and [false]", unmaps, visibility)
	}
	command(commandShow)
	handle(unsafe.Pointer(&mapEvent))
	if maps != 1 || unmaps != 2 || !slices.Equal(visibility, []bool{false, true}) {
		t.Fatalf("Show MapNotify: maps=%d unmaps=%d visibility=%v, want 1, 2, [false true]", maps, unmaps, visibility)
	}
	expose := xcbExposeEvent{ResponseType: xcbExpose, Window: state.win.id}
	handle(unsafe.Pointer(&expose))
	if renderer.fullPresents != 1 {
		t.Fatalf("first expose after Show: full presents=%d, want 1", renderer.fullPresents)
	}
	command(commandHide)
	handle(unsafe.Pointer(&expose))
	if renderer.fullPresents != 1 || !state.hidden {
		t.Fatalf("queued expose after Hide: full presents=%d hidden=%v, want 1 and true", renderer.fullPresents, state.hidden)
	}
}
