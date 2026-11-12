package text

// H01 producer contract tests. These are written against the public producer
// API only (TrackHistory/HistorySince plus ordinary editing entry points) and
// are deliberately independent of the existing fixture/replay suites: every
// expected value below is derived from the actual edit that was executed and
// from the source predicates recorded in
// docs/specs/001-vivapad/change-history-editor-contract.md, not from the
// producer's private representation.
//
// Conventions pinned here:
//   - A frame is published only for an accepted source operation. Coalesced
//     keystrokes merge into one ordinary Undo unit but still publish one frame
//     per primitive, in the order the primitives became accepted.
//   - HistoryStep.Primitive on a HistoryUndo or HistoryRedo step is the physical
//     primitive performed by that step: HistoryDelete removes Length bytes and
//     HistoryInsert adds them. For HistoryUndo this is the inverse of the
//     original forward primitive, which is why a replayed undo can contain both
//     "operation undo, primitive insert" and "operation undo, primitive delete".
//   - Save publishes a frame with one HistoryNoPrimitive step and unchanged
//     byte lengths, so an empty read is distinguishable from a Save-only frame.
//   - A frame chain always rewrites a byte-length model of the real editor text
//     from the bootstrap length; the model must equal len(Editor.String()).

import (
	"math/rand"
	"reflect"
	"strconv"
	"testing"
	"unsafe"
)

// contractStream is an incremental consumer that validates every delivered
// frame against a byte-length model of the real editor text.
type contractStream struct {
	t      *testing.T
	editor *Editor
	cursor HistoryCursor
	model  int
}

func contractTrack(t *testing.T, editor *Editor) *contractStream {
	t.Helper()
	cursor, bytes, ok := editor.TrackHistory()
	if !ok {
		t.Fatalf("TrackHistory refused a fresh source with no Undo/Redo history")
	}
	if bytes != len(editor.String()) {
		t.Fatalf("bootstrap bytes = %d, want actual UTF-8 length %d", bytes, len(editor.String()))
	}
	return &contractStream{t: t, editor: editor, cursor: cursor, model: bytes}
}

// read consumes every pending frame and returns it. It fails if the producer
// refuses a cursor it had accepted before, so a silent invalidation cannot be
// mistaken for "no changes".
func (stream *contractStream) read(stage string) []HistoryFrame {
	stream.t.Helper()
	frames, latest, ok := stream.editor.HistorySince(stream.cursor)
	if !ok {
		stream.t.Fatalf("%s: HistorySince refused a previously accepted cursor %+v", stage, stream.cursor)
	}
	contractCheckFrames(stream.t, stream.editor, stream.cursor, frames, &stream.model, stage)
	if len(frames) > 0 {
		if frames[len(frames)-1].To != latest {
			stream.t.Fatalf("%s: latest %+v does not match last frame To %+v", stage, latest, frames[len(frames)-1].To)
		}
	}
	stream.cursor = latest
	if stream.model != len(stream.editor.String()) {
		stream.t.Fatalf("%s: frame chain ends at %d bytes, text has %d", stage, stream.model, len(stream.editor.String()))
	}
	return frames
}

// expectFrames reads the pending frames and compares them step by step.
func (stream *contractStream) expectFrames(stage string, wants ...[]HistoryStep) {
	stream.t.Helper()
	frames := stream.read(stage)
	if len(frames) != len(wants) {
		stream.t.Fatalf("%s: %d frames, want %d: %+v", stage, len(frames), len(wants), frames)
	}
	for index, want := range wants {
		contractCheckSteps(stream.t, stage, index, frames[index].Steps, want)
	}
}

// expectNoChanges asserts that the producer reports a genuinely empty read:
// zero frames and ok. It fails if the cursor is refused, which is how lost
// history must be reported instead.
func (stream *contractStream) expectNoChanges(stage string) {
	stream.t.Helper()
	frames, latest, ok := stream.editor.HistorySince(stream.cursor)
	if !ok {
		stream.t.Fatalf("%s: empty read was refused instead of reported", stage)
	}
	if frames != nil || len(frames) != 0 {
		stream.t.Fatalf("%s: %d frames for a genuine no-op", stage, len(frames))
	}
	if latest != stream.cursor {
		stream.t.Fatalf("%s: latest advanced %+v -> %+v without a frame", stage, stream.cursor, latest)
	}
}

func contractCheckFrames(t *testing.T, editor *Editor, cursor HistoryCursor, frames []HistoryFrame, model *int, stage string) {
	t.Helper()
	for index, frame := range frames {
		if frame.From != cursor {
			t.Fatalf("%s: frame %d starts at %+v, want %+v", stage, index, frame.From, cursor)
		}
		if frame.To.sequence != frame.From.sequence+1 || frame.To.source != frame.From.source ||
			frame.To.generation != frame.From.generation {
			t.Fatalf("%s: frame %d cursor step %+v -> %+v is not one metadata event", stage, index, frame.From, frame.To)
		}
		if frame.BeforeBytes != *model {
			t.Fatalf("%s: frame %d before bytes = %d, model is %d", stage, index, frame.BeforeBytes, *model)
		}
		if len(frame.Steps) == 0 {
			t.Fatalf("%s: frame %d has no steps", stage, index)
		}
		running := frame.BeforeBytes
		for stepIndex, step := range frame.Steps {
			if step.BeforeBytes != running {
				t.Fatalf("%s: frame %d step %d before bytes = %d, running is %d", stage, index, stepIndex, step.BeforeBytes, running)
			}
			if step.Position < 0 || step.Length < 0 {
				t.Fatalf("%s: frame %d step %d negative range %+v", stage, index, stepIndex, step)
			}
			switch step.Primitive {
			case HistoryDelete:
				if step.Operation != HistoryForward && step.Operation != HistoryUndo && step.Operation != HistoryRedo {
					t.Fatalf("%s: frame %d step %d delete has operation %d", stage, index, stepIndex, step.Operation)
				}
				if step.AfterBytes != running-step.Length {
					t.Fatalf("%s: frame %d step %d delete %d -> %d, running %d", stage, index, stepIndex, step.BeforeBytes, step.AfterBytes, running)
				}
				if step.Position+step.Length > *model {
					t.Fatalf("%s: frame %d step %d delete [%d,%d) exceeds %d bytes", stage, index, stepIndex, step.Position, step.Position+step.Length, *model)
				}
			case HistoryInsert:
				if step.Operation != HistoryForward && step.Operation != HistoryUndo && step.Operation != HistoryRedo {
					t.Fatalf("%s: frame %d step %d insert has operation %d", stage, index, stepIndex, step.Operation)
				}
				if step.AfterBytes != running+step.Length {
					t.Fatalf("%s: frame %d step %d insert %d -> %d, running %d", stage, index, stepIndex, step.BeforeBytes, step.AfterBytes, running)
				}
				if step.Position > *model {
					t.Fatalf("%s: frame %d step %d insert at %d exceeds %d bytes", stage, index, stepIndex, step.Position, *model)
				}
			case HistoryNoPrimitive:
				if step.Operation != HistorySave || step.Length != 0 || step.Position != 0 || step.AfterBytes != running {
					t.Fatalf("%s: frame %d step %d is not a save step: %+v", stage, index, stepIndex, step)
				}
			default:
				t.Fatalf("%s: frame %d step %d has unknown primitive %d", stage, index, stepIndex, step.Primitive)
			}
			context := step.Context
			if context.Current < 0 || context.RetainedTo < context.Current ||
				context.Save < -1 || context.Detach < -1 ||
				context.RetainedFrom < 0 || context.RetainedFrom > context.Current {
				t.Fatalf("%s: frame %d step %d context out of range: %+v", stage, index, stepIndex, context)
			}
			if context.Save > context.RetainedTo || context.Detach > context.RetainedTo {
				t.Fatalf("%s: frame %d step %d context exceeds retained tail: %+v", stage, index, stepIndex, context)
			}
			if !context.Collecting {
				t.Fatalf("%s: frame %d step %d reports an uncollected edit", stage, index, stepIndex)
			}
			running = step.AfterBytes
			*model = running
		}
		if running != frame.AfterBytes {
			t.Fatalf("%s: frame %d ends at %d, steps end at %d", stage, index, frame.AfterBytes, running)
		}
		cursor = frame.To
	}
}

