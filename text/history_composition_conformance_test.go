package text

import (
	"reflect"
	"slices"
	"testing"
)

// conformanceState is the externally visible editor state used by the
// composition/refusal matrix. It deliberately does not include any producer
// representation: the producer contract is observed only through
// HistorySince/TrackHistory.
type conformanceState struct {
	text             string
	selection        Range
	caret            int
	composing        bool
	canUndo, canRedo bool
}

func conformanceStateOf(editor *Editor) conformanceState {
	_, composing := editor.Composition()
	return conformanceState{
		text:      editor.String(),
		selection: editor.Selection(),
		caret:     editor.Caret(),
		composing: composing,
		canUndo:   editor.CanUndo(),
		canRedo:   editor.CanRedo(),
	}
}

func conformanceMustTrack(t *testing.T, editor *Editor) HistoryCursor {
	t.Helper()
	cursor, _, ok := editor.TrackHistory()
	if !ok {
		t.Fatal("TrackHistory refused a fresh supported source")
	}
	return cursor
}

func conformanceMustRead(t *testing.T, editor *Editor, cursor HistoryCursor) ([]HistoryFrame, HistoryCursor) {
	t.Helper()
	frames, latest, ok := editor.HistorySince(cursor)
	if !ok {
		t.Fatalf("HistorySince(%+v) refused before the first composition event", cursor)
	}
	return frames, latest
}

// conformanceRequireRefused pins the public unsupported-source contract: no
// prefix read, no fabricated suffix and no new bootstrap. It intentionally does
// not assert a private guard pointer or function-table shape.
func conformanceRequireRefused(t *testing.T, editor *Editor, cursor HistoryCursor, stage string) {
	t.Helper()
	if frames, latest, ok := editor.HistorySince(cursor); ok || frames != nil {
		t.Fatalf("%s: unavailable producer served frames %+v at %+v", stage, frames, latest)
	}
	if got, bytes, ok := editor.TrackHistory(); ok || got != (HistoryCursor{}) || bytes != 0 {
		t.Fatalf("%s: unavailable producer rebootstrapped as %+v/%d/%t", stage, got, bytes, ok)
	}
}

// conformanceAssertUnavailableGuardAccounting checks the accepted 132.4A
// retained-guard charge without requiring that a guard be retained. A future
// shared-tombstone representation may release it; if retained, it must remain
// fully charged. No pointer identity is asserted.
func conformanceAssertUnavailableGuardAccounting(t *testing.T, editor *Editor, stage string) {
	t.Helper()
	if editor.journal == nil || editor.journal.history == nil {
		return
	}
	history := editor.journal.history
	if history.available {
		t.Fatalf("%s: composition source became available again", stage)
	}
	if history.cost < historyHeaderRunes {
		t.Fatalf("%s: retained unavailable guard cost = %d runes, want at least header %d", stage, history.cost, historyHeaderRunes)
	}
	if charged := editor.historyCost() - editor.historyRunes; charged < historyHeaderRunes {
		t.Fatalf("%s: retained unavailable guard charge = %d runes, want at least header %d", stage, charged, historyHeaderRunes)
	}
}

func conformanceCloneFrames(frames []HistoryFrame) []HistoryFrame {
	clone := append([]HistoryFrame(nil), frames...)
	for index := range clone {
		clone[index].Steps = append([]HistoryStep(nil), frames[index].Steps...)
	}
	return clone
}

