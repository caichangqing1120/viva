//go:build linux && vivagtk

package linux

import (
	"fmt"
	"os"
	"testing"

	"github.com/vivaui/viva/internal/platform"
	"github.com/vivaui/viva/text"
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

func TestGTKNativeSaveDialogResumesEditingAfterPreedit(t *testing.T) {
	if os.Getenv("VIVA_GTK_SAVE_DIALOG_NATIVE") != "1" {
		t.Skip("requires a private Wayland compositor")
	}
	testGTKNativeTentativeProbe(t, 1, func(p *gtkTentativeProbe) {
		w, a := p.w, p.w.app
		p.tentative = true
		editor := text.NewEditor("abc")
		editor.EnableTentativeComposition()
		p.install(editor)
		p.emit("preedit-start", "")
		p.w.ime.apply(platform.TextEvent{Kind: platform.TextSetMarked, CompositionPhase: text.CompositionUpdate, Text: "ni"})
		if !editor.CompositionActive() {
			p.record("save with preedit", "preedit fixture was not active")
			return
		}
		context := p.keys.ref(w.ime.ctx)
		defer w.g.objectUnref(context)
		var failures []string
		a.active = w
		a.enqueue(func() {
			if w.ime.ctx != 0 || editor.String() != "abc" || editor.CompositionActive() {
				failures = append(failures, fmt.Sprintf("dialog still exposed old input: context=%#x text=%q", w.ime.ctx, editor.String()))
			}
			if a.dismissSaveDialog != nil {
				a.dismissSaveDialog()
			}
		})
		path, canceled, err := a.saveFileDialog("Save As", "document.txt", nil)
		if err != nil || !canceled || path != "" || w.ime.ctx == 0 || w.ime.ctx == context {
			failures = append(failures, fmt.Sprintf("dialog did not resume a fresh context: path=%q canceled=%t reused=%t err=%v", path, canceled, w.ime.ctx == context, err))
		}
		signal, stale := cString("commit"), cString("stale")
		p.keys.emit(context, &signal[0], &stale[0])
		if editor.String() != "abc" || editor.CanUndo() || editor.CompositionActive() {
			failures = append(failures, fmt.Sprintf("dialog changed source or history: text=%q", editor.String()))
		}
		p.record("save with preedit", failures...)
	})
}
