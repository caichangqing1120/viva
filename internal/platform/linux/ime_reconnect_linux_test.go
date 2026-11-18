package linux

import (
	"testing"

	"github.com/vivaui/viva/internal/platform"
	"github.com/vivaui/viva/text"
)

func TestIMEReconnectRejectsChangedFocusOrEditor(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*linuxIME)
	}{
		{"refocused while connecting", func(ime *linuxIME) { ime.setFocused(false); ime.setFocused(true) }},
		{"changed to another editor", func(ime *linuxIME) {
			editor := text.NewEditor("")
			editor.EnableTentativeComposition()
			ime.text.state = platform.EditorState(editor)
		}},
		{"window closed", func(ime *linuxIME) { ime.close() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newIMEHarness(t)
			h.editor.EnableTentativeComposition()
			h.input.state = platform.EditorState(h.editor)
			h.ime.bindOwner()
			h.focus(t)
			h.ime.lost()
			attempt := &imeReconnect{ime: h.ime, span: h.ime.focusSpan, epoch: h.ime.epoch,
				target: h.input.input.Target, owner: h.input.state.InputOwner, identity: h.input.state.CompositionIdentity,
				tentative: h.input.state.TentativeComposition}
			if !attempt.matches(h.ime) {
				t.Fatal("current focus and editor must accept a replacement")
			}
			tc.change(h.ime)
			if attempt.matches(h.ime) {
				t.Fatal("replacement matched a stale input target")
			}
		})
	}
}

func TestIMEReconnectDoesNotDispatchUntilAdopted(t *testing.T) {
	h := newIMEHarness(t)
	h.focus(t)
	adopted := false
	handler := h.ime.handler(h.ime.epoch, func() bool { return adopted })
	handler.Commit("stale")
	if got := h.editor.String(); got != "" {
		t.Fatalf("unadopted connection changed the editor: %q", got)
	}
	adopted = true
	handler.Commit("live")
	if got := h.editor.String(); got != "live" {
		t.Fatalf("adopted connection did not deliver text: %q", got)
	}
}
