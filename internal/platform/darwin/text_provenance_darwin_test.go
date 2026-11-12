package darwin

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
	"unsafe"

	"github.com/vivaui/viva/internal/platform"
	"github.com/vivaui/viva/text"
	"github.com/vivaui/viva/ui"
)

// The native bridge carries the accepted E3A composition phase and raw UTF-16
// replacement facts. These tests bind the Go half of that handoff: native field
// decoding, capability gating, and the resulting editor behavior. The native
// half is proven separately by testdata/text_provenance.m against the real
// NSTextInputClient methods.
func TestNativeTextEventCompositionFields(t *testing.T) {
	update := nativeTextEvent{
		Kind: int(TextSetMarked), Text: "é𝄞", Value: "abcd",
		SelectedStart: 2, SelectedLength: 2,
		ReplacementStart: -1,
		Phase:            int(text.CompositionUpdate),
	}
	event := update.textEvent(true)
	if event.Kind != TextSetMarked || event.CompositionPhase != text.CompositionUpdate {
		t.Fatalf("update event = %+v", event)
	}
	if event.MarkedSelection != (text.Range{Start: 1, End: 2}) {
		t.Fatalf("update marked selection = %+v, want {1 2}", event.MarkedSelection)
	}
	if event.HasReplacement || event.HasReplacementUTF16 {
		t.Fatalf("unavailable replacement invented facts: %+v", event)
	}

	result := nativeTextEvent{
		Kind: int(TextInsert), Text: "r", Value: "aé𝄞cd",
		ReplacementStart: 5, ReplacementLength: 1,
		Phase:    int(text.CompositionResult),
		RawStart: 5, RawLength: 1, HasRawReplacement: true,
	}
	event = result.textEvent(true)
	if event.CompositionPhase != text.CompositionResult || !event.HasReplacement || !event.HasReplacementUTF16 {
		t.Fatalf("result event = %+v, want both rune and raw replacement", event)
	}
	if event.Replacement != (text.Range{Start: 4, End: 5}) || event.ReplacementUTF16 != (text.UTF16Range{Start: 5, Length: 1}) {
		t.Fatalf("result replacements = %+v / %+v", event.Replacement, event.ReplacementUTF16)
	}

	plain := nativeTextEvent{Kind: int(TextInsert), Text: "x", Value: "abcd", ReplacementStart: -1}
	if event := plain.textEvent(true); event.CompositionPhase != text.CompositionNone || event.HasReplacementUTF16 {
		t.Fatalf("ordinary insert was tagged: %+v", event)
	}
	if event := update.textEvent(false); event.CompositionPhase != text.CompositionNone || event.HasReplacementUTF16 {
		t.Fatalf("disabled capability kept a tagged route: %+v", event)
	}
}

// A real native update/result pair with a raw replacement must convert after
// rollback exactly as the accepted E1/E2 source behavior: pre-start deletion is
// ordinary, raw coordinates then resolve against that post-rollback text.
func TestNativeCompositionRawResultAfterRollback(t *testing.T) {
	editor := text.NewEditor("abcd")
	if !editor.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	editor.SetSelection(text.Range{Start: 1, End: 2})

	nativeUpdate := nativeTextEvent{
		Kind: int(TextSetMarked), Text: "é𝄞", Value: "abcd", ReplacementStart: -1,
		Phase: int(text.CompositionUpdate),
	}
	nativeUpdate.textEvent(true).Apply(editor)
	if got := editor.String(); got != "aé𝄞cd" {
		t.Fatalf("native update = %q, want aé𝄞cd", got)
	}

	nativeResult := nativeTextEvent{
		Kind: int(TextInsert), Text: "r", Value: "aé𝄞cd",
		ReplacementStart: 5, ReplacementLength: 1,
		Phase:    int(text.CompositionResult),
		RawStart: 5, RawLength: 1, HasRawReplacement: true,
	}
	nativeResult.textEvent(true).Apply(editor)
	if got := editor.String(); got != "acdr" {
		t.Fatalf("native raw result = %q, want acdr", got)
	}
	if state := platform.EditorState(editor); state.Composing || !state.TentativeComposition {
		t.Fatalf("state after raw result = %+v, want idle enabled capability", state)
	}
}

