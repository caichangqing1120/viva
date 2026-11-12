package text

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"unsafe"
)

// compositionApply applies one explicit event and requires the public handled
// result. It deliberately goes through the public API only.
func compositionApply(t *testing.T, editor *Editor, event CompositionEvent) {
	t.Helper()
	if !editor.ApplyComposition(event) {
		t.Fatalf("ApplyComposition(%+v) refused", event)
	}
}

func compositionReject(t *testing.T, editor *Editor, event CompositionEvent) {
	t.Helper()
	if editor.ApplyComposition(event) {
		t.Fatalf("ApplyComposition(%+v) was handled, want refusal", event)
	}
}

func compositionUpdate(text string, selected Range) CompositionEvent {
	return CompositionEvent{Phase: CompositionUpdate, Text: text, Selected: selected}
}

func TestCompositionEnableDisableAndIdempotence(t *testing.T) {
	var nilEditor *Editor
	if nilEditor.TentativeCompositionEnabled() {
		t.Fatal("nil editor reports tentative composition enabled")
	}
	if nilEditor.EnableTentativeComposition() || nilEditor.DisableTentativeComposition() {
		t.Fatal("nil editor enable/disable succeeded")
	}

	editor := NewEditor("abc")
	editor.SetSelection(Range{Start: 2, End: 2})
	before := conformanceStateOf(editor)
	if !editor.EnableTentativeComposition() {
		t.Fatal("first enable refused a fresh unrestricted insertion-mode editor")
	}
	if !editor.TentativeCompositionEnabled() {
		t.Fatal("enabled editor reports disabled")
	}
	if after := conformanceStateOf(editor); !reflect.DeepEqual(after, before) {
		t.Fatalf("enable changed visible state: %+v, want %+v", after, before)
	}
	// 768 of these bytes are the bidi.Resolver upstream's caret walk now keeps
	// between arrow keys; the rest is the accepted journal and history layout.
	if got := unsafe.Sizeof(Editor{}); got != 1136 {
		t.Fatalf("Editor size = %d, want 1136", got)
	}
	if !editor.EnableTentativeComposition() {
		t.Fatal("repeated enable refused an already-enabled source")
	}
	if !editor.DisableTentativeComposition() {
		t.Fatal("disable refused an enabled source")
	}
	if editor.TentativeCompositionEnabled() {
		t.Fatal("disabled editor reports enabled")
	}
	if !editor.DisableTentativeComposition() {
		t.Fatal("repeated disable refused an already-disabled source")
	}
	if after := conformanceStateOf(editor); !reflect.DeepEqual(after, before) {
		t.Fatalf("disable changed visible state: %+v, want %+v", after, before)
	}
}

func TestCompositionEnableRefusals(t *testing.T) {
	restricted := NewEditor("abc")
	restricted.Restrict(func(value rune) bool { return value >= '0' && value <= '9' }, 4)
	if restricted.EnableTentativeComposition() {
		t.Fatal("enable accepted a rune restriction")
	}

	maxed := NewEditor("abc")
	maxed.Restrict(nil, 4)
	if maxed.EnableTentativeComposition() {
		t.Fatal("enable accepted a maximum-length restriction")
	}

	overwrite := NewEditor("abc")
	overwrite.SetOverwrite(true)
	if !overwrite.EnableTentativeComposition() {
		t.Fatal("enable refused source overwrite mode")
	}

	legacy := NewEditor("abc")
	legacy.SetMarkedText("x", Range{})
	if legacy.EnableTentativeComposition() {
		t.Fatal("enable accepted an active legacy composition")
	}

	source := NewEditor("abc")
	if err := source.Transact(func(draft *Editor) error {
		if draft.EnableTentativeComposition() {
			return errors.New("private draft enabled tentative composition")
		}
		return nil
	}); err != nil {
		t.Fatalf("Transact refused the probe callback: %v", err)
	}
	if source.String() != "abc" || source.TentativeCompositionEnabled() {
		t.Fatal("private draft probe changed its source")
	}
}