func TestHistoryCompositionFirstEventInvalidatesBeforeFrame(t *testing.T) {
	const digitFilter = '0'
	acceptDigits := func(value rune) bool { return value >= digitFilter && value <= '9' }
	cases := []struct {
		name            string
		value           string
		accept          func(rune) bool
		selection       Range
		wantText        string
		wantSelection   Range
		wantComposing   bool
		wantComposition Range
	}{
		{"nonempty", "x", nil, Range{Start: 1, End: 2}, "axcd", Range{Start: 1, End: 1}, true, Range{Start: 1, End: 2}},
		{"empty-no-bytes", "", nil, Range{Start: 2, End: 2}, "abcd", Range{Start: 2, End: 2}, true, Range{Start: 2, End: 2}},
		{"empty-replacing-selection", "", nil, Range{Start: 1, End: 2}, "acd", Range{Start: 1, End: 1}, true, Range{Start: 1, End: 1}},
		{"all-filtered", "é", acceptDigits, Range{Start: 1, End: 2}, "acd", Range{Start: 1, End: 1}, false, Range{}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			editor := NewEditor("abc")
			cursor := conformanceMustTrack(t, editor)
			editor.Insert("d")
			frames, latest := conformanceMustRead(t, editor, cursor)
			if len(frames) != 1 || len(frames[0].Steps) != 1 {
				t.Fatalf("supported prefix = %+v, want one complete frame", frames)
			}
			prefix := conformanceCloneFrames(frames)

			editor.SetSelection(test.selection)
			if test.accept != nil {
				editor.Restrict(test.accept, 0)
			}
			editor.SetMarkedText(test.value, Range{})

			if !reflect.DeepEqual(frames, prefix) {
				t.Fatalf("previously returned prefix changed after first composition: %+v, want %+v", frames, prefix)
			}
			state := conformanceStateOf(editor)
			if state.text != test.wantText || state.selection != test.wantSelection || state.composing != test.wantComposing {
				t.Fatalf("first composition state = %+v, want text %q selection %+v composing %t",
					state, test.wantText, test.wantSelection, test.wantComposing)
			}
			if composition, composing := editor.Composition(); composing != test.wantComposing {
				t.Fatalf("Composition() active = %t, want %t", composing, test.wantComposing)
			} else if composing && composition != test.wantComposition {
				t.Fatalf("Composition() range = %+v, want %+v", composition, test.wantComposition)
			}
			if frames, latestAfter, ok := editor.HistorySince(latest); ok || frames != nil {
				t.Fatalf("first composition published a successful ordinary frame: %+v/%+v/%t", frames, latestAfter, ok)
			}
			conformanceRequireRefused(t, editor, cursor, test.name)
		})
	}
}

