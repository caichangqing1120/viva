package text

import (
	"reflect"
	"strings"
	"testing"
	"unsafe"
)

func compositionRetentionSnapshot(editor *Editor) conformanceState {
	return conformanceStateOf(editor)
}

func compositionLargeOverwrite(t *testing.T, editor *Editor, selection Range, count int) {
	t.Helper()
	editor.SetSelection(selection)
	editor.SetOverwrite(true)
	if !editor.EnableTentativeComposition() {
		t.Fatal("EnableTentativeComposition refused")
	}
	compositionApply(t, editor, compositionUpdate(strings.Repeat("z", count), Range{}))
}

// TestCompositionRetentionLargeOverwriteOneFrame covers >2048 ordinary
// overwrite units (2,049 scalars = 4,098 source steps) as one complete atomic
// frame, above the legacy 256-entry trim, while remaining fully cancelable.
func TestCompositionRetentionLargeOverwriteOneFrame(t *testing.T) {
	const count = 2049
	document := strings.Repeat("a", count)
	editor := NewEditor(document)
	cursor, _, ok := editor.TrackHistory()
	if !ok {
		t.Fatal("TrackHistory refused")
	}
	compositionLargeOverwrite(t, editor, Range{Start: 0, End: 0}, count)
	if got := editor.String(); got != strings.Repeat("z", count) {
		t.Fatalf("text length = %d, want %d", len(got), count)
	}
	if len(editor.history) != count {
		t.Fatalf("ordinary units = %d, want %d", len(editor.history), count)
	}
	state := editor.compositionState()
	if state == nil || !state.live || state.unitStart != 0 || state.eventStart != 0 {
		t.Fatalf("retention boundaries = %+v, want live/0/0", state)
	}
	if editor.historyCost() > maxSingleEditRunes {
		t.Fatalf("physical+metadata cost = %d, want <= single-action bound", editor.historyCost())
	}
	t.Logf("2049 event physical=%d runes header=%d cap=%d payload=%d metadataTotal=%d",
		state.cost+editor.historyRunes, state.cost, cap(editor.history), editor.historyRunes, editor.historyCost())
	frames, latest, ok := editor.HistorySince(cursor)
	if !ok || len(frames) != 1 {
		t.Fatalf("frames = %d/%t, want one atomic frame", len(frames), ok)
	}
	frame := frames[0]
	if len(frame.Steps) != 2*count {
		t.Fatalf("frame steps = %d, want %d", len(frame.Steps), 2*count)
	}
	if frame.From != cursor || frame.To.sequence != cursor.sequence+1 {
		t.Fatalf("frame cursors = %+v -> %+v, want one sequence step from %+v", frame.From, frame.To, cursor)
	}
	for index, step := range frame.Steps {
		want := HistoryDelete
		if index%2 == 1 {
			want = HistoryInsert
		}
		if step.Operation != HistoryForward || step.Primitive != want {
			t.Fatalf("step %d = %+v, want forward/%d", index, step, want)
		}
		if step.Context.Current != index+1 {
			t.Fatalf("step %d Current = %d, want %d", index, step.Context.Current, index+1)
		}
	}
	if again, latestAgain, againOK := editor.HistorySince(cursor); !againOK || !reflect.DeepEqual(again, frames) || latestAgain != latest {
		t.Fatal("repeated retained read did not return equal owned frames")
	}
	compositionApply(t, editor, CompositionEvent{Phase: CompositionCancel})
	if got := editor.String(); got != document {
		t.Fatalf("cancel text length = %d, want %d", len(got), count)
	}
	if len(editor.history) != 0 || editor.CanUndo() {
		t.Fatalf("cancel retained %d units/%t Undo", len(editor.history), editor.CanUndo())
	}
	if state = editor.compositionState(); state == nil || state.live || state.completed {
		t.Fatalf("cancel left retention = %+v", state)
	}
}

// TestCompositionRetentionWithoutProducer proves physical cancelability does not
// depend on TrackHistory or producer metadata.
func TestCompositionRetentionWithoutProducer(t *testing.T) {
	const count = 2049
	document := strings.Repeat("a", count)
	editor := NewEditor(document)
	compositionLargeOverwrite(t, editor, Range{Start: 0, End: 0}, count)
	if editor.historyAvailable() != nil {
		t.Fatal("producer was unexpectedly available")
	}
	if len(editor.history) != count {
		t.Fatalf("units = %d, want %d", len(editor.history), count)
	}
	if !editor.ApplyComposition(CompositionEvent{Phase: CompositionCancel}) {
		t.Fatal("Cancel refused without producer")
	}
	if got := editor.String(); got != document {
		t.Fatalf("cancel without producer restored %d runes, want %d", len(got), count)
	}
}