func TestCompositionEnableAllowedWithOrdinaryHistory(t *testing.T) {
	editor := NewEditor("abc")
	editor.Insert("d")
	editor.SetSavePoint()
	text, selection := editor.String(), editor.Selection()
	index, runes := editor.historyIndex, editor.historyRunes
	if !editor.EnableTentativeComposition() {
		t.Fatal("enable refused a source with ordinary history and an ordinary save point")
	}
	if editor.String() != text || editor.Selection() != selection ||
		editor.historyIndex != index || editor.historyRunes != runes || !editor.AtSavePoint() {
		t.Fatal("enable changed ordinary history, save point or selection")
	}
	// Idempotent enable must stay true during an owned tentative.
	compositionApply(t, editor, compositionUpdate("x", Range{}))
	if !editor.EnableTentativeComposition() {
		t.Fatal("enable refused an already-enabled source during its tentative")
	}
}

func TestCompositionSelectedUpdateCancelCommit(t *testing.T) {
	editor := NewEditor("abc")
	editor.SetSelection(Range{Start: 1, End: 2})
	if !editor.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	compositionApply(t, editor, compositionUpdate("x", Range{Start: 0, End: 0}))
	if got := editor.String(); got != "axc" {
		t.Fatalf("update text = %q, want axc", got)
	}
	if selection := editor.Selection(); selection != (Range{Start: 1, End: 1}) {
		t.Fatalf("update selection = %+v, want caret 1", selection)
	}
	if marked, active := editor.Composition(); !active || marked != (Range{Start: 1, End: 2}) {
		t.Fatalf("marked range = %+v/%t, want 1..2 active", marked, active)
	}
	compositionApply(t, editor, CompositionEvent{Phase: CompositionCancel})
	if got := editor.String(); got != "ac" {
		t.Fatalf("cancel text = %q, want ac (pre-start deletion preserved)", got)
	}
	if _, active := editor.Composition(); active {
		t.Fatal("cancel left marked text active")
	}
	if editor.CanRedo() {
		t.Fatal("cancel retained redo")
	}
	if !editor.Undo() || editor.String() != "abc" {
		t.Fatalf("Undo after cancel = %q, want abc", editor.String())
	}

	committed := NewEditor("abc")
	committed.SetSelection(Range{Start: 1, End: 2})
	if !committed.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	compositionApply(t, committed, compositionUpdate("x", Range{}))
	compositionApply(t, committed, CompositionEvent{Phase: CompositionCommit})
	if got := committed.String(); got != "axc" {
		t.Fatalf("commit text = %q, want axc", got)
	}
	if _, active := committed.Composition(); active {
		t.Fatal("commit left marked text active")
	}
	if !committed.Undo() || committed.String() != "ac" {
		t.Fatalf("Undo after commit = %q, want ac", committed.String())
	}
	if !committed.Undo() || committed.String() != "abc" {
		t.Fatalf("second Undo after commit = %q, want abc", committed.String())
	}
}

func TestCompositionUpdateReplacesTentativeSuffix(t *testing.T) {
	editor := NewEditor("abc")
	editor.SetSelection(Range{Start: 1, End: 2})
	if !editor.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	compositionApply(t, editor, compositionUpdate("x", Range{}))
	compositionApply(t, editor, compositionUpdate("yz", Range{Start: 1, End: 1}))
	if got := editor.String(); got != "ayzc" {
		t.Fatalf("active update text = %q, want ayzc", got)
	}
	if marked, active := editor.Composition(); !active || marked != (Range{Start: 1, End: 3}) {
		t.Fatalf("active marked range = %+v/%t, want 1..3", marked, active)
	}
	if selection := editor.Selection(); selection != (Range{Start: 2, End: 2}) {
		t.Fatalf("active update selection = %+v, want caret 2", selection)
	}
	// Explicit replacement while active is outside the proven core.
	compositionReject(t, editor, CompositionEvent{
		Phase: CompositionUpdate, Text: "z", HasReplacement: true, Replacement: Range{Start: 0, End: 1},
	})
	if got := editor.String(); got != "ayzc" {
		t.Fatalf("rejected active replacement changed text to %q", got)
	}
	compositionApply(t, editor, CompositionEvent{Phase: CompositionCancel})
	if editor.String() != "ac" {
		t.Fatalf("cancel after active update = %q, want ac", editor.String())
	}
}