// Invalid ranges are passed through to the accepted E1 conversion instead of
// being guessed natively. An unavailable replacement adds no raw fact and keeps
// the legacy selection target; an NSNotFound-style sentinel never becomes a
// document-wide replacement.
func TestNativeCompositionInvalidAndUnavailableRawRanges(t *testing.T) {
	for _, scenario := range []struct {
		name       string
		value      string
		selection  text.Range
		rawStart   int
		rawLength  int
		hasRaw     bool
		replStart  int
		replLength int
		want       string
	}{
		{name: "split surrogate", value: "a𝄞b", rawStart: 2, rawLength: 0, hasRaw: true, replStart: -1, want: "a𝄞br"},
		{name: "past end", value: "abc", rawStart: 99, rawLength: 99, hasRaw: true, replStart: -1, want: "abcr"},
		{name: "huge length", value: "a𝄞b", rawStart: 1, rawLength: int(^uint(0) >> 1), hasRaw: true, replStart: -1, want: "ar"},
		{name: "unavailable", value: "abc", selection: text.Range{Start: 1, End: 2}, hasRaw: false, replStart: -1, want: "arc"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			editor := text.NewEditor(scenario.value)
			if !editor.EnableTentativeComposition() {
				t.Fatal("enable refused")
			}
			if scenario.selection.Start != scenario.selection.End {
				editor.SetSelection(scenario.selection)
			}
			event := nativeTextEvent{
				Kind: int(TextInsert), Text: "r", Value: scenario.value,
				SelectedStart: scenario.selection.Start, SelectedLength: scenario.selection.End - scenario.selection.Start,
				ReplacementStart: scenario.replStart, ReplacementLength: scenario.replLength,
				Phase:    int(text.CompositionResult),
				RawStart: scenario.rawStart, RawLength: scenario.rawLength, HasRawReplacement: scenario.hasRaw,
			}.textEvent(true)
			event.Apply(editor)
			if got := editor.String(); got != scenario.want {
				t.Fatalf("%s = %q, want %q", scenario.name, got, scenario.want)
			}
		})
	}

	editor := text.NewEditor("abc")
	if !editor.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	before := platform.EditorState(editor)
	nativeTextEvent{
		Kind: int(TextInsert), Text: "BAD", Value: "abc",
		Phase: 255, RawStart: 0, RawLength: 1, HasRawReplacement: true,
	}.textEvent(true).Apply(editor)
	if got := editor.String(); got != "abc" {
		t.Fatalf("invalid native phase fell back and mutated: %q", got)
	}
	if state := platform.EditorState(editor); state != before {
		t.Fatalf("invalid native phase state = %+v, want %+v", state, before)
	}
}

// The exported callback's Go core forwards exact native facts only while the
// returned capability snapshot remains enabled. A passive/disabled snapshot
// makes the next event legacy again, with no composition route to reject.
func TestDeliverTextInputCompositionCapabilityGate(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("capability=%v", enabled), func(t *testing.T) {
			var received TextEvent
			state := &runState{
				textInput:       TextInput{Terminal: true},
				textInputActive: true,
				textState: TextState{
					Value: "abcd", Selection: text.Range{Start: 4, End: 4},
					TentativeComposition: enabled,
				},
			}
			state.textHandler = func(event TextEvent) (TextState, error) {
				received = event
				return state.textState, nil
			}
			deliverTextInput(state, nil, nativeTextEvent{
				Kind: int(TextInsert), Text: "r", Value: "abcd",
				ReplacementStart: 2, ReplacementLength: 1,
				Phase:    int(text.CompositionResult),
				RawStart: 2, RawLength: 1, HasRawReplacement: true,
			})

			if received.Kind != TextInsert || received.Text != "r" || !received.HasReplacement {
				t.Fatalf("legacy fields = %+v", received)
			}
			if !enabled {
				if received.CompositionPhase != text.CompositionNone || received.HasReplacementUTF16 {
					t.Fatalf("disabled capability routed tagged facts: %+v", received)
				}
				return
			}
			if received.CompositionPhase != text.CompositionResult ||
				!received.HasReplacementUTF16 || received.ReplacementUTF16 != (text.UTF16Range{Start: 2, Length: 1}) {
				t.Fatalf("enabled capability lost native facts: %+v", received)
			}
		})
	}
}