// TestCompositionRetentionMetadataFailureKeepsPhysicalWork drives optional
// metadata over the single-action bound while the physical event still fits;
// the accepted overwrite must survive and remain cancelable.
func TestCompositionRetentionMetadataFailureKeepsPhysicalWork(t *testing.T) {
	const count = 60000
	document := strings.Repeat("a", count)
	editor := NewEditor(document)
	if _, _, ok := editor.TrackHistory(); !ok {
		t.Fatal("TrackHistory refused")
	}
	compositionLargeOverwrite(t, editor, Range{Start: 0, End: 0}, count)
	if got := editor.String(); got != strings.Repeat("z", count) {
		t.Fatalf("physical text length = %d, want %d", len(got), count)
	}
	if len(editor.history) != count {
		t.Fatalf("ordinary units = %d, want %d", len(editor.history), count)
	}
	if editor.historyAvailable() != nil {
		t.Fatal("optional metadata was not invalidated at the over-budget event")
	}
	state := editor.compositionState()
	if state == nil || !state.live {
		t.Fatalf("physical retention lost: %+v", state)
	}
	physical := state.cost + editor.historyRunes
	if physical > maxSingleEditRunes {
		t.Fatalf("physical retained cost = %d, want <= %d", physical, maxSingleEditRunes)
	}
	t.Logf("60000 event physical=%d runes header=%d cap=%d payload=%d available=%t",
		physical, state.cost, cap(editor.history), editor.historyRunes, editor.historyAvailable() != nil)
	compositionApply(t, editor, CompositionEvent{Phase: CompositionCancel})
	if got := editor.String(); got != document {
		t.Fatalf("cancel after metadata failure restored %d runes, want %d", len(got), count)
	}
}

// TestCompositionRetentionPhysicalRefusalPreservesOldTentative preflights the
// whole replacement before rolling back an existing live preedit.
func TestCompositionRetentionPhysicalRefusalPreservesOldTentative(t *testing.T) {
	const count = 140000
	document := strings.Repeat("a", count)
	editor := NewEditor(document)
	compositionLargeOverwrite(t, editor, Range{Start: 0, End: 0}, 1)
	beforeText, beforeSelection := editor.String(), editor.Selection()
	beforeHistory := len(editor.history)
	beforeRunes := editor.historyRunes
	state := editor.compositionState()
	beforeStart, beforeUnitStart := state.start, state.unitStart
	if !editor.ApplyComposition(compositionUpdate(strings.Repeat("z", count), Range{})) {
		// Expected refusal: this is the assertion target, not a successful path.
	} else {
		t.Fatal("physically over-budget overwrite update was accepted")
	}
	if editor.String() != beforeText || editor.Selection() != beforeSelection ||
		len(editor.history) != beforeHistory || editor.historyRunes != beforeRunes ||
		state.start != beforeStart || state.unitStart != beforeUnitStart || !state.live {
		t.Fatal("physical refusal changed the old tentative bytes/history/cursor")
	}
	compositionApply(t, editor, CompositionEvent{Phase: CompositionCancel})
	if got := editor.String(); got != document {
		t.Fatalf("cancel after refusal restored %d runes, want %d", len(got), count)
	}
}

