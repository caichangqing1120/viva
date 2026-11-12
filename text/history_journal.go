package text

import (
	"sync/atomic"
	"unsafe"
)

// HistoryCursor identifies a position in one editor's optional history
// producer. The zero value is invalid. A cursor is independent of the byte and
// line journals' cursors.
type HistoryCursor struct {
	source, generation, sequence uint64
}

// HistoryOperation is the source event a HistoryFrame records.
type HistoryOperation uint8

const (
	HistoryForward HistoryOperation = iota + 1
	HistoryUndo
	HistoryRedo
	HistorySave
)

// HistoryPrimitive is the byte primitive inside a HistoryStep. A replacement is
// an ordered HistoryDelete followed by HistoryInsert, never one combined step.
type HistoryPrimitive uint8

const (
	HistoryNoPrimitive HistoryPrimitive = iota
	HistoryDelete
	HistoryInsert
)

// HistoryContext is the sampled source history position for a step. Current,
// Save and Detach are primitive boundaries; -1 means unreachable or absent.
// RetainedFrom and RetainedTo are inclusive retained-history boundaries.
type HistoryContext struct {
	Current, Save, Detach    int
	RetainedFrom, RetainedTo int
	Collecting               bool
}

// HistoryStep is one accepted delete or insert primitive, or a Save event.
// BeforeBytes and AfterBytes are the running logical UTF-8 lengths at this
// primitive. Position and Length are the actual source byte range.
type HistoryStep struct {
	Operation               HistoryOperation
	Primitive               HistoryPrimitive
	Position, Length        int
	BeforeBytes, AfterBytes int
	Context                 HistoryContext
}

// HistoryFrame is one complete accepted source operation. From is the cursor
// before it and To the cursor after it. Steps are ordered source primitives;
// their ranges may overlap for equal, zero-net or repeated-target operations.
type HistoryFrame struct {
	From, To                HistoryCursor
	BeforeBytes, AfterBytes int
	Steps                   []HistoryStep
}

const (
	historyFrameLimit = 256
	// The existing history budget counts rune-sized units. These charges are
	// derived from the actual structure sizes and rounded up, so the budget
	// covers the sidecar header, frame ring backing and step backing rather
	// than only the used lengths. The historyEdit primitive backing is charged
	// separately by historyRunes and is not duplicated here.
	historyStepRunes  = int((unsafe.Sizeof(HistoryStep{}) + unsafe.Sizeof(rune(0)) - 1) / unsafe.Sizeof(rune(0)))
	historyFrameRunes = int((unsafe.Sizeof(HistoryFrame{}) + unsafe.Sizeof(rune(0)) - 1) / unsafe.Sizeof(rune(0)))
)

type historyJournal struct {
	owner                        *Editor
	ops                          historyOps
	source, generation, sequence uint64
	current, save                int
	detach                       int
	retainedFrom, retainedTo     int
	frames                       []HistoryFrame
	epoch                        HistoryCursor
	windowStart                  uint64
	bootstrapBytes               int
	bytes                        int
	cost                         int
	frameCapacity                int
	available                    bool
}

const historyHeaderRunes = int((unsafe.Sizeof(historyJournal{}) + unsafe.Sizeof(rune(0)) - 1) / unsafe.Sizeof(rune(0)))

var nextHistorySource atomic.Uint64

// TrackHistory opts into the immutable primitive-history producer and returns
// its epoch cursor and the UTF-8 byte length captured at first enable. It never
// starts the ordinary byte journal. Repeated calls return that same epoch and
// captured length while its prefix is retained; an expired prefix refuses a new
// bootstrap without invalidating a consumer that still holds a retained cursor.
// First enable requires no Undo or Redo history and no active composition.
func (editor *Editor) TrackHistory() (cursor HistoryCursor, bootstrapBytes int, ok bool) {
	if editor == nil {
		return HistoryCursor{}, 0, false
	}
	journal := editor.journal
	if journal != nil && journal.history != nil {
		history := journal.history
		if !history.available || history.epoch.sequence < history.windowStart {
			return HistoryCursor{}, 0, false
		}
		return history.epoch, history.bootstrapBytes, true
	}
	if journal != nil && journal.private {
		return HistoryCursor{}, 0, false
	}
	if editor.composing || editor.CanUndo() || editor.CanRedo() {
		return HistoryCursor{}, 0, false
	}
	if journal == nil {
		journal = &editJournal{}
		editor.journal = journal
	}
	source := nextHistorySource.Add(1)
	for source == 0 {
		source = nextHistorySource.Add(1)
	}
	bootstrapBytes = len(editor.String())
	history := &historyJournal{
		owner: editor,
		ops: historyOps{
			record:            recordHistoryOwned,
			capturePrimitives: captureHistoryPrimitivesOwned,
			save:              captureHistorySaveOwned,
			replay:            replayHistoryOwned,
			clear:             clearHistoryOwned,
			invalidateFrom:    invalidateHistoryFromOwned,
			enforce:           enforceHistoryBudgetOwned,
		},
		source:         source,
		generation:     1,
		current:        0,
		save:           0,
		detach:         -1,
		retainedFrom:   0,
		retainedTo:     0,
		epoch:          HistoryCursor{source: source, generation: 1, sequence: 0},
		windowStart:    0,
		bootstrapBytes: bootstrapBytes,
		bytes:          bootstrapBytes,
		cost:           historyHeaderRunes,
		available:      true,
	}
	journal.history = history
	return history.epoch, history.bootstrapBytes, true
}