func TestCompositionEmptyUpdateAndEmptyCommit(t *testing.T) {
	inactive := NewEditor("abc")
	if !inactive.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	compositionApply(t, inactive, CompositionEvent{Phase: CompositionUpdate})
	if got := inactive.String(); got != "abc" {
		t.Fatalf("empty inactive update text = %q, want abc", got)
	}
	if _, active := inactive.Composition(); active {
		t.Fatal("empty inactive update started a mark")
	}

	active := NewEditor("abc")
	active.SetSelection(Range{Start: 1, End: 2})
	if !active.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	compositionApply(t, active, compositionUpdate("x", Range{}))
	compositionApply(t, active, CompositionEvent{Phase: CompositionUpdate})
	if got := active.String(); got != "ac" {
		t.Fatalf("empty active update text = %q, want ac", got)
	}
	if _, marked := active.Composition(); marked {
		t.Fatal("empty active update left a mark")
	}

	emptyCommit := NewEditor("abc")
	emptyCommit.SetSelection(Range{Start: 1, End: 2})
	if !emptyCommit.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	compositionApply(t, emptyCommit, CompositionEvent{Phase: CompositionEmptyCommit})
	if got := emptyCommit.String(); got != "ac" {
		t.Fatalf("inactive empty commit text = %q, want ac", got)
	}
	if emptyCommit.CanRedo() {
		t.Fatal("inactive empty commit retained redo")
	}

	redo := NewEditor("abc")
	if !redo.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	redo.Insert("d")
	if !redo.Undo() || !redo.CanRedo() {
		t.Fatal("redo setup failed")
	}
	compositionApply(t, redo, CompositionEvent{Phase: CompositionEmptyCommit})
	if redo.String() != "abc" || redo.CanRedo() {
		t.Fatalf("byte-less empty commit = %q redo=%t, want abc/false", redo.String(), redo.CanRedo())
	}

	activeEmpty := NewEditor("abc")
	activeEmpty.SetSelection(Range{Start: 1, End: 2})
	if !activeEmpty.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	compositionApply(t, activeEmpty, compositionUpdate("x", Range{}))
	compositionApply(t, activeEmpty, CompositionEvent{Phase: CompositionEmptyCommit})
	if got := activeEmpty.String(); got != "ac" {
		t.Fatalf("active empty commit text = %q, want ac", got)
	}
	if _, marked := activeEmpty.Composition(); marked {
		t.Fatal("active empty commit left a mark")
	}
}

func TestCompositionResultExplicitReplacement(t *testing.T) {
	editor := NewEditor("abc")
	editor.SetSelection(Range{Start: 1, End: 2})
	if !editor.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	compositionApply(t, editor, compositionUpdate("x", Range{}))
	compositionApply(t, editor, CompositionEvent{
		Phase: CompositionResult, Text: "r",
		HasReplacement: true, Replacement: Range{Start: 0, End: 1},
	})
	if got := editor.String(); got != "rc" {
		t.Fatalf("selected result = %q, want rc", got)
	}
	if _, marked := editor.Composition(); marked {
		t.Fatal("result left a mark")
	}
	for index, want := range []string{"c", "ac", "abc"} {
		if !editor.Undo() {
			t.Fatalf("Undo %d refused", index+1)
		}
		if got := editor.String(); got != want {
			t.Fatalf("Undo %d text = %q, want %q", index+1, got, want)
		}
	}
}

func TestCompositionResultUnicodeReplacement(t *testing.T) {
	editor := NewEditor("aé𝄞b")
	editor.SetSelection(Range{Start: 1, End: 3})
	if !editor.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	compositionApply(t, editor, compositionUpdate("x", Range{}))
	compositionApply(t, editor, CompositionEvent{
		Phase: CompositionResult, Text: "r",
		HasReplacement: true, Replacement: Range{Start: 0, End: 1},
	})
	if got := editor.String(); got != "rb" {
		t.Fatalf("unicode result = %q, want rb", got)
	}
	for index, want := range []string{"b", "ab", "aé𝄞b"} {
		if !editor.Undo() {
			t.Fatalf("Undo %d refused", index+1)
		}
		if got := editor.String(); got != want {
			t.Fatalf("Undo %d text = %q, want %q", index+1, got, want)
		}
	}
}

