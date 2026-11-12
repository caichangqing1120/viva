package text

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const (
	replayFixture     = "testdata/scintilla-history-position.tsv"
	replayFixtureHash = "86a18c4681356aa6b61c03d2fab97c6416ba2ba8f1981a75dfb48e06d59218ed"
)

type replaySample struct {
	length, current, save, detach, actions int
	collecting                             bool
}

type replayEvent struct {
	kind                                        string
	position, length                            int
	collecting, beforeSave, detached, reverting bool
	sample                                      replaySample
}

type replayRow struct {
	workflow          string
	step              int
	phase, kind, text string
	sample            replaySample
	canUndo, canRedo  bool
	callbacks         []replayEvent
}

func replayInt(what, value string) (int, error) {
	number, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s: %q is not an integer", what, value)
	}
	return number, nil
}

func replayBool(what, value string) (bool, error) {
	switch value {
	case "0":
		return false, nil
	case "1":
		return true, nil
	default:
		return false, fmt.Errorf("%s: %q is not 0 or 1", what, value)
	}
}

func replayDecodeText(encoded string) (string, error) {
	if encoded == "-" {
		return "", nil
	}
	if encoded == "" || len(encoded)%2 != 0 || strings.ToLower(encoded) != encoded {
		return "", fmt.Errorf("text is not lower-case even-length hex: %q", encoded)
	}
	decoded, err := hex.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

func replayParseSample(what string, values []string) (replaySample, error) {
	if len(values) != 8 {
		return replaySample{}, fmt.Errorf("%s: sample has %d fields, want 8", what, len(values))
	}
	var sample replaySample
	var err error
	if sample.length, err = replayInt(what+" length", values[0]); err != nil {
		return sample, err
	}
	if sample.current, err = replayInt(what+" current", values[1]); err != nil {
		return sample, err
	}
	if sample.save, err = replayInt(what+" save", values[2]); err != nil {
		return sample, err
	}
	if sample.detach, err = replayInt(what+" detach", values[3]); err != nil {
		return sample, err
	}
	if _, err = replayInt(what+" tentative", values[4]); err != nil {
		return sample, err
	}
	if _, err = replayBool(what+" tentative-active", values[5]); err != nil {
		return sample, err
	}
	if sample.collecting, err = replayBool(what+" collecting", values[6]); err != nil {
		return sample, err
	}
	if sample.actions, err = replayInt(what+" actions", values[7]); err != nil {
		return sample, err
	}
	return sample, nil
}

func replayParseEvent(record string) (replayEvent, error) {
	parts := strings.Split(record, "|")
	if len(parts) != 10 {
		return replayEvent{}, fmt.Errorf("callback has %d fields, want 10", len(parts))
	}
	if _, err := replayInt("callback sequence", parts[0]); err != nil {
		return replayEvent{}, err
	}
	sample, err := replayParseSample("callback", parts[2:])
	if err != nil {
		return replayEvent{}, err
	}
	fields := strings.Split(parts[1], ":")
	event := replayEvent{kind: fields[0], sample: sample}
	argument := func(index int) (int, error) {
		if index >= len(fields) {
			return 0, fmt.Errorf("%s callback is missing argument %d", fields[0], index)
		}
		return replayInt(fields[0]+" argument", fields[index])
	}
	switch fields[0] {
	case "insert", "delete":
		event.position, err = argument(1)
		if err != nil {
			return event, err
		}
		event.length, err = argument(2)
		if err != nil {
			return event, err
		}
		if fields[0] == "insert" {
			event.collecting, err = replayBool("insert collecting", fields[3])
			if err != nil {
				return event, err
			}
			event.beforeSave, err = replayBool("insert before-save", fields[4])
		} else {
			event.beforeSave, err = replayBool("delete before-save", fields[3])
			if err != nil {
				return event, err
			}
			event.detached, err = replayBool("delete detached", fields[4])
		}
		if err != nil {
			return event, err
		}
	case "undo-insert", "undo-delete":
		event.position, err = argument(1)
		if err != nil {
			return event, err
		}
		event.length, err = argument(2)
		if err != nil {
			return event, err
		}
		if fields[0] == "undo-insert" {
			event.reverting, err = replayBool("undo-insert reverting", fields[3])
		} else {
			event.detached, err = replayBool("undo-delete detached", fields[3])
		}
		if err != nil {
			return event, err
		}
	case "save", "start", "end":
		if len(fields) != 1 {
			return event, fmt.Errorf("%s callback carries arguments", fields[0])
		}
	default:
		return event, fmt.Errorf("unknown callback %q", parts[1])
	}
	return event, nil
}

func replayReadRows(t *testing.T) (map[string][]replayRow, int, int) {
	t.Helper()
	raw, err := os.ReadFile(replayFixture)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if fmt.Sprintf("%x", sum) != replayFixtureHash {
		t.Fatalf("reference fixture hash = %x, want %s", sum, replayFixtureHash)
	}
	reader := csv.NewReader(strings.NewReader(string(raw)))
	reader.Comma = '\t'
	reader.Comment = '#'
	reader.FieldsPerRecord = 16
	rowsByWorkflow := map[string][]replayRow{}
	callbacks := 0
	for {
		fields, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if fields[0] == "workflow" {
			continue
		}
		step, err := replayInt("step", fields[1])
		if err != nil {
			t.Fatal(err)
		}
		text, err := replayDecodeText(fields[4])
		if err != nil {
			t.Fatal(err)
		}
		sample, err := replayParseSample("row", fields[5:13])
		if err != nil {
			t.Fatal(err)
		}
		canUndo, err := replayBool("can-undo", fields[13])
		if err != nil {
			t.Fatal(err)
		}
		canRedo, err := replayBool("can-redo", fields[14])
		if err != nil {
			t.Fatal(err)
		}
		row := replayRow{workflow: fields[0], step: step, phase: fields[2], kind: fields[3],
			text: text, sample: sample, canUndo: canUndo, canRedo: canRedo}
		if fields[15] != "-" {
			for _, record := range strings.Split(fields[15], ";") {
				event, err := replayParseEvent(record)
				if err != nil {
					t.Fatalf("%s/%s: %v", fields[0], fields[2], err)
				}
				row.callbacks = append(row.callbacks, event)
				callbacks++
			}
		}
		rowsByWorkflow[fields[0]] = append(rowsByWorkflow[fields[0]], row)
	}
	return rowsByWorkflow, len(rowsByWorkflow), callbacks
}

// replayProducerEvents applies the canonical producer-to-consumer mapping to
// complete frames. Control events are emitted for every primitive whose sampled
// predicate holds, exactly as the original source calls them.
func replayProducerEvents(frames []HistoryFrame) []replayEvent {
	var events []replayEvent
	sample := func(step HistoryStep, length int) replaySample {
		return replaySample{length: length, current: step.Context.Current, save: step.Context.Save,
			detach: step.Context.Detach, actions: step.Context.RetainedTo,
			collecting: step.Context.Collecting}
	}
	after := func(step HistoryStep) int { return step.AfterBytes }
	before := func(step HistoryStep) int { return step.BeforeBytes }
	for _, frame := range frames {
		for _, step := range frame.Steps {
			p, s, d := step.Context.Current, step.Context.Save, step.Context.Detach
			beforeReachable := s > 0 && s > p
			afterOrAtDetach := d >= 0 && d <= p
			afterDetach := d >= 0 && d < p
			previousBeforeSave := s < 0 || s >= p
			afterSave := s >= 0 && s <= p
			switch step.Operation {
			case HistorySave:
				events = append(events, replayEvent{kind: "save", sample: sample(step, after(step))})
			case HistoryForward:
				switch step.Primitive {
				case HistoryDelete:
					events = append(events, replayEvent{kind: "delete", position: step.Position, length: step.Length,
						beforeSave: beforeReachable, detached: afterOrAtDetach, sample: sample(step, before(step))})
				case HistoryInsert:
					events = append(events, replayEvent{kind: "insert", position: step.Position, length: step.Length,
						collecting: step.Context.Collecting, beforeSave: beforeReachable, sample: sample(step, after(step))})
				}
			case HistoryUndo:
				if previousBeforeSave {
					events = append(events, replayEvent{kind: "start", sample: sample(step, before(step))})
				}
				switch step.Primitive {
				case HistoryDelete:
					events = append(events, replayEvent{kind: "undo-insert", position: step.Position, length: step.Length,
						reverting: previousBeforeSave && !afterDetach, sample: sample(step, before(step))})
				case HistoryInsert:
					events = append(events, replayEvent{kind: "undo-delete", position: step.Position, length: step.Length,
						detached: afterDetach, sample: sample(step, after(step))})
				}
			case HistoryRedo:
				switch step.Primitive {
				case HistoryDelete:
					events = append(events, replayEvent{kind: "delete", position: step.Position, length: step.Length,
						beforeSave: beforeReachable, detached: afterOrAtDetach, sample: sample(step, before(step))})
				case HistoryInsert:
					events = append(events, replayEvent{kind: "insert", position: step.Position, length: step.Length,
						collecting: step.Context.Collecting,
						beforeSave: (s < 0 || s > p) && !afterOrAtDetach, sample: sample(step, after(step))})
				}
				if afterSave {
					events = append(events, replayEvent{kind: "end", sample: sample(step, after(step))})
				}
			}
		}
	}
	return events
}

func replayEventString(event replayEvent) string {
	return fmt.Sprintf("%s pos=%d len=%d collecting=%t beforeSave=%t detached=%t reverting=%t sample=%+v",
		event.kind, event.position, event.length, event.collecting, event.beforeSave,
		event.detached, event.reverting, event.sample)
}

func replayCheckEvents(t *testing.T, workflow string, row replayRow, events []replayEvent) {
	t.Helper()
	if len(events) != len(row.callbacks) {
		t.Fatalf("%s/%s: %d producer events, %d original callbacks\ngot=%v", workflow, row.phase,
			len(events), len(row.callbacks), events)
	}
	for index, want := range row.callbacks {
		got := events[index]
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s/%s event %d = %s, want %s", workflow, row.phase, index,
				replayEventString(got), replayEventString(want))
		}
	}
}

