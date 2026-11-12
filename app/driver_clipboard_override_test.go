//go:build darwin || windows || linux

package app

import (
	"strings"
	"testing"

	"github.com/vivaui/viva/components"
	"github.com/vivaui/viva/gfx"
	"github.com/vivaui/viva/text"
)

func TestDriverTextInputCanOverrideCopyAndCutText(t *testing.T) {
	editor := text.NewEditor("abc\ndef")
	copyText := "bc\n  "
	copyCalls := 0
	cutEvents := 0
	config := Config{Title: "clipboard", Width: 300, Height: 120}
	config.Paint = func(canvas *gfx.Canvas, size gfx.Size) {
		components.PaintInput(canvas, gfx.LightTheme(), gfx.Rect{X: 10, Y: 10, Width: 220, Height: 30}, components.InputSnapshot{
			ID: gfx.Item(1), Label: "Editor", Value: editor.String(), PlatformEditing: canvas.Focused() == gfx.Item(1),
		})
	}
	config.TextInput = func(TextInputContext) *TextInput {
		return &TextInput{State: TextStateOf(editor), CopyOverride: true, CopyText: func() string {
			copyCalls++
			return copyText
		}, OnEvent: func(event TextEvent) (TextState, error) {
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
	if copyCalls != 0 {
		t.Fatalf("paint prepared copy text %d times", copyCalls)
	}
	driver.SetClipboard("sentinel")
	if handled, err := driver.ShortcutChord(gfx.ModifierControl, 'c'); !handled || err != nil || driver.Clipboard() != copyText || copyCalls != 1 {
		t.Fatalf("custom copy handled=%v err=%v clipboard=%q", handled, err, driver.Clipboard())
	}
	if handled, err := driver.ShortcutChord(gfx.ModifierControl, 'x'); !handled || err != nil || cutEvents != 1 || driver.Clipboard() != copyText || copyCalls != 2 {
		t.Fatalf("custom cut handled=%v err=%v events=%d clipboard=%q", handled, err, cutEvents, driver.Clipboard())
	}
	copyText = ""
	if handled, err := driver.ShortcutChord(gfx.ModifierControl, 'c'); !handled || err != nil || driver.Clipboard() != "bc\n  " {
		t.Fatalf("empty custom selection overwrote clipboard: %q, %v", driver.Clipboard(), err)
	}
}

func TestDriverCopyOverrideIsLazyForNonCopyShortcuts(t *testing.T) {
	for _, secure := range []bool{false, true} {
		name := "ordinary"
		if secure {
			name = "secure"
		}
		t.Run(name, func(t *testing.T) {
			editor := text.NewEditor("original")
			calls := 0
			config := Config{Title: "lazy clipboard", Width: 300, Height: 120}
			config.Paint = func(canvas *gfx.Canvas, _ gfx.Size) {
				canvas.HitWithOptions(gfx.Item(1), gfx.Rect{X: 10, Y: 10, Width: 220, Height: 30}, "editor", gfx.HitOptions{
					Role: gfx.RoleTextField, Secure: secure,
				})
			}
			config.TextInput = func(TextInputContext) *TextInput {
				return &TextInput{Editor: editor, CopyOverride: true, CopyText: func() string {
					calls++
					return "custom selection"
				}}
			}
			driver, err := NewDriver(config, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer driver.Close()
			if err := driver.Click(20, 20); err != nil {
				t.Fatal(err)
			}
			if err := driver.Resize(301, 120); err != nil {
				t.Fatal(err)
			}
			driver.SetClipboard("pasted")
			for _, shortcut := range []rune{'a', 'v'} {
				if handled, err := driver.Shortcut(shortcut); !handled || err != nil {
					t.Fatalf("shortcut %c handled=%v err=%v", shortcut, handled, err)
				}
				if calls != 0 {
					t.Fatalf("shortcut %c materialized copy text %d times", shortcut, calls)
				}
			}
			if editor.String() != "pasted" {
				t.Fatalf("paste used override text: %q", editor.String())
			}
			for index, shortcut := range []rune{'c', 'x'} {
				if handled, err := driver.Shortcut(shortcut); !handled || err != nil {
					t.Fatalf("shortcut %c handled=%v err=%v", shortcut, handled, err)
				}
				want := index + 1
				if secure {
					want = 0
				}
				if calls != want {
					t.Errorf("shortcut %c called override %d times, want %d", shortcut, calls, want)
				}
			}
		})
	}
}

func BenchmarkDriverCopyOverrideLargeSelectionRepaint(b *testing.B) {
	editor := text.NewEditor("value")
	calls := 0
	config := Config{Title: "large copy", Width: 300, Height: 120}
	config.Paint = func(canvas *gfx.Canvas, _ gfx.Size) {
		canvas.HitWithOptions(gfx.Item(1), gfx.Rect{X: 10, Y: 10, Width: 220, Height: 30}, "editor", gfx.HitOptions{Role: gfx.RoleTextField})
	}
	config.TextInput = func(TextInputContext) *TextInput {
		return &TextInput{Editor: editor, CopyOverride: true, CopyText: func() string {
			calls++
			return strings.Repeat("wide selection\n", 1<<16)
		}}
	}
	driver, err := NewDriver(config, nil)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(driver.Close)
	if err := driver.Click(20, 20); err != nil {
		b.Fatal(err)
	}
	if !driver.painter.nativeText.CopyOverride {
		b.Fatal("copy override is not active")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if err := driver.Resize(300+index%2, 120); err != nil {
			b.Fatal(err)
		}
	}
	if calls != 0 {
		b.Fatalf("large copy text materialized %d times without a copy action", calls)
	}
}

func BenchmarkDriverCopyOverrideLargeSelectionCopy(b *testing.B) {
	editor := text.NewEditor("value")
	config := Config{Title: "large copy", Width: 300, Height: 120}
	config.Paint = func(canvas *gfx.Canvas, _ gfx.Size) {
		canvas.HitWithOptions(gfx.Item(1), gfx.Rect{X: 10, Y: 10, Width: 220, Height: 30}, "editor", gfx.HitOptions{Role: gfx.RoleTextField})
	}
	config.TextInput = func(TextInputContext) *TextInput {
		return &TextInput{Editor: editor, CopyOverride: true, CopyText: func() string {
			return strings.Repeat("wide selection\n", 1<<16)
		}}
	}
	driver, err := NewDriver(config, nil)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(driver.Close)
	if err := driver.Click(20, 20); err != nil {
		b.Fatal(err)
	}
	if !driver.painter.nativeText.CopyOverride {
		b.Fatal("copy override is not active")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if handled, err := driver.ShortcutChord(gfx.ModifierControl, 'c'); err != nil || !handled {
			b.Fatalf("copy handled=%v err=%v", handled, err)
		}
	}
}

// TestDriverRectangularClipboardCopyAndPaste checks that an application-owned
// rectangular selection travels through the driver's private clipboard: Copy
// records the shape, Paste reports it back on the event, and ordinary text (or
// a file list) clears the marker.
func TestDriverRectangularClipboardCopyAndPaste(t *testing.T) {
	editor := text.NewEditor("abc\ndef")
	copyText := "bc\n  "
	rectangularPaste := false
	config := Config{Title: "rectangular", Width: 300, Height: 120}
	config.Paint = func(canvas *gfx.Canvas, _ gfx.Size) {
		components.PaintInput(canvas, gfx.LightTheme(), gfx.Rect{X: 10, Y: 10, Width: 220, Height: 30}, components.InputSnapshot{
			ID: gfx.Item(1), Label: "Editor", Value: editor.String(), PlatformEditing: canvas.Focused() == gfx.Item(1),
		})
	}
	config.TextInput = func(TextInputContext) *TextInput {
		return &TextInput{State: TextStateOf(editor), CopyOverride: true, CopyText: func() string {
			return copyText
		}, CopyRectangular: true, OnEvent: func(event TextEvent) (TextState, error) {
			if event.Kind == TextPaste && event.Text != "" {
				rectangularPaste = event.Rectangular
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
	if handled, err := driver.ShortcutChord(gfx.ModifierControl, 'c'); !handled || err != nil || driver.Clipboard() != copyText || !ClipboardRectangular() {
		t.Fatalf("custom copy handled=%v err=%v clipboard=%q rectangular=%v", handled, err, driver.Clipboard(), ClipboardRectangular())
	}
	if handled, err := driver.ShortcutChord(gfx.ModifierControl, 'v'); !handled || err != nil || !rectangularPaste {
		t.Fatalf("rectangular paste handled=%v err=%v rectangular=%v", handled, err, rectangularPaste)
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