func TestCompositionResultRawUTF16AfterRollback(t *testing.T) {
	editor := NewEditor("abcd")
	editor.SetSelection(Range{Start: 1, End: 2})
	if !editor.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	compositionApply(t, editor, compositionUpdate("é𝄞", Range{}))
	if got := editor.String(); got != "aé𝄞cd" {
		t.Fatalf("visible preedit = %q, want aé𝄞cd", got)
	}
	compositionApply(t, editor, CompositionEvent{
		Phase: CompositionResult, Text: "r",
		HasReplacementUTF16: true,
		ReplacementUTF16:    UTF16Range{Start: 5, Length: 1},
	})
	if got := editor.String(); got != "acdr" {
		t.Fatalf("raw post-rollback result = %q, want acdr", got)
	}

	split := NewEditor("a𝄞b")
	if !split.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	compositionApply(t, split, CompositionEvent{
		Phase: CompositionResult, Text: "r",
		HasReplacementUTF16: true,
		// UTF-16 boundary after a is 1; 2 is inside the astral surrogate pair.
		ReplacementUTF16: UTF16Range{Start: 2, Length: 0},
	})
	if got := split.String(); got != "a𝄞br" {
		t.Fatalf("split-surrogate fallback = %q, want a𝄞br", got)
	}

	negative := NewEditor("abc")
	if !negative.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	compositionReject(t, negative, CompositionEvent{
		Phase: CompositionResult, Text: "r",
		HasReplacementUTF16: true, ReplacementUTF16: UTF16Range{Start: -1, Length: 0},
	})
	if got := negative.String(); got != "abc" {
		t.Fatalf("negative UTF-16 range changed text to %q", got)
	}
}

func TestCompositionResultGroupingAndNoPreedit(t *testing.T) {
	selected := NewEditor("abc")
	selected.SetSelection(Range{Start: 1, End: 2})
	if !selected.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	compositionApply(t, selected, CompositionEvent{Phase: CompositionResult, Text: "r𝄞"})
	if got := selected.String(); got != "ar𝄞c" {
		t.Fatalf("selected multi-scalar result = %q, want ar𝄞c", got)
	}
	if !selected.Undo() || selected.String() != "arc" {
		t.Fatalf("first source group Undo = %q, want arc", selected.String())
	}
	if !selected.Undo() || selected.String() != "abc" {
		t.Fatalf("first-scalar replacement group Undo = %q, want abc", selected.String())
	}

	noPreedit := NewEditor("abc")
	if !noPreedit.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	compositionApply(t, noPreedit, CompositionEvent{Phase: CompositionResult, Text: "r"})
	compositionApply(t, noPreedit, CompositionEvent{Phase: CompositionResult, Text: "s"})
	if got := noPreedit.String(); got != "abcrs" {
		t.Fatalf("coalesced results = %q, want abcrs", got)
	}
	if _, marked := noPreedit.Composition(); marked {
		t.Fatal("result without preedit created a tentative mark")
	}
	if !noPreedit.Undo() || noPreedit.String() != "abc" {
		t.Fatalf("coalesced results Undo = %q, want abc", noPreedit.String())
	}

	implicitEmpty := NewEditor("abc")
	implicitEmpty.SetSelection(Range{Start: 1, End: 2})
	if !implicitEmpty.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	compositionApply(t, implicitEmpty, CompositionEvent{Phase: CompositionResult})
	if got := implicitEmpty.String(); got != "abc" {
		t.Fatalf("empty result deleted an implicit selection: %q", got)
	}
}

func TestCompositionCommitDoesNotForceGroup(t *testing.T) {
	editor := NewEditor("abc")
	editor.SetSelection(Range{Start: 1, End: 2})
	if !editor.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	compositionApply(t, editor, compositionUpdate("x", Range{Start: 1, End: 1}))
	compositionApply(t, editor, CompositionEvent{Phase: CompositionCommit})
	editor.Insert("y")
	if got := editor.String(); got != "axyc" {
		t.Fatalf("insert after commit = %q, want axyc", got)
	}
	if !editor.Undo() || editor.String() != "ac" {
		t.Fatalf("commit forced a group break: Undo = %q, want ac", editor.String())
	}
}