// TestCompositionRetentionPassiveDisableAndNextMutation keeps one completed
// action intact across inactive Disable, repeated Disable/getter/selection-only
// operations, then retires it on the next accepted actual mutation.
func TestCompositionRetentionPassiveDisableAndNextMutation(t *testing.T) {
	const count = 3000
	document := strings.Repeat("a", count)
	editor := NewEditor(document)
	compositionLargeOverwrite(t, editor, Range{Start: 0, End: 0}, count)
	compositionApply(t, editor, CompositionEvent{Phase: CompositionCommit})
	if got := editor.String(); got != strings.Repeat("z", count) {
		t.Fatalf("commit text length = %d, want %d", len(got), count)
	}
	if len(editor.history) != count {
		t.Fatalf("completed units = %d, want %d", len(editor.history), count)
	}
	text, selection, units, runes := editor.String(), editor.Selection(), len(editor.history), editor.historyRunes
	if !editor.DisableTentativeComposition() {
		t.Fatal("inactive Disable refused")
	}
	if editor.TentativeCompositionEnabled() {
		t.Fatal("getter reports enabled after inactive Disable")
	}
	state := editor.compositionState()
	if state == nil || state.enabled || !state.completed || !state.passive {
		t.Fatalf("passive descriptor = %+v, want disabled/completed/passive", state)
	}
	if editor.String() != text || editor.Selection() != selection || len(editor.history) != units || editor.historyRunes != runes {
		t.Fatal("inactive Disable changed text/history/cursors")
	}
	if !editor.DisableTentativeComposition() || editor.TentativeCompositionEnabled() {
		t.Fatal("repeated Disable did not stay true/disabled")
	}
	editor.SetSelection(Range{Start: 1, End: 1})
	editor.MoveTo(2, false)
	_ = editor.String()
	if len(editor.history) != units {
		t.Fatalf("read/getter/selection-only mutated units to %d", len(editor.history))
	}
	if !editor.EnableTentativeComposition() || !editor.TentativeCompositionEnabled() {
		t.Fatal("re-enable refused the passive completed owner")
	}
	if len(editor.history) != units {
		t.Fatalf("re-enable changed units to %d", len(editor.history))
	}
	// The next accepted actual mutation retires completed protection first and
	// then applies ordinary finite-history limits.
	editor.MoveTo(len(editor.runes), false)
	editor.Insert("Q")
	if len(editor.history) > maxHistoryEntries {
		t.Fatalf("next mutation kept %d units, want <= %d", len(editor.history), maxHistoryEntries)
	}
	if got := editor.String(); !strings.HasSuffix(got, "Q") {
		t.Fatalf("next mutation text suffix = %q", got[len(got)-1:])
	}
}

// TestCompositionRetentionProducerSurvivesPassiveDisable checks that Disable
// changes only capability state, not producer frames or cursors.
func TestCompositionRetentionProducerSurvivesPassiveDisable(t *testing.T) {
	editor := NewEditor("abc")
	cursor, _, ok := editor.TrackHistory()
	if !ok {
		t.Fatal("TrackHistory refused")
	}
	editor.SetSelection(Range{Start: 0, End: 0})
	editor.SetOverwrite(true)
	if !editor.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	compositionApply(t, editor, compositionUpdate("x", Range{}))
	compositionApply(t, editor, CompositionEvent{Phase: CompositionCommit})
	frames, latest, ok := editor.HistorySince(cursor)
	if !ok || len(frames) != 1 {
		t.Fatalf("frames before Disable = %d/%t", len(frames), ok)
	}
	if !editor.DisableTentativeComposition() {
		t.Fatal("Disable refused")
	}
	if empty, same, ok := editor.HistorySince(latest); !ok || len(empty) != 0 || same != latest {
		t.Fatal("post-Disable empty read changed the producer cursor")
	}
	again, latestAgain, ok := editor.HistorySince(cursor)
	if !ok || len(again) != 1 || latestAgain != latest {
		t.Fatalf("producer changed across passive Disable: %d/%t", len(again), ok)
	}
	if !reflect.DeepEqual(again, frames) {
		t.Fatal("passive Disable changed owned producer frames")
	}
}

// TestCompositionRetentionClearAndCostRelease covers active/passive Clear and
// exact release of the capacity charge.
func TestCompositionRetentionClearAndCostRelease(t *testing.T) {
	const count = 3000
	document := strings.Repeat("a", count)
	editor := NewEditor(document)
	compositionLargeOverwrite(t, editor, Range{Start: 0, End: 0}, count)
	state := editor.compositionState()
	header := compositionHeaderRunes
	if state.cost <= header {
		t.Fatalf("live retention cost = %d, want above header %d", editor.historyCost(), header)
	}
	editor.ClearHistory()
	if got := editor.String(); got != strings.Repeat("z", count) {
		t.Fatalf("active Clear changed text length to %d", len(got))
	}
	if len(editor.history) != 0 || editor.CanUndo() {
		t.Fatal("active Clear kept ordinary units/Undo")
	}
	if state = editor.compositionState(); state == nil || state.live || state.completed || stateTouchesCapacity(state) {
		t.Fatalf("active Clear left retention/capacity charge: %+v", state)
	}
	if editor.historyCost() != header {
		t.Fatalf("history cost after Clear = %d, want header %d", editor.historyCost(), header)
	}

	passive := NewEditor(document)
	compositionLargeOverwrite(t, passive, Range{Start: 0, End: 0}, count)
	compositionApply(t, passive, CompositionEvent{Phase: CompositionCommit})
	if !passive.DisableTentativeComposition() {
		t.Fatal("Disable refused")
	}
	passive.ClearHistory()
	if passive.compositionState() != nil || passive.TentativeCompositionEnabled() {
		t.Fatal("Clear kept a passive disabled descriptor")
	}
	if passive.String() != strings.Repeat("z", count) {
		t.Fatal("passive Clear changed visible text")
	}
}

