package linux

import (
	"testing"
	"time"

	"github.com/vivaui/viva/internal/platform"
	"github.com/vivaui/viva/text"
)

func TestIMEReconnectWakesOnlyForTheFocusedField(t *testing.T) {
	h := newIMEHarness(t)
	app, ime := h.ime.app, h.ime
	now := time.Unix(100, 0)
	app.now = func() time.Time { return now }
	h.focus(t)
	app.setInputMethodFocus(ime)
	if timeout := app.pollTimeout(); timeout != -1 {
		t.Fatalf("healthy IME woke the idle event loop: %d ms", timeout)
	}
	ime.lost()
	attempt := &imeReconnect{ime: ime, span: ime.focusSpan, epoch: ime.epoch,
		target: ime.text.input.Target, owner: ime.text.state.InputOwner}
	app.ibusReconnect = attempt
	if timeout := app.pollTimeout(); timeout != -1 {
		t.Fatalf("in-flight reconnect scheduled another poll: %d ms", timeout)
	}
	app.finishInputMethodReconnect(attempt)
	if timeout := app.pollTimeout(); timeout <= 0 || timeout > int32(time.Second.Milliseconds()) {
		t.Fatalf("failed reconnect did not schedule a bounded retry: %d ms", timeout)
	}
	for range 20 {
		app.delayInputMethodRetry()
	}
	if timeout := app.pollTimeout(); timeout <= 0 || timeout > int32(imeRetryMax.Milliseconds())+1 {
		t.Fatalf("repeated failures did not cap the retry interval: %d ms", timeout)
	}
	ime.text.enabled = false
	if timeout := app.pollTimeout(); timeout != -1 {
		t.Fatalf("disabled field kept the retry alive: %d ms", timeout)
	}
	ime.text.enabled = true
	ime.setFocused(false)
	if timeout := app.pollTimeout(); timeout != -1 {
		t.Fatalf("unfocused field kept the retry alive: %d ms", timeout)
	}
}

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