func replayAssertState(t *testing.T, workflow string, editor *Editor, row replayRow) {
	t.Helper()
	history := editor.historyAvailable()
	if history == nil {
		t.Fatalf("%s/%s: producer is unavailable", workflow, row.phase)
	}
	if got := editor.String(); got != row.text {
		t.Fatalf("%s/%s: text %q (%s), want %q (%s)", workflow, row.phase,
			got, hex.EncodeToString([]byte(got)), row.text, hex.EncodeToString([]byte(row.text)))
	}
	if history.bytes != row.sample.length || len(editor.String()) != row.sample.length {
		t.Fatalf("%s/%s: bytes=%d/%d, want %d", workflow, row.phase,
			history.bytes, len(editor.String()), row.sample.length)
	}
	if history.current != row.sample.current || history.save != row.sample.save || history.detach != row.sample.detach {
		t.Fatalf("%s/%s: current/save/detach=%d/%d/%d, want %d/%d/%d", workflow, row.phase,
			history.current, history.save, history.detach,
			row.sample.current, row.sample.save, row.sample.detach)
	}
	if history.retainedFrom != 0 || history.retainedTo != row.sample.actions {
		t.Fatalf("%s/%s: retained=%d/%d, want 0/%d", workflow, row.phase,
			history.retainedFrom, history.retainedTo, row.sample.actions)
	}
	if !row.sample.collecting {
		t.Fatalf("%s/%s: fixture expects unsupported uncollected editing", workflow, row.phase)
	}
	if editor.CanUndo() != row.canUndo || editor.CanRedo() != row.canRedo {
		t.Fatalf("%s/%s: can-undo/can-redo=%t/%t, want %t/%t", workflow, row.phase,
			editor.CanUndo(), editor.CanRedo(), row.canUndo, row.canRedo)
	}
}

type replayOp struct {
	phase string
	reset bool
	apply func(*testing.T, *Editor)
}

type replayWorkflow struct {
	initial string
	ops     []replayOp
}

