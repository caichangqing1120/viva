package windows

import (
	"errors"
	"slices"
	"testing"

	"github.com/vivaui/viva/gfx"
	"github.com/vivaui/viva/internal/platform"
	"github.com/vivaui/viva/text"
)

func TestCompositionIMEEndPreservesOptInAndLegacyBoundaries(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		name := "legacy"
		if enabled {
			name = "tentative"
		}
		t.Run(name, func(t *testing.T) {
			editor := text.NewEditor("abc")
			if enabled && !editor.EnableTentativeComposition() {
				t.Fatal("could not enable fixture capability")
			}
			editor.SetSelection(text.Range{Start: 1, End: 2})
			preedit := platform.TextEvent{Kind: platform.TextSetMarked, Text: "x", MarkedSelection: text.Range{Start: 1, End: 1}}
			if enabled {
				preedit.CompositionPhase = text.CompositionUpdate
			}
			preedit.Apply(editor)
			if editor.String() != "axc" {
				t.Fatal("fixture did not begin selected preedit")
			}
			state := &windowState{ready: true, textEnabled: true, imeComposing: true, textState: platform.EditorState(editor)}
			var events []platform.TextEvent
			stop := errors.New("stop before renderer submission")
			state.textInput.OnEvent = func(event platform.TextEvent) (platform.TextState, error) {
				events = append(events, event)
				event.Apply(editor)
				return platform.EditorState(editor), stop
			}
			// Exercise the actual dispatcher without a graphics device. The handler
			// error stops rendering only after the event has reached the real Editor.
			const handle = ^uintptr(0)
			previous := active.Load()
			windowStates.Store(handle, state)
			t.Cleanup(func() { windowStates.Delete(handle); active.Store(previous) })
			windowProc(handle, wmIMEEnd, 0, 0)
			if len(events) != 1 || state.err != stop || state.imeComposing {
				t.Fatalf("end delivery: events=%+v err=%v composing=%v", events, state.err, state.imeComposing)
			}
			wantPhase, wantText := text.CompositionNone, "axc"
			if enabled {
				wantPhase, wantText = text.CompositionCancel, "ac"
			}
			if events[0].CompositionPhase != wantPhase || editor.String() != wantText {
				t.Fatalf("end phase=%v text=%q, want phase=%v text=%q", events[0].CompositionPhase, editor.String(), wantPhase, wantText)
			}
			windowProc(handle, wmIMEEnd, 0, 0)
			if len(events) != 1 {
				t.Fatal("duplicate end delivered another edit")
			}
			if enabled {
				editor.Undo()
				if editor.String() != "abc" {
					t.Fatalf("ordinary selection deletion did not remain undoable: %q", editor.String())
				}
			}
		})
	}
}

func compositionMessageWindow(t *testing.T, editor *text.Editor) (*windowState, *[]platform.TextEvent) {
	t.Helper()
	state := dynamicTextState(0, 0)
	state.window, state.scale, state.ready = staticWindow(t), 1, true
	if err := state.renderer.initialize(state.window, platform.Color{Alpha: 1}); err != nil {
		t.Fatalf("initialize native message surface: %v", err)
	}
	t.Cleanup(func() {
		state.stopCaretTimer()
		state.renderer.close()
	})
	state.textEnabled = true
	state.textInput = testTextInput(editor.String(), 0)
	state.textInput.Target = gfx.Item(1)
	state.textState = platform.EditorState(editor)
	state.textInput.State = state.textState
	events := new([]platform.TextEvent)
	state.textInput.OnEvent = func(event platform.TextEvent) (platform.TextState, error) {
		*events = append(*events, event)
		event.Apply(editor)
		return platform.EditorState(editor), nil
	}
	previous := active.Load()
	windowStates.Store(state.window, state)
	t.Cleanup(func() { windowStates.Delete(state.window); active.Store(previous) })
	return state, events
}

