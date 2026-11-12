package text

import "testing"

// H04: a clear-change-history reset must preserve the real unsaved boundary.
// The public ClearHistory keeps its existing "rebase the engine point"
// semantics; ClearChangeHistory is the document-facing boundary.

func TestClearChangeHistoryPreservesEqualTextDirty(t *testing.T) {
	editor := NewEditor("abc")
	editor.SetSavePoint()
	editor.MoveTo(1, false)
	editor.Insert("x")
	editor.DeleteBackward()
	if editor.String() != "abc" || editor.AtSavePoint() {
		t.Fatalf("precondition: text=%q atSavePoint=%v", editor.String(), editor.AtSavePoint())
	}
	editor.ClearChangeHistory()
	if editor.String() != "abc" {
		t.Fatalf("clear changed the text: %q", editor.String())
	}
	if editor.CanUndo() || editor.CanRedo() {
		t.Fatal("clear kept undo or redo units")
	}
	if editor.AtSavePoint() {
		t.Fatal("equal-text dirty document became clean after Clear Change History")
	}
}

func TestClearChangeHistoryKeepsCleanDocumentClean(t *testing.T) {
	editor := NewEditor("abc")
	editor.SetSavePoint()
	editor.MoveTo(3, false)
	editor.Insert("d")
	editor.Undo()
	if !editor.AtSavePoint() {
		t.Fatal("precondition: undo back to the save point must be clean")
	}
	editor.ClearChangeHistory()
	if !editor.AtSavePoint() {
		t.Fatal("clean document became dirty after Clear Change History")
	}
	if editor.CanUndo() || editor.CanRedo() {
		t.Fatal("clear kept history units")
	}
}

func TestClearChangeHistoryKeepsFutureSavePointInRedoDirty(t *testing.T) {
	editor := NewEditor("abc")
	editor.SetSavePoint()
	editor.MoveTo(3, false)
	editor.Insert("d")
	editor.SetSavePoint()
	editor.Undo()
	if editor.AtSavePoint() {
		t.Fatal("precondition: the marked boundary lives in Redo, the document is modified")
	}
	editor.ClearChangeHistory()
	if editor.AtSavePoint() {
		t.Fatal("a save point reachable only through Redo must stay unreachable after clear")
	}
	if editor.CanRedo() {
		t.Fatal("clear kept the redo branch that held the save point")
	}
}

func TestClearChangeHistoryKeepsDiscardedBranchDirty(t *testing.T) {
	editor := NewEditor("abc")
	editor.SetSavePoint()
	editor.MoveTo(3, false)
	editor.Insert("d")
	editor.Undo()
	editor.Insert("e")
	if editor.AtSavePoint() {
		t.Fatal("precondition: the save point branch was discarded, the document is modified")
	}
	editor.ClearChangeHistory()
	if editor.AtSavePoint() {
		t.Fatal("a discarded save point must not be resurrected by clear")
	}
}

func TestClearChangeHistoryRebootsTheProducerGeneration(t *testing.T) {
	editor := NewEditor("abc")
	editor.SetSavePoint()
	if _, _, ok := editor.TrackHistory(); !ok {
		t.Fatal("precondition: TrackHistory refused")
	}
	editor.MoveTo(3, false)
	editor.Insert("d")
	editor.ClearChangeHistory()
	if editor.CanUndo() {
		t.Fatal("clear kept undo units")
	}
	rebuilt, _, rebuiltOK := editor.TrackHistory()
	if !rebuiltOK {
		t.Fatal("clear must leave a producer that can be bootstrapped again")
	}
	editor.MoveTo(3, false)
	editor.Insert("z")
	frames, _, ok := editor.HistorySince(rebuilt)
	if !ok || len(frames) != 1 {
		t.Fatalf("after clear the producer must deliver exactly the new frame: ok=%v frames=%d", ok, len(frames))
	}
	if len(frames[0].Steps) == 0 {
		t.Fatal("the new frame carries no steps")
	}
	context := frames[0].Steps[0].Context
	if context.Current != 1 {
		t.Fatalf("new generation current action position = %d, want 1", context.Current)
	}
	if context.Save != -1 {
		t.Fatalf("producer save marker = %d, want unreachable (-1) for a modified document", context.Save)
	}
	if context.Detach != -1 {
		t.Fatalf("fresh generation detach = %d, want -1", context.Detach)
	}
}

func TestClearChangeHistoryKeepsCleanProducerSaveMarker(t *testing.T) {
	editor := NewEditor("abc")
	editor.SetSavePoint()
	if _, _, ok := editor.TrackHistory(); !ok {
		t.Fatal("precondition: TrackHistory refused")
	}
	editor.ClearChangeHistory()
	if !editor.AtSavePoint() {
		t.Fatal("clean document became dirty")
	}
	rebuilt, _, rebuiltOK := editor.TrackHistory()
	if !rebuiltOK {
		t.Fatal("clear must leave a producer that can be bootstrapped again")
	}
	editor.MoveTo(3, false)
	editor.Insert("z")
	frames, _, ok := editor.HistorySince(rebuilt)
	if !ok || len(frames) != 1 || len(frames[0].Steps) == 0 {
		t.Fatalf("producer after clean clear: ok=%v frames=%d", ok, len(frames))
	}
	if save := frames[0].Steps[0].Context.Save; save != 0 {
		t.Fatalf("clean document save marker = %d, want 0 (reachable at the generation start)", save)
	}
}

func TestClearChangeHistoryBeforeTrackingKeepsProducerUnreachable(t *testing.T) {
	editor := NewEditor("abc")
	editor.SetSavePoint()
	editor.MoveTo(3, false)
	editor.Insert("d")
	editor.ClearChangeHistory()
	if editor.AtSavePoint() {
		t.Fatal("clear laundered the modified document")
	}
	cursor, _, ok := editor.TrackHistory()
	if !ok {
		t.Fatal("producer must be bootstrappable after the reset")
	}
	editor.MoveTo(3, false)
	editor.Insert("z")
	frames, _, ok := editor.HistorySince(cursor)
	if !ok || len(frames) != 1 || len(frames[0].Steps) == 0 {
		t.Fatalf("first generation after a pre-tracking clear: ok=%v frames=%d", ok, len(frames))
	}
	if save := frames[0].Steps[0].Context.Save; save != -1 {
		t.Fatalf("producer save marker = %d, want unreachable (-1) after a pre-tracking clear", save)
	}
}

func TestClearChangeHistoryDiscardedBranchIsUnreachable(t *testing.T) {
	editor := NewEditor("abc")
	editor.SetSavePoint()
	editor.MoveTo(3, false)
	editor.Insert("d")
	editor.SetSavePoint() // the boundary now lives at the edited state
	editor.Undo()         // the boundary is only reachable through Redo
	editor.Insert("e")    // branching discards that Redo branch
	if editor.AtSavePoint() {
		t.Fatal("precondition: the discarded boundary must be unreachable")
	}
	if editor.CanRedo() {
		t.Fatal("precondition: the branch must be discarded")
	}
	editor.ClearChangeHistory()
	if editor.AtSavePoint() {
		t.Fatal("clear resurrected a discarded save point")
	}
}
