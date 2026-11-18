//go:build darwin || windows || linux

package app

import (
	"errors"
	"testing"

	"github.com/vivaui/viva/internal/platform"
)

type nativeWindowTestPlatform struct {
	platform.Application
	control *nativeWindowTestControl
}

func (native *nativeWindowTestPlatform) OpenWindow(config platform.Config) (platform.Window, error) {
	native.control.config = config
	return native.control, nil
}

type nativeWindowTestControl struct {
	fakeWindow
	config     platform.Config
	fullscreen bool
	title      string
	size       [2]int
}

func (control *nativeWindowTestControl) Close()           { control.config.OnClosed() }
func (control *nativeWindowTestControl) Fullscreen() bool { return control.fullscreen }
func (control *nativeWindowTestControl) SetFullscreen(value bool) error {
	control.fullscreen = value
	return nil
}
func (control *nativeWindowTestControl) SetTitle(title string) error {
	control.title = title
	return nil
}
func (control *nativeWindowTestControl) SetSize(width, height int) error {
	control.size = [2]int{width, height}
	return nil
}
func (*nativeWindowTestControl) SetAlwaysOnTop(bool) error {
	return platform.ErrAlwaysOnTopUnsupported
}

func TestNativeOpenWindowKeepsOptionalWindowControls(t *testing.T) {
	control := &nativeWindowTestControl{}
	application := &Application{host: nativeHost{native: &nativeWindowTestPlatform{control: control}}}
	window, err := application.OpenWindow(Config{Title: "native controls", Width: 320, Height: 240}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(window.Close)
	if err := window.SetFullscreen(true); err != nil || !control.fullscreen || window.Fullscreen() {
		t.Errorf("fullscreen request: error=%v native=%v observed=%v", err, control.fullscreen, window.Fullscreen())
	}
	control.config.OnFullscreen(true)
	if !window.Fullscreen() {
		t.Error("native transition did not publish fullscreen state")
	}
	if err := window.SetTitle("updated title"); err != nil || control.title != "updated title" {
		t.Errorf("title request: error=%v native=%q", err, control.title)
	}
	if err := window.SetSize(640, 400); err != nil || control.size != [2]int{640, 400} {
		t.Errorf("size request: error=%v native=%v", err, control.size)
	}
	if err := window.SetAlwaysOnTop(true); !errors.Is(err, ErrUnsupported) || window.AlwaysOnTop() {
		t.Errorf("unsupported level request: error=%v observed=%v", err, window.AlwaysOnTop())
	}
	if window.applyResult == nil || window.requestRedraw == nil || window.runShortcut == nil {
		t.Error("native control lost the painter's menu wiring")
	}
}
