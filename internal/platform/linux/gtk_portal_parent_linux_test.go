//go:build linux && vivagtk

package linux

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/vivaui/viva/internal/platform"
)

func TestGTKNativePortalParentsArePerWindow(t *testing.T) {
	if os.Getenv("VIVA_GTK_PORTAL_PARENT_NATIVE") != "1" {
		t.Skip("requires a private Wayland compositor with xdg-foreign v1")
	}
	err := RunGTKApplication(func(application platform.Application) error {
		a := application.(*gtkApplication)
		var owners [2]*gtkWindow
		for i := range owners {
			w, err := a.OpenWindow(platform.Config{Title: fmt.Sprintf("Portal owner %d", i+1), Width: 360, Height: 240})
			if err != nil {
				return err
			}
			owners[i] = w.(*gtkWindow)
		}
		a.enqueue(func() {
			defer a.destroyAll()
			var handles [2]string
			for i, owner := range owners {
				a.active = owner
				handle, release, err := a.preparePortalChooser()
				if release != nil {
					defer release()
				}
				if err != nil || release == nil || !strings.HasPrefix(handle, "wayland:") || len(handle) == len("wayland:") {
					a.fail(fmt.Errorf("no valid Wayland parent for window %d: %v", i+1, err))
					return
				}
				handles[i] = handle
			}
			if handles[0] == handles[1] {
				a.fail(fmt.Errorf("two windows shared a portal parent handle"))
				return
			}
			owners[0].destroy()
			a.active = owners[0]
			handle, release, err := a.preparePortalChooser()
			if release != nil {
				release()
			}
			if err != nil || handle != "" || release != nil {
				a.fail(fmt.Errorf("a closed window was used as the portal parent"))
			}
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
