package windows

import (
	"os"
	"testing"
)

func TestRectangularClipboardFormatsAndPlainReplacement(t *testing.T) {
	if os.Getenv("VIVA_WINDOWS_CLIPBOARD") == "" {
		t.Skip("set VIVA_WINDOWS_CLIPBOARD=1 to overwrite the real clipboard")
	}
	if err := SetClipboardRectangularText("first\r\nsecond"); err != nil {
		t.Fatal(err)
	}
	if value, err := ClipboardText(); err != nil || value != "first\r\nsecond" || !ClipboardRectangular() {
		t.Fatalf("rectangular clipboard = %q, %v, marker=%v", value, err, ClipboardRectangular())
	}
	_, borland, err := rectangularClipboardFormats()
	if err != nil {
		t.Fatal(err)
	}
	if available, _, _ := clipboardFormatAvailable.Call(borland); available == 0 {
		t.Fatal("Borland IDE Block Type marker is missing")
	}
	if err := SetClipboardText("plain"); err != nil {
		t.Fatal(err)
	}
	if ClipboardRectangular() {
		t.Fatal("plain clipboard retained rectangular marker")
	}
	if available, _, _ := clipboardFormatAvailable.Call(borland); available != 0 {
		t.Fatal("plain clipboard retained Borland marker")
	}
}
