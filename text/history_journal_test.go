package text

import (
	"reflect"
	"testing"
	"unsafe"
)

type historyFrameWant struct {
	before, after int
	steps         []HistoryStep
}

func historyAssertFrames(t *testing.T, frames []HistoryFrame, base HistoryCursor, first uint64, wants ...historyFrameWant) {
	t.Helper()
	if len(frames) != len(wants) {
		t.Fatalf("frames = %d, want %d: %+v", len(frames), len(wants), frames)
	}
	for index, want := range wants {
		from, to := base, base
		from.sequence = first + uint64(index)
		to.sequence = from.sequence + 1
		got := frames[index]
		if got.From != from || got.To != to {
			t.Fatalf("frame %d cursors = %+v..%+v, want %+v..%+v", index, got.From, got.To, from, to)
		}
		if got.BeforeBytes != want.before || got.AfterBytes != want.after {
			t.Fatalf("frame %d bytes = %d->%d, want %d->%d", index, got.BeforeBytes, got.AfterBytes, want.before, want.after)
		}
		if !reflect.DeepEqual(got.Steps, want.steps) {
			t.Fatalf("frame %d steps = %+v, want %+v", index, got.Steps, want.steps)
		}
	}
}

func historyStep(op HistoryOperation, primitive HistoryPrimitive, position, length, before, after int, current, save, retainedTo int) HistoryStep {
	return HistoryStep{
		Operation: op, Primitive: primitive, Position: position, Length: length,
		BeforeBytes: before, AfterBytes: after,
		Context: HistoryContext{
			Current: current, Save: save, Detach: -1,
			RetainedFrom: 0, RetainedTo: retainedTo, Collecting: true,
		},
	}
}

func historySaveStep(before, after, current, retainedTo int) HistoryStep {
	return HistoryStep{
		Operation: HistorySave, Primitive: HistoryNoPrimitive,
		BeforeBytes: before, AfterBytes: after,
		Context: HistoryContext{
			Current: current, Save: current, Detach: -1,
			RetainedFrom: 0, RetainedTo: retainedTo, Collecting: true,
		},
	}
}

func historyMustTrack(t *testing.T, editor *Editor) (HistoryCursor, int) {
	t.Helper()
	cursor, bytes, ok := editor.TrackHistory()
	if !ok {
		t.Fatal("TrackHistory refused an empty untracked editor")
	}
	return cursor, bytes
}

func historyMustRead(t *testing.T, editor *Editor, cursor HistoryCursor) ([]HistoryFrame, HistoryCursor) {
	t.Helper()
	frames, latest, ok := editor.HistorySince(cursor)
	if !ok {
		t.Fatalf("HistorySince(%+v) refused", cursor)
	}
	return frames, latest
}

func historyAssertAccounting(t *testing.T, editor *Editor) {
	t.Helper()
	history := editor.journal.history
	if history == nil || !history.available {
		t.Fatal("accounting probe requires an available producer")
	}
	minimum := int(unsafe.Sizeof(*history)) + cap(history.frames)*int(unsafe.Sizeof(HistoryFrame{}))
	for _, frame := range history.frames {
		minimum += cap(frame.Steps) * int(unsafe.Sizeof(HistoryStep{}))
	}
	charged := (editor.historyCost() - editor.historyRunes) * int(unsafe.Sizeof(rune(0)))
	if charged < minimum {
		t.Fatalf("retained history charges %d bytes, structural lower bound is %d", charged, minimum)
	}
}

func historyAssertUnitCapacityAccounting(t *testing.T, editor *Editor) {
	t.Helper()
	history := editor.journal.history
	if history == nil || !history.available {
		t.Fatal("unit capacity accounting requires an available producer")
	}
	minimum := int(unsafe.Sizeof(*history)) + cap(history.frames)*int(unsafe.Sizeof(HistoryFrame{}))
	for _, frame := range history.frames {
		minimum += cap(frame.Steps) * int(unsafe.Sizeof(HistoryStep{}))
	}
	for _, edit := range editor.history {
		minimum += (len(edit.removed) + len(edit.inserted)) * int(unsafe.Sizeof(rune(0)))
		minimum += cap(edit.primitives) * int(unsafe.Sizeof(Edit{}))
	}
	charged := editor.historyCost() * int(unsafe.Sizeof(rune(0)))
	if charged < minimum {
		t.Fatalf("retained units charge %d bytes, structural lower bound is %d (primitives=%d capacity=%d)",
			charged, minimum, len(editor.history[0].primitives), cap(editor.history[0].primitives))
	}
}

func TestHistoryRetainedAccountingCoversStructure(t *testing.T) {
	editor := NewEditor("abc")
	historyMustTrack(t, editor)
	editor.Insert("!")
	historyAssertAccounting(t, editor)
	historyAssertUnitCapacityAccounting(t, editor)
	editor.SetSavePoint()
	historyAssertAccounting(t, editor)
	editor.SetSelection(Range{Start: 0, End: 1})
	editor.Insert("猫")
	historyAssertAccounting(t, editor)
	if err := editor.ReplaceRanges([]Replacement{
		{Range: Range{Start: 0, End: 1}, Text: "é"},
		{Range: Range{Start: 2, End: 3}, Text: "猫a"},
	}); err != nil {
		t.Fatal(err)
	}
	historyAssertAccounting(t, editor)
}