func TestCompositionMessageResultAndFollowingPreedit(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		name := "legacy"
		if enabled {
			name = "tentative"
		}
		t.Run(name, func(t *testing.T) {
			editor := text.NewEditor("abc")
			if enabled && !editor.EnableTentativeComposition() {
				t.Fatal("could not enable fixture capability")
			}
			editor.SetSelection(text.Range{Start: 1, End: 2})
			state, events := compositionMessageWindow(t, editor)
			state.applyIMEComposition(imeCompositionMessage{hasPreedit: true, preedit: "x", selection: text.Range{Start: 1, End: 1}})
			if state.err != nil || !state.textState.Composing || editor.String() != "axc" {
				t.Fatalf("preedit acknowledgement: %+v err=%v", state.textState, state.err)
			}
			// IMM may finish one conversion and begin the next in the same message.
			state.applyIMEComposition(imeCompositionMessage{
				hasResult: true, result: "r", hasPreedit: true, preedit: "y",
				selection: text.Range{Start: 1, End: 1}, target: text.Range{End: 1}, hasTarget: true,
			})
			if state.err != nil || len(*events) != 3 || editor.String() != "aryc" || !state.imeComposing || !state.imeHasTarget {
				t.Fatalf("combined result/preedit: text=%q events=%+v state=%+v err=%v", editor.String(), *events, state.textState, state.err)
			}
			want := []text.CompositionPhase{text.CompositionNone, text.CompositionNone, text.CompositionNone}
			if enabled {
				want = []text.CompositionPhase{text.CompositionUpdate, text.CompositionResult, text.CompositionUpdate}
			}
			phases := make([]text.CompositionPhase, len(*events))
			for index, event := range *events {
				phases[index] = event.CompositionPhase
			}
			if !slices.Equal(phases, want) {
				t.Fatalf("native phases=%v, want %v", phases, want)
			}
			windowProc(state.window, wmIMEEnd, 0, 0)
			wantText := "aryc"
			if enabled {
				wantText = "arc"
			}
			if state.err != nil || editor.String() != wantText || state.textState.Composing {
				t.Fatalf("end after combined message: text=%q state=%+v err=%v", editor.String(), state.textState, state.err)
			}
		})
	}
}

func TestCompositionMessageEmptyPreeditRemainsAnUpdate(t *testing.T) {
	editor := text.NewEditor("abc")
	if !editor.EnableTentativeComposition() {
		t.Fatal("could not enable fixture capability")
	}
	editor.SetSelection(text.Range{Start: 1, End: 2})
	state, events := compositionMessageWindow(t, editor)
	for _, value := range []string{"x", ""} {
		state.applyIMEComposition(imeCompositionMessage{hasPreedit: true, preedit: value})
	}
	if state.err != nil || len(*events) != 2 || editor.String() != "ac" {
		t.Fatalf("empty preedit: text=%q events=%+v err=%v", editor.String(), *events, state.err)
	}
	for _, event := range *events {
		if event.CompositionPhase != text.CompositionUpdate {
			t.Fatalf("empty preedit fabricated a terminal phase: %+v", event)
		}
	}
}

func TestCompositionResultDoesNotSuppressAnIdenticalOrdinaryCharacter(t *testing.T) {
	editor := text.NewEditor("a")
	if !editor.EnableTentativeComposition() {
		t.Fatal("could not enable fixture capability")
	}
	editor.MoveTo(1, false)
	state, events := compositionMessageWindow(t, editor)
	state.applyIMEComposition(imeCompositionMessage{hasPreedit: true, preedit: "x"})
	state.applyIMEComposition(imeCompositionMessage{hasResult: true, result: "r"})
	windowProc(state.window, wmIMEEnd, 0, 0)
	windowProc(state.window, wmChar, 'r', 0)
	if state.err != nil || editor.String() != "arr" || len(*events) != 3 || (*events)[2].CompositionPhase != text.CompositionNone {
		t.Fatalf("ordinary character after result: text=%q events=%+v err=%v", editor.String(), *events, state.err)
	}
}

func BenchmarkCompositionMessageDispatch(b *testing.B) {
	for _, enabled := range []bool{false, true} {
		name := "legacy"
		if enabled {
			name = "tentative"
		}
		b.Run(name, func(b *testing.B) {
			state := &windowState{textEnabled: true, textState: platform.TextState{TentativeComposition: enabled}}
			state.textInput.Terminal = true
			state.textInput.OnEvent = func(platform.TextEvent) (platform.TextState, error) {
				return state.textState, nil
			}
			message := imeCompositionMessage{hasPreedit: true, preedit: "x"}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				state.applyIMEComposition(message)
			}
		})
	}
}