func TestCompositionLegacyEscapes(t *testing.T) {
	newActive := func(t *testing.T) *Editor {
		t.Helper()
		editor := NewEditor("abc")
		editor.SetSelection(Range{Start: 1, End: 2})
		if !editor.EnableTentativeComposition() {
			t.Fatal("enable refused")
		}
		compositionApply(t, editor, compositionUpdate("x", Range{}))
		if got := editor.String(); got != "axc" {
			t.Fatalf("setup text = %q, want axc", got)
		}
		return editor
	}

	t.Run("insert targets visible composition", func(t *testing.T) {
		editor := NewEditor("abc")
		cursor, _, ok := editor.TrackHistory()
		if !ok {
			t.Fatal("TrackHistory refused before the escape")
		}
		editor.SetSelection(Range{Start: 1, End: 2})
		if !editor.EnableTentativeComposition() {
			t.Fatal("enable refused")
		}
		compositionApply(t, editor, compositionUpdate("x", Range{}))
		if got := editor.String(); got != "axc" {
			t.Fatalf("setup text = %q, want axc", got)
		}
		editor.Insert("r")
		if got := editor.String(); got != "arc" {
			t.Fatalf("legacy Insert text = %q, want arc", got)
		}
		state := editor.compositionState()
		if state == nil || state.active {
			t.Fatalf("legacy Insert left an owned tentative active: %+v", state)
		}
		if editor.CanRedo() {
			t.Fatal("legacy Insert retained redo")
		}
		if _, active := editor.Composition(); active {
			t.Fatal("legacy Insert left the mark active")
		}
		if frames, latest, ok := editor.HistorySince(cursor); ok || frames != nil {
			t.Fatalf("legacy escape kept producer metadata available: %+v/%+v", frames, latest)
		}
		if !editor.ApplyComposition(compositionUpdate("q", Range{})) || editor.String() != "arqc" {
			t.Fatalf("fresh event after legacy Insert = %q, want arqc", editor.String())
		}
	})

	t.Run("set marked text targets visible composition", func(t *testing.T) {
		editor := newActive(t)
		editor.SetMarkedText("r", Range{})
		if got := editor.String(); got != "arc" {
			t.Fatalf("legacy SetMarkedText text = %q, want arc", got)
		}
		if marked, active := editor.Composition(); !active || marked != (Range{Start: 1, End: 2}) {
			t.Fatalf("legacy SetMarkedText mark = %+v/%t, want 1..2", marked, active)
		}
	})

	t.Run("unmark keeps bytes and ends residue", func(t *testing.T) {
		editor := newActive(t)
		editor.Unmark()
		if got := editor.String(); got != "axc" {
			t.Fatalf("Unmark text = %q, want axc", got)
		}
		if _, active := editor.Composition(); active {
			t.Fatal("Unmark left the mark active")
		}
		// Capability stays enabled; a fresh event may start after the residue ends.
		compositionApply(t, editor, compositionUpdate("y", Range{}))
		if got := editor.String(); got != "ayxc" {
			t.Fatalf("fresh update after residue = %q, want ayxc", got)
		}
	})

	t.Run("selection keeps bytes and ends residue", func(t *testing.T) {
		editor := newActive(t)
		editor.SetSelection(Range{Start: 0, End: 0})
		if got := editor.String(); got != "axc" {
			t.Fatalf("SetSelection text = %q, want axc", got)
		}
		if _, active := editor.Composition(); active {
			t.Fatal("SetSelection left the mark active")
		}
	})

	t.Run("delete composition", func(t *testing.T) {
		editor := newActive(t)
		if !editor.DeleteBackward() {
			t.Fatal("DeleteBackward refused")
		}
		if got := editor.String(); got != "ac" {
			t.Fatalf("DeleteBackward text = %q, want ac", got)
		}
	})

	t.Run("undo and redo refuse", func(t *testing.T) {
		editor := newActive(t)
		if editor.Undo() || editor.Redo() {
			t.Fatal("Undo/Redo did not refuse an owned tentative")
		}
		if got := editor.String(); got != "axc" {
			t.Fatalf("refused Undo/Redo changed text to %q", got)
		}
	})

	t.Run("batch and transaction refuse unsealed", func(t *testing.T) {
		editor := newActive(t)
		if err := editor.ReplaceRanges([]Replacement{{Range: Range{Start: 0, End: 0}, Text: "z"}}); err == nil {
			t.Fatal("ReplaceRanges accepted an active tentative")
		}
		if err := editor.Transact(func(draft *Editor) error { return nil }); err == nil {
			t.Fatal("Transact accepted an active tentative")
		}
		if got := editor.String(); got != "axc" {
			t.Fatalf("rejected batch changed text to %q", got)
		}
		if _, active := editor.Composition(); !active {
			t.Fatal("rejected batch sealed the tentative")
		}
	})
}