func replayWorkflows(t *testing.T) map[string]replayWorkflow {
	t.Helper()
	undo := func(t *testing.T, editor *Editor) {
		t.Helper()
		if !editor.Undo() {
			t.Fatal("Undo refused")
		}
	}
	redo := func(t *testing.T, editor *Editor) {
		t.Helper()
		if !editor.Redo() {
			t.Fatal("Redo refused")
		}
	}
	insertAt := func(position int, value string) func(*testing.T, *Editor) {
		return func(t *testing.T, editor *Editor) {
			editor.SetSelection(Range{Start: position, End: position})
			editor.Insert(value)
		}
	}
	return map[string]replayWorkflow{
		"coalesced": {initial: "abcd", ops: []replayOp{
			{phase: "insert-coalesce-1", apply: insertAt(4, "x")},
			{phase: "insert-coalesce-2", apply: insertAt(5, "y")},
			{phase: "undo-coalesced-inserts", apply: undo},
			{phase: "redo-coalesced-inserts", apply: redo},
			{phase: "delete-coalesce-1", apply: func(t *testing.T, editor *Editor) {
				editor.MoveTo(4, false)
				if !editor.DeleteBackward() {
					t.Fatal("DeleteBackward refused")
				}
			}},
			{phase: "delete-coalesce-2", apply: func(t *testing.T, editor *Editor) {
				if !editor.DeleteBackward() {
					t.Fatal("DeleteBackward refused")
				}
			}},
			{phase: "undo-coalesced-deletes", apply: undo},
			{phase: "redo-coalesced-deletes", apply: redo},
		}},
		"grouped-replacement": {initial: "abc", ops: []replayOp{
			{phase: "group-replace", apply: func(t *testing.T, editor *Editor) {
				if err := editor.ReplaceRanges([]Replacement{{Range: Range{Start: 1, End: 2}, Text: "XY"}}); err != nil {
					t.Fatal(err)
				}
			}},
			{phase: "undo-group", apply: undo},
			{phase: "redo-group", apply: redo},
		}},
		"saved-crossing": {initial: "abcdef", ops: []replayOp{
			{phase: "insert-before-save", apply: insertAt(1, "X")},
			{phase: "save", apply: func(t *testing.T, editor *Editor) { editor.SetSavePoint() }},
			{phase: "insert-after-save", apply: insertAt(2, "Y")},
			{phase: "delete-after-save", apply: func(t *testing.T, editor *Editor) {
				editor.SetSelection(Range{Start: 3, End: 4})
				if !editor.DeleteBackward() {
					t.Fatal("DeleteBackward refused")
				}
			}},
			{phase: "undo-delete", apply: undo},
			{phase: "undo-insert-after-save", apply: undo},
			{phase: "undo-insert-before-save", apply: undo},
			{phase: "redo-before-save", apply: redo},
			{phase: "redo-after-save", apply: redo},
			{phase: "redo-delete", apply: redo},
		}},
		"detached-branch": {initial: "abcd", ops: []replayOp{
			{phase: "insert", apply: insertAt(1, "x")},
			{phase: "coalesce", apply: insertAt(2, "y")},
			{phase: "save", apply: func(t *testing.T, editor *Editor) { editor.SetSavePoint() }},
			{phase: "undo", apply: undo},
			{phase: "branch", apply: insertAt(0, "z")},
			{phase: "detached-delete", apply: func(t *testing.T, editor *Editor) {
				editor.SetSelection(Range{Start: 2, End: 3})
				if !editor.DeleteBackward() {
					t.Fatal("DeleteBackward refused")
				}
			}},
			{phase: "undo-detached-delete", apply: undo},
			{phase: "undo-branch-insert", apply: undo},
			{phase: "redo-branch", apply: redo},
			{phase: "new-save", apply: func(t *testing.T, editor *Editor) { editor.SetSavePoint() }},
		}},
		"equal-byte-replacement": {initial: "abc", ops: []replayOp{
			{phase: "replace-equal", apply: func(t *testing.T, editor *Editor) {
				if err := editor.ReplaceRanges([]Replacement{{Range: Range{Start: 1, End: 2}, Text: "b"}}); err != nil {
					t.Fatal(err)
				}
			}},
			{phase: "undo-equal", apply: undo},
			{phase: "redo-equal", apply: redo},
		}},
		"zero-net-transaction": {initial: "abc", ops: []replayOp{
			{phase: "zero-net-group", apply: func(t *testing.T, editor *Editor) {
				if err := editor.Transact(func(draft *Editor) error {
					draft.SetSelection(Range{Start: 3, End: 3})
					draft.Insert("xy")
					draft.SetSelection(Range{Start: 3, End: 5})
					if !draft.DeleteBackward() {
						t.Fatal("draft DeleteBackward refused")
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}},
			{phase: "undo-zero-net", apply: undo},
			{phase: "redo-zero-net", apply: redo},
		}},
		"live-clear-detach": {initial: "abcd", ops: []replayOp{
			{phase: "insert", apply: insertAt(1, "x")},
			{phase: "save", apply: func(t *testing.T, editor *Editor) { editor.SetSavePoint() }},
			{phase: "undo", apply: undo},
			{phase: "branch", apply: insertAt(0, "z")},
			{phase: "clear", reset: true, apply: func(t *testing.T, editor *Editor) { editor.ClearHistory() }},
			{phase: "post-clear-insert", apply: insertAt(0, "q")},
			{phase: "undo-post-clear-insert", apply: undo},
		}},
		"large-action": {initial: "abc", ops: []replayOp{
			{phase: "container-2049-inserts", apply: func(t *testing.T, editor *Editor) {
				if err := editor.Transact(func(draft *Editor) error {
					for range 2049 {
						draft.SetSelection(Range{Start: 0, End: 0})
						draft.Insert("x")
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}},
			{phase: "undo-action-2049", apply: undo},
			{phase: "redo-action-2049", apply: redo},
		}},
	}
}

func TestHistoryReferenceReplayWorkflows(t *testing.T) {
	rows, workflowsCount, callbacks := replayReadRows(t)
	if workflowsCount != 16 || callbacks != 8306 {
		t.Fatalf("reference has %d workflows/%d callbacks, want 16/8306", workflowsCount, callbacks)
	}
	definitions := replayWorkflows(t)
	names := []string{"coalesced", "grouped-replacement", "saved-crossing", "detached-branch",
		"equal-byte-replacement", "zero-net-transaction", "live-clear-detach", "large-action"}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			rowsForWorkflow := rows[name]
			if len(rowsForWorkflow) == 0 {
				t.Fatalf("fixture lacks workflow %q", name)
			}
			definition := definitions[name]
			ops := map[string]replayOp{}
			for _, op := range definition.ops {
				ops[op.phase] = op
			}
			editor := NewEditor(definition.initial)
			// Match the fixture bootstrap save point without emitting a frame.
			editor.SetSavePoint()
			cursor, bootstrapBytes, ok := editor.TrackHistory()
			if !ok || bootstrapBytes != len(definition.initial) {
				t.Fatalf("TrackHistory = %d/%t, want %d/true", bootstrapBytes, ok, len(definition.initial))
			}
			replayAssertState(t, name, editor, rowsForWorkflow[0])
			used := map[string]bool{}
			for _, row := range rowsForWorkflow[1:] {
				op, ok := ops[row.phase]
				if !ok {
					t.Fatalf("%s: no real-operation adapter for phase %q", name, row.phase)
				}
				used[row.phase] = true
				op.apply(t, editor)
				if op.reset {
					if row.kind != "reset" {
						t.Fatalf("%s/%s: reset operation has row kind %q", name, row.phase, row.kind)
					}
					if frames, _, ok := editor.HistorySince(cursor); ok || frames != nil {
						t.Fatalf("%s/%s: old cursor served frames after reset", name, row.phase)
					}
					newCursor, newBytes, ok := editor.TrackHistory()
					if !ok || newBytes != len(editor.String()) {
						t.Fatalf("%s/%s: new bootstrap = %d/%t, want %d/true", name, row.phase,
							newBytes, ok, len(editor.String()))
					}
					cursor = newCursor
					if len(row.callbacks) != 0 {
						t.Fatalf("%s/%s: reset row carries callbacks", name, row.phase)
					}
				} else {
					frames, latest, ok := editor.HistorySince(cursor)
					if !ok {
						t.Fatalf("%s/%s: HistorySince refused a retained cursor", name, row.phase)
					}
					if len(frames) != 1 {
						t.Fatalf("%s/%s: %d frames for one operation, want 1", name, row.phase, len(frames))
					}
					if frames[0].From != cursor {
						t.Fatalf("%s/%s: frame starts at %+v, want %+v", name, row.phase, frames[0].From, cursor)
					}
					replayCheckEvents(t, name, row, replayProducerEvents(frames))
					cursor = latest
				}
				replayAssertState(t, name, editor, row)
			}
			for phase := range ops {
				if !used[phase] {
					t.Fatalf("%s: adapter phase %q was never exercised", name, phase)
				}
			}
		})
	}
}

func TestHistoryUndoRedoPrimitiveFrames(t *testing.T) {
	editor := NewEditor("")
	editor.SetSavePoint()
	cursor, _ := historyMustTrack(t, editor)
	editor.Insert("a")
	editor.Insert("é")
	history := editor.historyAvailable()
	if history.current != 2 || history.retainedTo != 2 || history.bytes != 3 {
		t.Fatalf("forward state = %+v", history)
	}
	_, latest := historyMustRead(t, editor, cursor)

	if !editor.Undo() {
		t.Fatal("Undo refused")
	}
	frames, latest := historyMustRead(t, editor, latest)
	wantUndo := HistoryFrame{
		BeforeBytes: 3, AfterBytes: 0,
		Steps: []HistoryStep{
			{Operation: HistoryUndo, Primitive: HistoryDelete, Position: 1, Length: 2,
				BeforeBytes: 3, AfterBytes: 1,
				Context: HistoryContext{Current: 2, Save: 0, Detach: -1, RetainedFrom: 0, RetainedTo: 2, Collecting: true}},
			{Operation: HistoryUndo, Primitive: HistoryDelete, Position: 0, Length: 1,
				BeforeBytes: 1, AfterBytes: 0,
				Context: HistoryContext{Current: 1, Save: 0, Detach: -1, RetainedFrom: 0, RetainedTo: 2, Collecting: true}},
		},
	}
	if len(frames) != 1 {
		t.Fatalf("undo frames = %d, want 1", len(frames))
	}
	gotUndo := frames[0]
	gotUndo.From, gotUndo.To = HistoryCursor{}, HistoryCursor{}
	if !reflect.DeepEqual(gotUndo, wantUndo) {
		t.Fatalf("undo frame = %+v, want %+v", gotUndo, wantUndo)
	}
	if history.current != 0 || history.retainedTo != 2 {
		t.Fatalf("undo state current/retainedTo = %d/%d, want 0/2", history.current, history.retainedTo)
	}
	if editor.String() != "" {
		t.Fatalf("undo text = %q, want empty", editor.String())
	}

	if !editor.Redo() {
		t.Fatal("Redo refused")
	}
	frames, _ = historyMustRead(t, editor, latest)
	wantRedo := HistoryFrame{
		BeforeBytes: 0, AfterBytes: 3,
		Steps: []HistoryStep{
			{Operation: HistoryRedo, Primitive: HistoryInsert, Position: 0, Length: 1,
				BeforeBytes: 0, AfterBytes: 1,
				Context: HistoryContext{Current: 0, Save: 0, Detach: -1, RetainedFrom: 0, RetainedTo: 2, Collecting: true}},
			{Operation: HistoryRedo, Primitive: HistoryInsert, Position: 1, Length: 2,
				BeforeBytes: 1, AfterBytes: 3,
				Context: HistoryContext{Current: 1, Save: 0, Detach: -1, RetainedFrom: 0, RetainedTo: 2, Collecting: true}},
		},
	}
	if len(frames) != 1 {
		t.Fatalf("redo frames = %d, want 1", len(frames))
	}
	gotRedo := frames[0]
	gotRedo.From, gotRedo.To = HistoryCursor{}, HistoryCursor{}
	if !reflect.DeepEqual(gotRedo, wantRedo) {
		t.Fatalf("redo frame = %+v, want %+v", gotRedo, wantRedo)
	}
	if history.current != 2 || history.retainedTo != 2 {
		t.Fatalf("redo state current/retainedTo = %d/%d, want 2/2", history.current, history.retainedTo)
	}
	if editor.String() != "aé" {
		t.Fatalf("redo text = %q, want aé", editor.String())
	}
}

func TestHistoryBranchSaveDetach(t *testing.T) {
	editor := NewEditor("")
	editor.SetSavePoint()
	cursor, _ := historyMustTrack(t, editor)
	for _, value := range []string{"a", "b", "c", "d"} {
		editor.Insert(value)
		editor.BreakUndoGroup()
	}
	editor.SetSavePoint()
	history := editor.historyAvailable()
	if history.current != 4 || history.save != 4 || history.detach != -1 {
		t.Fatalf("saved state = %+v", history)
	}
	if !editor.Undo() || !editor.Undo() {
		t.Fatal("Undo refused")
	}
	editor.Insert("x")
	editor.BreakUndoGroup()
	editor.Insert("y")
	editor.BreakUndoGroup()
	if history.current != 4 || history.save != -1 || history.detach != 2 || history.retainedTo != 4 {
		t.Fatalf("branch state = %+v, want current=4 save=-1 detach=2 retainedTo=4", history)
	}
	if !editor.Undo() || !editor.Undo() || !editor.Undo() {
		t.Fatal("Undo refused")
	}
	if history.current != 1 || history.detach != 2 {
		t.Fatalf("pre-second-branch state = %+v, want current=1 detach=2", history)
	}
	editor.Insert("z")
	if history.current != 2 || history.save != -1 || history.detach != 1 || history.retainedTo != 2 {
		t.Fatalf("second branch state = %+v, want current=2 save=-1 detach=1 retainedTo=2", history)
	}
	frames, _ := historyMustRead(t, editor, cursor)
	last := frames[len(frames)-1]
	if len(last.Steps) != 1 || last.Steps[0].Context.Detach != 1 || last.Steps[0].Context.Save != -1 {
		t.Fatalf("branch step context = %+v", last.Steps)
	}
	editor.SetSavePoint()
	if history.current != 2 || history.save != 2 || history.detach != -1 {
		t.Fatalf("save after branch = %+v", history)
	}
	frames, _ = historyMustRead(t, editor, last.To)
	if len(frames) != 1 || len(frames[0].Steps) != 1 || frames[0].Steps[0].Context.Detach != -1 ||
		frames[0].Steps[0].Context.Save != 2 {
		t.Fatalf("save frame context = %+v", frames)
	}
}

func TestHistoryBranchKeepsReachableSaveWithoutDetach(t *testing.T) {
	editor := NewEditor("abc")
	editor.SetSavePoint()
	cursor, _ := historyMustTrack(t, editor)
	for _, value := range []string{"x", "y"} {
		editor.SetSelection(Range{Start: 0, End: 0})
		editor.Insert(value)
		editor.BreakUndoGroup()
	}
	if !editor.Undo() {
		t.Fatal("Undo refused")
	}
	editor.SetSelection(Range{Start: 0, End: 0})
	editor.Insert("z")
	history := editor.historyAvailable()
	if history.current != 2 || history.save != 0 || history.detach != -1 ||
		history.retainedFrom != 0 || history.retainedTo != 2 {
		t.Fatalf("reachable-save branch = current/save/detach/retained=%d/%d/%d/%d..%d, want 2/0/-1/0..2",
			history.current, history.save, history.detach, history.retainedFrom, history.retainedTo)
	}
	frames, latest := historyMustRead(t, editor, cursor)
	last := frames[len(frames)-1]
	step := last.Steps[len(last.Steps)-1]
	want := HistoryContext{Current: 2, Save: 0, Detach: -1, RetainedFrom: 0, RetainedTo: 2, Collecting: true}
	if step.Operation != HistoryForward || step.Primitive != HistoryInsert || step.Position != 0 ||
		step.Length != 1 || step.Context != want {
		t.Fatalf("reachable-save branch step = %+v, want context %+v", step, want)
	}
	if !editor.Undo() {
		t.Fatal("Undo after reachable-save branch refused")
	}
	undoFrames, undoLatest := historyMustRead(t, editor, latest)
	undoStep := undoFrames[0].Steps[0]
	wantUndo := HistoryContext{Current: 2, Save: 0, Detach: -1, RetainedFrom: 0, RetainedTo: 2, Collecting: true}
	if undoStep.Operation != HistoryUndo || undoStep.Primitive != HistoryDelete || undoStep.Position != 0 ||
		undoStep.Length != 1 || undoStep.Context != wantUndo {
		t.Fatalf("reachable-save Undo step = %+v, want context %+v", undoStep, wantUndo)
	}
	undoEvents := replayProducerEvents(undoFrames)
	if len(undoEvents) != 1 || undoEvents[0].kind != "undo-insert" || undoEvents[0].reverting {
		t.Fatalf("reachable-save Undo consumer events = %+v", undoEvents)
	}
	if !editor.Redo() {
		t.Fatal("Redo after reachable-save branch refused")
	}
	redoFrames, _ := historyMustRead(t, editor, undoLatest)
	redoStep := redoFrames[0].Steps[0]
	wantRedo := HistoryContext{Current: 1, Save: 0, Detach: -1, RetainedFrom: 0, RetainedTo: 2, Collecting: true}
	if redoStep.Operation != HistoryRedo || redoStep.Primitive != HistoryInsert || redoStep.Position != 0 ||
		redoStep.Length != 1 || redoStep.Context != wantRedo {
		t.Fatalf("reachable-save Redo step = %+v, want context %+v", redoStep, wantRedo)
	}
	redoEvents := replayProducerEvents(redoFrames)
	if len(redoEvents) != 2 || redoEvents[0].kind != "insert" || redoEvents[0].beforeSave ||
		redoEvents[1].kind != "end" {
		t.Fatalf("reachable-save Redo consumer events = %+v", redoEvents)
	}
}

func TestHistoryPostClearDetachMovesOnAppend(t *testing.T) {
	editor := NewEditor("abc")
	editor.SetSavePoint()
	historyMustTrack(t, editor)
	for _, value := range []string{"x", "y", "z"} {
		editor.SetSelection(Range{Start: 0, End: 0})
		editor.Insert(value)
		editor.BreakUndoGroup()
	}
	editor.SetSavePoint()
	if !editor.Undo() {
		t.Fatal("Undo refused")
	}
	editor.SetSelection(Range{Start: 0, End: 0})
	editor.Insert("q")
	history := editor.historyAvailable()
	if history.current != 3 || history.save != -1 || history.detach != 2 {
		t.Fatalf("pre-clear branch = current/save/detach=%d/%d/%d, want 3/-1/2",
			history.current, history.save, history.detach)
	}
	oldCursor := HistoryCursor{source: history.source, generation: history.generation, sequence: history.sequence}
	editor.ClearHistory()
	if frames, _, ok := editor.HistorySince(oldCursor); ok || frames != nil {
		t.Fatal("old cursor served frames after manual Clear")
	}
	cursor, bytes, ok := editor.TrackHistory()
	if !ok || bytes != len(editor.String()) {
		t.Fatalf("post-branch TrackHistory = %d/%t", bytes, ok)
	}
	history = editor.historyAvailable()
	if history.current != 0 || history.save != 0 || history.detach != 2 {
		t.Fatalf("post-clear state = current/save/detach=%d/%d/%d, want 0/0/2",
			history.current, history.save, history.detach)
	}
	editor.SetSelection(Range{Start: 0, End: 0})
	editor.Insert("r")
	if history.current != 1 || history.save != 0 || history.detach != 0 ||
		history.retainedFrom != 0 || history.retainedTo != 1 {
		t.Fatalf("post-clear append = current/save/detach/retained=%d/%d/%d/%d..%d, want 1/0/0/0..1",
			history.current, history.save, history.detach, history.retainedFrom, history.retainedTo)
	}
	frames, latest := historyMustRead(t, editor, cursor)
	if len(frames) != 1 || len(frames[0].Steps) != 1 {
		t.Fatalf("post-clear append frames = %+v", frames)
	}
	step := frames[0].Steps[0]
	want := HistoryContext{Current: 1, Save: 0, Detach: 0, RetainedFrom: 0, RetainedTo: 1, Collecting: true}
	if step.Operation != HistoryForward || step.Primitive != HistoryInsert || step.Position != 0 ||
		step.Length != 1 || step.Context != want {
		t.Fatalf("post-clear append step = %+v, want context %+v", step, want)
	}
	if !editor.Undo() {
		t.Fatal("Undo after post-clear append refused")
	}
	undoFrames, undoLatest := historyMustRead(t, editor, latest)
	undoStep := undoFrames[0].Steps[0]
	wantUndo := HistoryContext{Current: 1, Save: 0, Detach: 0, RetainedFrom: 0, RetainedTo: 1, Collecting: true}
	if undoStep.Operation != HistoryUndo || undoStep.Primitive != HistoryDelete || undoStep.Context != wantUndo {
		t.Fatalf("post-clear Undo step = %+v, want context %+v", undoStep, wantUndo)
	}
	if events := replayProducerEvents(undoFrames); len(events) != 1 || events[0].kind != "undo-insert" || events[0].reverting {
		t.Fatalf("post-clear Undo consumer events = %+v", events)
	}
	if !editor.Redo() {
		t.Fatal("Redo after post-clear append refused")
	}
	redoFrames, _ := historyMustRead(t, editor, undoLatest)
	redoStep := redoFrames[0].Steps[0]
	wantRedo := HistoryContext{Current: 0, Save: 0, Detach: 0, RetainedFrom: 0, RetainedTo: 1, Collecting: true}
	if redoStep.Operation != HistoryRedo || redoStep.Primitive != HistoryInsert || redoStep.Context != wantRedo {
		t.Fatalf("post-clear Redo step = %+v, want context %+v", redoStep, wantRedo)
	}
	redoEvents := replayProducerEvents(redoFrames)
	if len(redoEvents) != 2 || redoEvents[0].kind != "insert" || redoEvents[0].beforeSave ||
		redoEvents[1].kind != "end" {
		t.Fatalf("post-clear Redo consumer events = %+v", redoEvents)
	}
}

func TestHistoryManualClearRebootstrap(t *testing.T) {
	editor := NewEditor("abc")
	editor.SetSavePoint()
	cursor, _ := historyMustTrack(t, editor)
	editor.Insert("x")
	editor.SetSavePoint()
	if !editor.Undo() {
		t.Fatal("Undo refused")
	}
	editor.SetSelection(Range{Start: 0, End: 0})
	editor.Insert("z")
	history := editor.historyAvailable()
	if history.save != -1 || history.detach != 0 {
		t.Fatalf("branch state = %+v, want save=-1 detach=0", history)
	}
	oldGeneration, oldSequence := history.generation, history.sequence
	editor.ClearHistory()
	if frames, _, ok := editor.HistorySince(cursor); ok || frames != nil {
		t.Fatal("old cursor served frames after manual Clear")
	}
	history = editor.historyAvailable()
	if history == nil || !history.available {
		t.Fatal("manual Clear of a live context did not create a new generation")
	}
	if history.generation <= oldGeneration || history.sequence <= oldSequence {
		t.Fatalf("clear generation/sequence = %d/%d, want beyond %d/%d",
			history.generation, history.sequence, oldGeneration, oldSequence)
	}
	if history.current != 0 || history.save != 0 || history.detach != 0 ||
		history.retainedFrom != 0 || history.retainedTo != 0 || history.bytes != 4 {
		t.Fatalf("cleared producer state = %+v, want current=save=0 detach=0 bytes=4", history)
	}
	if len(history.frames) != 0 || history.cost != historyHeaderRunes {
		t.Fatalf("clear retained frames/cost = %d/%d", len(history.frames), history.cost)
	}
	cursor, bootstrapBytes, ok := editor.TrackHistory()
	if !ok || bootstrapBytes != 4 {
		t.Fatalf("TrackHistory after clear = %d/%t, want 4/true", bootstrapBytes, ok)
	}
	if history.epoch != cursor {
		t.Fatalf("TrackHistory cursor = %+v, want epoch %+v", cursor, history.epoch)
	}
	if frames, _, ok := editor.HistorySince(cursor); !ok || frames != nil {
		t.Fatalf("new bootstrap read = %+v/%t, want empty/true", frames, ok)
	}
	editor.SetSelection(Range{Start: 0, End: 0})
	editor.Insert("q")
	frames, _ := historyMustRead(t, editor, cursor)
	if len(frames) != 1 || len(frames[0].Steps) != 1 {
		t.Fatalf("post-clear frame = %+v", frames)
	}
	step := frames[0].Steps[0]
	if step.Context.Current != 1 || step.Context.Save != 0 || step.Context.Detach != 0 ||
		step.Context.RetainedTo != 1 || step.Context.RetainedFrom != 0 {
		t.Fatalf("post-clear step context = %+v", step.Context)
	}

	editor.ClearHistory()
	editor.ClearHistory()
	if history = editor.historyAvailable(); history == nil || history.generation <= oldGeneration {
		t.Fatal("repeated manual Clear did not advance the live generation")
	}
}

func TestHistoryUnavailableContextDoesNotReattachOnClear(t *testing.T) {
	editor := NewEditor("")
	historyMustTrack(t, editor)
	editor.invalidateHistory()
	before := editor.journal.history
	editor.ClearHistory()
	if editor.journal.history != before || before.available {
		t.Fatal("manual Clear reattached a previously unavailable producer")
	}
	if cursor, bytes, ok := editor.TrackHistory(); ok || cursor != (HistoryCursor{}) || bytes != 0 {
		t.Fatalf("TrackHistory after unavailable clear = %+v/%d/%t", cursor, bytes, ok)
	}
}

func TestHistoryAutomaticOversizedClearInvalidatesOnly(t *testing.T) {
	editor := NewEditor("")
	cursor, _ := historyMustTrack(t, editor)
	editor.Paste(strings.Repeat("x", maxSingleEditRunes+1))
	history := editor.journal.history
	if history.available {
		t.Fatal("automatic oversized-history Clear left the producer available")
	}
	if frames, _, ok := editor.HistorySince(cursor); ok || frames != nil {
		t.Fatal("automatic oversized-history Clear published frames")
	}
	if cursor, bytes, ok := editor.TrackHistory(); ok || cursor != (HistoryCursor{}) || bytes != 0 {
		t.Fatalf("automatic clear allowed a new bootstrap: %+v/%d/%t", cursor, bytes, ok)
	}
	editor.ClearHistory()
	if history.available {
		t.Fatal("manual Clear re-enabled a budget-invalidated producer")
	}
}

func TestHistoryUndoRedoAtomic2049(t *testing.T) {
	editor := NewEditor("abc")
	editor.SetSavePoint()
	bootstrap, _ := historyMustTrack(t, editor)
	const primitives = 2049
	if err := editor.Transact(func(draft *Editor) error {
		for range primitives {
			draft.SetSelection(Range{Start: 0, End: 0})
			draft.Insert("x")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	forward, afterForward := historyMustRead(t, editor, bootstrap)
	if len(forward) != 1 || len(forward[0].Steps) != primitives || forward[0].BeforeBytes != 3 || forward[0].AfterBytes != 3+primitives {
		t.Fatalf("forward atomic = %d frames/%d steps/%d->%d", len(forward), len(forward[0].Steps),
			forward[0].BeforeBytes, forward[0].AfterBytes)
	}
	for index, step := range forward[0].Steps {
		if step.Operation != HistoryForward || step.Primitive != HistoryInsert || step.Position != 0 ||
			step.Length != 1 || step.BeforeBytes != 3+index || step.AfterBytes != 4+index ||
			step.Context.Current != index+1 || step.Context.RetainedTo != index+1 {
			t.Fatalf("forward step %d = %+v", index, step)
		}
	}

	if !editor.Undo() {
		t.Fatal("Undo refused")
	}
	undoFrames, afterUndo := historyMustRead(t, editor, afterForward)
	if len(undoFrames) != 1 || len(undoFrames[0].Steps) != primitives {
		t.Fatalf("undo atomic = %d frames/%d steps", len(undoFrames), len(undoFrames[0].Steps))
	}
	for index, step := range undoFrames[0].Steps {
		if step.Operation != HistoryUndo || step.Primitive != HistoryDelete || step.Position != 0 ||
			step.Length != 1 || step.Context.Current != primitives-index || step.Context.RetainedTo != primitives {
			t.Fatalf("undo step %d = %+v", index, step)
		}
	}
	repeat, repeatLatest := historyMustRead(t, editor, afterForward)
	if repeatLatest != afterUndo || !reflect.DeepEqual(repeat, undoFrames) {
		t.Fatal("repeat read of the atomic undo frame changed")
	}
	repeat[0].Steps[0].Length = 99
	repeat[0].Steps = append(repeat[0].Steps, HistoryStep{})
	again, _ := historyMustRead(t, editor, afterForward)
	if !reflect.DeepEqual(again, undoFrames) {
		t.Fatal("returned undo frame was not owned by the caller")
	}
	if frames, _, ok := editor.HistorySince(afterForward); !ok || len(frames) != 1 {
		t.Fatal("retained undo frame disappeared")
	}

	if !editor.Redo() {
		t.Fatal("Redo refused")
	}
	redoFrames, _ := historyMustRead(t, editor, afterUndo)
	if len(redoFrames) != 1 || len(redoFrames[0].Steps) != primitives {
		t.Fatalf("redo atomic = %d frames/%d steps", len(redoFrames), len(redoFrames[0].Steps))
	}
	for index, step := range redoFrames[0].Steps {
		if step.Operation != HistoryRedo || step.Primitive != HistoryInsert || step.Position != 0 ||
			step.Length != 1 || step.Context.Current != index || step.Context.RetainedTo != primitives {
			t.Fatalf("redo step %d = %+v", index, step)
		}
	}
	again, _ = historyMustRead(t, editor, afterUndo)
	if !reflect.DeepEqual(again, redoFrames) {
		t.Fatal("repeat read of the atomic redo frame changed")
	}
	for range historyFrameLimit + 2 {
		editor.SetSavePoint()
	}
	if frames, _, ok := editor.HistorySince(bootstrap); ok || frames != nil {
		t.Fatal("expired prefix was bridged instead of refused")
	}
}

func replayUnitRuneTotal(editor *Editor) int {
	total := 0
	for index := range editor.history {
		total += editor.historyUnitRunes(&editor.history[index])
	}
	return total
}

func TestHistoryAccountingBalanceAcrossLifecycle(t *testing.T) {
	editor := NewEditor("")
	historyMustTrack(t, editor)
	for range 6 {
		editor.Insert("x")
	}
	if got, want := editor.historyRunes, replayUnitRuneTotal(editor); got != want {
		t.Fatalf("coalesced historyRunes = %d, want %d", got, want)
	}
	unit := &editor.history[0]
	if got, want := editor.historyUnitRunes(unit), len(unit.removed)+len(unit.inserted)+cap(unit.primitives)*historyPrimitiveRunes; got != want {
		t.Fatalf("coalesced unit charge = %d, want %d", got, want)
	}
	if err := editor.ReplaceRanges([]Replacement{
		{Range: Range{Start: 0, End: 1}, Text: "é"},
		{Range: Range{Start: 2, End: 3}, Text: "猫a"},
	}); err != nil {
		t.Fatal(err)
	}
	batch := &editor.history[len(editor.history)-1]
	if got, want := editor.historyUnitRunes(batch), len(batch.removed)+len(batch.inserted)+historyPrimitiveRunes*len(batch.primitives); got != want {
		t.Fatalf("batch unit charge = %d, want len-based %d", got, want)
	}
	if got, want := editor.historyRunes, replayUnitRuneTotal(editor); got != want {
		t.Fatalf("historyRunes before branch = %d, want %d", got, want)
	}
	editor.SetSavePoint()
	if !editor.Undo() {
		t.Fatal("Undo refused")
	}
	editor.SetSelection(Range{Start: 0, End: 0})
	editor.Insert("z")
	if got, want := editor.historyRunes, replayUnitRuneTotal(editor); got != want {
		t.Fatalf("historyRunes after branch/discarded redo = %d, want %d", got, want)
	}
	if editor.historyRunes < 0 {
		t.Fatalf("historyRunes went negative after discarded redo: %d", editor.historyRunes)
	}
	editor.ClearHistory()
	if editor.historyRunes != 0 || len(editor.history) != 0 {
		t.Fatalf("manual Clear retained units/runes: %d/%d", len(editor.history), editor.historyRunes)
	}
	history := editor.journal.history
	if history.cost != historyHeaderRunes || len(history.frames) != 0 || history.frameCapacity != 0 {
		t.Fatalf("manual Clear retained producer cost: %+v", history)
	}
}

func TestHistoryReplayPreservesExistingByteAndLineJournals(t *testing.T) {
	scenarios := []struct {
		name    string
		initial string
		apply   func(*testing.T, *Editor)
	}{
		{"coalesced", "a\nbcd", func(t *testing.T, editor *Editor) {
			editor.SetSelection(Range{Start: 5, End: 5})
			editor.Insert("x")
			editor.Insert("y")
		}},
		{"selected-replacement", "a\nbc\nd", func(t *testing.T, editor *Editor) {
			editor.SetSelection(Range{Start: 2, End: 4})
			editor.Insert("Q\n")
		}},
		{"paste", "a\nb", func(t *testing.T, editor *Editor) {
			editor.SetSelection(Range{Start: 2, End: 2})
			editor.Paste("x\ny\n")
		}},
		{"range-batch", "a\nbcd\ne", func(t *testing.T, editor *Editor) {
			if err := editor.ReplaceRanges([]Replacement{
				{Range: Range{Start: 1, End: 2}, Text: "X\n"},
				{Range: Range{Start: 5, End: 6}, Text: "Y"},
			}); err != nil {
				t.Fatal(err)
			}
		}},
		{"transaction-multiple-steps", "a\nb", func(t *testing.T, editor *Editor) {
			if err := editor.Transact(func(draft *Editor) error {
				draft.SetSelection(Range{Start: 2, End: 2})
				draft.Insert("x\ny")
				draft.SetSelection(Range{Start: 0, End: 1})
				if !draft.DeleteBackward() {
					t.Fatal("draft DeleteBackward refused")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			plain := NewEditor(scenario.initial)
			plainCursor := plain.TrackLineEdits()
			scenario.apply(t, plain)
			plainForward, plainUndoCursor, ok := plain.LineEditsSince(plainCursor, nil)
			if !ok {
				t.Fatal("plain line journal is incomplete")
			}
			plainForwardBytes, _, ok := plain.EditsSince(plainCursor, nil)
			if !ok {
				t.Fatal("plain byte journal is incomplete")
			}

			tracked := NewEditor(scenario.initial)
			tracked.TrackHistory()
			trackedCursor := tracked.TrackLineEdits()
			scenario.apply(t, tracked)
			trackedForward, trackedUndoCursor, ok := tracked.LineEditsSince(trackedCursor, nil)
			if !ok {
				t.Fatal("tracked line journal is incomplete")
			}
			trackedForwardBytes, _, ok := tracked.EditsSince(trackedCursor, nil)
			if !ok {
				t.Fatal("tracked byte journal is incomplete")
			}
			if !slices.Equal(trackedForwardBytes, plainForwardBytes) || !slices.Equal(trackedForward, plainForward) {
				t.Fatalf("forward journals changed with TrackHistory:\nbytes %+v, want %+v\nlines %+v, want %+v",
					trackedForwardBytes, plainForwardBytes, trackedForward, plainForward)
			}
			if !plain.Undo() || !tracked.Undo() {
				t.Fatal("Undo refused")
			}
			plainBackward, plainRedoCursor, ok := plain.LineEditsSince(plainUndoCursor, nil)
			if !ok {
				t.Fatal("plain Undo line journal is incomplete")
			}
			trackedBackward, trackedRedoCursor, ok := tracked.LineEditsSince(trackedUndoCursor, nil)
			if !ok {
				t.Fatal("tracked Undo line journal is incomplete")
			}
			plainBackwardBytes, _, _ := plain.EditsSince(plainUndoCursor, nil)
			trackedBackwardBytes, _, _ := tracked.EditsSince(trackedUndoCursor, nil)
			if !slices.Equal(trackedBackwardBytes, plainBackwardBytes) || !slices.Equal(trackedBackward, plainBackward) {
				t.Fatalf("Undo journals changed with TrackHistory:\nbytes %+v, want %+v\nlines %+v, want %+v",
					trackedBackwardBytes, plainBackwardBytes, trackedBackward, plainBackward)
			}
			if plain.String() != tracked.String() {
				t.Fatalf("Undo text = %q, want %q", tracked.String(), plain.String())
			}
			if !plain.Redo() || !tracked.Redo() {
				t.Fatal("Redo refused")
			}
			plainRedoLines, _, ok := plain.LineEditsSince(plainRedoCursor, nil)
			if !ok {
				t.Fatal("plain Redo line journal is incomplete")
			}
			trackedRedoLines, _, ok := tracked.LineEditsSince(trackedRedoCursor, nil)
			if !ok {
				t.Fatal("tracked Redo line journal is incomplete")
			}
			plainRedoBytes, _, _ := plain.EditsSince(plainRedoCursor, nil)
			trackedRedoBytes, _, _ := tracked.EditsSince(trackedRedoCursor, nil)
			if !slices.Equal(trackedRedoBytes, plainRedoBytes) || !slices.Equal(trackedRedoLines, plainRedoLines) {
				t.Fatalf("Redo journals changed with TrackHistory:\nbytes %+v, want %+v\nlines %+v, want %+v",
					trackedRedoBytes, plainRedoBytes, trackedRedoLines, plainRedoLines)
			}
		})
	}
}

func TestHistoryEvictsDeliveryFramesBeforePruningUndo(t *testing.T) {
	editor := NewEditor("")
	editor.SetSavePoint()
	bootstrap, _ := historyMustTrack(t, editor)
	const transactions = 20
	const primitivesPerTransaction = 1000
	for range transactions {
		if err := editor.Transact(func(draft *Editor) error {
			for range primitivesPerTransaction / 2 {
				draft.Insert("x")
			}
			for range primitivesPerTransaction / 2 {
				if !draft.DeleteBackward() {
					t.Fatal("draft DeleteBackward refused")
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	history := editor.historyAvailable()
	if history == nil {
		t.Fatal("producer was invalidated by delivery-frame pressure")
	}
	if len(editor.history) != transactions {
		t.Fatalf("actual Undo history = %d units, want %d", len(editor.history), transactions)
	}
	if len(history.frames) >= transactions {
		t.Fatalf("delivery frames = %d, want older frames evicted", len(history.frames))
	}
	if frames, _, ok := editor.HistorySince(bootstrap); ok || frames != nil {
		t.Fatal("expired delivery prefix was bridged")
	}
	latest := history.epoch
	latest.sequence = history.sequence
	frames, _, ok := editor.HistorySince(latest)
	if !ok || frames != nil {
		t.Fatal("current consumer could not read an empty retained window")
	}
	if editor.CanUndo() != true || len(editor.history[0].primitives) != primitivesPerTransaction {
		t.Fatal("delivery-frame eviction pruned or corrupted actual Undo primitives")
	}
}