func TestNativeTextProvenance(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "text-provenance")
	arguments := []string{"clang", "-fno-objc-arc", "-Werror", "-Wno-deprecated-declarations"}
	for name, phase := range map[string]text.CompositionPhase{
		"NONE": text.CompositionNone, "UPDATE": text.CompositionUpdate,
		"RESULT": text.CompositionResult, "COMMIT": text.CompositionCommit,
		"CANCEL": text.CompositionCancel, "EMPTY_COMMIT": text.CompositionEmptyCommit,
	} {
		arguments = append(arguments, fmt.Sprintf("-DGO_COMPOSITION_%s=%d", name, phase))
	}
	arguments = append(arguments, "testdata/text_provenance.m", "-o", executable,
		"-framework", "Cocoa", "-framework", "CoreText", "-framework", "Metal", "-framework", "QuartzCore")
	if output, err := exec.Command("xcrun", arguments...).CombinedOutput(); err != nil {
		t.Fatalf("compile native text provenance: %v\n%s", err, output)
	}
	if output, err := exec.Command(executable).CombinedOutput(); err != nil {
		t.Fatalf("native text provenance: %v\n%s", err, output)
	} else {
		t.Logf("%s", output)
	}
}

// Terminal phases consume the live native composition once and never replay
// their ordinary text kind through the legacy switch. An empty commit with an
// explicit raw range removes exactly that post-rollback range.
func TestNativeCompositionTerminalPhases(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		native nativeTextEvent
		want   string
	}{
		{
			name: "commit",
			native: nativeTextEvent{
				Kind: int(TextUnmark), Value: "abcx", ReplacementStart: -1,
				Phase: int(text.CompositionCommit),
			},
			want: "abcx",
		},
		{
			name: "cancel",
			native: nativeTextEvent{
				Kind: int(TextSetMarked), Value: "abcx", ReplacementStart: -1,
				Phase: int(text.CompositionCancel),
			},
			want: "abc",
		},
		{
			name: "empty commit",
			native: nativeTextEvent{
				Kind: int(TextInsert), Text: "ignored", Value: "abcx", ReplacementStart: -1,
				Phase: int(text.CompositionEmptyCommit),
			},
			want: "abc",
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			editor := text.NewEditor("abc")
			if !editor.EnableTentativeComposition() {
				t.Fatal("enable refused")
			}
			nativeTextEvent{
				Kind: int(TextSetMarked), Text: "x", Value: "abc", ReplacementStart: -1,
				Phase: int(text.CompositionUpdate),
			}.textEvent(true).Apply(editor)
			scenario.native.textEvent(true).Apply(editor)
			if got := editor.String(); got != scenario.want {
				t.Fatalf("%s = %q, want %q", scenario.name, got, scenario.want)
			}
			if _, composing := editor.Composition(); composing {
				t.Fatalf("%s left a live composition", scenario.name)
			}
		})
	}

	editor := text.NewEditor("abc")
	if !editor.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	editor.SetSelection(text.Range{Start: 1, End: 2})
	nativeTextEvent{
		Kind: int(TextInsert), Value: "abc", ReplacementStart: -1,
		Phase:    int(text.CompositionEmptyCommit),
		RawStart: 1, RawLength: 1, HasRawReplacement: true,
	}.textEvent(true).Apply(editor)
	if got := editor.String(); got != "ac" {
		t.Fatalf("explicit empty commit = %q, want ac", got)
	}
}

// A tagged event arriving after the capability is returned passive/disabled is
// still exactly the legacy edit: no composition state is created or mutated.
func TestNativeCompositionPassiveCapabilityLegacyPath(t *testing.T) {
	editor := text.NewEditor("abc")
	event := nativeTextEvent{
		Kind: int(TextInsert), Text: "X", Value: "abc",
		ReplacementStart: 1, ReplacementLength: 1,
		Phase:    int(text.CompositionResult),
		RawStart: 99, RawLength: 99, HasRawReplacement: true,
	}.textEvent(false)
	event.Apply(editor)
	if got := editor.String(); got != "aXc" {
		t.Fatalf("passive legacy edit = %q, want aXc", got)
	}
	if _, composing := editor.Composition(); editor.TentativeCompositionEnabled() || composing {
		t.Fatal("passive event created composition state")
	}
}

func TestNativeTextProvenanceLayout(t *testing.T) {
	t.Logf("nativeTextEvent=%d TextEvent=%d TextState=%d TextInput=%d",
		unsafe.Sizeof(nativeTextEvent{}), unsafe.Sizeof(TextEvent{}),
		unsafe.Sizeof(TextState{}), unsafe.Sizeof(TextInput{}))
}

