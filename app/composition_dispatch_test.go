package app

import (
	"runtime"
	"testing"
	"time"
	"unsafe"

	"github.com/vivaui/viva/gfx"
	"github.com/vivaui/viva/internal/platform"
	"github.com/vivaui/viva/text"
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
	var canvas gfx.Canvas
	config := Config{Title: "e3a", Width: 200, Height: 80}
	config.Paint = func(canvas *gfx.Canvas, size gfx.Size) {
		canvas.HitWithOptions(gfx.Item(field), gfx.Rect{Width: 120, Height: 24}, "Name", gfx.HitOptions{Role: gfx.RoleTextField})
	}
	var input TextInput
	config.TextInput = func(got TextInputContext) *TextInput {
		input = TextInput{Bounds: got.Bounds, FontSize: 13, Editor: editor}
		return &input
	}
	painter := newTestPainter(t, &canvas, config)
	var enabled *platform.TextInput
	for _, kind := range []platform.PointerEventKind{platform.PointerMove, platform.PointerDown, platform.PointerUp} {
		frame, err := painter.pointerEvent(platform.PointerEvent{Kind: kind, X: 10, Y: 10})
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

func TestPainterDistinguishesIdenticalTentativeEditorOwners(t *testing.T) {
	for _, custom := range []bool{false, true} {
		name := "editor"
		if custom {
			name = "custom handler"
		}
		t.Run(name, func(t *testing.T) {
			first, second := text.NewEditor("same"), text.NewEditor("same")
			if !first.EnableTentativeComposition() || !second.EnableTentativeComposition() {
				t.Fatal("capability setup refused")
			}
			current := first
			var canvas gfx.Canvas
			config := Config{Title: "composition ownership", Width: 200, Height: 80}
			config.Paint = func(canvas *gfx.Canvas, size gfx.Size) {
				canvas.HitWithOptions(gfx.Item(11), gfx.Rect{Width: 120, Height: 24}, "Name", gfx.HitOptions{Role: gfx.RoleTextField})
			}
			config.TextInput = func(context TextInputContext) *TextInput {
				owner := current
				input := &TextInput{Bounds: context.Bounds, FontSize: 13, Editor: owner}
				if custom {
					input.Editor = nil
					input.State = TextStateOf(owner)
					input.OnEvent = func(event TextEvent) (TextState, error) {
						event.Apply(owner)
						return TextStateOf(owner), nil
					}
				}
				return input
			}
			painter := newTestPainter(t, &canvas, config)
			for _, kind := range []platform.PointerEventKind{platform.PointerMove, platform.PointerDown, platform.PointerUp} {
				if _, err := painter.pointerEvent(platform.PointerEvent{Kind: kind, X: 10, Y: 10}); err != nil {
					t.Fatal(err)
				}
			}
			if update := painter.syncTextInput(); update.Changed {
				t.Fatal("the same owner's identical snapshot forced an input replacement")
			}
			current = second
			if update := painter.syncTextInput(); !update.Changed || update.Input == nil {
				t.Fatal("a different tentative Editor with identical state was not published as a replacement")
			}
		})
	}
}

func TestPainterReentrantTextEventKeepsLatestAcknowledgment(t *testing.T) {
	for _, mode := range []string{"nested edit", "ignored newline"} {
		t.Run(mode, func(t *testing.T) {
			editor := text.NewEditor("a")
			editor.MoveTo(1, false)
			owner := &painter{nativeText: platform.TextInput{Multiline: mode == "nested edit"}}
			owner.nativeText.State = platform.EditorState(editor)
			owner.activeText = &TextInput{State: TextStateOf(editor)}
			owner.activeText.OnEvent = func(event TextEvent) (TextState, error) {
				event.Apply(editor)
				ack := TextStateOf(editor)
				if event.Text == "r" {
					nested := platform.TextEvent{Kind: platform.TextInsert, Text: "z"}
					if mode == "ignored newline" {
						nested = platform.TextEvent{Kind: platform.TextCommandEvent, Command: platform.TextInsertNewline}
					}
					if _, err := owner.applyTextEvent(nested); err != nil {
						t.Fatal(err)
					}
				}
				return ack, nil
			}
			ack, err := owner.applyTextEvent(platform.TextEvent{Kind: platform.TextInsert, Text: "r"})
			want := TextStateOf(editor)
			wantValue := "arz"
			if mode == "ignored newline" {
				wantValue = "ar"
			}
			if err != nil || want.Value != wantValue || owner.activeText.State != want ||
				owner.nativeText.State != platformTextState(want) || ack.WithoutRedraw() != platformTextState(want) {
				t.Fatalf("nested text acknowledgment: editor=%+v active=%+v native=%+v ack=%+v err=%v", want, owner.activeText.State, owner.nativeText.State, ack, err)
			}
		})
	}
}

func TestPainterTextCallbackSyncPreservesInputOwnership(t *testing.T) {
	for _, mode := range []string{"same owner fresh provider", "same owner geometry", "replacement owner"} {
		t.Run(mode, func(t *testing.T) {
			first, second := text.NewEditor("a"), text.NewEditor("other")
			first.MoveTo(1, false)
			current := first
			extraWidth := float32(0)
			var canvas gfx.Canvas
			config := Config{Title: "reentrant provider", Width: 200, Height: 80}
			config.Paint = func(canvas *gfx.Canvas, size gfx.Size) {
				canvas.HitWithOptions(gfx.Item(11), gfx.Rect{Width: 120, Height: 24}, "Name", gfx.HitOptions{Role: gfx.RoleTextField})
			}
			var owner *painter
			config.TextInput = func(context TextInputContext) *TextInput {
				editor := current
				bounds := context.Bounds
				bounds.Width += extraWidth
				return &TextInput{Bounds: bounds, FontSize: 13, State: TextStateOf(editor), OnEvent: func(event TextEvent) (TextState, error) {
					if mode == "replacement owner" {
						event.Apply(editor)
						current = second
						owner.syncTextInput()
					} else {
						// A paint may return a new wrapper for an unchanged owner.
						if mode == "same owner geometry" {
							extraWidth = 10
						}
						owner.syncTextInput()
						event.Apply(editor)
					}
					ack := TextStateOf(editor)
					ack.Redraw = true
					return ack, nil
				}}
			}
			owner = newTestPainter(t, &canvas, config)
			for _, kind := range []platform.PointerEventKind{platform.PointerMove, platform.PointerDown, platform.PointerUp} {
				if _, err := owner.pointerEvent(platform.PointerEvent{Kind: kind, X: 10, Y: 10}); err != nil {
					t.Fatal(err)
				}
			}
			ack, err := owner.applyTextEvent(platform.TextEvent{Kind: platform.TextInsert, Text: "r"})
			want := TextStateOf(current)
			if err != nil || first.String() != "ar" || owner.activeText.State != want ||
				owner.nativeText.State != platformTextState(want) || ack.WithoutRedraw() != platformTextState(want) || !ack.Redraw {
				t.Fatalf("provider acknowledgment: editor=%+v active=%+v native=%+v ack=%+v err=%v", want, owner.activeText.State, owner.nativeText.State, ack, err)
			}
		})
	}
}

func TestTentativeStateSnapshotDoesNotRetainTheEditor(t *testing.T) {
	released := make(chan struct{})
	makeSnapshot := func() TextState {
		editor := text.NewEditor("snapshot remains readable")
		if !editor.EnableTentativeComposition() {
			t.Fatal("capability setup refused")
		}
		runtime.AddCleanup(editor, func(done chan struct{}) { close(done) }, released)
		return TextStateOf(editor)
	}
	snapshot := makeSnapshot()
	deadline := time.Now().Add(5 * time.Second)
	for {
		runtime.GC()
		select {
		case <-released:
			if snapshot.Value != "snapshot remains readable" {
				t.Fatal("reclaiming the Editor invalidated its value snapshot")
			}
			runtime.KeepAlive(snapshot)
			return
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("a retained TextState snapshot kept the Editor and its history alive")
		}
		runtime.Gosched()
	}
}

var nativeStateBenchmarkSink TextState

func BenchmarkPainterTextAcknowledgment(b *testing.B) {
	editor := text.NewEditor("a")
	owner := &painter{activeText: &TextInput{Editor: editor}, nativeText: platform.TextInput{Multiline: true}}
	b.ReportAllocs()
	b.ResetTimer()
	index := 0
	for b.Loop() {
		index ^= 1
		if _, err := owner.applyTextEvent(platform.TextEvent{Kind: platform.TextSetSelection, Selection: text.Range{Start: index, End: index}}); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(unsafe.Sizeof(painter{})), "painter-B")
}

func BenchmarkNativeStateSnapshot(b *testing.B) {
	for _, enabled := range []bool{false, true} {
		name := "legacy"
		if enabled {
			name = "tentative"
		}
		b.Run(name, func(b *testing.B) {
			editor := text.NewEditor("state snapshot")
			if enabled && !editor.EnableTentativeComposition() {
				b.Fatal("capability setup refused")
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				nativeStateBenchmarkSink = TextStateOf(editor)
			}
			b.ReportMetric(float64(unsafe.Sizeof(TextState{})), "app-state-B")
			b.ReportMetric(float64(unsafe.Sizeof(platform.TextState{})), "native-state-B")
			runtime.KeepAlive(editor)
		})
	}
}

func BenchmarkNativeStateFirstSnapshot(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		editor := text.NewEditor("first snapshot")
		if !editor.EnableTentativeComposition() {
			b.Fatal("capability setup refused")
		}
		nativeStateBenchmarkSink = TextStateOf(editor)
	}
}

func BenchmarkNativeStateDefaultConstruction(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		editor := text.NewEditor("first snapshot")
		nativeStateBenchmarkSink = TextStateOf(editor)
	}
}

func TestDefaultStateSnapshotPreservesConstructionAllocations(t *testing.T) {
	allocations := testing.AllocsPerRun(100, func() {
		editor := text.NewEditor("first snapshot")
		nativeStateBenchmarkSink = TextStateOf(editor)
	})
	if allocations > 2 {
		t.Fatalf("a default value snapshot exceeded the measured construction baseline: %.0f allocations, want at most 2", allocations)
	}
}