func TestCompositionActiveClearAndConfiguration(t *testing.T) {
	t.Run("active clear preserves bytes and refuses residue", func(t *testing.T) {
		editor := NewEditor("abc")
		editor.SetSelection(Range{Start: 1, End: 2})
		if !editor.EnableTentativeComposition() {
			t.Fatal("enable refused")
		}
		cursor, _, ok := editor.TrackHistory()
		if !ok {
			t.Fatal("TrackHistory refused")
		}
		compositionApply(t, editor, compositionUpdate("x", Range{}))
		editor.ClearHistory()
		if got := editor.String(); got != "axc" {
			t.Fatalf("active Clear text = %q, want axc", got)
		}
		if _, active := editor.Composition(); !active {
			t.Fatal("active Clear dropped the visible UI marking")
		}
		if editor.CanUndo() {
			t.Fatal("active Clear retained ordinary Undo")
		}
		if frames, _, ok := editor.HistorySince(cursor); ok || frames != nil {
			t.Fatal("active Clear rebootstrapped producer metadata")
		}
		compositionReject(t, editor, CompositionEvent{Phase: CompositionResult, Text: "r"})
		editor.Unmark()
		compositionApply(t, editor, compositionUpdate("y", Range{}))
		if got := editor.String(); got != "ayxc" {
			t.Fatalf("post-clear fresh update = %q, want ayxc", got)
		}
	})

	t.Run("restrict seals and disables", func(t *testing.T) {
		editor := NewEditor("abc")
		editor.SetSelection(Range{Start: 1, End: 2})
		if !editor.EnableTentativeComposition() {
			t.Fatal("enable refused")
		}
		compositionApply(t, editor, compositionUpdate("x", Range{}))
		editor.Restrict(func(value rune) bool { return value >= '0' && value <= '9' }, 3)
		if editor.TentativeCompositionEnabled() {
			t.Fatal("Restrict left the capability enabled")
		}
		if got := editor.String(); got != "axc" {
			t.Fatalf("Restrict changed visible bytes to %q", got)
		}
		if _, active := editor.Composition(); !active {
			t.Fatal("Restrict dropped the visible UI marking")
		}
		compositionReject(t, editor, compositionUpdate("1", Range{}))
	})

	t.Run("set overwrite is a flag change during a live tentative", func(t *testing.T) {
		editor := NewEditor("abc")
		editor.SetSelection(Range{Start: 0, End: 0})
		if !editor.EnableTentativeComposition() {
			t.Fatal("enable refused")
		}
		// Insertion preedit x, then turn overwrite on while it is live.
		compositionApply(t, editor, compositionUpdate("x", Range{}))
		if got := editor.String(); got != "xabc" {
			t.Fatalf("insertion preedit = %q, want xabc", got)
		}
		editor.SetOverwrite(true)
		if !editor.TentativeCompositionEnabled() {
			t.Fatal("SetOverwrite(true) disabled a live tentative")
		}
		if _, active := editor.Composition(); !active {
			t.Fatal("SetOverwrite(true) ended the live marking")
		}
		// The next event uses the new flag; rollback still reverses the units
		// actually recorded under the old one.
		compositionApply(t, editor, compositionUpdate("y", Range{}))
		if got := editor.String(); got != "ybc" {
			t.Fatalf("overwrite update after toggle = %q, want ybc", got)
		}
		compositionApply(t, editor, CompositionEvent{Phase: CompositionCancel})
		if got := editor.String(); got != "abc" {
			t.Fatalf("cancel after toggle = %q, want abc", got)
		}
	})
}