func BenchmarkNativeTextEventMarshal(b *testing.B) {
	legacy := nativeTextEvent{
		Kind: int(TextInsert), Text: "x", Value: "abcd", ReplacementStart: -1,
	}
	tagged := nativeTextEvent{
		Kind: int(TextInsert), Text: "r", Value: "abcd",
		ReplacementStart: 2, ReplacementLength: 1,
		Phase:    int(text.CompositionResult),
		RawStart: 2, RawLength: 1, HasRawReplacement: true,
	}
	b.Run("legacy", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if event := legacy.textEvent(true); event.Kind != TextInsert {
				b.Fatal(event)
			}
		}
	})
	b.Run("tagged", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if event := tagged.textEvent(true); !event.HasReplacementUTF16 {
				b.Fatal(event)
			}
		}
	})
}

func BenchmarkNativeTextEventDelivery(b *testing.B) {
	native := nativeTextEvent{
		Kind: int(TextInsert), Text: "r", Value: "abcd",
		ReplacementStart: 2, ReplacementLength: 1,
		Phase:    int(text.CompositionResult),
		RawStart: 2, RawLength: 1, HasRawReplacement: true,
	}
	for _, enabled := range []bool{false, true} {
		name := "legacy"
		if enabled {
			name = "tagged"
		}
		b.Run(name, func(b *testing.B) {
			state := &runState{
				textInput:       TextInput{Terminal: true},
				textInputActive: true,
				textState: TextState{
					Value: "abcd", Selection: text.Range{Start: 4, End: 4},
					TentativeComposition: enabled,
				},
			}
			state.textHandler = func(TextEvent) (TextState, error) { return state.textState, nil }
			b.ReportAllocs()
			for b.Loop() {
				deliverTextInput(state, nil, native)
			}
		})
	}
}

func textInputTarget(bits uint64) ui.ID { return ui.IDFromBits(bits) }

// A different target, or a same-target snapshot that drops the mark, replaces
// the editor that owned the live composition. A nil or passive/disabled
// snapshot is the legacy discard and must not route a tag.
func TestTextInputReplacementEndsComposition(t *testing.T) {
	previous := TextState{
		Value: "abcx", Selection: text.Range{Start: 4, End: 4}, Composition: text.Range{Start: 3, End: 4},
		Composing: true, TentativeComposition: true,
	}
	current := TextInput{Target: textInputTarget(7)}
	active := func(target ui.ID, composing bool, tentative bool) *TextInput {
		return &TextInput{
			Target: target,
			State: TextState{
				Value: "abcx", Selection: text.Range{Start: 4, End: 4}, Composition: text.Range{Start: 3, End: 4},
				Composing: composing, TentativeComposition: tentative,
			},
		}
	}
	for _, scenario := range []struct {
		name string
		next *TextInput
		want bool
	}{
		{name: "different target", next: active(textInputTarget(8), true, true), want: true},
		{name: "state drops mark", next: active(textInputTarget(7), false, true), want: true},
		{name: "same live mark", next: active(textInputTarget(7), true, true), want: false},
		{name: "passive replacement", next: active(textInputTarget(8), false, false), want: false},
		{name: "disabled replacement", next: nil, want: false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if got := textInputReplacementEndsComposition(previous, current, scenario.next); got != scenario.want {
				t.Fatalf("replacement ends composition = %v, want %v", got, scenario.want)
			}
		})
	}
}

