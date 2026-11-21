package linux

import (
	"sync/atomic"

	"github.com/vivaui/viva/gfx"
	"github.com/vivaui/viva/internal/platform"
)

// shellHooks is how a window shell that owns the display connection itself (the GTK shell) takes
// desktop services that need its display connection or window ownership. Nil
// means to use the XCB backend and external dialog helpers.
var shellHooks atomic.Pointer[desktopHooks]

type desktopHooks struct {
	clipboardText        func() (string, error)
	setClipboardText     func(string) error
	setClipboardHTML     func(html, alt string) error
	clipboardSource      func(platform.ClipboardSourceFormat, int) ([]byte, error)
	hasClipboardImage    func() bool
	clipboardImagePNG    func() ([]byte, error)
	setClipboardImage    func([]byte) error
	clipboardFiles       func() ([]string, error)
	setClipboardFiles    func([]string) error
	primaryText          func() (string, error)
	setPrimaryText       func(string) error
	setCursorShape       func(uint8) error
	setCursorImage       func(*gfx.CursorImage) error
	saveFileDialog       func(title, name string, filters []portalFilter) (string, bool, error)
	preparePortalChooser func() (parent string, cleanup func(), err error)
}