// TestCompositionRetentionOwnedTextCapacity keeps the actual retained rune
// backing charged after Commit/passive Disable. Every event unit must own
// exactly its used slots, never an oversized shared event buffer or allocator
// slack; the lower bound is the real unit/text/frame storage.
func TestCompositionRetentionOwnedTextCapacity(t *testing.T) {
	const count = 2049
	editor := NewEditor(strings.Repeat("a", count))
	cursor, _, ok := editor.TrackHistory()
	if !ok {
		t.Fatal("TrackHistory refused")
	}
	editor.SetSelection(Range{Start: 0, End: 0})
	editor.SetOverwrite(true)
	if !editor.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	compositionApply(t, editor, compositionUpdate(strings.Repeat("b", count), Range{}))
	compositionApply(t, editor, CompositionEvent{Phase: CompositionCommit})
	_, cursor, ok = editor.HistorySince(cursor)
	if !ok {
		t.Fatal("committed producer read refused")
	}
	if !editor.DisableTentativeComposition() || editor.TentativeCompositionEnabled() {
		t.Fatal("inactive Disable failed")
	}
	editor.DisableTentativeComposition()
	editor.MoveTo(0, false)
	editor.SetSelection(Range{})
	if len(editor.history) != count {
		t.Fatalf("passive units = %d, want %d", len(editor.history), count)
	}
	minimum := cap(editor.history) * int(unsafe.Sizeof(historyEdit{}))
	for index := range editor.history {
		edit := &editor.history[index]
		if cap(edit.removed) != len(edit.removed) || cap(edit.inserted) != len(edit.inserted) {
			t.Fatalf("unit %d text len/cap removed=%d/%d inserted=%d/%d", index,
				len(edit.removed), cap(edit.removed), len(edit.inserted), cap(edit.inserted))
		}
		minimum += (cap(edit.removed) + cap(edit.inserted)) * int(unsafe.Sizeof(rune(0)))
		minimum += cap(edit.primitives) * int(unsafe.Sizeof(Edit{}))
	}
	history := editor.journal.history
	minimum += int(unsafe.Sizeof(*history)) + history.frameCapacity*int(unsafe.Sizeof(HistoryFrame{}))
	for _, frame := range history.frames {
		minimum += cap(frame.Steps) * int(unsafe.Sizeof(HistoryStep{}))
	}
	if charged := editor.historyCost() * 4; charged < minimum {
		t.Fatalf("owned text storage undercharged: charged=%d minimum=%d", charged, minimum)
	} else {
		t.Logf("passive capacity charged=%d minimum=%d textCap=%d", charged, minimum, func() int {
			total := 0
			for index := range editor.history {
				total += cap(editor.history[index].removed) + cap(editor.history[index].inserted)
			}
			return total
		}())
	}
	if frames, latest, ok := editor.HistorySince(cursor); !ok || len(frames) != 0 || latest != cursor {
		t.Fatal("passive capacity reads lost the producer")
	}
	if !editor.Undo() || editor.String() != strings.Repeat("b", count-1)+"a" {
		t.Fatal("capacity ownership changed ordinary Undo")
	}
}

func compositionRetiredStorageMinimum(editor *Editor) int {
	minimum := max(0, cap(editor.history)-maxHistoryEntries) * int(unsafe.Sizeof(historyEdit{}))
	for index := range editor.history {
		edit := &editor.history[index]
		minimum += (cap(edit.removed) + cap(edit.inserted)) * int(unsafe.Sizeof(rune(0)))
	}
	return minimum
}