func contractCheckSteps(t *testing.T, stage string, frame int, got, want []HistoryStep) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: frame %d steps =\n got %+v\nwant %+v", stage, frame, got, want)
	}
}

func contractStep(operation HistoryOperation, primitive HistoryPrimitive, position, length, before, after, current, save, detach, retainedTo int) HistoryStep {
	return HistoryStep{
		Operation: operation, Primitive: primitive, Position: position, Length: length,
		BeforeBytes: before, AfterBytes: after,
		Context: HistoryContext{
			Current: current, Save: save, Detach: detach,
			RetainedFrom: 0, RetainedTo: retainedTo, Collecting: true,
		},
	}
}

func contractForward(primitive HistoryPrimitive, position, length, before, after, current, save, detach, retainedTo int) HistoryStep {
	return contractStep(HistoryForward, primitive, position, length, before, after, current, save, detach, retainedTo)
}

func contractUndo(primitive HistoryPrimitive, position, length, before, after, current, save, detach, retainedTo int) HistoryStep {
	return contractStep(HistoryUndo, primitive, position, length, before, after, current, save, detach, retainedTo)
}

func contractRedo(primitive HistoryPrimitive, position, length, before, after, current, save, detach, retainedTo int) HistoryStep {
	return contractStep(HistoryRedo, primitive, position, length, before, after, current, save, detach, retainedTo)
}

func contractSave(bytes, current, save, detach, retainedTo int) HistoryStep {
	return HistoryStep{
		Operation: HistorySave, Primitive: HistoryNoPrimitive,
		BeforeBytes: bytes, AfterBytes: bytes,
		Context: HistoryContext{
			Current: current, Save: save, Detach: detach,
			RetainedFrom: 0, RetainedTo: retainedTo, Collecting: true,
		},
	}
}

// TestHistoryProducerCapturesTypingAndCoalescing verifies that ordinary typing
// (ASCII plus multi-byte runes) publishes one frame per accepted primitive with
// the actual UTF-8 byte positions, that merged keystrokes do not lose their
// primitive order, and that Undo/Redo replay the exact inverse sequence.
func TestHistoryProducerCapturesTypingAndCoalescing(t *testing.T) {
	editor := NewEditor("")
	stream := contractTrack(t, editor)

	editor.Insert("a")
	editor.Insert("é")
	editor.Insert("漢")
	stream.expectFrames("coalesced typing",
		[]HistoryStep{contractForward(HistoryInsert, 0, 1, 0, 1, 1, 0, -1, 1)},
		[]HistoryStep{contractForward(HistoryInsert, 1, 2, 1, 3, 2, 0, -1, 2)},
		[]HistoryStep{contractForward(HistoryInsert, 3, 3, 3, 6, 3, 0, -1, 3)},
	)
	if editor.RuneCount() != 3 || editor.String() != "aé漢" {
		t.Fatalf("typed text = %q/%d runes", editor.String(), editor.RuneCount())
	}
	// The three keystrokes coalesce into one ordinary Undo unit; the producer
	// still published three frames.
	if !editor.Undo() {
		t.Fatal("coalesced Undo refused")
	}
	stream.expectFrames("undo coalesced",
		[]HistoryStep{
			contractUndo(HistoryDelete, 3, 3, 6, 3, 3, 0, -1, 3),
			contractUndo(HistoryDelete, 1, 2, 3, 1, 2, 0, -1, 3),
			contractUndo(HistoryDelete, 0, 1, 1, 0, 1, 0, -1, 3),
		})
	if editor.String() != "" {
		t.Fatalf("text after one Undo = %q, want empty", editor.String())
	}
	if !editor.Redo() {
		t.Fatal("coalesced Redo refused")
	}
	stream.expectFrames("redo coalesced",
		[]HistoryStep{
			contractRedo(HistoryInsert, 0, 1, 0, 1, 0, 0, -1, 3),
			contractRedo(HistoryInsert, 1, 2, 1, 3, 1, 0, -1, 3),
			contractRedo(HistoryInsert, 3, 3, 3, 6, 2, 0, -1, 3),
		})
	if editor.String() != "aé漢" {
		t.Fatalf("text after Redo = %q", editor.String())
	}
}

// TestHistoryProducerCapturesDeleteCoalescing covers merged backward and
// forward deletion: the ordinary unit holds several primitives, while the
// producer must publish each accepted deletion and inverse each of them in
// reverse order on Undo.
func TestHistoryProducerCapturesDeleteCoalescing(t *testing.T) {
	backward := NewEditor("abcde")
	stream := contractTrack(t, backward)
	backward.MoveTo(3, false)
	if !backward.DeleteBackward() {
		t.Fatal("first DeleteBackward refused")
	}
	if !backward.DeleteBackward() {
		t.Fatal("second DeleteBackward refused")
	}
	stream.expectFrames("backward delete",
		[]HistoryStep{contractForward(HistoryDelete, 2, 1, 5, 4, 1, 0, -1, 1)},
		[]HistoryStep{contractForward(HistoryDelete, 1, 1, 4, 3, 2, 0, -1, 2)},
	)
	if backward.String() != "ade" {
		t.Fatalf("text after backward deletes = %q", backward.String())
	}
	if !backward.Undo() {
		t.Fatal("merged backward Undo refused")
	}
	stream.expectFrames("undo backward delete",
		[]HistoryStep{
			contractUndo(HistoryInsert, 1, 1, 3, 4, 2, 0, -1, 2),
			contractUndo(HistoryInsert, 2, 1, 4, 5, 1, 0, -1, 2),
		})
	if backward.String() != "abcde" {
		t.Fatalf("text after Undo = %q", backward.String())
	}
	if !backward.Redo() {
		t.Fatal("merged backward Redo refused")
	}
	stream.expectFrames("redo backward delete",
		[]HistoryStep{
			contractRedo(HistoryDelete, 2, 1, 5, 4, 0, 0, -1, 2),
			contractRedo(HistoryDelete, 1, 1, 4, 3, 1, 0, -1, 2),
		})

	forward := NewEditor("abcde")
	stream = contractTrack(t, forward)
	forward.MoveTo(1, false)
	if !forward.DeleteForward() {
		t.Fatal("first DeleteForward refused")
	}
	if !forward.DeleteForward() {
		t.Fatal("second DeleteForward refused")
	}
	stream.expectFrames("forward delete",
		[]HistoryStep{contractForward(HistoryDelete, 1, 1, 5, 4, 1, 0, -1, 1)},
		[]HistoryStep{contractForward(HistoryDelete, 1, 1, 4, 3, 2, 0, -1, 2)},
	)
	if !forward.Undo() {
		t.Fatal("merged forward Undo refused")
	}
	stream.expectFrames("undo forward delete",
		[]HistoryStep{
			contractUndo(HistoryInsert, 1, 1, 3, 4, 2, 0, -1, 2),
			contractUndo(HistoryInsert, 1, 1, 4, 5, 1, 0, -1, 2),
		})
	if forward.String() != "abcde" {
		t.Fatalf("text after forward Undo = %q", forward.String())
	}
}

