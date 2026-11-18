//go:build linux && vivagtk

package linux

import (
	"fmt"
	"os"
	"testing"

	"github.com/vivaui/viva/internal/platform"
)

func TestGTKNativeSaveDialogClosesWithParent(t *testing.T) {
	if os.Getenv("VIVA_GTK_SAVE_DIALOG_NATIVE") != "1" {
		t.Skip("requires a private Wayland compositor")
	}
	err := RunGTKApplication(func(application platform.Application) error {
		a := application.(*gtkApplication)
		window, err := application.OpenWindow(platform.Config{Title: "Save dialog lifetime", Width: 360, Height: 240})
		if err != nil {
			return err
		}
		if !a.wayland {
			return fmt.Errorf("the test requires a Wayland display")
		}
		parent := window.(*gtkWindow)
		a.active = parent
		a.enqueue(func() {
			a.enqueue(parent.destroy)
			path, canceled, err := a.saveFileDialog("Save As", "document.txt", nil)
			if err != nil || !canceled || path != "" || !parent.closed {
				a.fail(fmt.Errorf("closing the parent left save dialog result path=%q canceled=%v closed=%v: %v", path, canceled, parent.closed, err))
			}
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
