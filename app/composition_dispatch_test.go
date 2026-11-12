package app

import (
	"testing"

	"github.com/vivaui/viva/internal/platform"
	"github.com/vivaui/viva/text"
	"github.com/vivaui/viva/ui"
)

// The application mirror must copy the canonical provenance fields value for
// value and delegate the phase decision to the one platform Apply.
func TestTextEventApplyForwardsCompositionFields(t *testing.T) {
	editor := text.NewEditor("abcd")
	if !editor.EnableTentativeComposition() {
		t.Fatal("capability setup refused")
	}
	editor.SetSelection(text.Range{Start: 1, End: 2})
	TextEvent{
		Kind: TextSetMarked, CompositionPhase: text.CompositionUpdate, Text: "é𝄞",
		MarkedSelection: TextRange{Start: 2, End: 2},
	}.Apply(editor)
	TextEvent{
		Kind: TextInsert, CompositionPhase: text.CompositionResult, Text: "r",
		HasReplacement: true, Replacement: TextRange{Start: 0, End: 1},
		HasReplacementUTF16: true, ReplacementUTF16: text.UTF16Range{Start: 5, Length: 1},
	}.Apply(editor)
	if got := editor.String(); got != "acdr" {
		t.Fatalf("app Apply raw result = %q, want acdr", got)
	}
}

func TestTextStateOfTentativeCompositionSnapshot(t *testing.T) {
	editor := text.NewEditor("abc")
	if TextStateOf(editor).TentativeComposition {
		t.Fatal("default editor reported the opt-in capability")
	}
	if !editor.EnableTentativeComposition() || !TextStateOf(editor).TentativeComposition {
		t.Fatal("app snapshot did not follow opt-in")
	}
	TextEvent{Kind: TextSetMarked, CompositionPhase: text.CompositionUpdate, Text: "x"}.Apply(editor)
	if state := TextStateOf(editor); !state.Composing || !state.TentativeComposition {
		t.Fatalf("live app state = %+v, want composing enabled", state)
	}
	TextEvent{Kind: TextInsert, CompositionPhase: text.CompositionCommit}.Apply(editor)
	if state := TextStateOf(editor); state.Composing || !state.TentativeComposition {
		t.Fatalf("idle app state = %+v, want idle enabled", state)
	}
	if !editor.DisableTentativeComposition() {
		t.Fatal("disable refused")
	}
	if state := TextStateOf(editor); state.Composing || state.TentativeComposition {
		t.Fatalf("passive disabled app state = %+v, want no capability", state)
	}
}

func TestPainterApplyTextEventCopiesTaggedFieldsAndCapability(t *testing.T) {
	editor := text.NewEditor("abcd")
	if !editor.EnableTentativeComposition() {
		t.Fatal("capability setup refused")
	}
	editor.SetSelection(text.Range{Start: 1, End: 2})
	owner := &painter{activeText: &TextInput{Editor: editor}, nativeText: platform.TextInput{Multiline: true}}
	state, err := owner.applyTextEvent(platform.TextEvent{
		Kind: platform.TextSetMarked, CompositionPhase: text.CompositionUpdate, Text: "é𝄞",
		MarkedSelection: text.Range{Start: 2, End: 2},
	})
	if err != nil || !state.TentativeComposition || !state.Composing ||
		!owner.activeText.State.TentativeComposition || !owner.nativeText.State.TentativeComposition {
		t.Fatalf("direct Update adaptation lost capability: %+v, %v", state, err)
	}
	state, err = owner.applyTextEvent(platform.TextEvent{
		Kind: platform.TextInsert, CompositionPhase: text.CompositionResult, Text: "r",
		HasReplacementUTF16: true, ReplacementUTF16: text.UTF16Range{Start: 5, Length: 1},
	})
	if err != nil || state.Value != "acdr" || state.Composing || !state.TentativeComposition ||
		owner.nativeText.State != state || owner.nativeText.State.TentativeComposition != true {
		t.Fatalf("direct Result adaptation lost raw replacement/state: %+v, %v", state, err)
	}
	if !editor.DisableTentativeComposition() || TextStateOf(editor).TentativeComposition {
		t.Fatal("app snapshot confused a passive owner with an enabled capability")
	}

	// A custom OnEvent handler owns its own state; the adaptation must still
	// carry every field and the returned capability snapshot exactly.
	var received TextEvent
	owner.activeText = &TextInput{OnEvent: func(event TextEvent) (TextState, error) {
		received = event
		return TextState{Value: "handled", TentativeComposition: true, Redraw: true}, nil
	}}
	event := platform.TextEvent{
		Kind: platform.TextInsert, CompositionPhase: text.CompositionResult, Text: "native", Rectangular: true,
		Selection: text.Range{Start: 7, End: 8}, MarkedSelection: text.Range{Start: 2, End: 3},
		HasReplacement: true, Replacement: text.Range{Start: 4, End: 5},
		HasReplacementUTF16: true, ReplacementUTF16: text.UTF16Range{Start: 9, Length: 11},
	}
	state, err = owner.applyTextEvent(event)
	if err != nil || received.CompositionPhase != event.CompositionPhase || received.Text != event.Text ||
		received.ReplacementUTF16 != event.ReplacementUTF16 || !received.HasReplacementUTF16 ||
		!received.Rectangular || received.Selection != (TextRange{Start: 7, End: 8}) ||
		received.MarkedSelection != (TextRange{Start: 2, End: 3}) || !received.HasReplacement ||
		received.Replacement != (TextRange{Start: 4, End: 5}) {
		t.Fatalf("custom handler adaptation lost fields: %+v, %v", received, err)
	}
	if !state.TentativeComposition || !state.Redraw || !owner.nativeText.State.TentativeComposition || owner.nativeText.State.Redraw {
		t.Fatal("returned/mirrored capability or one-shot redraw changed")
	}
}

