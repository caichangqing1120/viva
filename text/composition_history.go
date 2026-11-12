package text

import "unsafe"

// historyEvent is the one pending producer frame for an explicit composition
// event. It is owned by the optional sidecar, so default editors retain no
// accumulator storage. Steps mix Forward and Undo operations in exact source
// order while the physical history keeps its ordinary units.
type historyEvent struct {
	from       HistoryCursor
	startBytes int
	bytes      int
	current    int
	retainedTo int
	steps      []HistoryStep
	cost       int
	failed     bool
}

const historyEventRunes = int((unsafe.Sizeof(historyEvent{}) + unsafe.Sizeof(rune(0)) - 1) / unsafe.Sizeof(rune(0)))

// beginCompositionHistory reserves the complete pending frame and step backing
// before the event mutates text. Old frames are evicted first; if the complete
// action still cannot fit, metadata is invalidated and physical editing later
// proceeds without a partial frame.
func (editor *Editor) beginCompositionHistory(state *compositionState, steps int) *historyEvent {
	history := editor.historyAvailable()
	if history == nil || steps <= 0 {
		return nil
	}
	cost := steps*historyStepRunes + historyFrameRunes + historyEventRunes
	if cost > maxSingleEditRunes {
		editor.invalidateHistory()
		return nil
	}
	for len(history.frames) > 0 && editor.historyCost()+cost > maxHistoryRunes {
		editor.dropOldestHistoryFrame()
	}
	if editor.historyCost()+cost > maxSingleEditRunes {
		editor.invalidateHistory()
		return nil
	}
	history.cost += cost
	event := &historyEvent{
		from:       history.cursor(),
		startBytes: history.bytes,
		bytes:      history.bytes,
		current:    history.current,
		retainedTo: history.retainedTo,
		cost:       cost,
		steps:      make([]HistoryStep, 0, steps),
	}
	state.event = event
	return event
}

func (editor *Editor) finishCompositionHistory(state *compositionState, event *historyEvent) {
	state.event = nil
	if event == nil {
		return
	}
	history := editor.historyAvailable()
	if history == nil {
		return
	}
	history.cost -= event.cost
	if event.failed {
		editor.invalidateHistory()
		return
	}
	if len(event.steps) == 0 {
		// Pure commit/truncation has no frame and never advances sequence.
		history.current = event.current
		history.bytes = event.bytes
		history.retainedTo = event.retainedTo
		return
	}
	if !validateCompositionHistoryEvent(event) {
		editor.invalidateHistory()
		return
	}
	history.sequence++
	to := history.cursor()
	if len(history.frames) == historyFrameLimit {
		editor.dropOldestHistoryFrame()
	}
	oldCapacity := history.frameCapacity
	history.cost += cap(event.steps) * historyStepRunes
	history.frames = append(history.frames, HistoryFrame{
		From: event.from, To: to,
		BeforeBytes: event.startBytes, AfterBytes: event.bytes,
		Steps: event.steps,
	})
	if cap(history.frames) > historyFrameLimit {
		frames := make([]HistoryFrame, len(history.frames), historyFrameLimit)
		copy(frames, history.frames)
		history.frames = frames
	}
	history.cost += (cap(history.frames) - oldCapacity) * historyFrameRunes
	history.frameCapacity = cap(history.frames)
	history.current = event.current
	history.bytes = event.bytes
	history.retainedTo = event.retainedTo
	editor.enforceHistoryBudget()
}

func validateCompositionHistoryEvent(event *historyEvent) bool {
	running := event.startBytes
	for _, step := range event.steps {
		if step.Position < 0 || step.Length < 0 || step.BeforeBytes != running {
			return false
		}
		switch step.Primitive {
		case HistoryDelete:
			if step.AfterBytes != running-step.Length || step.Position+step.Length > running {
				return false
			}
		case HistoryInsert:
			if step.AfterBytes != running+step.Length || step.Position > running {
				return false
			}
		default:
			return false
		}
		switch step.Operation {
		case HistoryForward, HistoryUndo, HistoryRedo:
		default:
			return false
		}
		running = step.AfterBytes
	}
	return running == event.bytes && event.current >= 0 && event.retainedTo >= event.current
}