// HistorySince copies every complete frame after cursor. Returned frames and
// their nested steps are owned by the caller; a repeated read of the same
// retained cursor returns equal values again. A wrong source or generation, a
// future cursor, or a cursor whose frame prefix has been evicted returns no
// frames and false. An empty read is distinct from a Save-only frame.
func (editor *Editor) HistorySince(cursor HistoryCursor) (frames []HistoryFrame, latest HistoryCursor, ok bool) {
	if editor == nil || editor.journal == nil || editor.journal.history == nil {
		return nil, HistoryCursor{}, false
	}
	history := editor.journal.history
	if !history.available {
		return nil, HistoryCursor{}, false
	}
	latest = history.cursor()
	if cursor == (HistoryCursor{}) || cursor.source != history.source || cursor.generation != history.generation {
		return nil, latest, false
	}
	if cursor.sequence > history.sequence || cursor.sequence < history.windowStart {
		return nil, latest, false
	}
	want := int(history.sequence - cursor.sequence)
	if want == 0 {
		return nil, latest, true
	}
	frames = make([]HistoryFrame, 0, want)
	for _, frame := range history.frames {
		if frame.To.sequence <= cursor.sequence {
			continue
		}
		copied := frame
		if len(frame.Steps) > 0 {
			copied.Steps = append([]HistoryStep(nil), frame.Steps...)
		}
		frames = append(frames, copied)
	}
	if len(frames) != want {
		return nil, latest, false
	}
	return frames, latest, true
}

func (history *historyJournal) cursor() HistoryCursor {
	return HistoryCursor{source: history.source, generation: history.generation, sequence: history.sequence}
}

func (history *historyJournal) context(current, retainedTo int, collecting bool) HistoryContext {
	return HistoryContext{
		Current: current, Save: history.save, Detach: history.detach,
		RetainedFrom: history.retainedFrom, RetainedTo: retainedTo,
		Collecting: collecting,
	}
}

func (editor *Editor) historyAvailable() *historyJournal {
	if editor == nil || editor.journal == nil {
		return nil
	}
	history := editor.journal.history
	if history == nil || !history.available {
		return nil
	}
	return history
}

// historyUnitRunes charges producer-added coalesced primitive backing by its
// actual capacity. Existing batch units keep their established len-based
// retainedRunes charge so shared batch backing is not charged twice.
func (editor *Editor) historyUnitRunes(edit *historyEdit) int {
	runes := edit.retainedRunes()
	if edit.kind != editSeparate && len(edit.primitives) > 0 {
		runes += (cap(edit.primitives) - len(edit.primitives)) * historyPrimitiveRunes
	}
	return runes
}

func (editor *Editor) historyChangePrimitive(change textChange, kind editKind) (Edit, bool) {
	if kind == editComposition {
		editor.invalidateHistory()
		return Edit{}, false
	}
	history := editor.historyAvailable()
	if history == nil {
		return Edit{}, false
	}
	start := editor.offsets.offset
	if editor.offsets.index != change.start {
		start = editor.ByteOffset(change.start)
	}
	removed, inserted := 0, 0
	for _, character := range change.removed {
		removed += runeBytes(character)
	}
	for _, character := range change.inserted {
		inserted += runeBytes(character)
	}
	if removed == 0 && inserted == 0 {
		return Edit{}, false
	}
	return Edit{Start: start, Removed: removed, Inserted: inserted}, true
}