func compositionRetiredLargeOwner(t *testing.T, metadata, disable bool) *Editor {
	t.Helper()
	const count = 2049
	editor := NewEditor(strings.Repeat("a", count))
	if metadata {
		if _, _, ok := editor.TrackHistory(); !ok {
			t.Fatal("TrackHistory refused")
		}
	}
	editor.SetSelection(Range{Start: 0, End: 0})
	editor.SetOverwrite(true)
	if !editor.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	compositionApply(t, editor, compositionUpdate(strings.Repeat("b", count), Range{}))
	compositionApply(t, editor, CompositionEvent{Phase: CompositionCommit})
	if disable && (!editor.DisableTentativeComposition() || editor.TentativeCompositionEnabled()) {
		t.Fatal("passive Disable failed")
	}
	return editor
}

// TestCompositionRetiredStorageOwnership keeps a charged nonprotecting owner
// after the completed pin retires. Real large array/text backing stays charged
// until it is compacted or released; ordinary Undo/Redo units are never cut.
func TestCompositionRetiredStorageOwnership(t *testing.T) {
	t.Run("undo keeps units and charges actual capacity", func(t *testing.T) {
		editor := compositionRetiredLargeOwner(t, false, true)
		if !editor.Undo() {
			t.Fatal("ordinary Undo refused")
		}
		if len(editor.history) != 2049 || editor.CanRedo() == false {
			t.Fatalf("Undo units/redo = %d/%t", len(editor.history), editor.CanRedo())
		}
		if charged, minimum := editor.historyCost()*4, compositionRetiredStorageMinimum(editor); charged < minimum {
			t.Fatalf("retired Undo charge = %d, minimum = %d", charged, minimum)
		} else {
			t.Logf("retired Undo units len/cap=%d/%d charged=%d minimum=%d", len(editor.history), cap(editor.history), charged, minimum)
		}
		state := editor.compositionState()
		if state == nil || state.enabled || state.completed || state.passive {
			t.Fatalf("retired owner = %+v, want disabled/nonprotecting", state)
		}
		if !editor.Redo() || editor.String() != strings.Repeat("b", 2049) {
			t.Fatal("Redo lost the retired ownership units")
		}
	})

	t.Run("append trims and settles backing", func(t *testing.T) {
		editor := compositionRetiredLargeOwner(t, false, true)
		editor.MoveTo(editor.RuneCount(), false)
		editor.Insert("Q")
		if len(editor.history) > maxHistoryEntries || cap(editor.history) > maxHistoryEntries {
			t.Fatalf("append backing len/cap = %d/%d", len(editor.history), cap(editor.history))
		}
		if charged, minimum := editor.historyCost()*4, compositionRetiredStorageMinimum(editor); charged < minimum {
			t.Fatalf("retired append charge = %d, minimum = %d", charged, minimum)
		} else {
			t.Logf("retired append units len/cap=%d/%d charged=%d minimum=%d", len(editor.history), cap(editor.history), charged, minimum)
		}
		if editor.compositionState() != nil || editor.TentativeCompositionEnabled() {
			t.Fatal("ordinary append did not release the settled disabled owner")
		}
		if !strings.HasSuffix(editor.String(), "Q") || !editor.Undo() {
			t.Fatal("append/Undo changed ordinary ownership")
		}
	})

	t.Run("merge and discarded redo", func(t *testing.T) {
		editor := compositionRetiredLargeOwner(t, false, false)
		if !editor.Undo() {
			t.Fatal("Undo refused")
		}
		editor.MoveTo(editor.RuneCount(), false)
		editor.Insert("Q")
		editor.Insert("R")
		if len(editor.history) > maxHistoryEntries || cap(editor.history) > maxHistoryEntries {
			t.Fatalf("merge/redo backing len/cap = %d/%d", len(editor.history), cap(editor.history))
		}
		if charged, minimum := editor.historyCost()*4, compositionRetiredStorageMinimum(editor); charged < minimum {
			t.Fatalf("merge charge = %d, minimum = %d", charged, minimum)
		}
		if editor.CanRedo() {
			t.Fatal("new mutation retained discarded redo")
		}
	})
	t.Run("disabled discarded redo releases", func(t *testing.T) {
		editor := compositionRetiredLargeOwner(t, false, true)
		if !editor.Undo() {
			t.Fatal("Undo refused")
		}
		editor.MoveTo(editor.RuneCount(), false)
		editor.Insert("Q")
		if editor.CanRedo() {
			t.Fatal("new mutation retained discarded redo")
		}
		if charged, minimum := editor.historyCost()*4, compositionRetiredStorageMinimum(editor); charged < minimum {
			t.Fatalf("disabled redo charge = %d, minimum = %d", charged, minimum)
		}
	})

	t.Run("restrict and clear release", func(t *testing.T) {
		editor := compositionRetiredLargeOwner(t, false, false)
		editor.Restrict(func(rune) bool { return true }, 0)
		if editor.TentativeCompositionEnabled() {
			t.Fatal("Restrict left the getter enabled")
		}
		if charged, minimum := editor.historyCost()*4, compositionRetiredStorageMinimum(editor); charged < minimum {
			t.Fatalf("Restrict charge = %d, minimum = %d", charged, minimum)
		}
		editor.ClearHistory()
		if editor.compositionState() != nil || editor.historyCost() != 0 {
			t.Fatalf("Clear retained owner/cost: %+v/%d", editor.compositionState(), editor.historyCost())
		}
	})

	t.Run("enabled nonprotecting owner", func(t *testing.T) {
		editor := compositionRetiredLargeOwner(t, true, false)
		if !editor.Undo() {
			t.Fatal("Undo refused")
		}
		if !editor.TentativeCompositionEnabled() {
			t.Fatal("enabled receiver lost its capability on pin retirement")
		}
		if charged, minimum := editor.historyCost()*4, compositionRetiredStorageMinimum(editor); charged < minimum {
			t.Fatalf("enabled retired charge = %d, minimum = %d", charged, minimum)
		}
	})

	t.Run("producer unavailable", func(t *testing.T) {
		editor := compositionRetiredLargeOwner(t, true, true)
		editor.invalidateHistory()
		if !editor.Undo() {
			t.Fatal("Undo refused with unavailable metadata")
		}
		if charged, minimum := editor.historyCost()*4, compositionRetiredStorageMinimum(editor); charged < minimum {
			t.Fatalf("unavailable retired charge = %d, minimum = %d", charged, minimum)
		}
	})
}