// Single-line sanitation remains the painter's existing policy for an Insert
// even when the same event carries a tentative phase.
func TestPainterTaggedCompositionKeepsSingleLineSanitation(t *testing.T) {
	editor := text.NewEditor("abc")
	if !editor.EnableTentativeComposition() {
		t.Fatal("capability setup refused")
	}
	editor.SetSelection(text.Range{Start: 1, End: 1})
	owner := &painter{activeText: &TextInput{Editor: editor}, nativeText: platform.TextInput{}}
	if _, err := owner.applyTextEvent(platform.TextEvent{
		Kind: platform.TextSetMarked, CompositionPhase: text.CompositionUpdate, Text: "p",
	}); err != nil {
		t.Fatal(err)
	}
	state, err := owner.applyTextEvent(platform.TextEvent{
		Kind: platform.TextInsert, CompositionPhase: text.CompositionResult, Text: "x\ny",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := editor.String(); got != "ax ybc" || state.Value != "ax ybc" {
		t.Fatalf("tagged single-line result = %q (state %q), want ax ybc", got, state.Value)
	}
}

// Focused/initial native state is rebuilt from the app snapshot, so the
// capability must be present there and disappear with a passive Disable.
func TestPainterFocusedStateCarriesTentativeCapability(t *testing.T) {
	const field = 11
	editor := text.NewEditor("Go")
	if !editor.EnableTentativeComposition() {
		t.Fatal("capability setup refused")
	}
	var canvas ui.Canvas
	config := Config{Title: "e3a", Width: 200, Height: 80}
	config.Paint = func(canvas *ui.Canvas, size ui.Size) {
		canvas.HitWithOptions(ui.Item(field), ui.Rect{Width: 120, Height: 24}, "Name", ui.HitOptions{Role: ui.RoleTextField})
	}
	var input TextInput
	config.TextInput = func(got TextInputContext) *TextInput {
		input = TextInput{Bounds: got.Bounds, FontSize: 13, Editor: editor}
		return &input
	}
	painter := newTestPainter(t, &canvas, config)
	var enabled *platform.TextInput
	for _, kind := range []platform.PointerEventKind{platform.PointerMove, platform.PointerDown, platform.PointerUp} {
		frame, err := painter.pointer(platform.PointerEvent{Kind: kind, X: 10, Y: 10})
		if err != nil {
			t.Fatal(err)
		}
		if frame.TextInput.Changed && frame.TextInput.Input != nil {
			enabled = frame.TextInput.Input
		}
	}
	if enabled == nil || !enabled.State.TentativeComposition || !TextStateOf(editor).TentativeComposition {
		t.Fatalf("focused native state lost capability: %+v", enabled)
	}
	painter.syncTextInput()
	if !painter.nativeText.State.TentativeComposition {
		t.Fatal("sync lost the focused capability snapshot")
	}
	if !editor.DisableTentativeComposition() {
		t.Fatal("disable refused")
	}
	painter.syncTextInput()
	if painter.nativeText.State.TentativeComposition || TextStateOf(editor).TentativeComposition {
		t.Fatal("passive disabled owner leaked an enabled native snapshot")
	}
}