func (editor *Editor) captureHistoryPrimitive(primitive Edit) {
	if event := editor.compositionEvent(); event != nil {
		editor.appendEventForwardPrimitive(event, primitive)
		return
	}
	history := editor.historyAvailable()
	if history == nil || primitive.Removed == 0 && primitive.Inserted == 0 {
		return
	}
	before := history.bytes
	after := before - primitive.Removed + primitive.Inserted
	steps := make([]HistoryStep, 0, 2)
	running := before
	if primitive.Removed != 0 {
		steps = editor.appendForwardHistoryStep(history, steps, HistoryDelete, primitive.Start,
			primitive.Removed, running, running-primitive.Removed)
		running -= primitive.Removed
	}
	if primitive.Inserted != 0 {
		steps = editor.appendForwardHistoryStep(history, steps, HistoryInsert, primitive.Start,
			primitive.Inserted, running, running+primitive.Inserted)
		running += primitive.Inserted
	}
	editor.appendHistoryFrame(steps, before, after)
}

func (editor *Editor) captureHistoryPrimitives(primitives []Edit) {
	if event := editor.compositionEvent(); event != nil {
		editor.appendEventPrimitives(event, primitives)
		return
	}
	history := editor.historyAvailable()
	if history == nil || len(primitives) == 0 {
		return
	}
	net := 0
	for _, primitive := range primitives {
		net += primitive.Inserted - primitive.Removed
	}
	before := history.bytes
	after := before + net
	steps := make([]HistoryStep, 0, len(primitives)*2)
	running := before
	for _, primitive := range primitives {
		if primitive.Removed != 0 {
			steps = editor.appendForwardHistoryStep(history, steps, HistoryDelete, primitive.Start,
				primitive.Removed, running, running-primitive.Removed)
			running -= primitive.Removed
		}
		if primitive.Inserted != 0 {
			steps = editor.appendForwardHistoryStep(history, steps, HistoryInsert, primitive.Start,
				primitive.Inserted, running, running+primitive.Inserted)
			running += primitive.Inserted
		}
	}
	editor.appendHistoryFrame(steps, before, after)
}

func (editor *Editor) captureHistorySave() {
	history := editor.historyAvailable()
	if history == nil {
		return
	}
	history.save = history.current
	history.detach = -1
	bytes := history.bytes
	step := HistoryStep{
		Operation: HistorySave, Primitive: HistoryNoPrimitive,
		BeforeBytes: bytes, AfterBytes: bytes,
		Context: history.context(history.current, history.retainedTo, true),
	}
	editor.appendHistoryFrame([]HistoryStep{step}, bytes, bytes)
}

func (editor *Editor) appendHistoryFrame(steps []HistoryStep, before, after int) bool {
	if len(steps) == 0 {
		return false
	}
	return editor.appendHistoryFrameAt(steps, before, after, steps[len(steps)-1].Context.Current)
}

func (editor *Editor) appendHistoryFrameAt(steps []HistoryStep, before, after, finalCurrent int) bool {
	history := editor.historyAvailable()
	if history == nil || len(steps) == 0 {
		return false
	}
	running := before
	for _, step := range steps {
		if step.Position < 0 || step.Length < 0 || step.BeforeBytes != running {
			editor.invalidateHistory()
			return false
		}
		switch step.Primitive {
		case HistoryDelete:
			if step.AfterBytes != running-step.Length || step.Position+step.Length > running {
				editor.invalidateHistory()
				return false
			}
		case HistoryInsert:
			if step.AfterBytes != running+step.Length || step.Position > running {
				editor.invalidateHistory()
				return false
			}
		case HistoryNoPrimitive:
			if step.AfterBytes != running || step.Position != 0 || step.Length != 0 {
				editor.invalidateHistory()
				return false
			}
		default:
			editor.invalidateHistory()
			return false
		}
		running = step.AfterBytes
	}
	if running != after {
		editor.invalidateHistory()
		return false
	}
	retainedTo := steps[len(steps)-1].Context.RetainedTo
	if finalCurrent < 0 || retainedTo < finalCurrent || finalCurrent < history.retainedFrom {
		editor.invalidateHistory()
		return false
	}
	from := history.cursor()
	history.sequence++
	to := history.cursor()
	if len(history.frames) == historyFrameLimit {
		editor.dropOldestHistoryFrame()
	}
	history.frames = append(history.frames, HistoryFrame{
		From: from, To: to, BeforeBytes: before, AfterBytes: after, Steps: steps,
	})
	if cap(history.frames) > historyFrameLimit {
		// Keep the live ring backing at the charged slot count rather than the
		// growth capacity a forward-sliced ring can leave behind.
		frames := make([]HistoryFrame, len(history.frames), historyFrameLimit)
		copy(frames, history.frames)
		history.frames = frames
	}
	history.cost += (cap(history.frames) - history.frameCapacity) * historyFrameRunes
	history.frameCapacity = cap(history.frames)
	history.cost += cap(steps) * historyStepRunes
	history.current = finalCurrent
	history.retainedTo = retainedTo
	history.bytes = after
	editor.enforceHistoryBudget()
	return history.available
}