func TestHistoryTrackHistoryBootstrap(t *testing.T) {
	editor := NewEditor("aé🙂")
	cursor, bytes := historyMustTrack(t, editor)
	if bytes != 7 {
		t.Fatalf("bootstrap bytes = %d, want 7", bytes)
	}
	if cursor == (HistoryCursor{}) || cursor.source == 0 {
		t.Fatalf("bootstrap cursor is invalid: %+v", cursor)
	}
	again, againBytes, ok := editor.TrackHistory()
	if !ok || again != cursor || againBytes != bytes {
		t.Fatalf("repeated TrackHistory = %+v/%d/%t, want %+v/%d/true", again, againBytes, ok, cursor, bytes)
	}
	if editor.AtSavePoint() {
		t.Fatal("TrackHistory changed the existing saved state")
	}
	if _, _, tracked := editor.EditsSince(0, nil); tracked {
		t.Fatal("TrackHistory started the ordinary byte journal")
	}
	frames, latest, ok := editor.HistorySince(cursor)
	if !ok || frames != nil || latest != cursor {
		t.Fatalf("initial HistorySince = %+v/%+v/%t, want nil frames and the bootstrap cursor", frames, latest, ok)
	}

	editor.SetSavePoint()
	frames, latest = historyMustRead(t, editor, cursor)
	historyAssertFrames(t, frames, cursor, 0, historyFrameWant{7, 7, []HistoryStep{historySaveStep(7, 7, 0, 0)}})
	if latest.sequence != 1 {
		t.Fatalf("Save-only latest = %+v, want sequence 1", latest)
	}
	if !editor.AtSavePoint() {
		t.Fatal("TrackHistory or Save frame changed AtSavePoint")
	}

	saved := NewEditor("abc")
	saved.SetSavePoint()
	if !saved.AtSavePoint() {
		t.Fatal("fixture did not mark the saved boundary")
	}
	if _, savedBytes, ok := saved.TrackHistory(); !ok || savedBytes != 3 {
		t.Fatalf("TrackHistory on a saved editor = %d/%t", savedBytes, ok)
	}
	if !saved.AtSavePoint() {
		t.Fatal("TrackHistory cleared an existing saved boundary")
	}
}

func TestHistoryTrackHistoryRefusal(t *testing.T) {
	editor := NewEditor("abc")
	editor.Insert("x")
	beforeText, beforeSelection := editor.String(), editor.Selection()
	if _, _, ok := editor.TrackHistory(); ok {
		t.Fatal("TrackHistory accepted an editor with Undo history")
	}
	if editor.String() != beforeText || editor.Selection() != beforeSelection || !editor.CanUndo() {
		t.Fatal("refused TrackHistory changed text, selection or Undo")
	}
	if _, _, tracked := editor.EditsSince(0, nil); tracked {
		t.Fatal("refused TrackHistory started the ordinary byte journal")
	}

	if !editor.Undo() || !editor.CanRedo() {
		t.Fatal("fixture did not create redo-only history")
	}
	if _, _, ok := editor.TrackHistory(); ok {
		t.Fatal("TrackHistory accepted redo-only history")
	}
	if !editor.CanRedo() {
		t.Fatal("refused TrackHistory discarded redo history")
	}

	composing := NewEditor("abc")
	composing.SetSelection(Range{Start: 1, End: 2})
	composing.SetMarkedText("é", Range{})
	markedText, markedSelection := composing.String(), composing.Selection()
	composingRange, wasComposing := composing.Composition()
	if !wasComposing {
		t.Fatal("fixture did not start composition")
	}
	if _, _, ok := composing.TrackHistory(); ok {
		t.Fatal("TrackHistory accepted active composition")
	}
	if composing.String() != markedText || composing.Selection() != markedSelection || composing.CanUndo() == false {
		t.Fatal("refused TrackHistory changed composition state")
	}
	if got, active := composing.Composition(); !active || got != composingRange {
		t.Fatal("refused TrackHistory changed the composition range")
	}

	saved := NewEditor("abc")
	saved.SetSavePoint()
	saved.Insert("x")
	if saved.AtSavePoint() {
		t.Fatal("fixture unexpectedly remained at the save point")
	}
	if _, _, ok := saved.TrackHistory(); ok {
		t.Fatal("TrackHistory accepted an unsaved editor with Undo history")
	}
	if saved.AtSavePoint() {
		t.Fatal("refused TrackHistory changed AtSavePoint")
	}
}

