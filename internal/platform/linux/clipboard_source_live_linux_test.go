package linux

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/vivaui/viva/internal/platform"
)

func TestClipboardHTMLSourceReleasesTheSharedReadConnection(t *testing.T) {
	liveSelectionWindow(t)
	const html = "<b>external</b>"
	command := exec.Command("xclip", "-i", "-selection", "clipboard", "-t", "text/html")
	command.Stdin = strings.NewReader(html)
	if err := command.Run(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := ClipboardSource(platform.ClipboardHTMLSource, len(html)-1); !errors.Is(err, platform.ErrClipboardSourceTooLarge) {
		t.Fatalf("read with a short limit: %v", err)
	}
	got, err := ClipboardSource(platform.ClipboardHTMLSource, len(html))
	if err != nil || string(got) != html {
		t.Fatalf("HTML read returned %q, %v", got, err)
	}
	xclipIn(t, "clipboard", []byte("plain text"))
	text, err := ClipboardText()
	if err != nil || text != "plain text" {
		t.Fatalf("plain read after HTML returned %q, %v", text, err)
	}
}
