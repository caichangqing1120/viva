//go:build darwin || windows || linux

package app

import (
	"errors"
	"testing"

	"github.com/vivaui/viva/gfx"
	"github.com/vivaui/viva/internal/platform"
)

type nativeWindowTestHost struct {
	fakeHost
	control *nativeWindowTestControl
}

func (host *nativeWindowTestHost) OpenWindow(config Config, _ *gfx.Canvas, closed func()) (WindowControl, error) {
	host.control.config, host.control.closed = config, closed
	return resultWindow{WindowControl: host.control, painter: &painter{}}, nil
}

type nativeWindowTestControl struct {
	fakeWindow
	fullscreen bool
	title      string
	size       [2]int
}

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
	application := &Application{host: &nativeWindowTestHost{control: control}}
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
