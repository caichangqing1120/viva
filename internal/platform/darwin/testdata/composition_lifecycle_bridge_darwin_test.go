package darwin

import (
	"slices"
	"testing"

	"github.com/vivaui/viva/gfx"
	"github.com/vivaui/viva/internal/platform"
	"github.com/vivaui/viva/text"
)

func compositionLifecycleEditor(t *testing.T) (*text.Editor, *runState) {
	t.Helper()
	editor := text.NewEditor("abc")
	editor.SetSelection(text.Range{Start: 3, End: 3})
	if !editor.EnableTentativeComposition() || !editor.ApplyComposition(text.CompositionEvent{
		Phase: text.CompositionUpdate, Text: "x", Selected: text.Range{Start: 1, End: 1},
	}) {
		t.Fatal("cannot create live composition")
	}
	initial := platform.EditorState(editor)
	return editor, &runState{
		textState:       initial,
		textInput:       TextInput{Target: gfx.IDFromBits(7), Width: 100, Height: 20, FontSize: 12, State: initial},
		textInputActive: true,
	}
}

func TestCompositionLifecycleProbeDeferredCancel(t *testing.T) {
	editor, state := compositionLifecycleEditor(t)
	view, cleanup := compositionLifecycleView(state)
	defer cleanup()
	var phases []text.CompositionPhase
	state.textHandler = func(event TextEvent) (TextState, error) {
		phases = append(phases, event.CompositionPhase)
		if event.CompositionPhase == text.CompositionCancel {
			event.Apply(editor)
			return platform.EditorState(editor), nil
		}
		next := platform.EditorState(editor)
		next.Composing = false
		next.Composition = text.Range{}
		return next, nil
	}
	if err := deliverTextInput(state, view, nativeTextEvent{Kind: int(TextSetSelection), ReplacementStart: -1}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(phases, []text.CompositionPhase{text.CompositionNone, text.CompositionCancel}) || editor.String() != "abc" {
		t.Fatalf("phases=%v editor=%q, want one real native lost Cancel and rollback", phases, editor.String())
	}
	if state.textState != platform.EditorState(editor) || state.textInput.State != state.textState || compositionLifecycleText(view) != "abc" {
		t.Fatalf("Cancel snapshot overwritten: editor=%q bridge=%+v input=%+v native=%q", editor.String(), state.textState, state.textInput.State, compositionLifecycleText(view))
	}
	state.compositionLost(view)
	if len(phases) != 2 {
		t.Fatalf("repeated loss delivered phases %v", phases)
	}
}

func TestCompositionLifecycleProbePresentationCancel(t *testing.T) {
	for _, target := range []uint64{7, 8} {
		t.Run(map[uint64]string{7: "same target", 8: "new target"}[target], func(t *testing.T) {
			editor, state := compositionLifecycleEditor(t)
			view, cleanup := compositionLifecycleView(state)
			defer cleanup()
			var phases []text.CompositionPhase
			state.textHandler = func(event TextEvent) (TextState, error) {
				phases = append(phases, event.CompositionPhase)
				event.Apply(editor)
				return platform.EditorState(editor), nil
			}
			state.textInput.OnEvent = state.textHandler
			next := state.textInput
			next.Target = gfx.IDFromBits(target)
			next.State.Composing = false
			next.State.Composition = text.Range{}
			want := "abc"
			if target == 8 {
				want = "next"
				next.State.Value = want
				next.OnEvent = func(TextEvent) (TextState, error) {
					t.Fatal("new target received the old target's Cancel")
					return TextState{}, nil
				}
			}
			if err := presentTextInput(state, view, TextInputUpdate{Changed: true, Input: &next}); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(phases, []text.CompositionPhase{text.CompositionCancel}) || editor.String() != "abc" {
				t.Fatalf("old target phases=%v editor=%q, want Cancel and rollback", phases, editor.String())
			}
			if state.textState.Value != want || state.textInput.State != state.textState || compositionLifecycleText(view) != want || state.textInput.Target != next.Target {
				t.Fatalf("presentation snapshot: bridge=%+v input=%+v native=%q, want %q target %v", state.textState, state.textInput.State, compositionLifecycleText(view), want, next.Target)
			}
		})
	}
}

func TestCompositionLifecycleProbePresentationRefresh(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		terminal bool
		change   func(*TextInput)
	}{
		{name: "ordinary move", change: func(input *TextInput) { input.X++ }},
		{name: "ordinary resize", change: func(input *TextInput) { input.Width++ }},
		{name: "ordinary font", change: func(input *TextInput) { input.FontSize++ }},
		{name: "terminal move", terminal: true, change: func(input *TextInput) { input.X++ }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			editor, state := compositionLifecycleEditor(t)
			state.textInput.Terminal = scenario.terminal
			view, cleanup := compositionLifecycleView(state)
			defer cleanup()
			state.textHandler = func(event TextEvent) (TextState, error) {
				t.Fatalf("presentation refresh emitted an unexpected event: %+v", event)
				return TextState{}, nil
			}
			state.textInput.OnEvent = state.textHandler
			next := state.textInput
			scenario.change(&next)
			resetCompositionLifecycleDiscards()
			if err := presentTextInput(state, view, TextInputUpdate{Changed: true, Input: &next}); err != nil {
				t.Fatal(err)
			}
			if compositionLifecycleDiscards() != 0 || state.textState != platform.EditorState(editor) || compositionLifecycleText(view) != "abcx" {
				t.Fatalf("live refresh: discards=%d bridge=%+v native=%q", compositionLifecycleDiscards(), state.textState, compositionLifecycleText(view))
			}
		})
	}
}