// TestHistoryProducerSaveOnlyFrames pins the metadata-event contract: Save
// publishes exactly one frame whose only step carries no primitive and no byte
// change, repeated Saves are distinct frames, and an empty read stays
// distinguishable from a Save-only frame.
func TestHistoryProducerSaveOnlyFrames(t *testing.T) {
	editor := NewEditor("abc")
	stream := contractTrack(t, editor)

	stream.expectNoChanges("before any event")
	editor.SetSavePoint()
	frames := stream.read("save at bootstrap")
	if len(frames) != 1 {
		t.Fatalf("save frames = %d, want 1", len(frames))
	}
	contractCheckSteps(t, "save at bootstrap", 0, frames[0].Steps,
		[]HistoryStep{contractSave(3, 0, 0, -1, 0)})
	if editor.String() != "abc" || editor.AtSavePoint() != true {
		t.Fatalf("save changed text/state: %q at-save=%t", editor.String(), editor.AtSavePoint())
	}

	editor.Insert("x")
	stream.expectFrames("insert after save",
		[]HistoryStep{contractForward(HistoryInsert, 3, 1, 3, 4, 1, 0, -1, 1)})
	editor.SetSavePoint()
	frames = stream.read("second save")
	contractCheckSteps(t, "second save", 0, frames[0].Steps,
		[]HistoryStep{contractSave(4, 1, 1, -1, 1)})
	editor.SetSavePoint()
	frames = stream.read("repeated save")
	contractCheckSteps(t, "repeated save", 0, frames[0].Steps,
		[]HistoryStep{contractSave(4, 1, 1, -1, 1)})

	if !editor.Undo() {
		t.Fatal("Undo refused")
	}
	stream.expectFrames("undo after saves",
		[]HistoryStep{contractUndo(HistoryDelete, 3, 1, 4, 3, 1, 1, -1, 1)})
	editor.SetSavePoint()
	frames = stream.read("save after undo")
	contractCheckSteps(t, "save after undo", 0, frames[0].Steps,
		[]HistoryStep{contractSave(3, 0, 0, -1, 1)})
	stream.expectNoChanges("after save read")

	// A Save-only frame must never be collapsed into an empty read: reading the
	// frames one at a time from the bootstrap cursor yields four Save frames and
	// the metadata sequence advances by one per Save even though bytes changed
	// only twice.
	fresh := NewEditor("abc")
	cursor, _, ok := fresh.TrackHistory()
	if !ok {
		t.Fatal("TrackHistory refused")
	}
	fresh.SetSavePoint()
	fresh.SetSavePoint()
	frames, latest, ok := fresh.HistorySince(cursor)
	if !ok || len(frames) != 2 {
		t.Fatalf("two Saves delivered %d frames/ok=%t", len(frames), ok)
	}
	if latest.sequence != cursor.sequence+2 {
		t.Fatalf("metadata sequence advanced to %d, want %d", latest.sequence, cursor.sequence+2)
	}
	if frames[0].BeforeBytes != frames[0].AfterBytes || frames[1].BeforeBytes != frames[1].AfterBytes {
		t.Fatalf("Save frames changed byte lengths: %+v", frames)
	}
}