// TestCompositionRetiredBudgetCompaction keeps the ordinary 256-entry limit
// authoritative after a large completed pin is retired. Stale array capacity
// must not force the eviction loop below maxHistoryEntries before compaction.
func TestCompositionRetiredBudgetCompaction(t *testing.T) {
	const count = 10000
	initial := strings.Repeat("a", count)
	editor := NewEditor(initial)
	editor.SetSelection(Range{Start: 0, End: 0})
	editor.SetOverwrite(true)
	if !editor.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	compositionApply(t, editor, compositionUpdate(strings.Repeat("b", count), Range{}))
	compositionApply(t, editor, CompositionEvent{Phase: CompositionCommit})
	if !editor.DisableTentativeComposition() {
		t.Fatal("Disable refused")
	}
	editor.MoveTo(editor.RuneCount(), false)
	editor.Insert("Q")
	if len(editor.history) != maxHistoryEntries {
		t.Fatalf("retired budget compaction retained %d units, want %d", len(editor.history), maxHistoryEntries)
	}
	if !editor.Undo() || editor.String() != strings.Repeat("b", count) {
		t.Fatal("post-compaction ordinary Undo failed")
	}
	if !editor.Redo() || editor.String() != strings.Repeat("b", count)+"Q" {
		t.Fatal("post-compaction ordinary Redo failed")
	}
	if len(editor.history) != maxHistoryEntries {
		t.Fatalf("Redo changed surviving units to %d", len(editor.history))
	}
}

// TestCompositionDisableStates covers the public already-disabled/ordinary and
// private/active refusals without allocating a capable journal unnecessarily.
func TestCompositionDisableStates(t *testing.T) {
	var nilEditor *Editor
	if nilEditor.DisableTentativeComposition() {
		t.Fatal("nil receiver Disable succeeded")
	}
	ordinary := NewEditor("abc")
	if !ordinary.DisableTentativeComposition() || ordinary.journal != nil {
		t.Fatal("no-journal ordinary Disable allocated or failed")
	}
	journalOnly := NewEditor("abc")
	journalOnly.TrackEdits()
	before := conformanceStateOf(journalOnly)
	if !journalOnly.DisableTentativeComposition() || journalOnly.journal == nil || journalOnly.journal.composition != nil {
		t.Fatal("journal-only ordinary Disable changed capability state")
	}
	if after := conformanceStateOf(journalOnly); !reflect.DeepEqual(after, before) {
		t.Fatal("journal-only Disable changed visible/Undo state")
	}
	passive := NewEditor("abc")
	if !passive.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	passive.SetSelection(Range{Start: 0, End: 0})
	compositionApply(t, passive, compositionUpdate("x", Range{}))
	compositionApply(t, passive, CompositionEvent{Phase: CompositionCommit})
	if !passive.DisableTentativeComposition() || passive.TentativeCompositionEnabled() {
		t.Fatal("passive Disable failed")
	}
	if !passive.DisableTentativeComposition() || passive.TentativeCompositionEnabled() {
		t.Fatal("repeated passive Disable failed")
	}
	active := NewEditor("abc")
	if !active.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	compositionApply(t, active, compositionUpdate("x", Range{}))
	if active.DisableTentativeComposition() {
		t.Fatal("active composition Disable succeeded")
	}
	source := NewEditor("abc")
	if err := source.Transact(func(draft *Editor) error {
		if draft.DisableTentativeComposition() {
			return nil
		}
		return errPrivateDisable
	}); err != errPrivateDisable {
		t.Fatalf("private draft Disable callback = %v", err)
	}
}

