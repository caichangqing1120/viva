package text

import (
	"strings"
	"testing"
)

func historySingleActionOverhead(t *testing.T) int {
	t.Helper()
	calibration := NewEditor("")
	historyMustTrack(t, calibration)
	calibration.Insert("x")
	return calibration.historyCost() - 1
}

func historyFillSingleActionBudget(t *testing.T, editor *Editor, overhead int) int {
	t.Helper()
	count := maxSingleEditRunes - overhead
	editor.Insert(strings.Repeat("x", count))
	if editor.historyAvailable() == nil || editor.historyCost() != maxSingleEditRunes {
		t.Fatalf("budget-edge setup cost=%d, available=%t", editor.historyCost(), editor.historyAvailable() != nil)
	}
	return count
}

func historyAssertNoCoalescedPrimitives(t *testing.T, editor *Editor) {
	t.Helper()
	for index := range editor.history {
		edit := &editor.history[index]
		if edit.kind != editSeparate && len(edit.primitives) != 0 {
			t.Fatalf("unit %d retained %d producer-only primitives after budget invalidation", index, len(edit.primitives))
		}
	}
}

func TestHistoryBudgetInvalidationDropsMergedProducerPrimitive(t *testing.T) {
	editor := NewEditor("")
	cursor, _, ok := editor.TrackHistory()
	if !ok {
		t.Fatal("TrackHistory refused")
	}
	count := historyFillSingleActionBudget(t, editor, historySingleActionOverhead(t))
	editor.Insert("y")
	if editor.historyAvailable() != nil {
		t.Fatal("delivery storage failed to invalidate at the single-action limit")
	}
	if frames, _, ok := editor.HistorySince(cursor); ok || frames != nil {
		t.Fatal("unavailable budget replay served frames")
	}
	if len(editor.history) != 1 {
		t.Fatalf("merging budget edge produced %d units, want 1", len(editor.history))
	}
	historyAssertNoCoalescedPrimitives(t, editor)
	if editor.RuneCount() != count+1 {
		t.Fatalf("accepted text length = %d, want %d", editor.RuneCount(), count+1)
	}
	if !editor.Undo() {
		t.Fatal("ordinary Undo refused after budget invalidation")
	}
	if editor.RuneCount() != 0 || !editor.CanRedo() {
		t.Fatalf("ordinary Undo/Redo state = %d/%t, want empty/redo", editor.RuneCount(), editor.CanRedo())
	}
}

func TestHistoryBudgetInvalidationDropsNewUnitProducerPrimitive(t *testing.T) {
	editor := NewEditor("")
	cursor, _, ok := editor.TrackHistory()
	if !ok {
		t.Fatal("TrackHistory refused")
	}
	count := historyFillSingleActionBudget(t, editor, historySingleActionOverhead(t))
	editor.BreakUndoGroup()
	editor.Insert("y")
	if editor.historyAvailable() != nil {
		t.Fatal("delivery storage failed to invalidate at the single-action limit")
	}
	if frames, _, ok := editor.HistorySince(cursor); ok || frames != nil {
		t.Fatal("unavailable budget replay served frames")
	}
	// The surrounding ordinary trim may evict the older oversized unit; the
	// assertion that matters is that the new ordinary unit owns no stale
	// producer-only primitive.
	historyAssertNoCoalescedPrimitives(t, editor)
	last := &editor.history[len(editor.history)-1]
	if last.kind != editInsert {
		t.Fatalf("new budget-edge unit kind = %d, want ordinary insert", last.kind)
	}
	if editor.RuneCount() != count+1 {
		t.Fatalf("accepted text length = %d, want %d", editor.RuneCount(), count+1)
	}
	if !editor.Undo() {
		t.Fatal("ordinary Undo refused after budget invalidation")
	}
	if editor.RuneCount() != count {
		t.Fatalf("ordinary Undo left %d runes, want %d", editor.RuneCount(), count)
	}
}

func TestHistoryBudgetInvalidationPreservesBatchPrimitives(t *testing.T) {
	editor := NewEditor("")
	historyMustTrack(t, editor)
	historyFillSingleActionBudget(t, editor, historySingleActionOverhead(t))
	// A range batch pushes the same producer over the single-action budget. Its
	// primitives are real ordinary-Undo backing, so invalidation must keep them
	// even though coalesced producer-only primitives are released.
	if err := editor.ReplaceRanges([]Replacement{{Range: Range{Start: 0, End: 0}, Text: "z"}}); err != nil {
		t.Fatal(err)
	}
	if editor.historyAvailable() != nil {
		t.Fatal("batch budget edge failed to invalidate the producer")
	}
	last := &editor.history[len(editor.history)-1]
	if last.kind != editSeparate || len(last.primitives) == 0 {
		t.Fatalf("batch backing after invalidation = kind %d/%d primitives", last.kind, len(last.primitives))
	}
	if !editor.Undo() {
		t.Fatal("ordinary batch Undo refused after budget invalidation")
	}
}