// TestHistoryProducerEqualTextAndZeroNetFrames pins that equal targets and
// zero-net accepted work are retained with their primitive steps instead of
// being discarded because the final bytes match, while genuine no-ops publish
// nothing.
func TestHistoryProducerEqualTextAndZeroNetFrames(t *testing.T) {
	equal := NewEditor("abc")
	stream := contractTrack(t, equal)
	equal.ReplaceRange(Range{Start: 0, End: 1}, "a")
	stream.expectFrames("equal replacement",
		[]HistoryStep{
			contractForward(HistoryDelete, 0, 1, 3, 2, 1, 0, -1, 1),
			contractForward(HistoryInsert, 0, 1, 2, 3, 2, 0, -1, 2),
		})
	if equal.String() != "abc" {
		t.Fatalf("equal replacement changed text to %q", equal.String())
	}
	if !equal.Undo() {
		t.Fatal("Undo of equal replacement refused")
	}
	stream.expectFrames("undo equal replacement",
		[]HistoryStep{
			contractUndo(HistoryDelete, 0, 1, 3, 2, 2, 0, -1, 2),
			contractUndo(HistoryInsert, 0, 1, 2, 3, 1, 0, -1, 2),
		})
	if equal.String() != "abc" {
		t.Fatalf("undo of equal replacement changed text to %q", equal.String())
	}

	if err := equal.ReplaceRangesWith([]Replacement{{Range: Range{Start: 1, End: 2}, Text: "b"}},
		ReplaceRangesOptions{ApplyUnchanged: true}); err != nil {
		t.Fatal(err)
	}
	stream.expectFrames("apply-unchanged batch",
		[]HistoryStep{
			contractForward(HistoryDelete, 1, 1, 3, 2, 1, 0, -1, 1),
			contractForward(HistoryInsert, 1, 1, 2, 3, 2, 0, -1, 2),
		})

	zeroNet := NewEditor("abc")
	stream = contractTrack(t, zeroNet)
	if err := zeroNet.Transact(func(draft *Editor) error {
		draft.SetSelection(Range{Start: 3, End: 3})
		draft.Insert("xy")
		if !draft.DeleteBackward() || !draft.DeleteBackward() {
			t.Fatal("draft delete refused")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// The source applies the draft's actual primitive sequence as one forward
	// atomic action: insert "xy" at 3, then the draft's own inverse deletes.
	stream.expectFrames("zero-net transaction",
		[]HistoryStep{
			contractForward(HistoryInsert, 3, 2, 3, 5, 1, 0, -1, 1),
			contractForward(HistoryDelete, 4, 1, 5, 4, 2, 0, -1, 2),
			contractForward(HistoryDelete, 3, 1, 4, 3, 3, 0, -1, 3),
		})
	if zeroNet.String() != "abc" {
		t.Fatalf("zero-net transaction changed text to %q", zeroNet.String())
	}

	// Genuine no-ops: selection movement, empty insertion, empty replacement,
	// a failed backward delete at the start and a refused Undo publish nothing.
	zeroNet.SetSelection(Range{Start: 1, End: 1})
	zeroNet.Insert("")
	zeroNet.ReplaceRange(Range{Start: 0, End: 0}, "")
	zeroNet.MoveTo(0, false)
	zeroNet.DeleteBackward()
	zeroNet.Redo()
	stream.expectNoChanges("no-op edit batch")
}

// TestHistoryProducerBranchDetachMatchesSourcePredicates replays a save/branch
// workflow and asserts the sampled save/detach values at each forward step,
// including that an existing detach moves earlier on a new branch and that a
// Save clears it.
func TestHistoryProducerBranchDetachMatchesSourcePredicates(t *testing.T) {
	editor := NewEditor("")
	stream := contractTrack(t, editor)

	editor.BreakUndoGroup()
	editor.Insert("a")
	editor.BreakUndoGroup()
	editor.Insert("b")
	editor.BreakUndoGroup()
	editor.Insert("c")
	editor.BreakUndoGroup()
	editor.Insert("d")
	stream.expectFrames("typed units",
		[]HistoryStep{contractForward(HistoryInsert, 0, 1, 0, 1, 1, 0, -1, 1)},
		[]HistoryStep{contractForward(HistoryInsert, 1, 1, 1, 2, 2, 0, -1, 2)},
		[]HistoryStep{contractForward(HistoryInsert, 2, 1, 2, 3, 3, 0, -1, 3)},
		[]HistoryStep{contractForward(HistoryInsert, 3, 1, 3, 4, 4, 0, -1, 4)},
	)
	editor.SetSavePoint()
	frames := stream.read("save at 4")
	contractCheckSteps(t, "save at 4", 0, frames[0].Steps, []HistoryStep{contractSave(4, 4, 4, -1, 4)})

	if !editor.Undo() || !editor.Undo() {
		t.Fatal("Undo refused")
	}
	stream.expectFrames("undo to saved branch point",
		[]HistoryStep{contractUndo(HistoryDelete, 3, 1, 4, 3, 4, 4, -1, 4)},
		[]HistoryStep{contractUndo(HistoryDelete, 2, 1, 3, 2, 3, 4, -1, 4)},
	)
	// Appending before a reachable saved boundary makes the save unreachable and
	// records the old current as the first detach point (AppendAction source
	// predicate), sampled after the branch update.
	editor.Insert("x")
	stream.expectFrames("first branch",
		[]HistoryStep{contractForward(HistoryInsert, 2, 1, 2, 3, 3, -1, 2, 3)})
	editor.BreakUndoGroup()
	editor.Insert("y")
	stream.expectFrames("second append after branch",
		[]HistoryStep{contractForward(HistoryInsert, 3, 1, 3, 4, 4, -1, 2, 4)})
	if !editor.Undo() || !editor.Undo() || !editor.Undo() {
		t.Fatal("Undo after branch refused")
	}
	stream.expectFrames("undo branch",
		[]HistoryStep{contractUndo(HistoryDelete, 3, 1, 4, 3, 4, -1, 2, 4)},
		[]HistoryStep{contractUndo(HistoryDelete, 2, 1, 3, 2, 3, -1, 2, 4)},
		[]HistoryStep{contractUndo(HistoryDelete, 1, 1, 2, 1, 2, -1, 2, 4)},
	)
	// A second, earlier branch moves the existing detach earlier (else-if path)
	// and truncates the retained tail to the new backing position.
	editor.Insert("z")
	stream.expectFrames("earlier branch moves detach",
		[]HistoryStep{contractForward(HistoryInsert, 1, 1, 1, 2, 2, -1, 1, 2)})
	// Save after a live detach sets save = current, removes the detach, and does
	// not require any byte change.
	editor.SetSavePoint()
	frames = stream.read("save after branch")
	contractCheckSteps(t, "save after branch", 0, frames[0].Steps, []HistoryStep{contractSave(2, 2, 2, -1, 2)})
	if !editor.AtSavePoint() {
		t.Fatal("editor is not at its save point after Save")
	}
}

// contractRequireRefused pins the refusal shape: no frames, ok=false, and the
// returned latest cursor is a real current cursor (an empty read of it succeeds)
// so a consumer can distinguish "lost history" from "nothing new".
func contractRequireRefused(t *testing.T, editor *Editor, cursor HistoryCursor, stage string) HistoryCursor {
	t.Helper()
	frames, latest, ok := editor.HistorySince(cursor)
	if ok || frames != nil {
		t.Fatalf("%s: cursor %+v was served %d frames", stage, cursor, len(frames))
	}
	if editor.journal != nil && editor.journal.history != nil && editor.journal.history.available {
		if more, current, ok := editor.HistorySince(latest); !ok || more != nil || current != latest {
			t.Fatalf("%s: returned latest %+v is not the current cursor (%d frames/ok=%t)", stage, latest, len(more), ok)
		}
	}
	return latest
}

// TestHistoryProducerOversizedAtomicActionComplete pins the >=1024-primitive
// requirement: one atomic Transact with 2049 primitives publishes exactly one
// complete frame, even though the ordinary byte journal is capped at 1024 and
// reports a gap for the same action.
func TestHistoryProducerOversizedAtomicActionComplete(t *testing.T) {
	editor := NewEditor("base")
	byteCursor := editor.TrackEdits()
	stream := contractTrack(t, editor)
	editor.SetSavePoint()
	stream.read("save")

	const primitives = 2049
	if err := editor.Transact(func(draft *Editor) error {
		end := 4
		for range primitives {
			draft.SetSelection(Range{Start: end, End: end})
			draft.Insert("x")
			end++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := len(editor.String()); got != 4+primitives {
		t.Fatalf("committed text length = %d, want %d", got, 4+primitives)
	}
	edits, _, byteOK := editor.EditsSince(byteCursor, nil)
	if byteOK || len(edits) != 0 {
		t.Fatalf("byte journal delivered %d of %d primitives", len(edits), primitives)
	}
	frames := stream.read("2049-primitive action")
	if len(frames) != 1 {
		t.Fatalf("oversized action published %d frames, want one complete frame", len(frames))
	}
	if got := len(frames[0].Steps); got != primitives {
		t.Fatalf("oversized action published %d steps, want %d", got, primitives)
	}
	if frames[0].BeforeBytes != 4 || frames[0].AfterBytes != 4+primitives {
		t.Fatalf("oversized frame bytes = %d->%d", frames[0].BeforeBytes, frames[0].AfterBytes)
	}
	for index, step := range frames[0].Steps {
		if step.Operation != HistoryForward || step.Primitive != HistoryInsert ||
			step.Position != 4+index || step.Length != 1 {
			t.Fatalf("step %d of the oversized action = %+v", index, step)
		}
	}

	if !editor.Undo() {
		t.Fatal("Undo of oversized action refused")
	}
	frames = stream.read("undo 2049-primitive action")
	if len(frames) != 1 || len(frames[0].Steps) != primitives {
		t.Fatalf("oversized undo = %d frames/%d steps", len(frames), len(frames[0].Steps))
	}
	for index, step := range frames[0].Steps {
		if step.Operation != HistoryUndo || step.Primitive != HistoryDelete ||
			step.Position != 4+primitives-1-index || step.Length != 1 {
			t.Fatalf("undo step %d of the oversized action = %+v", index, step)
		}
	}
	if editor.String() != "base" {
		t.Fatalf("text after oversized Undo = %q", editor.String())
	}
	if !editor.Redo() {
		t.Fatal("Redo of oversized action refused")
	}
	frames = stream.read("redo 2049-primitive action")
	if len(frames) != 1 || len(frames[0].Steps) != primitives {
		t.Fatalf("oversized redo = %d frames/%d steps", len(frames), len(frames[0].Steps))
	}
}

// TestHistoryProducerRepeatedReadsAndDeepCopyIsolation verifies that read
// methods copy values: a repeated read of the same cursor returns equal frames,
// caller mutation of returned frames cannot corrupt the producer, and reads at
// every boundary stay consistent.
func TestHistoryProducerRepeatedReadsAndDeepCopyIsolation(t *testing.T) {
	editor := NewEditor("abc")
	stream := contractTrack(t, editor)
	editor.Insert("é")
	editor.SetSavePoint()
	editor.MoveTo(0, false)
	editor.Insert("Z")
	base := stream.cursor
	first := stream.read("prefix")
	if len(first) != 3 {
		t.Fatalf("prefix frames = %d, want 3", len(first))
	}
	snapshot := make([]HistoryFrame, len(first))
	for index := range first {
		snapshot[index] = first[index]
		snapshot[index].Steps = append([]HistoryStep(nil), first[index].Steps...)
	}
	second, latest, ok := editor.HistorySince(base)
	if !ok {
		t.Fatal("repeated read refused")
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("repeated read differs:\nfirst  %+v\nsecond %+v", first, second)
	}
	// Mutate the returned frames deeply. The producer must remain untouched.
	for index := range first {
		first[index].BeforeBytes = -1
		first[index].AfterBytes = -1
		first[index].From = HistoryCursor{}
		for step := range first[index].Steps {
			first[index].Steps[step].Position = -1
			first[index].Steps[step].Operation = HistorySave
			first[index].Steps[step].Context.Current = -1
		}
		first[index].Steps = append(first[index].Steps, HistoryStep{Position: -99})
	}
	third, thirdLatest, ok := editor.HistorySince(base)
	if !ok || thirdLatest != latest {
		t.Fatalf("read after caller mutation failed: %d frames/ok=%t latest %+v", len(third), ok, thirdLatest)
	}
	if !reflect.DeepEqual(third, snapshot) {
		t.Fatalf("caller mutation changed producer frames:\n got %+v\nwant %+v", third, snapshot)
	}
	// The producer still works: the next edit publishes exactly one frame at the
	// old latest cursor.
	editor.BreakUndoGroup()
	editor.Insert("Q")
	frames, next, ok := editor.HistorySince(latest)
	if !ok || len(frames) != 1 || next != frames[0].To {
		t.Fatalf("post-mutation edit delivered %d frames/ok=%t", len(frames), ok)
	}
	contractCheckSteps(t, "post-mutation edit", 0, frames[0].Steps,
		[]HistoryStep{contractForward(HistoryInsert, 1, 1, 6, 7, 3, 1, -1, 3)})
	// Reading with the latest cursor is a true empty read; reading from the
	// first frame's From cursor returns every retained frame again.
	if frames, current, ok := editor.HistorySince(next); !ok || frames != nil || current != next {
		t.Fatalf("empty read at latest = %d frames/ok=%t", len(frames), ok)
	}
	if frames, _, ok := editor.HistorySince(snapshot[0].From); !ok || len(frames) != 4 {
		t.Fatalf("read from first frame cursor = %d frames/ok=%t", len(frames), ok)
	}
}

// TestHistoryProducerCursorRefusals covers wrong source, wrong generation,
// zero and future cursors, pre-reset cursors and untracked editors. A refusal
// must never masquerade as an empty successful read.
func TestHistoryProducerCursorRefusals(t *testing.T) {
	first := NewEditor("abc")
	stream := contractTrack(t, first)
	first.Insert("x")
	stream.read("one frame")
	current := stream.cursor

	second := NewEditor("abc")
	other, _, ok := second.TrackHistory()
	if !ok {
		t.Fatal("second TrackHistory refused")
	}
	contractRequireRefused(t, first, other, "cursor from another source")
	contractRequireRefused(t, first, HistoryCursor{}, "zero cursor")

	future := current
	future.sequence += 5
	contractRequireRefused(t, first, future, "future sequence")
	futureGeneration := current
	futureGeneration.generation++
	contractRequireRefused(t, first, futureGeneration, "unknown generation")
	futureSource := current
	futureSource.source++
	contractRequireRefused(t, first, futureSource, "unknown source")

	var untracked *Editor
	if frames, latest, ok := untracked.HistorySince(HistoryCursor{}); ok || frames != nil || latest != (HistoryCursor{}) {
		t.Fatal("nil editor served frames")
	}
	if cursor, bytes, ok := untracked.TrackHistory(); ok || cursor != (HistoryCursor{}) || bytes != 0 {
		t.Fatal("nil editor enabled a producer")
	}

	plain := NewEditor("abc")
	plain.Insert("x")
	if frames, latest, ok := plain.HistorySince(HistoryCursor{}); ok || frames != nil || latest != (HistoryCursor{}) {
		t.Fatal("untracked editor served frames")
	}
	if _, _, ok := plain.TrackHistory(); ok {
		t.Fatal("late enable with Undo history accepted")
	}

	// A reset generation refuses the pre-clear cursor and nobody can pass the
	// old generation through the live mapping.
	beforeClear := stream.cursor
	first.ClearHistory()
	contractRequireRefused(t, first, beforeClear, "pre-clear cursor")
	epoch, bytes, ok := first.TrackHistory()
	if !ok || bytes != len(first.String()) {
		t.Fatalf("post-clear TrackHistory = %d/%t", bytes, ok)
	}
	if epoch.generation != beforeClear.generation+1 {
		t.Fatalf("post-clear generation = %d, want %d", epoch.generation, beforeClear.generation+1)
	}
	if frames, latest, ok := first.HistorySince(epoch); !ok || frames != nil || latest != epoch {
		t.Fatalf("post-clear epoch read = %d frames/ok=%t", len(frames), ok)
	}
}

// TestHistoryProducerLateEnableAndResetBootstrap pins when a producer may be
// enabled (no retained Undo/Redo, no active composition), that repeated Track
// calls return the same epoch while the prefix is retained, and that a manual
// Clear starts a new generation with a fresh byte bootstrap.
func TestHistoryProducerLateEnableAndResetBootstrap(t *testing.T) {
	editor := NewEditor("abc")
	editor.Insert("d")
	if _, _, ok := editor.TrackHistory(); ok {
		t.Fatal("late enable with Undo history accepted")
	}
	editor.Undo()
	if _, _, ok := editor.TrackHistory(); ok {
		t.Fatal("late enable with Redo history accepted")
	}
	editor.Redo()
	if _, _, ok := editor.TrackHistory(); ok {
		t.Fatal("late enable with live history accepted")
	}
	editor.ClearHistory()
	stream := contractTrack(t, editor)
	stream.expectNoChanges("post-clear bootstrap")
	if len(editor.String()) != 4 {
		t.Fatalf("post-clear text = %q", editor.String())
	}

	// Repeated TrackHistory returns the same epoch and bootstrap length while
	// the retained prefix is intact, including after edits.
	epoch := stream.cursor
	againCursor, againBytes, againOK := editor.TrackHistory()
	if !againOK || againCursor != epoch || againBytes != 4 {
		t.Fatalf("repeated TrackHistory = %+v/%d/%t, want %+v/4/true", againCursor, againBytes, againOK, epoch)
	}
	editor.Insert("e")
	frames := stream.read("first post-enable edit")
	contractCheckSteps(t, "first post-enable edit", 0, frames[0].Steps,
		[]HistoryStep{contractForward(HistoryInsert, 4, 1, 4, 5, 1, 0, -1, 1)})
	againCursor, againBytes, againOK = editor.TrackHistory()
	if !againOK || againCursor != epoch || againBytes != 4 {
		t.Fatalf("TrackHistory after edits = %+v/%d/%t, want epoch %+v/4/true", againCursor, againBytes, againOK, epoch)
	}

	// An editor that was already at an ordinary save point but has no undo
	// history may opt in; the producer samples Save=0 at the bootstrap.
	saved := NewEditor("abc")
	saved.SetSavePoint()
	savedStream := contractTrack(t, saved)
	saved.Insert("x")
	frames = savedStream.read("edit after save-point bootstrap")
	contractCheckSteps(t, "edit after save-point bootstrap", 0, frames[0].Steps,
		[]HistoryStep{contractForward(HistoryInsert, 3, 1, 3, 4, 1, 0, -1, 1)})

	// Active legacy composition refuses a first enable.
	composing := NewEditor("abc")
	composing.SetMarkedText("x", Range{})
	if _, _, ok := composing.TrackHistory(); ok {
		t.Fatal("late enable during composition accepted")
	}

	// A manually cleared producer starts a new generation; the old epoch is
	// refused, the new epoch is empty, and the next edit continues from it.
	reset := NewEditor("abc")
	resetStream := contractTrack(t, reset)
	reset.Insert("x")
	resetStream.read("pre-reset edit")
	old := resetStream.cursor
	reset.ClearHistory()
	contractRequireRefused(t, reset, old, "pre-reset cursor")
	resetStream = contractTrack(t, reset)
	if resetStream.model != 4 {
		t.Fatalf("post-reset bootstrap = %d, want 4", resetStream.model)
	}
	resetStream.expectNoChanges("post-reset empty")
	reset.Insert("y")
	frames = resetStream.read("post-reset edit")
	contractCheckSteps(t, "post-reset edit", 0, frames[0].Steps,
		[]HistoryStep{contractForward(HistoryInsert, 4, 1, 4, 5, 1, 0, -1, 1)})

	// An invalidated producer stays invalidated across later Clear calls: an
	// unknown context never reboots as a complete-looking bootstrap (H04 owns
	// whether a reset may prove a new one).
	invalidated := NewEditor("abc")
	_ = contractTrack(t, invalidated)
	invalidated.SetMarkedText("x", Range{})
	invalidated.Unmark()
	invalidated.ClearHistory()
	if _, _, ok := invalidated.TrackHistory(); ok {
		t.Fatal("invalidated producer rebootstrapped after Clear")
	}
}

// TestHistoryProducerPruneNeverReportsNoChanges drives the producer past its
// frame ring and past the ordinary Undo trim boundary. Lost delivery must be
// reported as a refusal, never as "no changes"; a consumer that kept reading
// must still be able to continue from a retained frame.
func TestHistoryProducerPruneNeverReportsNoChanges(t *testing.T) {
	// (a) 300 Save metadata frames evict the oldest 44 while the producer stays
	// available: the bootstrap cursor is refused, a mid cursor still works.
	saved := NewEditor("abc")
	savedStream := contractTrack(t, saved)
	bootstrap := savedStream.cursor
	var mid HistoryCursor
	midModel := 0
	for index := 0; index < 300; index++ {
		saved.SetSavePoint()
		savedStream.read("save")
		if index == 49 {
			mid, midModel = savedStream.cursor, savedStream.model
		}
	}
	contractRequireRefused(t, saved, bootstrap, "evicted bootstrap cursor")
	frames, _, ok := saved.HistorySince(mid)
	if !ok {
		t.Fatal("mid cursor refused after frame-ring prune")
	}
	if len(frames) != 250 {
		t.Fatalf("mid cursor delivered %d frames, want 250", len(frames))
	}
	model := midModel
	contractCheckFrames(t, saved, mid, frames, &model, "mid after prune")
	if _, _, ok := saved.TrackHistory(); ok {
		t.Fatal("expired bootstrap was re-tracked after prune")
	}
	if frames, current, ok := saved.HistorySince(savedStream.cursor); !ok || frames != nil || current != savedStream.cursor {
		t.Fatalf("current cursor after prune = %d frames/ok=%t", len(frames), ok)
	}

	// (b) Coalesced typing produces one ordinary unit but one frame per
	// keystroke; the frame ring evicts without any ordinary trim. Reading from
	// the retained mid cursor and undoing the unit must still work.
	typed := NewEditor("")
	typedStream := contractTrack(t, typed)
	typedBootstrap := typedStream.cursor
	var typedMid HistoryCursor
	typedMidModel := 0
	for index := 0; index < 300; index++ {
		typed.Insert("x")
		typedStream.read("typing")
		if index == 49 {
			typedMid, typedMidModel = typedStream.cursor, typedStream.model
		}
	}
	contractRequireRefused(t, typed, typedBootstrap, "evicted typing bootstrap")
	frames, _, ok = typed.HistorySince(typedMid)
	if !ok || len(frames) != 250 {
		t.Fatalf("typing mid cursor delivered %d frames/ok=%t", len(frames), ok)
	}
	model = typedMidModel
	contractCheckFrames(t, typed, typedMid, frames, &model, "typing mid after prune")
	if !typed.Undo() {
		t.Fatal("Undo after typed prune refused")
	}
	frames = typedStream.read("undo after typed prune")
	if len(frames) != 1 || len(frames[0].Steps) != 300 {
		t.Fatalf("post-prune undo = %d frames/%d steps", len(frames), len(frames[0].Steps))
	}
	if typed.String() != "" {
		t.Fatalf("text after post-prune Undo = %q", typed.String())
	}
	if !typed.Redo() {
		t.Fatal("Redo after typed prune refused")
	}
	frames = typedStream.read("redo after typed prune")
	if len(frames) != 1 || len(frames[0].Steps) != 300 {
		t.Fatalf("post-prune redo = %d frames/%d steps", len(frames), len(frames[0].Steps))
	}

	// (c) One ordinary unit per edit reaches the Undo trim boundary at 257
	// units. The producer invalidates (it cannot rebase lost restoration
	// stacks) and every read from a formerly valid cursor is refused; ordinary
	// Undo/Redo keep working.
	trimmed := NewEditor("")
	trimmedStream := contractTrack(t, trimmed)
	trimmedBootstrap := trimmedStream.cursor
	for index := 0; index < 256; index++ {
		trimmed.BreakUndoGroup()
		trimmed.Insert("x")
		trimmedStream.read("unit")
	}
	trimmed.BreakUndoGroup()
	trimmed.Insert("x")
	if frames, latest, ok := trimmed.HistorySince(trimmedStream.cursor); ok || frames != nil {
		t.Fatalf("trim-invalidated producer served %d frames at %+v", len(frames), latest)
	}
	// Not an empty read, and not a rebootstrapped prefix.
	if frames, latest, ok := trimmed.HistorySince(trimmedBootstrap); ok || frames != nil {
		t.Fatalf("trim-invalidated bootstrap served %d frames at %+v", len(frames), latest)
	}
	if _, _, ok := trimmed.TrackHistory(); ok {
		t.Fatal("trim-invalidated producer rebootstrapped")
	}
	if got := len(trimmed.String()); got != 257 {
		t.Fatalf("text length after trim = %d, want 257", got)
	}
	if !trimmed.Undo() {
		t.Fatal("ordinary Undo refused after trim invalidation")
	}
	if !trimmed.Redo() {
		t.Fatal("ordinary Redo refused after trim invalidation")
	}
	if frames, _, ok := trimmed.HistorySince(trimmedStream.cursor); ok || frames != nil {
		t.Fatal("trim-invalidated producer revived after Undo/Redo")
	}
}

// TestHistoryProducerMixedForwardUndoInOneAtomicFrame uses the explicit
// composition ingress, where one atomic input must publish Forward and Undo
// primitives in one frame in source order (an active update first reverses its
// own tentative suffix, then inserts the new text).
func TestHistoryProducerMixedForwardUndoInOneAtomicFrame(t *testing.T) {
	editor := NewEditor("abc")
	if !editor.EnableTentativeComposition() {
		t.Fatal("EnableTentativeComposition refused")
	}
	stream := contractTrack(t, editor)
	editor.SetSelection(Range{Start: 3, End: 3})
	if !editor.ApplyComposition(CompositionEvent{Phase: CompositionUpdate, Text: "に"}) {
		t.Fatal("first update refused")
	}
	stream.expectFrames("first tentative update",
		[]HistoryStep{contractForward(HistoryInsert, 3, 3, 3, 6, 1, 0, -1, 1)})

	if !editor.ApplyComposition(CompositionEvent{Phase: CompositionUpdate, Text: "には"}) {
		t.Fatal("second update refused")
	}
	// One frame: undo the previous tentative insert, then insert both scalars.
	stream.expectFrames("mixed atomic update",
		[]HistoryStep{
			contractUndo(HistoryDelete, 3, 3, 6, 3, 1, 0, -1, 1),
			contractForward(HistoryInsert, 3, 3, 3, 6, 1, 0, -1, 1),
			contractForward(HistoryInsert, 6, 3, 6, 9, 2, 0, -1, 2),
		})
	if editor.String() != "abcには" {
		t.Fatalf("text after mixed update = %q", editor.String())
	}
	// Commit is a physical acceptance boundary, not a byte/metadata event.
	if !editor.ApplyComposition(CompositionEvent{Phase: CompositionCommit}) {
		t.Fatal("commit refused")
	}
	stream.expectNoChanges("commit")
	if !editor.Undo() {
		t.Fatal("Undo after commit refused")
	}
	// The two committed scalars coalesced into one ordinary unit; undo publishes
	// both inverse primitives in one frame.
	stream.expectFrames("undo committed action",
		[]HistoryStep{
			contractUndo(HistoryDelete, 6, 3, 9, 6, 2, 0, -1, 2),
			contractUndo(HistoryDelete, 3, 3, 6, 3, 1, 0, -1, 2),
		})
	if editor.String() != "abc" {
		t.Fatalf("text after undo = %q", editor.String())
	}
	// A cancel frame is published when the tentative suffix is physically
	// rolled back; this editor has no redo suffix, which is the supported flow
	// for a fresh tentative event.
	canceled := NewEditor("abc")
	if !canceled.EnableTentativeComposition() {
		t.Fatal("EnableTentativeComposition refused")
	}
	cancelStream := contractTrack(t, canceled)
	canceled.SetSelection(Range{Start: 3, End: 3})
	if !canceled.ApplyComposition(CompositionEvent{Phase: CompositionUpdate, Text: "か"}) {
		t.Fatal("cancel update refused")
	}
	cancelStream.expectFrames("update before cancel",
		[]HistoryStep{contractForward(HistoryInsert, 3, 3, 3, 6, 1, 0, -1, 1)})
	if !canceled.ApplyComposition(CompositionEvent{Phase: CompositionCancel}) {
		t.Fatal("cancel refused")
	}
	cancelStream.expectFrames("cancel rolls back",
		[]HistoryStep{contractUndo(HistoryDelete, 3, 3, 6, 3, 1, 0, -1, 1)})
	if canceled.String() != "abc" {
		t.Fatalf("text after cancel = %q", canceled.String())
	}
}

// TestHistoryProducerImeIngressRefusal keeps the legacy tentative ingress
// fail-closed: marked text invalidates producer metadata instead of publishing
// guessed frames, and no later ending may reboot it.
func TestHistoryProducerImeIngressRefusal(t *testing.T) {
	editor := NewEditor("abc")
	stream := contractTrack(t, editor)
	editor.Insert("x")
	stream.read("prefix")
	editor.SetMarkedText("y", Range{})
	contractRequireRefused(t, editor, stream.cursor, "legacy marked text")
	if _, _, ok := editor.TrackHistory(); ok {
		t.Fatal("TrackHistory accepted during legacy marked text")
	}
	editor.Unmark()
	if _, _, ok := editor.TrackHistory(); ok {
		t.Fatal("TrackHistory accepted after Unmark")
	}
	editor.ClearHistory()
	if _, _, ok := editor.TrackHistory(); ok {
		t.Fatal("TrackHistory rebootstrapped after legacy invalidation and Clear")
	}
	if frames, _, ok := editor.HistorySince(HistoryCursor{}); ok || frames != nil {
		t.Fatal("invalidated producer served a zero cursor")
	}
}

// TestHistoryProducerRestrictedAndOverwriteCapture verifies the producer
// samples the actually accepted primitives after restriction/truncation and
// after overwrite typing, not the requested text.
func TestHistoryProducerRestrictedAndOverwriteCapture(t *testing.T) {
	restricted := NewEditor("abc")
	restricted.Restrict(func(value rune) bool { return value >= '0' && value <= '9' }, 0)
	stream := contractTrack(t, restricted)
	restricted.Insert("a1é2")
	stream.expectFrames("filtered insertion",
		[]HistoryStep{contractForward(HistoryInsert, 3, 2, 3, 5, 1, 0, -1, 1)})
	if restricted.String() != "abc12" {
		t.Fatalf("filtered text = %q", restricted.String())
	}

	capped := NewEditor("abc")
	capped.Restrict(nil, 5)
	stream = contractTrack(t, capped)
	capped.Insert("wxyz")
	stream.expectFrames("truncated insertion",
		[]HistoryStep{contractForward(HistoryInsert, 3, 2, 3, 5, 1, 0, -1, 1)})
	if capped.String() != "abcwx" {
		t.Fatalf("truncated text = %q", capped.String())
	}

	overwrite := NewEditor("abcd")
	overwrite.SetOverwrite(true)
	stream = contractTrack(t, overwrite)
	overwrite.SetSelection(Range{Start: 1, End: 1})
	overwrite.Insert("é")
	stream.expectFrames("overwrite replacement",
		[]HistoryStep{
			contractForward(HistoryDelete, 1, 1, 4, 3, 1, 0, -1, 1),
			contractForward(HistoryInsert, 1, 2, 3, 5, 2, 0, -1, 2),
		})
	if overwrite.String() != "aécd" {
		t.Fatalf("overwrite text = %q", overwrite.String())
	}
}

// TestHistoryProducerMixedWorkflowInvariants runs deterministic mixed workflows
// through real editing entry points and checks every delivered frame against a
// byte-length model. It is the broadest check that the producer's ordered
// primitives, byte accounting and cursor advance agree with the actual editor.
func TestHistoryProducerMixedWorkflowInvariants(t *testing.T) {
	choices := []string{"a", "b", "é", "漢", "\n", "xy", "🙂"}
	for seed := int64(1); seed <= 10; seed++ {
		random := rand.New(rand.NewSource(seed))
		editor := NewEditor("start")
		editor.SetSavePoint()
		stream := contractTrack(t, editor)
		for step := 0; step < 120; step++ {
			stage := "seed " + strconv.FormatInt(seed, 10) + " step " + strconv.Itoa(step)
			switch random.Intn(12) {
			case 0, 1, 2, 3:
				target := random.Intn(editor.RuneCount() + 1)
				editor.SetSelection(Range{Start: target, End: target})
				editor.Insert(choices[random.Intn(len(choices))])
			case 4:
				if editor.RuneCount() > 0 {
					editor.MoveTo(random.Intn(editor.RuneCount()), false)
					editor.DeleteBackward()
				}
			case 5:
				if editor.RuneCount() > 0 {
					editor.MoveTo(random.Intn(editor.RuneCount()), false)
					editor.DeleteForward()
				}
			case 6:
				if editor.RuneCount() > 1 {
					start := random.Intn(editor.RuneCount() - 1)
					editor.SetSelection(Range{Start: start, End: start + 1 + random.Intn(2)})
					editor.Insert(choices[random.Intn(len(choices))])
				}
			case 7:
				if random.Intn(2) == 0 {
					editor.Undo()
				} else {
					editor.Redo()
				}
			case 8:
				editor.SetSavePoint()
			case 9:
				editor.Paste(choices[random.Intn(len(choices))] + "P")
			case 10:
				if editor.RuneCount() > 2 {
					start := random.Intn(editor.RuneCount() - 1)
					_ = editor.ReplaceRanges([]Replacement{{Range: Range{Start: start, End: start + 1}, Text: choices[random.Intn(len(choices))]}})
				}
			case 11:
				_ = editor.Transact(func(draft *Editor) error {
					draft.SetSelection(Range{Start: draft.RuneCount(), End: draft.RuneCount()})
					draft.Insert(choices[random.Intn(len(choices))])
					if random.Intn(2) == 0 {
						draft.Undo()
					}
					draft.SetSelection(Range{Start: 0, End: 0})
					draft.Insert(choices[random.Intn(len(choices))])
					return nil
				})
			}
			stream.read(stage)
		}
		stream.expectNoChanges("workflow end")
		if stream.model != len(editor.String()) {
			t.Fatalf("seed %d: model %d != text %d", seed, stream.model, len(editor.String()))
		}
	}
}

var historyContractCostSink *Editor

// TestHistoryProducerCostOnlyWhenEnabled records the honest nonzero cost: a
// default editor retains no producer structure and pays nothing per edit, the
// opted-in editor pays a header plus per-frame and per-step backing charged to
// the existing history budget, and opting in allocates strictly more than the
// plain construction path. Exact numbers are recorded by the H01 evidence run.
func TestHistoryProducerCostOnlyWhenEnabled(t *testing.T) {
	plain := NewEditor("base")
	plain.Insert("x")
	plain.SetSavePoint()
	plain.Undo()
	plain.Redo()
	plain.ClearHistory()
	if plain.journal != nil && plain.journal.history != nil {
		t.Fatalf("default editor retained a producer: %+v", plain.journal.history)
	}
	if frames, latest, ok := plain.HistorySince(HistoryCursor{}); ok || frames != nil || latest != (HistoryCursor{}) {
		t.Fatal("default editor served producer data")
	}

	enabled := NewEditor("abc")
	stream := contractTrack(t, enabled)
	history := enabled.journal.history
	headerCharge := history.cost
	if headerCharge != historyHeaderRunes {
		t.Fatalf("enabled header charge = %d runes, want %d", headerCharge, historyHeaderRunes)
	}
	if chargedBytes := headerCharge * int(unsafe.Sizeof(rune(0))); chargedBytes < int(unsafe.Sizeof(*history)) {
		t.Fatalf("header charge %d bytes does not cover historyJournal %d bytes", chargedBytes, unsafe.Sizeof(*history))
	}
	for range 16 {
		enabled.BreakUndoGroup()
		enabled.Insert("x")
	}
	stream.read("16 units")
	if len(history.frames) != 16 || !history.available {
		t.Fatalf("retained frames/availability = %d/%t, want 16/true", len(history.frames), history.available)
	}
	structural := int(unsafe.Sizeof(*history)) + cap(history.frames)*int(unsafe.Sizeof(HistoryFrame{}))
	for _, frame := range history.frames {
		structural += cap(frame.Steps) * int(unsafe.Sizeof(HistoryStep{}))
	}
	charged := (enabled.historyCost() - enabled.historyRunes) * int(unsafe.Sizeof(rune(0)))
	if charged < structural {
		t.Fatalf("producer charges %d bytes, structural lower bound is %d", charged, structural)
	}
	if history.cost <= headerCharge {
		t.Fatalf("frame backing was not charged: cost still %d runes", history.cost)
	}
	// Coalesced producer-only primitives are charged per unit while enabled.
	primitiveCharge := 0
	for index := range enabled.history {
		primitiveCharge += cap(enabled.history[index].primitives) * historyPrimitiveRunes
	}
	if primitiveCharge <= 0 {
		t.Fatal("producer-only primitive backing is not charged")
	}
	t.Logf("sizes: Editor=%d historyJournal=%d historyEdit=%d HistoryFrame=%d HistoryStep=%d",
		unsafe.Sizeof(Editor{}), unsafe.Sizeof(historyJournal{}), unsafe.Sizeof(historyEdit{}),
		unsafe.Sizeof(HistoryFrame{}), unsafe.Sizeof(HistoryStep{}))
	t.Logf("charges: header=%d runes (%d bytes) frame=%d runes step=%d runes; retained frames=16 cost=%d runes, unit primitive charge=%d runes",
		headerCharge, headerCharge*int(unsafe.Sizeof(rune(0))), historyFrameRunes, historyStepRunes, history.cost, primitiveCharge)

	allocPlain := testing.AllocsPerRun(100, func() {
		historyContractCostSink = NewEditor("base")
	})
	allocEnabled := testing.AllocsPerRun(100, func() {
		editor := NewEditor("base")
		editor.TrackHistory()
		historyContractCostSink = editor
	})
	if allocEnabled <= allocPlain {
		t.Fatalf("TrackHistory allocation count %v is not above the plain construction %v", allocEnabled, allocPlain)
	}
	t.Logf("allocations: NewEditor=%v NewEditor+TrackHistory=%v", allocPlain, allocEnabled)

	// A producer-enabled editor that is invalidated keeps only the refusal
	// guard charge and releases frame backing (documented in history_boundary_test).
	enabled.invalidateHistory()
	if history.cost != historyHeaderRunes {
		t.Fatalf("invalidated guard charge = %d runes, want header %d", history.cost, historyHeaderRunes)
	}
}

// BenchmarkHistoryProducerReadPendingFrames measures the consumer-side cost of
// HistorySince copying 16 retained frames (values only, nested steps copied).
func BenchmarkHistoryProducerReadPendingFrames(b *testing.B) {
	editor := NewEditor("")
	epoch, _, ok := editor.TrackHistory()
	if !ok {
		b.Fatal("TrackHistory refused")
	}
	for range 16 {
		editor.BreakUndoGroup()
		editor.Insert("x")
	}
	b.ReportAllocs()
	for b.Loop() {
		frames, _, ok := editor.HistorySince(epoch)
		if !ok || len(frames) != 16 {
			b.Fatalf("read = %d frames/ok=%t", len(frames), ok)
		}
	}
}

func benchmarkHistoryMatchedWorkflow(b *testing.B, enabled bool) {
	for b.Loop() {
		editor := NewEditor("")
		var cursor HistoryCursor
		if enabled {
			var ok bool
			cursor, _, ok = editor.TrackHistory()
			if !ok {
				b.Fatal("TrackHistory refused")
			}
		}
		for _, value := range []string{"h", "e", "\n", "l", "l", "o"} {
			editor.Insert(value)
		}
		editor.SetSavePoint()
		editor.Undo()
		editor.Redo()
		if enabled {
			frames, _, ok := editor.HistorySince(cursor)
			if !ok || len(frames) == 0 {
				b.Fatalf("HistorySince = %d frames/ok=%t", len(frames), ok)
			}
		}
		historyContractCostSink = editor
	}
}

// BenchmarkHistoryProducerMatchedWorkflow runs the identical edit workflow with
// and without the producer so the default/enabled delta is directly comparable.
func BenchmarkHistoryProducerMatchedWorkflow(b *testing.B) {
	b.Run("default", func(b *testing.B) {
		b.ReportAllocs()
		benchmarkHistoryMatchedWorkflow(b, false)
	})
	b.Run("enabled", func(b *testing.B) {
		b.ReportAllocs()
		benchmarkHistoryMatchedWorkflow(b, true)
	})
}