func TestHistoryForwardPrimitives(t *testing.T) {
	editor := NewEditor("abcd")
	cursor, _ := historyMustTrack(t, editor)
	editor.SetSelection(Range{Start: 1, End: 1})
	editor.Insert("é")
	frames, latest := historyMustRead(t, editor, cursor)
	historyAssertFrames(t, frames, cursor, 0, historyFrameWant{4, 6, []HistoryStep{
		historyStep(HistoryForward, HistoryInsert, 1, 2, 4, 6, 1, 0, 1),
	}})

	editor.SetSelection(Range{Start: 2, End: 4})
	editor.Insert("XY")
	frames, latest = historyMustRead(t, editor, latest)
	historyAssertFrames(t, frames, cursor, 1, historyFrameWant{6, 6, []HistoryStep{
		historyStep(HistoryForward, HistoryDelete, 3, 2, 6, 4, 2, 0, 2),
		historyStep(HistoryForward, HistoryInsert, 3, 2, 4, 6, 3, 0, 3),
	}})

	if !editor.DeleteBackward() {
		t.Fatal("DeleteBackward refused")
	}
	frames, latest = historyMustRead(t, editor, latest)
	historyAssertFrames(t, frames, cursor, 2, historyFrameWant{6, 5, []HistoryStep{
		historyStep(HistoryForward, HistoryDelete, 4, 1, 6, 5, 4, 0, 4),
	}})
	if !editor.DeleteBackward() {
		t.Fatal("DeleteBackward refused")
	}
	frames, latest = historyMustRead(t, editor, latest)
	historyAssertFrames(t, frames, cursor, 3, historyFrameWant{5, 4, []HistoryStep{
		historyStep(HistoryForward, HistoryDelete, 3, 1, 5, 4, 5, 0, 5),
	}})
	if editor.String() != "aéd" {
		t.Fatalf("text = %q, want aéd", editor.String())
	}

	overwrite := NewEditor("abc")
	overwriteCursor, _ := historyMustTrack(t, overwrite)
	overwrite.SetSelection(Range{Start: 0, End: 0})
	overwrite.SetOverwrite(true)
	overwrite.Insert("Z")
	frames, _ = historyMustRead(t, overwrite, overwriteCursor)
	historyAssertFrames(t, frames, overwriteCursor, 0, historyFrameWant{3, 3, []HistoryStep{
		historyStep(HistoryForward, HistoryDelete, 0, 1, 3, 2, 1, 0, 1),
		historyStep(HistoryForward, HistoryInsert, 0, 1, 2, 3, 2, 0, 2),
	}})
	if overwrite.String() != "Zbc" {
		t.Fatalf("overwrite text = %q, want Zbc", overwrite.String())
	}
}

func TestHistoryCoexistsWithOrdinaryJournal(t *testing.T) {
	editor := NewEditor("abc")
	revision := editor.TrackEdits()
	cursor, _ := historyMustTrack(t, editor)
	editor.Insert("x")
	edits, next, ok := editor.EditsSince(revision, nil)
	if !ok || next != revision+1 || len(edits) != 1 || edits[0] != (Edit{Start: 3, Inserted: 1}) {
		t.Fatalf("ordinary journal after TrackHistory = %+v/%d/%t", edits, next, ok)
	}
	frames, _ := historyMustRead(t, editor, cursor)
	historyAssertFrames(t, frames, cursor, 0, historyFrameWant{3, 4, []HistoryStep{
		historyStep(HistoryForward, HistoryInsert, 3, 1, 3, 4, 1, 0, 1),
	}})
}

func TestHistoryOwnedPrimitivesDoNotChangeOrdinaryUndoJournal(t *testing.T) {
	editor := NewEditor("")
	revision := editor.TrackEdits()
	editor.TrackHistory()
	editor.Insert("a")
	editor.Insert("猫")
	editor.Insert("b")
	edits, undoRevision, ok := editor.EditsSince(revision, nil)
	if !ok || len(edits) != 3 || len(editor.history[0].primitives) != 3 {
		t.Fatalf("forward ownership = %+v/%d/%t", edits, len(editor.history[0].primitives), ok)
	}
	if !editor.Undo() {
		t.Fatal("Undo refused")
	}
	edits, _, ok = editor.EditsSince(undoRevision, nil)
	if !ok || !reflect.DeepEqual(edits, []Edit{{Start: 0, Removed: 5}}) {
		t.Fatalf("Undo byte journal = %+v/%t, want the original coalesced replacement", edits, ok)
	}
}

func TestHistoryOwnedPrimitivesPreserveOrdinaryJournals(t *testing.T) {
	scenarios := []struct {
		name  string
		apply func(*Editor)
	}{
		{"coalesced-insert", func(editor *Editor) {
			editor.SetSelection(Range{Start: 6, End: 6})
			editor.Insert("x")
			editor.Insert("猫")
		}},
		{"selection-replace", func(editor *Editor) {
			editor.SetSelection(Range{Start: 1, End: 3})
			editor.Insert("猫")
		}},
		{"overwrite", func(editor *Editor) {
			editor.SetSelection(Range{Start: 2, End: 2})
			editor.SetOverwrite(true)
			editor.Insert("Z")
		}},
		{"paste-boundary", func(editor *Editor) {
			editor.SetSelection(Range{Start: 3, End: 3})
			editor.Paste("x\n")
		}},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			plain := NewEditor("abcdef")
			plainRevision := plain.TrackEdits()
			scenario.apply(plain)
			plainForward, plainUndoCursor, ok := plain.EditsSince(plainRevision, nil)
			if !ok {
				t.Fatal("plain forward journal is incomplete")
			}

			history := NewEditor("abcdef")
			history.TrackHistory()
			historyRevision := history.TrackEdits()
			scenario.apply(history)
			historyForward, historyUndoCursor, ok := history.EditsSince(historyRevision, nil)
			if !ok || !reflect.DeepEqual(historyForward, plainForward) {
				t.Fatalf("forward journal = %+v, want %+v", historyForward, plainForward)
			}
			if !plain.Undo() || !history.Undo() {
				t.Fatal("Undo refused")
			}
			plainBackward, _, _ := plain.EditsSince(plainUndoCursor, nil)
			historyBackward, _, _ := history.EditsSince(historyUndoCursor, nil)
			if !reflect.DeepEqual(historyBackward, plainBackward) {
				t.Fatalf("Undo journal = %+v, want %+v", historyBackward, plainBackward)
			}
			if history.String() != plain.String() {
				t.Fatalf("Undo text = %q, want %q", history.String(), plain.String())
			}
		})
	}
}