type compositionDisableError string

func (e compositionDisableError) Error() string { return string(e) }

const errPrivateDisable = compositionDisableError("private draft Disable refused")

func stateTouchesCapacity(state *compositionState) bool {
	return state != nil && state.retentionActive()
}

// TestCompositionRetentionNoOpsDoNotRetire keeps one completed action across
// valid no-op events, repeated reads and selection-only operations.
func TestCompositionRetentionNoOpsDoNotRetire(t *testing.T) {
	const count = 3000
	document := strings.Repeat("a", count)
	editor := NewEditor(document)
	compositionLargeOverwrite(t, editor, Range{Start: 0, End: 0}, count)
	compositionApply(t, editor, CompositionEvent{Phase: CompositionCommit})
	state := editor.compositionState()
	if state == nil || !state.completed || state.completedStart != 0 {
		t.Fatalf("completed retention = %+v", state)
	}
	units := len(editor.history)
	for _, event := range []CompositionEvent{
		{Phase: CompositionResult},
		{Phase: CompositionCommit},
		{Phase: CompositionCancel},
		{Phase: CompositionUpdate},
	} {
		compositionApply(t, editor, event)
		if len(editor.history) != units || !state.completed || state.completedStart != 0 {
			t.Fatalf("no-op %+v retired completed retention: units=%d state=%+v", event.Phase, len(editor.history), state)
		}
	}
	editor.SetSelection(Range{Start: 0, End: 0})
	editor.SetSelection(Range{Start: 2, End: 2})
	compositionApply(t, editor, CompositionEvent{Phase: CompositionEmptyCommit})
	if len(editor.history) != units || !state.completed {
		t.Fatal("selection-only EmptyCommit retired completed retention")
	}
	editor.SetSelection(Range{Start: len(editor.runes), End: len(editor.runes)})
	editor.Insert("Q")
	if state.completed {
		t.Fatal("accepted actual mutation did not retire completed retention")
	}
}

// TestCompositionRetentionUndoAndRedoRetire keeps completed protection from
// surviving an ordinary Undo/Redo mutation.
func TestCompositionRetentionUndoAndRedoRetire(t *testing.T) {
	const count = 3000
	document := strings.Repeat("a", count)
	editor := NewEditor(document)
	compositionLargeOverwrite(t, editor, Range{Start: 0, End: 0}, count)
	compositionApply(t, editor, CompositionEvent{Phase: CompositionCommit})
	state := editor.compositionState()
	if state == nil || !state.completed {
		t.Fatalf("completed retention = %+v", state)
	}
	if !editor.Undo() {
		t.Fatal("Undo refused")
	}
	if state.completed || state.passive {
		t.Fatalf("Undo did not retire completed protection: %+v", state)
	}
	if !editor.Redo() {
		t.Fatal("Redo refused")
	}
	if state.completed {
		t.Fatal("Redo re-created completed protection")
	}
}

