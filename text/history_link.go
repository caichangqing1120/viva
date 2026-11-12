package text

// historyOps is the private dynamic boundary between ordinary editing paths and
// the optional history producer. TrackHistory installs it together with the
// producer; editors that never enable history leave it zero-valued and cannot
// reach any producer implementation. Keeping the real functions only in this
// table lets the linker drop the complete opted-in subgraph for default users.
type historyOps struct {
	record            func(*historyJournal, textChange, editKind) (Edit, bool)
	capturePrimitives func(*historyJournal, []Edit)
	save              func(*historyJournal)
	replay            func(*historyJournal, int, bool)
	clear             func(*historyJournal, bool)
	invalidateFrom    func(*historyJournal, int)
	enforce           func(*historyJournal)
}

func (editor *Editor) historyState() *historyJournal {
	if editor == nil || editor.journal == nil {
		return nil
	}
	history := editor.journal.history
	if history == nil || !history.available || history.ops.record == nil {
		return nil
	}
	return history
}

func (editor *Editor) retainEqualHistory() bool {
	if editor == nil || editor.journal == nil {
		return false
	}
	if editor.journal.retainEqual {
		return true
	}
	history := editor.journal.history
	return history != nil && history.available
}

func (editor *Editor) retainEqualEdits() bool {
	return editor.savePointIndex() != nil || editor.retainEqualHistory()
}

func (editor *Editor) invalidateHistory() {
	editor.invalidateHistoryFrom(0)
}

func (editor *Editor) invalidateHistoryFrom(first int) {
	if editor == nil {
		return
	}
	if history := editor.historyState(); history != nil {
		history.ops.invalidateFrom(history, first)
	}
}

func recordHistoryOwned(history *historyJournal, change textChange, kind editKind) (Edit, bool) {
	editor := history.owner
	primitive, ok := editor.historyChangePrimitive(change, kind)
	if !ok {
		return primitive, false
	}
	editor.captureHistoryPrimitive(primitive)
	// Capture can invalidate on a budget/frame-validation failure. Unit
	// ownership exists only while the producer is still available; returning the
	// pre-capture "primitive prepared" result would recreate producer-only
	// primitive backing after invalidation already released it.
	return primitive, history.available
}

func captureHistoryPrimitivesOwned(history *historyJournal, primitives []Edit) {
	history.owner.captureHistoryPrimitives(primitives)
}

func captureHistorySaveOwned(history *historyJournal) {
	history.owner.captureHistorySave()
}

func replayHistoryOwned(history *historyJournal, index int, inverse bool) {
	editor := history.owner
	if inverse {
		editor.captureHistoryUndo(&editor.history[index])
		return
	}
	editor.captureHistoryRedo(&editor.history[index])
}

func clearHistoryOwned(history *historyJournal, manual bool) {
	if manual {
		history.owner.resetHistoryAfterClear(history.detach)
		return
	}
	invalidateHistoryFromOwned(history, 0)
}

func enforceHistoryBudgetOwned(history *historyJournal) {
	history.owner.enforceHistoryBudget()
}