// The lost/discard route is bridge-observable: the previous handler sees one
// physical Cancel, and no second event follows once the active tentative ends
// or the capability is passive.
// A live mark dropped by a state push is a lost composition unless the event
// already carried a terminal phase or the returned snapshot is passive. This
// is the bridge's source-bound distinction; native updateText reports only the
// false case.
func TestTextStatePushEndsComposition(t *testing.T) {
	live := TextState{TentativeComposition: true}
	passive := TextState{}
	for _, scenario := range []struct {
		name  string
		event TextEvent
		next  TextState
		want  bool
	}{
		{name: "untagged idle push", event: TextEvent{Kind: TextInsert}, next: live, want: false},
		{name: "update idle push", event: TextEvent{Kind: TextSetMarked, CompositionPhase: text.CompositionUpdate}, next: live, want: false},
		{name: "result resolves", event: TextEvent{Kind: TextInsert, CompositionPhase: text.CompositionResult}, next: live, want: true},
		{name: "commit resolves", event: TextEvent{Kind: TextUnmark, CompositionPhase: text.CompositionCommit}, next: live, want: true},
		{name: "cancel resolves", event: TextEvent{Kind: TextSetMarked, CompositionPhase: text.CompositionCancel}, next: live, want: true},
		{name: "empty commit resolves", event: TextEvent{Kind: TextInsert, CompositionPhase: text.CompositionEmptyCommit}, next: live, want: true},
		{name: "passive snapshot is legacy", event: TextEvent{Kind: TextInsert}, next: passive, want: true},
		{name: "update passive is legacy", event: TextEvent{Kind: TextSetMarked, CompositionPhase: text.CompositionUpdate}, next: passive, want: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if got := textStatePushEndsComposition(scenario.event, scenario.next); got != scenario.want {
				t.Fatalf("state push ends composition = %v, want %v", got, scenario.want)
			}
		})
	}
}

// A native lost report raised inside updateText is deferred: no handler event
// may run while the state push is still on the stack, and one state push can
// flush at most one Cancel. The next state push then starts clean.
func TestCompositionLostDuringStatePushIsDeferredOnce(t *testing.T) {
	state := &runState{
		textInput:       TextInput{Terminal: true},
		textInputActive: true,
		textState: TextState{
			Value: "abcx", Selection: text.Range{Start: 4, End: 4}, Composition: text.Range{Start: 3, End: 4},
			Composing: true, TentativeComposition: true,
		},
	}
	var events []TextEvent
	state.textHandler = func(event TextEvent) (TextState, error) {
		events = append(events, event)
		return state.textState, nil
	}

	state.textUpdateDepth = 1
	state.compositionLost(nil)
	if len(events) != 0 {
		t.Fatalf("deferred lost ran %d handler event(s) inside the state push", len(events))
	}
	if !state.textCompositionLostPending {
		t.Fatal("lost report inside updateText was not recorded")
	}
	state.compositionLost(nil)
	state.textUpdateDepth = 0
	if len(events) != 0 {
		t.Fatalf("repeated deferred lost ran %d handler event(s) inside the state push", len(events))
	}

	if err := state.reconcilePendingCompositionLoss(nil); err != nil {
		t.Fatalf("flush deferred lost: %v", err)
	}
	if len(events) != 1 || events[0].CompositionPhase != text.CompositionCancel {
		t.Fatalf("deferred lost delivered %+v, want one Cancel", events)
	}
	if state.textCompositionLostPending {
		t.Fatal("pending lost marker survived the flush")
	}
	if err := state.reconcilePendingCompositionLoss(nil); err != nil {
		t.Fatalf("second flush: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("second flush produced %d events, want 1", len(events))
	}
}

func TestDeliverCompositionCancelIsGatedAndIdempotent(t *testing.T) {
	state := &runState{
		textInput:       TextInput{Terminal: true},
		textInputActive: true,
		textState: TextState{
			Value: "abcx", Selection: text.Range{Start: 4, End: 4}, Composition: text.Range{Start: 3, End: 4},
			Composing: true, TentativeComposition: true,
		},
	}
	var events []TextEvent
	state.textHandler = func(event TextEvent) (TextState, error) {
		events = append(events, event)
		return state.textState, nil
	}
	if err := deliverCompositionCancel(state, nil, true); err != nil {
		t.Fatalf("first lost: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("lost events = %d, want 1", len(events))
	}
	event := events[0]
	if event.Kind != TextSetMarked || event.Text != "" ||
		event.CompositionPhase != text.CompositionCancel ||
		event.HasReplacement || event.HasReplacementUTF16 {
		t.Fatalf("lost event = %+v, want untagged-empty Cancel shape", event)
	}
	// The real handler returns an idle state. A repeated lost is then a no-op.
	state.textState.Composing = false
	if err := deliverCompositionCancel(state, nil, true); err != nil {
		t.Fatalf("idempotent lost: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("repeated lost produced %d events, want 1", len(events))
	}
	// A passive capability never routes a tagged event, even if stale marking
	// still says composing.
	state.textState = TextState{Value: "abcx", Composing: true, TentativeComposition: false}
	if err := deliverCompositionCancel(state, nil, true); err != nil {
		t.Fatalf("passive lost: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("passive lost produced %d events, want 1", len(events))
	}
}