func TestCompositionRefusalsDoNotMutate(t *testing.T) {
	editor := NewEditor("abc")
	before := conformanceStateOf(editor)
	compositionReject(t, editor, CompositionEvent{Phase: CompositionUpdate, Text: "x"})
	compositionReject(t, editor, CompositionEvent{Phase: CompositionResult, Text: "x"})
	if after := conformanceStateOf(editor); !reflect.DeepEqual(after, before) {
		t.Fatalf("disabled source mutated: %+v, want %+v", after, before)
	}

	if !editor.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	compositionReject(t, editor, CompositionEvent{Phase: CompositionNone})
	compositionReject(t, editor, CompositionEvent{Phase: CompositionPhase(200)})
	compositionReject(t, editor, CompositionEvent{Phase: CompositionUpdate, Text: string([]byte{0xff})})
	compositionReject(t, editor, CompositionEvent{Phase: CompositionResult, Text: string([]byte{0xff})})
	if got := editor.String(); got != "abc" {
		t.Fatalf("invalid phase or UTF-8 changed text to %q", got)
	}
}

func TestCompositionCapacityRejectPrecedesMutation(t *testing.T) {
	editor := NewEditor("abc")
	editor.SetSelection(Range{Start: 1, End: 2})
	if !editor.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	before := conformanceStateOf(editor)
	oversized := strings.Repeat("x", maxSingleEditRunes+1)
	compositionReject(t, editor, compositionUpdate(oversized, Range{}))
	if after := conformanceStateOf(editor); !reflect.DeepEqual(after, before) {
		t.Fatalf("capacity refusal mutated state: %+v, want %+v", after, before)
	}
}

func TestCompositionSelectedRedoRestoresUpdateSelection(t *testing.T) {
	editor := NewEditor("abc")
	editor.SetSelection(Range{Start: 1, End: 2})
	if !editor.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	compositionApply(t, editor, compositionUpdate("x", Range{Start: 0, End: 0}))
	compositionApply(t, editor, CompositionEvent{Phase: CompositionCommit})
	if got := editor.Caret(); got != 1 {
		t.Fatalf("selected update caret = %d, want 1", got)
	}
	if !editor.Undo() || editor.String() != "ac" {
		t.Fatalf("Undo after selected commit = %q, want ac", editor.String())
	}
	if !editor.Redo() || editor.String() != "axc" {
		t.Fatalf("Redo after selected commit = %q, want axc", editor.String())
	}
	if got := editor.Caret(); got != 1 {
		t.Fatalf("Redo caret = %d, want the event's Selected 1", got)
	}
}

func TestCompositionEqualResultRetainsOrdinaryUnit(t *testing.T) {
	editor := NewEditor("abc")
	if !editor.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	editor.SetSelection(Range{Start: 1, End: 2})
	if !editor.ApplyComposition(CompositionEvent{Phase: CompositionResult, Text: "b"}) {
		t.Fatal("equal native replacement refused")
	}
	if got := editor.String(); got != "abc" {
		t.Fatalf("equal result text = %q, want abc", got)
	}
	if !editor.CanUndo() {
		t.Fatal("equal result lost its ordinary unit without a fake save point")
	}
	if !editor.Undo() || editor.String() != "abc" || !editor.CanRedo() {
		t.Fatalf("equal result Undo/Redo = %q/%t", editor.String(), editor.CanRedo())
	}
	if editor.AtSavePoint() {
		t.Fatal("equal result fabricated a saved boundary")
	}
}

func TestCompositionPhysicalCapacityDoesNotUseMetadataEstimate(t *testing.T) {
	const count = 700000
	editor := NewEditor("abc")
	if !editor.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	if !editor.ApplyComposition(compositionUpdate(strings.Repeat("x", count), Range{Start: count, End: count})) {
		t.Fatal("physically retainable preedit was rejected by a metadata estimate")
	}
	if editor.RuneCount() != count+3 {
		t.Fatalf("preedit length = %d, want %d", editor.RuneCount(), count+3)
	}
	if !editor.ApplyComposition(CompositionEvent{Phase: CompositionCancel}) || editor.String() != "abc" {
		t.Fatalf("physical preedit was not cancelable: %d runes", editor.RuneCount())
	}
}