func (editor *Editor) dropOldestHistoryFrame() {
	history := editor.historyAvailable()
	if history == nil || len(history.frames) == 0 {
		return
	}
	frame := history.frames[0]
	history.cost -= cap(frame.Steps) * historyStepRunes
	history.windowStart = frame.To.sequence
	history.frames[0] = HistoryFrame{}
	history.frames = history.frames[1:]
}

func (editor *Editor) historyCost() int {
	if editor == nil {
		return 0
	}
	cost := editor.historyRunes
	journal := editor.journal
	if journal != nil && journal.composition != nil {
		cost += journal.composition.cost
	}
	if journal != nil && journal.history != nil {
		// The header charge covers the owned guard even after invalidation:
		// frames and primitive backing are released, but the distinct refusal
		// journal is still retained by the editor.
		cost += journal.history.cost
	}
	return cost
}

func (editor *Editor) enforceHistoryBudget() {
	history := editor.historyAvailable()
	if history == nil {
		return
	}
	for len(history.frames) > 1 && editor.historyCost() > maxHistoryRunes {
		editor.dropOldestHistoryFrame()
	}
	if len(history.frames) == 1 && editor.historyCost() > maxSingleEditRunes {
		editor.invalidateHistory()
	}
}

// updateHistoryBranch applies the exact pinned UndoHistory::AppendAction source
// predicate at one primitive boundary. It runs before every accepted primitive
// append, including ordinary appends with no redo suffix: reaching an earlier
// save makes it unreachable and creates an absent detach; an existing detach
// moves earlier only in the source's else-if path. It deliberately does not
// normalize a live post-Clear Save/Detach coexistence.
func (editor *Editor) updateHistoryBranch(boundary int) {
	history := editor.historyAvailable()
	if history == nil {
		return
	}
	if boundary < history.save {
		history.save = -1
		if history.detach == -1 {
			history.detach = boundary
		}
	} else if history.detach != -1 && history.detach > boundary {
		history.detach = boundary
	}
}

func (editor *Editor) appendForwardHistoryStep(history *historyJournal, steps []HistoryStep,
	primitive HistoryPrimitive, position, length, before, after int) []HistoryStep {
	boundary := history.current + len(steps)
	editor.updateHistoryBranch(boundary)
	return append(steps, HistoryStep{
		Operation: HistoryForward, Primitive: primitive,
		Position: position, Length: length,
		BeforeBytes: before, AfterBytes: after,
		Context: history.context(boundary+1, boundary+1, true),
	})
}

type historyReplayPrimitive struct {
	primitive        HistoryPrimitive
	position, length int
}

func replayRuneBytes(values []rune) int {
	total := 0
	for _, value := range values {
		total += runeBytes(value)
	}
	return total
}

func (editor *Editor) historyReplayPrimitives(edit *historyEdit) []historyReplayPrimitive {
	primitives := make([]historyReplayPrimitive, 0, len(edit.primitives)*2)
	if len(edit.primitives) > 0 {
		for _, primitive := range edit.primitives {
			if primitive.Removed != 0 {
				primitives = append(primitives, historyReplayPrimitive{HistoryDelete, primitive.Start, primitive.Removed})
			}
			if primitive.Inserted != 0 {
				primitives = append(primitives, historyReplayPrimitive{HistoryInsert, primitive.Start, primitive.Inserted})
			}
		}
		return primitives
	}
	start := editor.ByteOffset(edit.start)
	removed, inserted := replayRuneBytes(edit.removed), replayRuneBytes(edit.inserted)
	if removed != 0 {
		primitives = append(primitives, historyReplayPrimitive{HistoryDelete, start, removed})
	}
	if inserted != 0 {
		primitives = append(primitives, historyReplayPrimitive{HistoryInsert, start, inserted})
	}
	return primitives
}