func TestHistoryCompositionMarkedUpdatesAndEndingsRefuse(t *testing.T) {
	type scenario struct {
		name            string
		apply           func(*Editor)
		want            conformanceState
		wantComposition Range
		undo, redo      conformanceState
	}
	// Every case starts from "abc", tracks, selects b and marks "x": axc with
	// composition [1,2) and one ordinary composition unit. Expected states are
	// the existing editor outcomes, not a tentative-history comparison.
	cases := []scenario{
		{
			name:            "marked-update",
			apply:           func(editor *Editor) { editor.SetMarkedText("xy", Range{}) },
			want:            conformanceState{text: "axyc", selection: Range{Start: 1, End: 1}, caret: 1, composing: true, canUndo: true},
			wantComposition: Range{Start: 1, End: 3},
			undo:            conformanceState{text: "abc", selection: Range{Start: 1, End: 2}, caret: 2, canRedo: true},
			redo:            conformanceState{text: "axyc", selection: Range{Start: 1, End: 1}, caret: 1, canUndo: true},
		},
		{
			name:  "insert",
			apply: func(editor *Editor) { editor.Insert("r") },
			want:  conformanceState{text: "arc", selection: Range{Start: 2, End: 2}, caret: 2, canUndo: true},
			undo:  conformanceState{text: "abc", selection: Range{Start: 1, End: 2}, caret: 2, canRedo: true},
			redo:  conformanceState{text: "arc", selection: Range{Start: 2, End: 2}, caret: 2, canUndo: true},
		},
		{
			name:  "unmark",
			apply: (*Editor).Unmark,
			want:  conformanceState{text: "axc", selection: Range{Start: 1, End: 1}, caret: 1, canUndo: true},
			undo:  conformanceState{text: "abc", selection: Range{Start: 1, End: 2}, caret: 2, canRedo: true},
			redo:  conformanceState{text: "axc", selection: Range{Start: 1, End: 1}, caret: 1, canUndo: true},
		},
		{
			name:  "set-selection",
			apply: func(editor *Editor) { editor.SetSelection(Range{Start: 2, End: 3}) },
			want:  conformanceState{text: "axc", selection: Range{Start: 2, End: 3}, caret: 3, canUndo: true},
			undo:  conformanceState{text: "abc", selection: Range{Start: 1, End: 2}, caret: 2, canRedo: true},
			redo:  conformanceState{text: "axc", selection: Range{Start: 1, End: 1}, caret: 1, canUndo: true},
		},
		{
			name:  "move-to",
			apply: func(editor *Editor) { editor.MoveTo(3, false) },
			want:  conformanceState{text: "axc", selection: Range{Start: 3, End: 3}, caret: 3, canUndo: true},
			undo:  conformanceState{text: "abc", selection: Range{Start: 1, End: 2}, caret: 2, canRedo: true},
			redo:  conformanceState{text: "axc", selection: Range{Start: 1, End: 1}, caret: 1, canUndo: true},
		},
		{
			name:  "move-left",
			apply: func(editor *Editor) { editor.MoveLeft(false) },
			want:  conformanceState{text: "axc", selection: Range{Start: 0, End: 0}, caret: 0, canUndo: true},
			undo:  conformanceState{text: "abc", selection: Range{Start: 1, End: 2}, caret: 2, canRedo: true},
			redo:  conformanceState{text: "axc", selection: Range{Start: 1, End: 1}, caret: 1, canUndo: true},
		},
		{
			name:  "move-right",
			apply: func(editor *Editor) { editor.MoveRight(false) },
			want:  conformanceState{text: "axc", selection: Range{Start: 2, End: 2}, caret: 2, canUndo: true},
			undo:  conformanceState{text: "abc", selection: Range{Start: 1, End: 2}, caret: 2, canRedo: true},
			redo:  conformanceState{text: "axc", selection: Range{Start: 1, End: 1}, caret: 1, canUndo: true},
		},
		{
			name:  "delete-backward",
			apply: func(editor *Editor) { editor.DeleteBackward() },
			want:  conformanceState{text: "ac", selection: Range{Start: 1, End: 1}, caret: 1, canUndo: true},
			undo:  conformanceState{text: "axc", selection: Range{Start: 1, End: 1}, caret: 1, canUndo: true, canRedo: true},
			redo:  conformanceState{text: "ac", selection: Range{Start: 1, End: 1}, caret: 1, canUndo: true},
		},
		{
			name:  "delete-forward",
			apply: func(editor *Editor) { editor.DeleteForward() },
			want:  conformanceState{text: "ac", selection: Range{Start: 1, End: 1}, caret: 1, canUndo: true},
			undo:  conformanceState{text: "axc", selection: Range{Start: 1, End: 1}, caret: 1, canUndo: true, canRedo: true},
			redo:  conformanceState{text: "ac", selection: Range{Start: 1, End: 1}, caret: 1, canUndo: true},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			editor := NewEditor("abc")
			cursor := conformanceMustTrack(t, editor)
			editor.SetSelection(Range{Start: 1, End: 2})
			editor.SetMarkedText("x", Range{})
			conformanceRequireRefused(t, editor, cursor, test.name+"/first-event")

			test.apply(editor)
			if got := conformanceStateOf(editor); got != test.want {
				t.Fatalf("after %s = %+v, want %+v", test.name, got, test.want)
			}
			if _, composing := editor.Composition(); composing != test.want.composing {
				t.Fatalf("%s: Composition() active = %t, want %t", test.name, composing, test.want.composing)
			} else if composing {
				if composition, _ := editor.Composition(); composition != test.wantComposition {
					t.Fatalf("%s: Composition() range = %+v, want %+v", test.name, composition, test.wantComposition)
				}
			}
			conformanceRequireRefused(t, editor, cursor, test.name+"/ending")

			if !editor.Undo() {
				t.Fatalf("%s: Undo refused", test.name)
			}
			if got := conformanceStateOf(editor); got != test.undo {
				t.Fatalf("%s: after Undo = %+v, want %+v", test.name, got, test.undo)
			}
			conformanceRequireRefused(t, editor, cursor, test.name+"/undo")

			if !editor.Redo() {
				t.Fatalf("%s: Redo refused", test.name)
			}
			if got := conformanceStateOf(editor); got != test.redo {
				t.Fatalf("%s: after Redo = %+v, want %+v", test.name, got, test.redo)
			}
			conformanceRequireRefused(t, editor, cursor, test.name+"/redo")
		})
	}
}