// TestCompositionRetentionMixedLargeActiveUpdate keeps one complete mixed frame
// (rollback inverses plus >2048 overwrite scalar forward steps) from the exact
// incoming cursor.
func TestCompositionRetentionMixedLargeActiveUpdate(t *testing.T) {
	const count = 2049
	document := strings.Repeat("a", count)
	editor := NewEditor(document)
	cursor, _, ok := editor.TrackHistory()
	if !ok {
		t.Fatal("TrackHistory refused")
	}
	editor.SetSelection(Range{Start: 0, End: 0})
	editor.SetOverwrite(true)
	if !editor.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	compositionApply(t, editor, compositionUpdate("x", Range{}))
	compositionApply(t, editor, compositionUpdate(strings.Repeat("z", count), Range{}))
	if got := editor.String(); got != strings.Repeat("z", count) {
		t.Fatalf("active large overwrite text length = %d, want %d", len(got), count)
	}
	if len(editor.history) != count {
		t.Fatalf("units = %d, want %d", len(editor.history), count)
	}
	frames, _, ok := editor.HistorySince(cursor)
	if !ok || len(frames) != 2 {
		t.Fatalf("frames = %d/%t, want initial preedit plus mixed update", len(frames), ok)
	}
	mixed := frames[1]
	if mixed.From.sequence != cursor.sequence+1 {
		t.Fatalf("mixed frame From = %+v, want the preedit cursor", mixed.From)
	}
	if len(mixed.Steps) != 2+2*count {
		t.Fatalf("mixed frame steps = %d, want rollback 2 + forward %d", len(mixed.Steps), 2*count)
	}
	if mixed.Steps[0].Operation != HistoryUndo || mixed.Steps[1].Operation != HistoryUndo {
		t.Fatalf("mixed rollback prefix = %d/%d, want Undo/Undo", mixed.Steps[0].Operation, mixed.Steps[1].Operation)
	}
	for index, step := range mixed.Steps[2:] {
		if step.Operation != HistoryForward {
			t.Fatalf("mixed forward step %d = %d, want HistoryForward", index, step.Operation)
		}
	}
	compositionApply(t, editor, CompositionEvent{Phase: CompositionCancel})
	if got := editor.String(); got != document {
		t.Fatalf("cancel mixed update restored %d runes, want %d", len(got), count)
	}
}

// TestCompositionRetentionRestrictKeepsPassivePin verifies the configuration
// escape still seals producer metadata but retains one passive physical action.
func TestCompositionRetentionRestrictKeepsPassivePin(t *testing.T) {
	const count = 3000
	document := strings.Repeat("a", count)
	editor := NewEditor(document)
	compositionLargeOverwrite(t, editor, Range{Start: 0, End: 0}, count)
	compositionApply(t, editor, CompositionEvent{Phase: CompositionCommit})
	editor.Restrict(func(value rune) bool { return true }, 0)
	if editor.TentativeCompositionEnabled() {
		t.Fatal("Restrict left the capability enabled")
	}
	state := editor.compositionState()
	if state == nil || state.enabled || !state.completed || !state.passive {
		t.Fatalf("Restrict passive state = %+v", state)
	}
	if len(editor.history) != count || editor.String() != strings.Repeat("z", count) {
		t.Fatal("Restrict changed completed bytes/units")
	}
	editor.SetSelection(Range{Start: len(editor.runes), End: len(editor.runes)})
	editor.Insert("Q")
	if len(editor.history) > maxHistoryEntries {
		t.Fatalf("post-Restrict mutation kept %d units", len(editor.history))
	}
}

// TestCompositionRetentionPrefixMovement repairs live physical indices when an
// older ordinary prefix is trimmed.
func TestCompositionRetentionPrefixMovement(t *testing.T) {
	editor := NewEditor("")
	for index := 0; index < 40; index++ {
		editor.BreakUndoGroup()
		editor.Insert("a")
	}
	prefix := len(editor.history)
	if prefix < 40 {
		t.Fatalf("prefix units = %d, want 40", prefix)
	}
	editor.SetSelection(Range{Start: editor.RuneCount(), End: editor.RuneCount()})
	editor.SetOverwrite(true)
	if !editor.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	// 300 overwrite units force the older 40-unit prefix out while the event is
	// live; the rollback boundary must move with the slice.
	compositionApply(t, editor, compositionUpdate(strings.Repeat("b", 300), Range{}))
	state := editor.compositionState()
	if state == nil || !state.live {
		t.Fatalf("retention state = %+v", state)
	}
	if state.unitStart != 0 || state.eventStart != 0 {
		t.Fatalf("repaired boundaries = unitStart %d/eventStart %d, want 0/0", state.unitStart, state.eventStart)
	}
	if len(editor.history) != 300 {
		t.Fatalf("units after prefix movement = %d, want 300", len(editor.history))
	}
	if !editor.ApplyComposition(CompositionEvent{Phase: CompositionCancel}) {
		t.Fatal("Cancel refused after prefix movement")
	}
	if editor.RuneCount() != 40 || editor.String() != strings.Repeat("a", 40) {
		t.Fatalf("cancel after prefix movement left %q", editor.String())
	}
}
