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
	config := Config{Title: "clipboard", Width: 300, Height: 120}
	config.Paint = func(canvas *ui.Canvas, size ui.Size) {
		components.PaintInput(canvas, ui.LightTheme(), ui.Rect{X: 10, Y: 10, Width: 220, Height: 30}, components.InputSnapshot{
			ID: ui.Item(1), Label: "Editor", Value: editor.String(), PlatformEditing: canvas.Focused() == ui.Item(1),
		})
	}
	config.TextInput = func(TextInputContext) *TextInput {
		return &TextInput{State: TextStateOf(editor), CopyOverride: true, CopyText: copyText, OnEvent: func(event TextEvent) (TextState, error) {
			if event.Kind == TextPaste && event.Text == "" {
				cutEvents++
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
	driver.SetClipboard("sentinel")
	if handled, err := driver.ShortcutChord(ui.ModifierControl, 'c'); !handled || err != nil || driver.Clipboard() != copyText {
		t.Fatalf("custom copy handled=%v err=%v clipboard=%q", handled, err, driver.Clipboard())
	}
	if handled, err := driver.ShortcutChord(ui.ModifierControl, 'x'); !handled || err != nil || cutEvents != 1 || driver.Clipboard() != copyText {
		t.Fatalf("custom cut handled=%v err=%v events=%d clipboard=%q", handled, err, cutEvents, driver.Clipboard())
	}
	copyText = ""
	if handled, err := driver.ShortcutChord(ui.ModifierControl, 'c'); !handled || err != nil || driver.Clipboard() != "bc\n  " {
		t.Fatalf("empty custom selection overwrote clipboard: %q, %v", driver.Clipboard(), err)
	}
}