func TestHistoryCompositionUnknownSourceNeverRebootstraps(t *testing.T) {
	editor := NewEditor("abc")
	cursor := conformanceMustTrack(t, editor)
	editor.SetSelection(Range{Start: 1, End: 2})
	editor.SetMarkedText("x", Range{})
	conformanceRequireRefused(t, editor, cursor, "invalidate")

	editor.SetSavePoint()
	conformanceAssertUnavailableGuardAccounting(t, editor, "save")
	conformanceRequireRefused(t, editor, cursor, "save")

	editor.Unmark()
	editor.Insert("y")
	conformanceAssertUnavailableGuardAccounting(t, editor, "edit")
	conformanceRequireRefused(t, editor, cursor, "edit")

	editor.ClearHistory()
	conformanceAssertUnavailableGuardAccounting(t, editor, "clear-1")
	conformanceRequireRefused(t, editor, cursor, "clear-1")

	editor.ClearHistory()
	conformanceAssertUnavailableGuardAccounting(t, editor, "clear-2")
	conformanceRequireRefused(t, editor, cursor, "clear-2")

	editor.SetSelection(Range{Start: 0, End: 0})
	editor.Insert("z")
	conformanceAssertUnavailableGuardAccounting(t, editor, "later-edit")
	conformanceRequireRefused(t, editor, cursor, "later-edit")
	if !editor.Undo() || editor.String() != "ayxc" {
		t.Fatalf("ordinary Undo after unavailable Clear = %q, want ayxc", editor.String())
	}
	conformanceRequireRefused(t, editor, cursor, "later-undo")
	if !editor.Redo() || editor.String() != "zayxc" {
		t.Fatalf("ordinary Redo after unavailable Clear = %q, want zayxc", editor.String())
	}
	conformanceRequireRefused(t, editor, cursor, "later-redo")
}

func TestHistoryCompositionNoOpEditingKeepsSupportedSource(t *testing.T) {
	editor := NewEditor("abc")
	cursor := conformanceMustTrack(t, editor)
	editor.Insert("d")
	_, latest := conformanceMustRead(t, editor, cursor)
	revision := editor.TrackEdits()

	editor.Unmark()
	editor.MoveTo(editor.Caret(), false)
	editor.Unmark()
	if !editor.MoveLeft(false) || !editor.MoveRight(false) {
		t.Fatal("movement fixture failed")
	}
	if editor.MoveRight(false) {
		t.Fatal("MoveRight at the end unexpectedly moved")
	}

	if edits, next, ok := editor.EditsSince(revision, nil); !ok || len(edits) != 0 || next != revision {
		t.Fatalf("no-op editing fabricated byte steps: %+v/%d/%t", edits, next, ok)
	}
	frames, again, ok := editor.HistorySince(latest)
	if !ok || len(frames) != 0 || again != latest {
		t.Fatalf("no-op editing changed supported history: %+v/%+v/%t", frames, again, ok)
	}
	got, bytes, ok := editor.TrackHistory()
	if !ok || got != cursor || bytes != 3 {
		t.Fatalf("no-op editing invalidated supported history: %+v/%d/%t", got, bytes, ok)
	}
}