func TestCompositionRawUTF16HugePositiveLengthFallsToEnd(t *testing.T) {
	editor := NewEditor("a𝄞b")
	if !editor.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	editor.MoveTo(0, false)
	maxLength := int(^uint(0) >> 1)
	if !editor.ApplyComposition(CompositionEvent{
		Phase: CompositionResult, Text: "r",
		HasReplacementUTF16: true, ReplacementUTF16: UTF16Range{Start: 1, Length: maxLength},
	}) {
		t.Fatal("huge positive relative UTF-16 length rejected")
	}
	if got := editor.String(); got != "ar" {
		t.Fatalf("huge relative end fallback = %q, want ar", got)
	}
}

func TestCompositionRejectedEnableLeavesNoNewState(t *testing.T) {
	editor := NewEditor("")
	editor.Insert(strings.Repeat("x", maxSingleEditRunes))
	if !editor.CanUndo() || editor.journal != nil {
		t.Fatal("budget-edge setup failed")
	}
	if editor.EnableTentativeComposition() || editor.TentativeCompositionEnabled() {
		t.Fatal("enable accepted a sidecar beyond the single-action budget")
	}
	if editor.journal != nil || !editor.CanUndo() || editor.RuneCount() != maxSingleEditRunes {
		t.Fatal("rejected enable retained new source state or pruned ordinary history")
	}
}

func TestCompositionLayoutRecord(t *testing.T) {
	t.Logf("Editor=%d historyEdit=%d editJournal=%d historyJournal=%d compositionState=%d historyEvent=%d compositionHeaderRunes=%d historyEventRunes=%d historyHeaderRunes=%d stepRunes=%d frameRunes=%d",
		unsafe.Sizeof(Editor{}), unsafe.Sizeof(historyEdit{}), unsafe.Sizeof(editJournal{}), unsafe.Sizeof(historyJournal{}),
		unsafe.Sizeof(compositionState{}), unsafe.Sizeof(historyEvent{}), compositionHeaderRunes,
		historyEventRunes, historyHeaderRunes, historyStepRunes, historyFrameRunes)
}

// BenchmarkCompositionDisabledApplyLocal measures the ordinary disabled
// rejection path with a local editor/event and no forced sink. It must not
// allocate or force the local editor to the heap through a sidecar callback.
func BenchmarkCompositionDisabledApplyLocal(b *testing.B) {
	editor := NewEditor("abc")
	event := CompositionEvent{Phase: CompositionResult, Text: "x"}
	b.ReportAllocs()
	for b.Loop() {
		if editor.ApplyComposition(event) {
			b.Fatal("disabled Apply was handled")
		}
	}
}

func BenchmarkCompositionEnableDisable(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		editor := NewEditor("abc")
		editor.EnableTentativeComposition()
		editor.DisableTentativeComposition()
	}
}

func BenchmarkCompositionUpdateCancel(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		editor := NewEditor("abc")
		editor.EnableTentativeComposition()
		editor.SetSelection(Range{Start: 1, End: 2})
		editor.ApplyComposition(CompositionEvent{Phase: CompositionUpdate, Text: "é𝄞", Selected: Range{Start: 2, End: 2}})
		editor.ApplyComposition(CompositionEvent{Phase: CompositionCancel})
	}
}

func BenchmarkCompositionResult(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		editor := NewEditor("abc")
		editor.EnableTentativeComposition()
		editor.SetSelection(Range{Start: 1, End: 2})
		editor.ApplyComposition(CompositionEvent{Phase: CompositionResult, Text: "r𝄞"})
	}
}

func TestCompositionSelectedResultFollowingInputGrouping(t *testing.T) {
	editor := NewEditor("abc")
	editor.SetSelection(Range{Start: 1, End: 2})
	if !editor.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	if !editor.ApplyComposition(CompositionEvent{Phase: CompositionResult, Text: "r𝄞"}) {
		t.Fatal("selected result refused")
	}
	editor.Insert("z")
	if got := editor.String(); got != "ar𝄞zc" {
		t.Fatalf("following input text = %q, want ar𝄞zc", got)
	}
	if !editor.Undo() || editor.String() != "arc" {
		t.Fatalf("following input did not join the trailing scalar group: %q", editor.String())
	}
	if !editor.Undo() || editor.String() != "abc" {
		t.Fatalf("first-scalar replacement group Undo = %q, want abc", editor.String())
	}
}