func TestHistoryForwardRefusalsAndNoOps(t *testing.T) {
	editor := NewEditor("abc")
	cursor, _ := historyMustTrack(t, editor)
	editor.Restrict(func(value rune) bool { return value >= '0' && value <= '9' }, 0)
	editor.SetSelection(Range{Start: 3, End: 3})
	editor.Insert("x")
	editor.Insert("")
	editor.MoveTo(0, false)
	editor.SetSelection(Range{Start: 0, End: 2})
	frames, latest, ok := editor.HistorySince(cursor)
	if !ok || frames != nil || latest != cursor {
		t.Fatalf("refused/no-op edits produced frames: %+v/%+v/%t", frames, latest, ok)
	}
	if editor.String() != "abc" {
		t.Fatalf("refused edits changed text: %q", editor.String())
	}

	editor.Restrict(nil, 0)
	noOpSelection := editor.Selection()
	if err := editor.ReplaceRanges([]Replacement{{Range: Range{Start: 1, End: 1}}}); err != nil {
		t.Fatal(err)
	}
	if editor.Selection() != noOpSelection {
		t.Fatal("history-enabled empty replacement moved the selection")
	}
	if frames, latest, ok := editor.HistorySince(cursor); !ok || frames != nil || latest != cursor {
		t.Fatalf("history-enabled empty replacement produced a frame: %+v/%+v/%t", frames, latest, ok)
	}

	editor.SetSelection(Range{Start: 0, End: 1})
	editor.Insert("9")
	frames, _ = historyMustRead(t, editor, cursor)
	historyAssertFrames(t, frames, cursor, 0, historyFrameWant{3, 3, []HistoryStep{
		historyStep(HistoryForward, HistoryDelete, 0, 1, 3, 2, 1, 0, 1),
		historyStep(HistoryForward, HistoryInsert, 0, 1, 2, 3, 2, 0, 2),
	}})
	if editor.String() != "9bc" {
		t.Fatalf("accepted replacement text = %q, want 9bc", editor.String())
	}
}

func TestHistoryCoalescedInsertsKeepPrimitives(t *testing.T) {
	editor := NewEditor("abcd")
	cursor, _ := historyMustTrack(t, editor)
	editor.SetSelection(Range{Start: 4, End: 4})
	editor.Insert("x")
	editor.Insert("y")
	if !editor.DeleteBackward() || !editor.DeleteBackward() {
		t.Fatal("coalesced DeleteBackward refused")
	}
	if editor.String() != "abcd" {
		t.Fatalf("text = %q, want abcd", editor.String())
	}
	frames, latest := historyMustRead(t, editor, cursor)
	historyAssertFrames(t, frames, cursor, 0,
		historyFrameWant{4, 5, []HistoryStep{historyStep(HistoryForward, HistoryInsert, 4, 1, 4, 5, 1, 0, 1)}},
		historyFrameWant{5, 6, []HistoryStep{historyStep(HistoryForward, HistoryInsert, 5, 1, 5, 6, 2, 0, 2)}},
		historyFrameWant{6, 5, []HistoryStep{historyStep(HistoryForward, HistoryDelete, 5, 1, 6, 5, 3, 0, 3)}},
		historyFrameWant{5, 4, []HistoryStep{historyStep(HistoryForward, HistoryDelete, 4, 1, 5, 4, 4, 0, 4)}},
	)
	if latest.sequence != 4 {
		t.Fatalf("latest sequence = %d, want 4 (two Viva Undo units, four primitives)", latest.sequence)
	}
}

func TestHistoryCoalescedUnitsOwnEveryPrimitive(t *testing.T) {
	values := []string{"é", "猫", "🙂", "a", "€", "b"}
	editor := NewEditor("")
	bootstrap, _ := historyMustTrack(t, editor)
	for index := range 44 {
		editor.Insert(values[index%len(values)])
	}
	_, cursor44 := historyMustRead(t, editor, bootstrap)
	for index := 44; index < 300; index++ {
		editor.Insert(values[index%len(values)])
	}
	if len(editor.history) != 1 {
		t.Fatalf("coalesced typing produced %d history units, want 1", len(editor.history))
	}
	unit := &editor.history[0]
	if len(unit.primitives) != 300 {
		t.Fatalf("coalesced unit owns %d primitives, want 300", len(unit.primitives))
	}
	position := 0
	for index, primitive := range unit.primitives {
		value := values[index%len(values)]
		if primitive.Start != position || primitive.Removed != 0 || primitive.Inserted != len(value) {
			t.Fatalf("primitive %d = %+v, want start=%d insert=%d", index, primitive, position, len(value))
		}
		position += len(value)
	}
	historyAssertAccounting(t, editor)
	historyAssertUnitCapacityAccounting(t, editor)
	if frames, _, ok := editor.HistorySince(bootstrap); ok || frames != nil {
		t.Fatal("expired coalesced frame prefix was served")
	}
	frames, latest, ok := editor.HistorySince(cursor44)
	if !ok || len(frames) != 256 || latest.sequence != 300 {
		t.Fatalf("retained coalesced consumer = %d frames/%+v/%t", len(frames), latest, ok)
	}
	first := unit.primitives[0]
	frames[0].Steps[0].Length = 999
	frames[0].Steps[0].Context.Current = 999
	frames[0].Steps = append(frames[0].Steps, HistoryStep{Operation: HistoryRedo})
	if unit.primitives[0] != first {
		t.Fatal("returned frames corrupted retained unit primitive ownership")
	}
	editor.ClearHistory()
	history := editor.journal.history
	if !history.available || len(history.frames) != 0 || history.cost != historyHeaderRunes ||
		len(editor.history) != 0 || editor.historyRunes != 0 {
		t.Fatalf("manual Clear retained unit ownership/accounting: available=%t frames=%d cost=%d units=%d runes=%d",
			history.available, len(history.frames), history.cost, len(editor.history), editor.historyRunes)
	}
}