// buildHistoryReplay derives one complete Undo or Redo frame from the actual
// accepted primitives before the editor is mutated. Undo reverses replacement
// order and swaps insert/delete; Redo replays forward. Context is the
// pre-completion primitive boundary and RetainedTo stays the backed tail.
func (editor *Editor) buildHistoryReplay(edit *historyEdit, inverse bool) (steps []HistoryStep, before, after, finalCurrent int, ok bool) {
	history := editor.historyAvailable()
	if history == nil {
		return nil, 0, 0, 0, false
	}
	primitives := editor.historyReplayPrimitives(edit)
	if len(primitives) == 0 {
		return nil, 0, 0, 0, false
	}
	before = history.bytes
	after = before
	start := history.current
	operation := HistoryRedo
	if inverse {
		operation = HistoryUndo
	}
	steps = make([]HistoryStep, 0, len(primitives))
	if inverse {
		for index := len(primitives) - 1; index >= 0; index-- {
			primitive := primitives[index]
			inverted := HistoryInsert
			if primitive.primitive == HistoryInsert {
				inverted = HistoryDelete
			}
			step := HistoryStep{
				Operation: operation, Primitive: inverted,
				Position: primitive.position, Length: primitive.length,
				BeforeBytes: after, Context: history.context(start-len(steps), history.retainedTo, true),
			}
			if inverted == HistoryDelete {
				step.AfterBytes = after - primitive.length
			} else {
				step.AfterBytes = after + primitive.length
			}
			if step.AfterBytes < 0 {
				return nil, 0, 0, 0, false
			}
			after = step.AfterBytes
			steps = append(steps, step)
		}
		finalCurrent = start - len(steps)
	} else {
		for _, primitive := range primitives {
			step := HistoryStep{
				Operation: operation, Primitive: primitive.primitive,
				Position: primitive.position, Length: primitive.length,
				BeforeBytes: after, Context: history.context(start+len(steps), history.retainedTo, true),
			}
			if primitive.primitive == HistoryDelete {
				step.AfterBytes = after - primitive.length
			} else {
				step.AfterBytes = after + primitive.length
			}
			if step.AfterBytes < 0 {
				return nil, 0, 0, 0, false
			}
			after = step.AfterBytes
			steps = append(steps, step)
		}
		finalCurrent = start + len(steps)
	}
	if finalCurrent < 0 {
		return nil, 0, 0, 0, false
	}
	return steps, before, after, finalCurrent, true
}

func (editor *Editor) captureHistoryUndo(edit *historyEdit) {
	if event := editor.compositionEvent(); event != nil {
		editor.captureEventReplay(event, edit, true)
		return
	}
	steps, before, after, finalCurrent, ok := editor.buildHistoryReplay(edit, true)
	if !ok {
		editor.invalidateHistory()
		return
	}
	editor.appendHistoryFrameAt(steps, before, after, finalCurrent)
}

func (editor *Editor) captureHistoryRedo(edit *historyEdit) {
	if event := editor.compositionEvent(); event != nil {
		editor.captureEventReplay(event, edit, false)
		return
	}
	steps, before, after, finalCurrent, ok := editor.buildHistoryReplay(edit, false)
	if !ok {
		editor.invalidateHistory()
		return
	}
	editor.appendHistoryFrameAt(steps, before, after, finalCurrent)
}

// resetHistoryAfterClear creates a new available generation for a proven live
// clear. It keeps the source detach qualification, resets current/save to zero
// and captures the exact text length as the new bootstrap. The caller drops the
// actual Undo units separately, so no producer-added primitive is double
// released here.
func (editor *Editor) resetHistoryAfterClear(detach int) {
	history := editor.historyAvailable()
	if history == nil {
		return
	}
	history.frames = nil
	history.cost = historyHeaderRunes
	history.frameCapacity = 0
	history.generation++
	history.sequence++
	history.current, history.save = 0, 0
	history.detach = detach
	history.retainedFrom, history.retainedTo = 0, 0
	history.bytes = len(editor.String())
	history.bootstrapBytes = history.bytes
	history.epoch = HistoryCursor{source: history.source, generation: history.generation, sequence: history.sequence}
	history.windowStart = history.sequence
}

func invalidateHistoryFromOwned(history *historyJournal, first int) {
	editor := history.owner
	if !history.available {
		return
	}
	for index := first; index < len(editor.history); index++ {
		edit := &editor.history[index]
		if edit.kind == editSeparate || len(edit.primitives) == 0 {
			continue
		}
		// Coalesced ownership was added only for this producer. Batch units keep
		// their existing primitive backing because Undo/Redo still needs it.
		editor.historyRunes -= cap(edit.primitives) * historyPrimitiveRunes
		edit.primitives = nil
	}
	history.available = false
	history.frames = nil
	history.cost = historyHeaderRunes
	history.frameCapacity = 0
	history.bootstrapBytes = 0
	history.generation++
	history.sequence++
	history.current, history.save = 0, 0
	history.detach = -1
	history.retainedFrom, history.retainedTo = 0, 0
	history.bytes = 0
	history.epoch = HistoryCursor{}
}