func (editor *Editor) appendEventForwardPrimitive(event *historyEvent, primitive Edit) {
	if event == nil || primitive.Removed == 0 && primitive.Inserted == 0 {
		return
	}
	history := editor.historyAvailable()
	if history == nil {
		event.failed = true
		return
	}
	running := event.bytes
	if primitive.Removed != 0 {
		after := running - primitive.Removed
		editor.appendEventForwardStep(history, event, HistoryDelete, primitive.Start, primitive.Removed, running, after)
		running = after
	}
	if primitive.Inserted != 0 {
		after := running + primitive.Inserted
		editor.appendEventForwardStep(history, event, HistoryInsert, primitive.Start, primitive.Inserted, running, after)
		running = after
	}
	event.bytes = running
}

func (editor *Editor) appendEventForwardStep(history *historyJournal, event *historyEvent,
	primitive HistoryPrimitive, position, length, before, after int) {
	if len(event.steps) >= cap(event.steps) {
		event.failed = true
		editor.invalidateHistory()
		return
	}
	boundary := event.current
	editor.updateHistoryBranch(boundary)
	event.steps = append(event.steps, HistoryStep{
		Operation: HistoryForward, Primitive: primitive,
		Position: position, Length: length,
		BeforeBytes: before, AfterBytes: after,
		Context: HistoryContext{
			Current: boundary + 1, Save: history.save, Detach: history.detach,
			RetainedFrom: history.retainedFrom, RetainedTo: boundary + 1, Collecting: true,
		},
	})
	event.current = boundary + 1
	event.retainedTo = boundary + 1
	event.bytes = after
}

func (editor *Editor) appendEventInverseStep(history *historyJournal, event *historyEvent,
	primitive HistoryPrimitive, position, length, before, after int) {
	if len(event.steps) >= cap(event.steps) {
		event.failed = true
		editor.invalidateHistory()
		return
	}
	event.steps = append(event.steps, HistoryStep{
		Operation: HistoryUndo, Primitive: primitive,
		Position: position, Length: length,
		BeforeBytes: before, AfterBytes: after,
		Context: HistoryContext{
			Current: event.current, Save: history.save, Detach: history.detach,
			RetainedFrom: history.retainedFrom, RetainedTo: event.retainedTo, Collecting: true,
		},
	})
	event.current--
	if event.current < 0 {
		event.failed = true
		editor.invalidateHistory()
	}
	event.bytes = after
}

func (editor *Editor) appendEventRedoStep(history *historyJournal, event *historyEvent,
	primitive HistoryPrimitive, position, length, before, after int) {
	if len(event.steps) >= cap(event.steps) {
		event.failed = true
		editor.invalidateHistory()
		return
	}
	event.steps = append(event.steps, HistoryStep{
		Operation: HistoryRedo, Primitive: primitive,
		Position: position, Length: length,
		BeforeBytes: before, AfterBytes: after,
		Context: HistoryContext{
			Current: event.current, Save: history.save, Detach: history.detach,
			RetainedFrom: history.retainedFrom, RetainedTo: event.retainedTo, Collecting: true,
		},
	})
	event.current++
	event.bytes = after
}

func (editor *Editor) appendEventPrimitives(event *historyEvent, primitives []Edit) {
	for _, primitive := range primitives {
		editor.appendEventForwardPrimitive(event, primitive)
	}
}

func (editor *Editor) captureEventReplay(event *historyEvent, edit *historyEdit, inverse bool) {
	history := editor.historyAvailable()
	if history == nil {
		event.failed = true
		return
	}
	primitives := editor.historyReplayPrimitives(edit)
	if len(primitives) == 0 {
		return
	}
	if inverse {
		for index := len(primitives) - 1; index >= 0; index-- {
			primitive := primitives[index]
			inverted := HistoryInsert
			if primitive.primitive == HistoryInsert {
				inverted = HistoryDelete
			}
			before := event.bytes
			after := before + primitive.length
			if inverted == HistoryDelete {
				after = before - primitive.length
			}
			if after < 0 {
				event.failed = true
				editor.invalidateHistory()
				return
			}
			editor.appendEventInverseStep(history, event, inverted, primitive.position, primitive.length, before, after)
			event.bytes = after
		}
		return
	}
	for _, primitive := range primitives {
		before := event.bytes
		after := before + primitive.length
		if primitive.primitive == HistoryDelete {
			after = before - primitive.length
		}
		if after < 0 {
			event.failed = true
			editor.invalidateHistory()
			return
		}
		editor.appendEventRedoStep(history, event, primitive.primitive, primitive.position, primitive.length, before, after)
		event.bytes = after
	}
}