func TestHistoryEqualReplacementAndZeroNetTransaction(t *testing.T) {
	equal := NewEditor("abc")
	cursor, _ := historyMustTrack(t, equal)
	if err := equal.ReplaceRanges([]Replacement{{Range: Range{Start: 1, End: 2}, Text: "b"}}); err != nil {
		t.Fatal(err)
	}
	frames, _ := historyMustRead(t, equal, cursor)
	historyAssertFrames(t, frames, cursor, 0, historyFrameWant{3, 3, []HistoryStep{
		historyStep(HistoryForward, HistoryDelete, 1, 1, 3, 2, 1, 0, 1),
		historyStep(HistoryForward, HistoryInsert, 1, 1, 2, 3, 2, 0, 2),
	}})
	if equal.String() != "abc" || !equal.CanUndo() {
		t.Fatalf("equal replacement = %q/undo %t, want abc and a retained unit", equal.String(), equal.CanUndo())
	}

	plain := NewEditor("abc")
	if err := plain.ReplaceRanges([]Replacement{{Range: Range{Start: 1, End: 2}, Text: "b"}}); err != nil {
		t.Fatal(err)
	}
	if plain.CanUndo() {
		t.Fatal("default equal replacement created Undo history")
	}

	transaction := NewEditor("abc")
	transactionCursor, _ := historyMustTrack(t, transaction)
	err := transaction.Transact(func(draft *Editor) error {
		draft.Insert("xy")
		if !draft.DeleteBackward() || !draft.DeleteBackward() {
			t.Fatal("draft DeleteBackward refused")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if transaction.String() != "abc" || !transaction.CanUndo() {
		t.Fatalf("zero-net transaction = %q/undo %t, want abc and a retained unit", transaction.String(), transaction.CanUndo())
	}
	frames, _ = historyMustRead(t, transaction, transactionCursor)
	historyAssertFrames(t, frames, transactionCursor, 0, historyFrameWant{3, 3, []HistoryStep{
		historyStep(HistoryForward, HistoryInsert, 3, 2, 3, 5, 1, 0, 1),
		historyStep(HistoryForward, HistoryDelete, 4, 1, 5, 4, 2, 0, 2),
		historyStep(HistoryForward, HistoryDelete, 3, 1, 4, 3, 3, 0, 3),
	}})

	plainTransaction := NewEditor("abc")
	if err := plainTransaction.Transact(func(draft *Editor) error {
		draft.Insert("xy")
		draft.DeleteBackward()
		draft.DeleteBackward()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if plainTransaction.CanUndo() {
		t.Fatal("default zero-net transaction created Undo history")
	}
}

func TestHistoryTransactionDraftOwnership(t *testing.T) {
	editor := NewEditor("abc")
	cursor, _ := historyMustTrack(t, editor)
	if err := editor.Transact(func(draft *Editor) error {
		return draft.Transact(func(inner *Editor) error {
			inner.Insert("x")
			return nil
		})
	}); err != nil {
		t.Fatal(err)
	}
	if editor.String() != "abcx" {
		t.Fatalf("nested transaction text = %q, want abcx", editor.String())
	}
	frames, _ := historyMustRead(t, editor, cursor)
	historyAssertFrames(t, frames, cursor, 0, historyFrameWant{3, 4, []HistoryStep{
		historyStep(HistoryForward, HistoryInsert, 3, 1, 3, 4, 1, 0, 1),
	}})

	before := editor.String()
	selection := editor.Selection()
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("transaction panic did not propagate")
			}
		}()
		_ = editor.Transact(func(draft *Editor) error {
			draft.Insert("lost")
			panic("transaction panic")
		})
	}()
	if editor.String() != before || editor.Selection() != selection {
		t.Fatal("panicking transaction changed the source")
	}
	frames, latest, ok := editor.HistorySince(cursor)
	if !ok || len(frames) != 1 || latest.sequence != 1 {
		t.Fatalf("panicking transaction changed history: %+v/%+v/%t", frames, latest, ok)
	}
}

func TestHistoryPrivateTransactionDraftsRefuseProducer(t *testing.T) {
	for _, tracked := range []bool{false, true} {
		name := "ordinary-source"
		if tracked {
			name = "tracked-source"
		}
		t.Run(name, func(t *testing.T) {
			editor := NewEditor("abc")
			var cursor HistoryCursor
			if tracked {
				cursor, _ = historyMustTrack(t, editor)
			}
			var accepted bool
			var retained *Editor
			if err := editor.Transact(func(draft *Editor) error {
				retained = draft
				_, _, accepted = draft.TrackHistory()
				draft.Insert("!")
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if accepted {
				t.Fatal("private transaction draft created a public producer")
			}
			if editor.String() != "abc!" || retained == nil || retained.journal != nil {
				t.Fatal("draft producer attempt changed the committed transaction or leaked its recorder")
			}
			if tracked {
				frames, latest := historyMustRead(t, editor, cursor)
				historyAssertFrames(t, frames, cursor, 0, historyFrameWant{3, 4, []HistoryStep{
					historyStep(HistoryForward, HistoryInsert, 3, 1, 3, 4, 1, 0, 1),
				}})
				if latest.sequence != 1 {
					t.Fatalf("source sequence = %d, want 1", latest.sequence)
				}
			}

			beforeText := editor.String()
			var failedAccepted bool
			if err := editor.Transact(func(draft *Editor) error {
				_, _, failedAccepted = draft.TrackHistory()
				draft.Insert("?")
				return errHistoryTestTransaction
			}); err != errHistoryTestTransaction {
				t.Fatalf("failing transaction error = %v", err)
			}
			if failedAccepted || editor.String() != beforeText {
				t.Fatal("failing transaction draft accepted a producer or changed the source")
			}
			if tracked {
				frames, latest := historyMustRead(t, editor, cursor)
				if len(frames) != 1 || latest.sequence != 1 {
					t.Fatalf("failing transaction changed source frames: %+v/%+v", frames, latest)
				}
			}

			retained.Insert("z")
			if editor.String() != beforeText {
				t.Fatal("retained draft mutation changed the source")
			}
			if _, _, ok := retained.TrackHistory(); ok {
				t.Fatal("retained edited draft created a producer instead of refusing its own Undo history")
			}
			if tracked {
				frames, latest := historyMustRead(t, editor, cursor)
				if len(frames) != 1 || latest.sequence != 1 {
					t.Fatalf("retained draft changed source frames: %+v/%+v", frames, latest)
				}
			}
		})
	}
}

func TestHistoryRangeBatch(t *testing.T) {
	editor := NewEditor("abcdef")
	cursor, _ := historyMustTrack(t, editor)
	if err := editor.ReplaceRanges([]Replacement{
		{Range: Range{Start: 1, End: 2}, Text: "X"},
		{Range: Range{Start: 4, End: 5}, Text: "YZ"},
	}); err != nil {
		t.Fatal(err)
	}
	if editor.String() != "aXcdYZf" {
		t.Fatalf("range batch text = %q, want aXcdYZf", editor.String())
	}
	frames, latest := historyMustRead(t, editor, cursor)
	historyAssertFrames(t, frames, cursor, 0, historyFrameWant{6, 7, []HistoryStep{
		historyStep(HistoryForward, HistoryDelete, 4, 1, 6, 5, 1, 0, 1),
		historyStep(HistoryForward, HistoryInsert, 4, 2, 5, 7, 2, 0, 2),
		historyStep(HistoryForward, HistoryDelete, 1, 1, 7, 6, 3, 0, 3),
		historyStep(HistoryForward, HistoryInsert, 1, 1, 6, 7, 4, 0, 4),
	}})

	beforeText := editor.String()
	frames, latest, ok := editor.HistorySince(latest)
	if !ok || frames != nil {
		t.Fatalf("history after batch = %+v/%t, want empty", frames, ok)
	}
	if err := editor.ReplaceRanges([]Replacement{
		{Range: Range{Start: 1, End: 2}, Text: "A"},
		{Range: Range{Start: 1, End: 3}, Text: "B"},
	}); err == nil {
		t.Fatal("overlapping replacement was accepted")
	}
	if editor.String() != beforeText {
		t.Fatal("invalid replacement changed text")
	}
	frames, afterFailed, ok := editor.HistorySince(latest)
	if !ok || frames != nil || afterFailed != latest {
		t.Fatalf("invalid replacement changed history: %+v/%+v/%t", frames, afterFailed, ok)
	}

	if err := editor.Transact(func(draft *Editor) error {
		draft.Insert("changed")
		return errHistoryTestTransaction
	}); err == nil {
		t.Fatal("transaction error was discarded")
	}
	if editor.String() != beforeText {
		t.Fatal("failed transaction changed the source")
	}
	frames, afterFailed, ok = editor.HistorySince(latest)
	if !ok || frames != nil || afterFailed != latest {
		t.Fatalf("failed transaction changed history: %+v/%+v/%t", frames, afterFailed, ok)
	}

	forward := NewEditor("abcdef")
	forwardCursor, _ := historyMustTrack(t, forward)
	if err := forward.ReplaceRangesWith([]Replacement{
		{Range: Range{Start: 1, End: 2}, Text: "XYZ"},
		{Range: Range{Start: 4, End: 5}, Text: "Q"},
	}, ReplaceRangesOptions{Forward: true}); err != nil {
		t.Fatal(err)
	}
	if forward.String() != "aXYZcdQf" {
		t.Fatalf("forward batch text = %q, want aXYZcdQf", forward.String())
	}
	frames, _ = historyMustRead(t, forward, forwardCursor)
	historyAssertFrames(t, frames, forwardCursor, 0, historyFrameWant{6, 8, []HistoryStep{
		historyStep(HistoryForward, HistoryDelete, 1, 1, 6, 5, 1, 0, 1),
		historyStep(HistoryForward, HistoryInsert, 1, 3, 5, 8, 2, 0, 2),
		historyStep(HistoryForward, HistoryDelete, 6, 1, 8, 7, 3, 0, 3),
		historyStep(HistoryForward, HistoryInsert, 6, 1, 7, 8, 4, 0, 4),
	}})
}

func TestHistoryOversizedActionIsComplete(t *testing.T) {
	editor := NewEditor("")
	cursor, _ := historyMustTrack(t, editor)
	const primitives = 2049
	if err := editor.Transact(func(draft *Editor) error {
		for range primitives {
			draft.BreakUndoGroup()
			draft.Insert("x")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	frames, latest := historyMustRead(t, editor, cursor)
	if len(frames) != 1 || len(frames[0].Steps) != primitives {
		t.Fatalf("oversized action = %d frames with %d steps, want 1/%d", len(frames), len(frames[0].Steps), primitives)
	}
	frame := frames[0]
	if frame.BeforeBytes != 0 || frame.AfterBytes != primitives || latest.sequence != 1 {
		t.Fatalf("oversized action bytes/cursor = %d->%d/%+v", frame.BeforeBytes, frame.AfterBytes, latest)
	}
	for index, step := range frame.Steps {
		if step.Operation != HistoryForward || step.Primitive != HistoryInsert || step.Position != index ||
			step.Length != 1 || step.BeforeBytes != index || step.AfterBytes != index+1 ||
			step.Context.Current != index+1 || step.Context.RetainedTo != index+1 {
			t.Fatalf("oversized step %d = %+v", index, step)
		}
	}
	historyAssertAccounting(t, editor)
}

var errHistoryTestTransaction = &historyTestError{}

type historyTestError struct{}

func (*historyTestError) Error() string { return "history test transaction error" }

func TestHistorySaveFrames(t *testing.T) {
	editor := NewEditor("abc")
	cursor, _ := historyMustTrack(t, editor)
	editor.Insert("x")
	frames, latest := historyMustRead(t, editor, cursor)
	historyAssertFrames(t, frames, cursor, 0, historyFrameWant{3, 4, []HistoryStep{
		historyStep(HistoryForward, HistoryInsert, 3, 1, 3, 4, 1, 0, 1),
	}})

	editor.SetSavePoint()
	frames, latest = historyMustRead(t, editor, latest)
	historyAssertFrames(t, frames, cursor, 1, historyFrameWant{4, 4, []HistoryStep{historySaveStep(4, 4, 1, 1)}})
	if !editor.AtSavePoint() {
		t.Fatal("Save frame did not mark the source saved")
	}

	editor.SetSavePoint()
	frames, latest = historyMustRead(t, editor, latest)
	historyAssertFrames(t, frames, cursor, 2, historyFrameWant{4, 4, []HistoryStep{historySaveStep(4, 4, 1, 1)}})

	editor.SetSelection(Range{Start: 0, End: 0})
	editor.SetSavePoint()
	frames, latest = historyMustRead(t, editor, latest)
	historyAssertFrames(t, frames, cursor, 3, historyFrameWant{4, 4, []HistoryStep{historySaveStep(4, 4, 1, 1)}})

	editor.Insert("y")
	frames, latest = historyMustRead(t, editor, latest)
	historyAssertFrames(t, frames, cursor, 4, historyFrameWant{4, 5, []HistoryStep{
		historyStep(HistoryForward, HistoryInsert, 0, 1, 4, 5, 2, 1, 2),
	}})
	if editor.AtSavePoint() {
		t.Fatal("edit after Save remained at the save point")
	}
	editor.SetSavePoint()
	frames, _ = historyMustRead(t, editor, latest)
	historyAssertFrames(t, frames, cursor, 5, historyFrameWant{5, 5, []HistoryStep{historySaveStep(5, 5, 2, 2)}})
}

func TestHistoryOwnedFramesAndCursorRefusal(t *testing.T) {
	editor := NewEditor("a")
	cursor, _ := historyMustTrack(t, editor)
	editor.SetSelection(Range{Start: 1, End: 1})
	editor.Insert("b")
	editor.Insert("c")
	editor.Insert("d")

	first, latest := historyMustRead(t, editor, cursor)
	first[0].Steps[0].Position = 99
	first[0].Steps[0].BeforeBytes = 99
	first[0].Steps = append(first[0].Steps, HistoryStep{Operation: HistoryRedo})
	first[0].BeforeBytes = 99
	first = append(first, HistoryFrame{})
	second, latestAgain := historyMustRead(t, editor, cursor)
	if latestAgain != latest || len(second) != 3 {
		t.Fatalf("repeat read = %d frames/%+v, want 3/%+v", len(second), latestAgain, latest)
	}
	historyAssertFrames(t, second, cursor, 0,
		historyFrameWant{1, 2, []HistoryStep{historyStep(HistoryForward, HistoryInsert, 1, 1, 1, 2, 1, 0, 1)}},
		historyFrameWant{2, 3, []HistoryStep{historyStep(HistoryForward, HistoryInsert, 2, 1, 2, 3, 2, 0, 2)}},
		historyFrameWant{3, 4, []HistoryStep{historyStep(HistoryForward, HistoryInsert, 3, 1, 3, 4, 3, 0, 3)}},
	)

	thirdConsumer, thirdLatest := historyMustRead(t, editor, cursor)
	if thirdLatest != latest || len(thirdConsumer) != 3 {
		t.Fatal("a third consumer could not reread the retained cursor")
	}

	wrongSource := cursor
	wrongSource.source++
	if frames, _, ok := editor.HistorySince(wrongSource); ok || frames != nil {
		t.Fatal("wrong source cursor was accepted")
	}
	wrongGeneration := cursor
	wrongGeneration.generation++
	if frames, _, ok := editor.HistorySince(wrongGeneration); ok || frames != nil {
		t.Fatal("wrong generation cursor was accepted")
	}
	future := latest
	future.sequence++
	if frames, _, ok := editor.HistorySince(future); ok || frames != nil {
		t.Fatal("future cursor was accepted")
	}
	if frames, _, ok := editor.HistorySince(HistoryCursor{}); ok || frames != nil {
		t.Fatal("zero cursor was accepted")
	}

	limit := NewEditor("")
	limitCursor, _ := historyMustTrack(t, limit)
	limit.SetSelection(Range{Start: 0, End: 0})
	for range 43 {
		limit.Insert("x")
	}
	_, cursor43 := historyMustRead(t, limit, limitCursor)
	for range 1 {
		limit.Insert("x")
	}
	_, cursor44 := historyMustRead(t, limit, cursor43)
	for range 256 {
		limit.Insert("x")
	}
	frames, latestLimit, ok := limit.HistorySince(cursor44)
	if !ok || len(frames) != 256 || latestLimit.sequence != 300 {
		t.Fatalf("retained cursor after eviction = %d frames/%+v/%t, want 256/%d/true", len(frames), latestLimit, ok, 300)
	}
	if frames, _, ok := limit.HistorySince(cursor43); ok || frames != nil {
		t.Fatal("a gap cursor older than the retained ring was accepted")
	}
	if frames, _, ok := limit.HistorySince(limitCursor); ok || frames != nil {
		t.Fatal("the expired bootstrap cursor was accepted")
	}
	if _, _, ok := limit.TrackHistory(); ok {
		t.Fatal("TrackHistory returned an expired bootstrap")
	}
	frames, latestAgain, ok = limit.HistorySince(cursor44)
	if !ok || len(frames) != 256 || latestAgain.sequence != 300 {
		t.Fatal("refused second bootstrap disturbed a retained consumer")
	}
}

func TestHistoryUnsupportedLifecycleInvalidates(t *testing.T) {
	composition := NewEditor("abc")
	compositionCursor, _ := historyMustTrack(t, composition)
	composition.SetMarkedText("x", Range{})
	if frames, _, ok := composition.HistorySince(compositionCursor); ok || frames != nil {
		t.Fatal("composition published a frame instead of invalidating")
	}
	if _, _, ok := composition.TrackHistory(); ok {
		t.Fatal("TrackHistory resumed after composition invalidation")
	}

	trim := NewEditor("")
	trimCursor, _ := historyMustTrack(t, trim)
	for range maxHistoryEntries + 1 {
		trim.Paste("x")
	}
	if frames, _, ok := trim.HistorySince(trimCursor); ok || frames != nil {
		t.Fatal("history trim published retained frames instead of invalidating")
	}
	if _, _, ok := trim.TrackHistory(); ok {
		t.Fatal("TrackHistory resumed after trim invalidation")
	}
	if trim.RuneCount() != maxHistoryEntries+1 {
		t.Fatalf("trim fixture text length = %d", trim.RuneCount())
	}
}

func TestHistoryTrimReleasesCoalescedPrimitiveAccounting(t *testing.T) {
	editor := NewEditor("")
	historyMustTrack(t, editor)
	for range maxHistoryEntries + 1 {
		editor.BreakUndoGroup()
		editor.Insert("x")
	}
	history := editor.journal.history
	if history.available {
		t.Fatal("true history trim did not invalidate the producer")
	}
	if len(editor.history) != maxHistoryEntries || editor.historyRunes != maxHistoryEntries {
		t.Fatalf("trim retained %d units/%d runes, want %d/%d", len(editor.history), editor.historyRunes, maxHistoryEntries, maxHistoryEntries)
	}
	for index := range editor.history {
		if len(editor.history[index].primitives) != 0 {
			t.Fatalf("trim retained coalesced primitive ownership on unit %d", index)
		}
	}
	editor.ClearHistory()
	if editor.historyRunes != 0 || len(editor.history) != 0 {
		t.Fatal("ClearHistory did not release the remaining trimmed units")
	}
}

func TestHistoryDefaultLayoutAndAllocations(t *testing.T) {
	if got := unsafe.Sizeof(Editor{}); got != 368 {
		t.Fatalf("Editor size = %d, want 368", got)
	}
	if got := unsafe.Sizeof(historyEdit{}); got != 120 {
		t.Fatalf("historyEdit size = %d, want 120", got)
	}
	construction := testing.AllocsPerRun(100, func() {
		historyLayoutSink = NewEditor("base")
	})
	if construction != 2 {
		t.Fatalf("default construction allocations = %.0f, want 2", construction)
	}
	var editor *Editor
	typing := testing.AllocsPerRun(100, func() {
		editor = NewEditor("")
		for _, value := range []string{"h", "e", "\n", "l", "l", "o"} {
			editor.Insert(value)
		}
		editor.Undo()
		editor.Redo()
		historyLayoutSink = editor
	})
	// The repository's b.Loop benchmarks report 13 allocations; AllocsPerRun
	// includes one harness allocation for the same cycle at the unmodified
	// baseline, so 14 is the unchanged default here.
	if typing != 14 {
		t.Fatalf("default typing allocations = %.0f, want 14", typing)
	}
}

var historyLayoutSink *Editor

func TestHistoryJournalSizeRecorded(t *testing.T) {
	t.Logf("Editor=%d historyEdit=%d editJournal=%d historyJournal=%d", unsafe.Sizeof(Editor{}), unsafe.Sizeof(historyEdit{}), unsafe.Sizeof(editJournal{}), unsafe.Sizeof(historyJournal{}))
}