func TestHistoryCompositionActiveRefusalVersusOrdinaryEdits(t *testing.T) {
	editor := NewEditor("abc")
	cursor := conformanceMustTrack(t, editor)
	editor.SetSelection(Range{Start: 1, End: 2})
	editor.SetMarkedText("x", Range{})
	before := conformanceStateOf(editor)
	beforeComposition, beforeComposing := editor.Composition()

	assertUnchanged := func(stage string) {
		t.Helper()
		if got := conformanceStateOf(editor); got != before {
			t.Fatalf("%s changed the source: %+v, want %+v", stage, got, before)
		}
		if composition, composing := editor.Composition(); composition != beforeComposition || composing != beforeComposing {
			t.Fatalf("%s changed composition: %+v/%t, want %+v/%t", stage, composition, composing, beforeComposition, beforeComposing)
		}
		conformanceRequireRefused(t, editor, cursor, stage)
	}

	called := false
	if err := editor.Transact(func(draft *Editor) error {
		called = true
		draft.Insert("!")
		return nil
	}); err == nil || called {
		t.Fatal("active composition ran a transaction callback")
	}
	assertUnchanged("transact")

	for _, test := range []struct {
		name string
		call func() error
	}{
		{"replace-ranges", func() error {
			return editor.ReplaceRanges([]Replacement{{Range: Range{Start: 0, End: 1}, Text: "Q"}})
		}},
		{"replace-ranges-with", func() error {
			return editor.ReplaceRangesWith([]Replacement{{Range: Range{Start: 0, End: 1}, Text: "Q"}}, ReplaceRangesOptions{})
		}},
		{"validate-replace-ranges", func() error {
			return editor.ValidateReplaceRanges([]Replacement{{Range: Range{Start: 0, End: 1}, Text: "Q"}}, ReplaceRangesOptions{})
		}},
	} {
		if err := test.call(); err == nil {
			t.Fatalf("%s accepted active composition", test.name)
		}
		assertUnchanged(test.name)
	}

	editor.Unmark()
	if _, composing := editor.Composition(); composing {
		t.Fatal("Unmark left composition active")
	}
	conformanceRequireRefused(t, editor, cursor, "after-unmark")

	editor.Insert("Q")
	if editor.String() != "aQxc" || editor.Selection() != (Range{Start: 2, End: 2}) {
		t.Fatalf("ordinary insert after Unmark = %q/%+v", editor.String(), editor.Selection())
	}
	conformanceRequireRefused(t, editor, cursor, "insert-after-unmark")

	if err := editor.ReplaceRanges([]Replacement{{Range: Range{Start: 0, End: 1}, Text: "R"}}); err != nil {
		t.Fatalf("ReplaceRanges after Unmark refused: %v", err)
	}
	if editor.String() != "RQxc" {
		t.Fatalf("ReplaceRanges after Unmark = %q, want RQxc", editor.String())
	}
	conformanceRequireRefused(t, editor, cursor, "ranges-after-unmark")

	if err := editor.Transact(func(draft *Editor) error {
		draft.SetSelection(Range{Start: 0, End: 0})
		draft.Insert("T")
		return nil
	}); err != nil {
		t.Fatalf("Transact after Unmark refused: %v", err)
	}
	if editor.String() != "TRQxc" || editor.Selection() != (Range{Start: 1, End: 1}) {
		t.Fatalf("Transact after Unmark = %q/%+v, want TRQxc/1..1", editor.String(), editor.Selection())
	}
	conformanceRequireRefused(t, editor, cursor, "transact-after-unmark")
}

