//go:build darwin || windows || linux

package app

import (
	"testing"

	"github.com/vivaui/viva/components"
	"github.com/vivaui/viva/text"
	"github.com/vivaui/viva/ui"
)

func TestDriverTextInputCanOverrideCopyAndCutText(t *testing.T) {
	editor := text.NewEditor("abc\ndef")
	copyText := "bc\n  "
	cutEvents := 0
	markedPaste := false
	config := Config{Title: "clipboard", Width: 300, Height: 120}
	config.Paint = func(canvas *ui.Canvas, size ui.Size) {
		components.PaintInput(canvas, ui.LightTheme(), ui.Rect{X: 10, Y: 10, Width: 220, Height: 30}, components.InputSnapshot{
			ID: ui.Item(1), Label: "Editor", Value: editor.String(), PlatformEditing: canvas.Focused() == ui.Item(1),
		})
	}
	config.TextInput = func(TextInputContext) *TextInput {
		return &TextInput{State: TextStateOf(editor), CopyOverride: true, CopyText: copyText, CopyRectangular: true, OnEvent: func(event TextEvent) (TextState, error) {
			if event.Kind == TextPaste && event.Text == "" {
				cutEvents++
			}
			if event.Kind == TextPaste && event.Text != "" {
				markedPaste = event.Rectangular
			}
			return TextStateOf(editor), nil
		}}
	}
	driver, err := NewDriver(config, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer driver.Close()
	if err := driver.Click(50, 25); err != nil {
		t.Fatal(err)
	}
	if !driver.painter.nativeText.CopyRectangular {
		t.Fatal("rectangular copy intent did not reach the platform input")
	}
	driver.SetClipboard("sentinel")
	if handled, err := driver.ShortcutChord(ui.ModifierControl, 'c'); !handled || err != nil || driver.Clipboard() != copyText || !ClipboardRectangular() {
		t.Fatalf("custom copy handled=%v err=%v clipboard=%q", handled, err, driver.Clipboard())
	}
	if handled, err := driver.ShortcutChord(ui.ModifierControl, 'x'); !handled || err != nil || cutEvents != 1 || driver.Clipboard() != copyText {
		t.Fatalf("custom cut handled=%v err=%v events=%d clipboard=%q", handled, err, cutEvents, driver.Clipboard())
	}
	if handled, err := driver.ShortcutChord(ui.ModifierControl, 'v'); !handled || err != nil || !markedPaste {
		t.Fatalf("rectangular paste handled=%v err=%v marked=%v", handled, err, markedPaste)
	}
	copyText = ""
	if handled, err := driver.ShortcutChord(ui.ModifierControl, 'c'); !handled || err != nil || driver.Clipboard() != "bc\n  " {
		t.Fatalf("empty custom selection overwrote clipboard: %q, %v", driver.Clipboard(), err)
	}
	if err := SetClipboardText("plain"); err != nil || ClipboardRectangular() {
		t.Fatalf("ordinary text kept rectangle marker: %v", err)
	}
}

func TestDriverRectangularClipboardSetAndPlainReset(t *testing.T) {
	driver, err := NewDriver(Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer driver.Close()
	if err := SetRectangularClipboardText("first\nsecond"); err != nil || driver.Clipboard() != "first\nsecond" || !ClipboardRectangular() {
		t.Fatalf("rectangular clipboard = %q, %v, marker=%v", driver.Clipboard(), err, ClipboardRectangular())
	}
	if err := SetClipboardText("plain"); err != nil || ClipboardRectangular() || driver.Clipboard() != "plain" {
		t.Fatalf("plain clipboard = %q, %v, marker=%v", driver.Clipboard(), err, ClipboardRectangular())
	}
	if err := SetRectangularClipboardText("column"); err != nil {
		t.Fatal(err)
	}
	driver.SetClipboardFiles([]string{"/tmp/document.txt"})
	if ClipboardRectangular() {
		t.Fatal("copied files retained rectangular text marker")
	}
}
