package text

// SetSavePoint opts into saved-history tracking and marks the current Undo
// boundary without discarding Undo/Redo or enabling byte tracking. In this mode,
// accepted nonempty equal-text edits retain history; default editors are unchanged.
func (editor *Editor) SetSavePoint() {
	if editor.journal == nil {
		editor.journal = &editJournal{}
	}
	if editor.journal.savePoint == nil {
		editor.journal.savePoint = new(int)
	}
	*editor.journal.savePoint = editor.historyIndex
	if history := editor.historyState(); history != nil {
		history.ops.save(history)
	}
	editor.BreakUndoGroup()
}

// AtSavePoint reports whether the marked boundary is reachable and current.
// It is false before SetSavePoint or after that boundary was discarded.
func (editor *Editor) AtSavePoint() bool {
	point := editor.savePointIndex()
	return point != nil && *point >= 0 && *point == editor.historyIndex
}

func (editor *Editor) savePointIndex() *int {
	if editor.journal == nil {
		return nil
	}
	return editor.journal.savePoint
}

// ClearChangeHistory forgets every undo and redo unit without changing the text
// or the document's saved/dirty truth, and restarts a proven history producer as
// a new generation. A document at its save point stays clean; an unsaved
// document stays unsaved, with its save boundary no longer reachable. The
// public ClearHistory keeps its existing rebasing semantics.
func (editor *Editor) ClearChangeHistory() {
	point := editor.savePointIndex()
	dirty := point != nil && !editor.AtSavePoint()
	editor.ClearHistory()
	if point != nil {
		if dirty {
			// The boundary is no longer reachable in the new generation, so the
			// document keeps reporting modified data instead of laundering it.
			*point = -1
		} else {
			*point = 0
		}
	}
	if dirty {
		if history := editor.historyAvailable(); history != nil {
			history.save = -1
		}
	}
}