func TestHistoryCompositionLegacyJournalsAndPrivateDrafts(t *testing.T) {
	apply := func(editor *Editor) {
		editor.SetSelection(Range{Start: 1, End: 2})
		editor.SetMarkedText("x", Range{})
		editor.SetMarkedText("xy", Range{})
		editor.Unmark()
	}

	plain := NewEditor("abc")
	plainCursor := plain.TrackEdits()
	plainLineCursor := plain.TrackLineEdits()
	apply(plain)
	plainBytes, plainNext, ok := plain.EditsSince(plainCursor, nil)
	if !ok {
		t.Fatal("plain byte journal is incomplete")
	}
	plainLines, plainLineNext, ok := plain.LineEditsSince(plainLineCursor, nil)
	if !ok {
		t.Fatal("plain line journal is incomplete")
	}

	tracked := NewEditor("abc")
	cursor := conformanceMustTrack(t, tracked)
	trackedCursor := tracked.TrackEdits()
	trackedLineCursor := tracked.TrackLineEdits()
	apply(tracked)
	trackedBytes, trackedNext, ok := tracked.EditsSince(trackedCursor, nil)
	if !ok {
		t.Fatal("tracked byte journal is incomplete")
	}
	trackedLines, trackedLineNext, ok := tracked.LineEditsSince(trackedLineCursor, nil)
	if !ok {
		t.Fatal("tracked line journal is incomplete")
	}
	wantEdits := []Edit{
		{Start: 1, Removed: 1, Inserted: 1},
		{Start: 1, Removed: 1, Inserted: 2},
	}
	if !slices.Equal(plainBytes, wantEdits) || !slices.Equal(trackedBytes, wantEdits) {
		t.Fatalf("composition byte journals = plain %+v tracked %+v, want %+v", plainBytes, trackedBytes, wantEdits)
	}
	if !slices.Equal(plainLines, trackedLines) || plainNext != trackedNext || plainLineNext != trackedLineNext {
		t.Fatalf("composition line journals differ:\nplain %+v\ntracked %+v", plainLines, trackedLines)
	}
	conformanceRequireRefused(t, tracked, cursor, "legacy-journals")

	// A manual private draft is not a producer and cannot become one. This is
	// recorder refusal only, not evidence of native/tentative composition.
	source := NewEditor("abc")
	sourceCursor := conformanceMustTrack(t, source)
	var accepted bool
	if err := source.Transact(func(draft *Editor) error {
		_, _, accepted = draft.TrackHistory()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if accepted {
		t.Fatal("private transaction draft created a public producer")
	}
	if frames, latest, ok := source.HistorySince(sourceCursor); !ok || frames != nil || latest != sourceCursor {
		t.Fatalf("empty private draft changed producer history: %+v/%+v/%t", frames, latest, ok)
	}

	beforeText := source.String()
	if err := source.Transact(func(draft *Editor) error {
		draft.SetSelection(Range{Start: 1, End: 1})
		draft.SetMarkedText("x", Range{})
		return nil
	}); err == nil {
		t.Fatal("unfinished private composition escaped its draft")
	}
	if source.String() != beforeText {
		t.Fatalf("unfinished private draft changed the source: %q, want %q", source.String(), beforeText)
	}
	if frames, latest, ok := source.HistorySince(sourceCursor); !ok || frames != nil || latest != sourceCursor {
		t.Fatalf("unfinished private draft changed producer history: %+v/%+v/%t", frames, latest, ok)
	}
}

func TestHistoryCompositionFirstEnableAfterNeverEnabled(t *testing.T) {
	t.Run("never-enabled-first-enable", func(t *testing.T) {
		editor := NewEditor("abc")
		editor.SetSelection(Range{Start: 1, End: 2})
		editor.SetMarkedText("x", Range{})
		editor.Unmark()
		if editor.String() != "axc" {
			t.Fatalf("marked text = %q, want axc", editor.String())
		}
		editor.ClearHistory()
		cursor, bytes, ok := editor.TrackHistory()
		if !ok || bytes != 3 {
			t.Fatalf("first enable after never-enabled composition = %+v/%d/%t, want 3 bytes/true", cursor, bytes, ok)
		}
		frames, latest, ok := editor.HistorySince(cursor)
		if !ok || frames != nil || latest != cursor {
			t.Fatalf("fresh first enable served history: %+v/%+v/%t", frames, latest, ok)
		}
	})

	t.Run("previously-enabled-unknown-source", func(t *testing.T) {
		editor := NewEditor("abc")
		cursor := conformanceMustTrack(t, editor)
		editor.SetSelection(Range{Start: 1, End: 2})
		editor.SetMarkedText("x", Range{})
		editor.Unmark()
		editor.ClearHistory()
		conformanceRequireRefused(t, editor, cursor, "previously-enabled-unknown-source")
	})
}
