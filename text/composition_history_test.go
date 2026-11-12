package text

import (
	"bufio"
	"os"
	"strings"
	"testing"
)

type compositionSourceSample struct {
	text    string
	current int
	save    int
	detach  int
}

func compositionSourceSamples(t *testing.T) map[string]compositionSourceSample {
	t.Helper()
	file, err := os.Open("testdata/composition-source-reference.txt")
	if err != nil {
		t.Fatalf("open pinned source reference: %v", err)
	}
	defer file.Close()
	samples := make(map[string]compositionSourceSample)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		space := strings.IndexByte(line, ' ')
		if space <= 0 {
			continue
		}
		name := line[:space]
		sample := compositionSourceSample{}
		for _, field := range strings.Fields(line[space+1:]) {
			key, value, ok := strings.Cut(field, "=")
			if !ok {
				continue
			}
			switch key {
			case "text":
				sample.text = value
			case "current":
				sample.current = compositionAtoi(t, value)
			case "save":
				sample.save = compositionAtoi(t, value)
			case "detach":
				sample.detach = compositionAtoi(t, value)
			}
		}
		samples[name] = sample
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read pinned source reference: %v", err)
	}
	if len(samples) < 25 {
		t.Fatalf("pinned source reference parsed only %d samples", len(samples))
	}
	return samples
}

func compositionAtoi(t *testing.T, value string) int {
	t.Helper()
	sign := 1
	if strings.HasPrefix(value, "-") {
		sign = -1
		value = value[1:]
	}
	number := 0
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			t.Fatalf("invalid source integer %q", value)
		}
		number = number*10 + int(digit-'0')
	}
	return sign * number
}

func compositionAssertSource(t *testing.T, editor *Editor, samples map[string]compositionSourceSample, name string) {
	t.Helper()
	want := samples[name]
	if got := editor.String(); got != want.text {
		t.Fatalf("%s text = %q, want pinned-source %q", name, got, want.text)
	}
	history := editor.journal.history
	if history == nil || !history.available {
		t.Fatalf("%s: producer unavailable, want source current/save/detach %d/%d/%d",
			name, want.current, want.save, want.detach)
	}
	if history.current != want.current || history.save != want.save || history.detach != want.detach {
		t.Fatalf("%s producer context = current/save/detach %d/%d/%d, want pinned-source %d/%d/%d",
			name, history.current, history.save, history.detach, want.current, want.save, want.detach)
	}
}

func compositionHistoryFrames(t *testing.T, editor *Editor, from HistoryCursor) ([]HistoryFrame, HistoryCursor) {
	t.Helper()
	frames, latest, ok := editor.HistorySince(from)
	if !ok {
		t.Fatalf("HistorySince(%+v) refused", from)
	}
	return frames, latest
}

func TestCompositionHistoryScalarOrderAndBytes(t *testing.T) {
	editor := NewEditor("abc")
	cursor, _, ok := editor.TrackHistory()
	if !ok {
		t.Fatal("TrackHistory refused")
	}
	if !editor.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	editor.SetSelection(Range{Start: 1, End: 2})
	compositionApply(t, editor, compositionUpdate("é𝄞", Range{}))

	frames, latest := compositionHistoryFrames(t, editor, cursor)
	if len(frames) != 1 {
		t.Fatalf("scalar update produced %d frames, want 1", len(frames))
	}
	frame := frames[0]
	if frame.BeforeBytes != 3 || frame.AfterBytes != 8 {
		t.Fatalf("frame bytes = %d->%d, want 3->8", frame.BeforeBytes, frame.AfterBytes)
	}
	if len(frame.Steps) != 3 {
		t.Fatalf("frame steps = %d, want delete plus two scalars", len(frame.Steps))
	}
	want := []HistoryStep{
		{Operation: HistoryForward, Primitive: HistoryDelete, Position: 1, Length: 1, BeforeBytes: 3, AfterBytes: 2,
			Context: HistoryContext{Current: 1, Save: 0, Detach: -1, RetainedFrom: 0, RetainedTo: 1, Collecting: true}},
		{Operation: HistoryForward, Primitive: HistoryInsert, Position: 1, Length: 2, BeforeBytes: 2, AfterBytes: 4,
			Context: HistoryContext{Current: 2, Save: 0, Detach: -1, RetainedFrom: 0, RetainedTo: 2, Collecting: true}},
		{Operation: HistoryForward, Primitive: HistoryInsert, Position: 3, Length: 4, BeforeBytes: 4, AfterBytes: 8,
			Context: HistoryContext{Current: 3, Save: 0, Detach: -1, RetainedFrom: 0, RetainedTo: 3, Collecting: true}},
	}
	for index := range want {
		if frame.Steps[index] != want[index] {
			t.Fatalf("step %d = %+v, want %+v", index, frame.Steps[index], want[index])
		}
	}
	if latest.sequence != cursor.sequence+1 {
		t.Fatalf("latest sequence = %d, want one event increment", latest.sequence)
	}
}

func TestCompositionHistoryMixedRollbackForward(t *testing.T) {
	editor := NewEditor("abc")
	cursor, _, ok := editor.TrackHistory()
	if !ok {
		t.Fatal("TrackHistory refused")
	}
	if !editor.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	editor.SetSelection(Range{Start: 1, End: 2})
	compositionApply(t, editor, compositionUpdate("é𝄞", Range{}))
	_, firstLatest := compositionHistoryFrames(t, editor, cursor)

	compositionApply(t, editor, compositionUpdate("x", Range{}))
	frames, latest := compositionHistoryFrames(t, editor, firstLatest)
	if len(frames) != 1 {
		t.Fatalf("mixed update produced %d frames, want 1", len(frames))
	}
	frame := frames[0]
	if frame.BeforeBytes != 8 || frame.AfterBytes != 3 {
		t.Fatalf("mixed frame bytes = %d->%d, want 8->3", frame.BeforeBytes, frame.AfterBytes)
	}
	wantOps := []HistoryOperation{HistoryUndo, HistoryUndo, HistoryForward}
	wantPrim := []HistoryPrimitive{HistoryDelete, HistoryDelete, HistoryInsert}
	wantPos := []int{3, 1, 1}
	wantLen := []int{4, 2, 1}
	wantBefore := []int{8, 4, 2}
	wantAfter := []int{4, 2, 3}
	wantCurrent := []int{3, 2, 2}
	if len(frame.Steps) != len(wantOps) {
		t.Fatalf("mixed steps = %d, want %d", len(frame.Steps), len(wantOps))
	}
	for index, step := range frame.Steps {
		if step.Operation != wantOps[index] || step.Primitive != wantPrim[index] ||
			step.Position != wantPos[index] || step.Length != wantLen[index] ||
			step.BeforeBytes != wantBefore[index] || step.AfterBytes != wantAfter[index] ||
			step.Context.Current != wantCurrent[index] {
			t.Fatalf("mixed step %d = %+v, want op %d prim %d pos %d len %d bytes %d->%d current %d",
				index, step, wantOps[index], wantPrim[index], wantPos[index], wantLen[index],
				wantBefore[index], wantAfter[index], wantCurrent[index])
		}
	}
	if latest.sequence != firstLatest.sequence+1 {
		t.Fatalf("mixed latest sequence = %d, want %d", latest.sequence, firstLatest.sequence+1)
	}
	if got := editor.String(); got != "axc" {
		t.Fatalf("mixed text = %q, want axc", got)
	}
}

func TestCompositionHistoryCancelFrameAndSaveContext(t *testing.T) {
	editor := NewEditor("abc")
	cursor, _, ok := editor.TrackHistory()
	if !ok {
		t.Fatal("TrackHistory refused")
	}
	if !editor.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	compositionApply(t, editor, compositionUpdate("x", Range{}))
	_, latest := compositionHistoryFrames(t, editor, cursor)
	editor.SetSavePoint()
	_, saved := compositionHistoryFrames(t, editor, latest)

	compositionApply(t, editor, CompositionEvent{Phase: CompositionCancel})
	frames, cancelled := compositionHistoryFrames(t, editor, saved)
	if len(frames) != 1 || len(frames[0].Steps) != 1 {
		t.Fatalf("cancel frame = %+v, want one inverse step", frames)
	}
	step := frames[0].Steps[0]
	if step.Operation != HistoryUndo || step.Primitive != HistoryDelete ||
		step.Position != 3 || step.Length != 1 || step.Context.Save != 1 || step.Context.Current != 1 {
		t.Fatalf("cancel step = %+v, want Undo/Delete at 3 len 1 current/save 1/1", step)
	}
	history := editor.journal.history
	if history.save != 1 || history.current != 0 || history.detach != -1 {
		t.Fatalf("after cancel context = save/current/detach %d/%d/%d, want 1/0/-1",
			history.save, history.current, history.detach)
	}
	if cancelled.sequence != saved.sequence+1 {
		t.Fatalf("cancel sequence = %d, want %d", cancelled.sequence, saved.sequence+1)
	}

	compositionApply(t, editor, compositionUpdate("y", Range{}))
	if history.save != -1 || history.detach != 0 || history.current != 1 {
		t.Fatalf("after next append context = save/current/detach %d/%d/%d, want -1/1/0",
			history.save, history.current, history.detach)
	}
}

func TestCompositionHistoryCommitAndTruncationHaveNoFrame(t *testing.T) {
	editor := NewEditor("abc")
	cursor, _, ok := editor.TrackHistory()
	if !ok {
		t.Fatal("TrackHistory refused")
	}
	if !editor.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	compositionApply(t, editor, compositionUpdate("x", Range{}))
	_, latest := compositionHistoryFrames(t, editor, cursor)
	compositionApply(t, editor, CompositionEvent{Phase: CompositionCommit})
	frames, afterCommit := compositionHistoryFrames(t, editor, latest)
	if len(frames) != 0 || afterCommit != latest {
		t.Fatalf("commit published %+v / %+v, want no frame and unchanged cursor", frames, afterCommit)
	}

	compositionApply(t, editor, CompositionEvent{Phase: CompositionCancel})
	_, afterCancel := compositionHistoryFrames(t, editor, afterCommit)
	compositionApply(t, editor, CompositionEvent{Phase: CompositionEmptyCommit})
	frames, afterEmpty := compositionHistoryFrames(t, editor, afterCancel)
	if len(frames) != 0 || afterEmpty != afterCancel {
		t.Fatalf("byte-less empty commit published %+v / %+v, want no frame", frames, afterEmpty)
	}
}

func TestCompositionHistoryOversizedCompleteFrame(t *testing.T) {
	editor := NewEditor("")
	cursor, _, ok := editor.TrackHistory()
	if !ok {
		t.Fatal("TrackHistory refused")
	}
	if !editor.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	const count = 2049
	compositionApply(t, editor, compositionUpdate(strings.Repeat("x", count), Range{}))
	frames, latest := compositionHistoryFrames(t, editor, cursor)
	if len(frames) != 1 {
		t.Fatalf("oversized event produced %d frames, want exactly 1", len(frames))
	}
	if len(frames[0].Steps) != count {
		t.Fatalf("oversized frame steps = %d, want %d", len(frames[0].Steps), count)
	}
	if frames[0].BeforeBytes != 0 || frames[0].AfterBytes != count {
		t.Fatalf("oversized frame bytes = %d->%d, want 0->%d", frames[0].BeforeBytes, frames[0].AfterBytes, count)
	}
	if last := frames[0].Steps[count-1]; last.Context.Current != count || last.Position != count-1 {
		t.Fatalf("oversized last step = %+v, want current %d position %d", last, count, count-1)
	}
	if latest.sequence != cursor.sequence+1 {
		t.Fatalf("oversized sequence = %d, want one increment", latest.sequence)
	}
	// A read at the start still returns the complete action, not a suffix.
	again, _, ok := editor.HistorySince(cursor)
	if !ok || len(again) != 1 || len(again[0].Steps) != count {
		t.Fatalf("repeat oversized read = %d frames/%d steps", len(again), len(again[0].Steps))
	}
}

func TestCompositionHistoryMetadataRefusalKeepsPhysicalWork(t *testing.T) {
	editor := NewEditor("abc")
	cursor, _, ok := editor.TrackHistory()
	if !ok {
		t.Fatal("TrackHistory refused")
	}
	if !editor.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	editor.Insert("d")
	history := editor.journal.history
	history.cost = maxSingleEditRunes // force the event reservation over the single-action bound

	compositionApply(t, editor, compositionUpdate("x", Range{}))
	if got := editor.String(); got != "abcdx" {
		t.Fatalf("metadata refusal text = %q, want abcdx", got)
	}
	if history.available {
		t.Fatal("failed metadata reservation left the producer available")
	}
	if frames, _, ok := editor.HistorySince(cursor); ok || frames != nil {
		t.Fatal("failed metadata reservation served a partial frame")
	}
	compositionApply(t, editor, CompositionEvent{Phase: CompositionCancel})
	if got := editor.String(); got != "abcd" {
		t.Fatalf("cancel after metadata refusal = %q, want abcd", got)
	}
	if !editor.Undo() || editor.String() != "abc" {
		t.Fatalf("ordinary Undo after metadata refusal = %q, want abc", editor.String())
	}
}

func TestCompositionHistoryPinnedSourceFlows(t *testing.T) {
	samples := compositionSourceSamples(t)

	t.Run("ascii selected replacement", func(t *testing.T) {
		editor := NewEditor("abc")
		if _, _, ok := editor.TrackHistory(); !ok {
			t.Fatal("TrackHistory refused")
		}
		if !editor.EnableTentativeComposition() {
			t.Fatal("enable refused")
		}
		editor.SetSelection(Range{Start: 1, End: 2})
		compositionApply(t, editor, compositionUpdate("x", Range{}))
		compositionAssertSource(t, editor, samples, "ascii-pre")
		compositionApply(t, editor, CompositionEvent{Phase: CompositionCancel})
		compositionAssertSource(t, editor, samples, "ascii-rollback")
		compositionApply(t, editor, CompositionEvent{
			Phase: CompositionResult, Text: "r",
			HasReplacement: true, Replacement: Range{Start: 0, End: 1},
		})
		compositionAssertSource(t, editor, samples, "ascii-replacement-result")
		for index, name := range []string{"ascii-replacement-undo1", "ascii-replacement-undo2", "ascii-replacement-undo3"} {
			if !editor.Undo() {
				t.Fatalf("Undo %d refused", index+1)
			}
			compositionAssertSource(t, editor, samples, name)
		}
	})

	t.Run("unicode selected replacement", func(t *testing.T) {
		editor := NewEditor("aé𝄞b")
		if _, _, ok := editor.TrackHistory(); !ok {
			t.Fatal("TrackHistory refused")
		}
		if !editor.EnableTentativeComposition() {
			t.Fatal("enable refused")
		}
		editor.SetSelection(Range{Start: 1, End: 3})
		compositionApply(t, editor, compositionUpdate("x", Range{}))
		compositionAssertSource(t, editor, samples, "unicode-pre")
		compositionApply(t, editor, CompositionEvent{Phase: CompositionCancel})
		compositionAssertSource(t, editor, samples, "unicode-rollback")
		compositionApply(t, editor, CompositionEvent{
			Phase: CompositionResult, Text: "r",
			HasReplacement: true, Replacement: Range{Start: 0, End: 1},
		})
		compositionAssertSource(t, editor, samples, "unicode-replacement-result")
	})

	t.Run("raw UTF16 after rollback", func(t *testing.T) {
		editor := NewEditor("abcd")
		if _, _, ok := editor.TrackHistory(); !ok {
			t.Fatal("TrackHistory refused")
		}
		if !editor.EnableTentativeComposition() {
			t.Fatal("enable refused")
		}
		editor.SetSelection(Range{Start: 1, End: 2})
		compositionApply(t, editor, compositionUpdate("é𝄞", Range{}))
		compositionAssertSource(t, editor, samples, "preedit-bmp-astral-visible")
		compositionApply(t, editor, CompositionEvent{
			Phase: CompositionResult, Text: "r",
			HasReplacementUTF16: true, ReplacementUTF16: UTF16Range{Start: 5, Length: 1},
		})
		compositionAssertSource(t, editor, samples, "preedit-bmp-astral-result")
	})

	t.Run("result without preedit", func(t *testing.T) {
		editor := NewEditor("abc")
		if _, _, ok := editor.TrackHistory(); !ok {
			t.Fatal("TrackHistory refused")
		}
		if !editor.EnableTentativeComposition() {
			t.Fatal("enable refused")
		}
		compositionApply(t, editor, compositionUpdate("x", Range{}))
		compositionAssertSource(t, editor, samples, "no-selection-preedit")
		compositionApply(t, editor, CompositionEvent{Phase: CompositionCancel})
		compositionAssertSource(t, editor, samples, "no-selection-rollback")
		compositionApply(t, editor, CompositionEvent{Phase: CompositionResult, Text: "r"})
		compositionAssertSource(t, editor, samples, "no-selection-result")
	})

	t.Run("mixed result and preedit caret", func(t *testing.T) {
		editor := NewEditor("abc")
		if _, _, ok := editor.TrackHistory(); !ok {
			t.Fatal("TrackHistory refused")
		}
		if !editor.EnableTentativeComposition() {
			t.Fatal("enable refused")
		}
		editor.SetSelection(Range{Start: 1, End: 2})
		compositionApply(t, editor, CompositionEvent{Phase: CompositionResult, Text: "r"})
		compositionAssertSource(t, editor, samples, "mixed-after-result-r")
		compositionApply(t, editor, compositionUpdate("x", Range{}))
		compositionAssertSource(t, editor, samples, "mixed-after-preedit-x")
		compositionApply(t, editor, CompositionEvent{Phase: CompositionResult, Text: "y"})
		compositionAssertSource(t, editor, samples, "mixed-after-result-y")
	})

	t.Run("save during tentative and rollback", func(t *testing.T) {
		editor := NewEditor("abc")
		if _, _, ok := editor.TrackHistory(); !ok {
			t.Fatal("TrackHistory refused")
		}
		if !editor.EnableTentativeComposition() {
			t.Fatal("enable refused")
		}
		compositionApply(t, editor, compositionUpdate("x", Range{}))
		editor.SetSavePoint()
		compositionAssertSource(t, editor, samples, "save-in-rollback-before")
		compositionApply(t, editor, CompositionEvent{Phase: CompositionCancel})
		compositionAssertSource(t, editor, samples, "save-in-rollback-after")
		compositionApply(t, editor, compositionUpdate("y", Range{}))
		compositionAssertSource(t, editor, samples, "save-in-rollback-next-append")
	})

	t.Run("save during tentative and commit", func(t *testing.T) {
		editor := NewEditor("abc")
		if _, _, ok := editor.TrackHistory(); !ok {
			t.Fatal("TrackHistory refused")
		}
		if !editor.EnableTentativeComposition() {
			t.Fatal("enable refused")
		}
		compositionApply(t, editor, compositionUpdate("x", Range{}))
		editor.SetSavePoint()
		compositionAssertSource(t, editor, samples, "save-in-tentative")
		compositionApply(t, editor, CompositionEvent{Phase: CompositionCommit})
		compositionAssertSource(t, editor, samples, "save-in-tentative-commit")
	})

	t.Run("active clear keeps visible text", func(t *testing.T) {
		editor := NewEditor("abc")
		if _, _, ok := editor.TrackHistory(); !ok {
			t.Fatal("TrackHistory refused")
		}
		if !editor.EnableTentativeComposition() {
			t.Fatal("enable refused")
		}
		compositionApply(t, editor, compositionUpdate("x", Range{}))
		source := samples["clear-tentative-before"]
		if got := editor.String(); got != source.text {
			t.Fatalf("clear-tentative-before text = %q, want %q", got, source.text)
		}
		editor.ClearHistory()
		if got := editor.String(); got != samples["clear-tentative-after"].text {
			t.Fatalf("clear-tentative-after text = %q, want %q", got, samples["clear-tentative-after"].text)
		}
		if editor.journal.history != nil && editor.journal.history.available {
			t.Fatal("active Clear left producer metadata available")
		}
	})
}

func TestCompositionHistoryMultiScalarCancelFrame(t *testing.T) {
	editor := NewEditor("abc")
	cursor, _, ok := editor.TrackHistory()
	if !ok {
		t.Fatal("TrackHistory refused")
	}
	if !editor.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	editor.SetSelection(Range{Start: 1, End: 2})
	compositionApply(t, editor, compositionUpdate("é𝄞", Range{}))
	_, latest := compositionHistoryFrames(t, editor, cursor)
	compositionApply(t, editor, CompositionEvent{Phase: CompositionCancel})
	frames, _ := compositionHistoryFrames(t, editor, latest)
	if len(frames) != 1 || len(frames[0].Steps) != 2 {
		t.Fatalf("multi-scalar cancel frame = %+v, want two inverse steps", frames)
	}
	wantPositions := []int{3, 1}
	wantLengths := []int{4, 2}
	wantCurrent := []int{3, 2}
	for index, step := range frames[0].Steps {
		if step.Operation != HistoryUndo || step.Primitive != HistoryDelete ||
			step.Position != wantPositions[index] || step.Length != wantLengths[index] ||
			step.Context.Current != wantCurrent[index] {
			t.Fatalf("cancel step %d = %+v, want Undo/Delete pos %d len %d current %d",
				index, step, wantPositions[index], wantLengths[index], wantCurrent[index])
		}
	}
	if got := editor.String(); got != "ac" {
		t.Fatalf("multi-scalar cancel text = %q, want ac", got)
	}
	if history := editor.journal.history; history.current != 1 || history.retainedTo != 1 {
		t.Fatalf("cancel current/retainedTo = %d/%d, want 1/1", history.current, history.retainedTo)
	}
}

func TestCompositionHistoryEmptyCommitActiveFrame(t *testing.T) {
	editor := NewEditor("abc")
	cursor, _, ok := editor.TrackHistory()
	if !ok {
		t.Fatal("TrackHistory refused")
	}
	if !editor.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	editor.SetSelection(Range{Start: 1, End: 2})
	compositionApply(t, editor, compositionUpdate("x", Range{}))
	_, latest := compositionHistoryFrames(t, editor, cursor)
	compositionApply(t, editor, CompositionEvent{Phase: CompositionEmptyCommit})
	frames, emptyCommit := compositionHistoryFrames(t, editor, latest)
	if len(frames) != 1 || len(frames[0].Steps) != 1 {
		t.Fatalf("active empty commit frame = %+v, want one inverse step", frames)
	}
	if step := frames[0].Steps[0]; step.Operation != HistoryUndo || step.Context.Current != 2 {
		t.Fatalf("active empty commit step = %+v, want Undo at current 2", step)
	}
	if emptyCommit.sequence != latest.sequence+1 {
		t.Fatalf("active empty commit sequence = %d, want %d", emptyCommit.sequence, latest.sequence+1)
	}
	if got := editor.String(); got != "ac" {
		t.Fatalf("active empty commit text = %q, want ac", got)
	}
}

func TestCompositionTrimRepairsTentativeUnitIndex(t *testing.T) {
	editor := NewEditor("")
	if _, _, ok := editor.TrackHistory(); !ok {
		t.Fatal("TrackHistory refused")
	}
	if !editor.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	// One real ordinary unit larger than the retained-history budget but below
	// the single-action limit. Starting a tentative makes the prefix trim real.
	if err := editor.ReplaceRanges([]Replacement{{Range: Range{Start: 0, End: 0}, Text: strings.Repeat("a", maxHistoryRunes+1)}}); err != nil {
		t.Fatal(err)
	}
	if len(editor.history) != 1 || editor.historyRunes <= maxHistoryRunes {
		t.Fatalf("ordinary setup units=%d runes=%d", len(editor.history), editor.historyRunes)
	}
	compositionApply(t, editor, compositionUpdate("x", Range{}))
	state := editor.compositionState()
	if state == nil || !state.active || state.unitStart != 0 {
		t.Fatalf("tentative state = %+v, want repaired unitStart 0", state)
	}
	if len(editor.history) != 1 {
		t.Fatalf("trim retained %d units, want the tentative unit only", len(editor.history))
	}
	if got, want := editor.historyRunes, replayUnitRuneTotal(editor); got != want {
		t.Fatalf("trim historyRunes=%d, want exact unit total %d", got, want)
	}
	compositionApply(t, editor, CompositionEvent{Phase: CompositionCancel})
	if got := editor.String(); got != strings.Repeat("a", maxHistoryRunes+1) {
		t.Fatalf("cancel after prefix trim text length = %d", len([]rune(got)))
	}
	if got, want := editor.historyRunes, replayUnitRuneTotal(editor); got != want {
		t.Fatalf("post-trim cancel historyRunes=%d, want exact unit total %d", got, want)
	}
}

func TestCompositionHistorySaveCommitAndPublicBoundary(t *testing.T) {
	t.Run("commit keeps save", func(t *testing.T) {
		editor := NewEditor("abc")
		if _, _, ok := editor.TrackHistory(); !ok {
			t.Fatal("TrackHistory refused")
		}
		if !editor.EnableTentativeComposition() {
			t.Fatal("enable refused")
		}
		compositionApply(t, editor, compositionUpdate("x", Range{}))
		editor.SetSavePoint()
		if !editor.AtSavePoint() {
			t.Fatal("SetSavePoint did not mark the tentative current boundary")
		}
		compositionApply(t, editor, CompositionEvent{Phase: CompositionCommit})
		if !editor.AtSavePoint() {
			t.Fatal("commit lost the public save boundary")
		}
		history := editor.journal.history
		if history.current != 1 || history.save != 1 || history.detach != -1 {
			t.Fatalf("commit context = %d/%d/%d, want 1/1/-1", history.current, history.save, history.detach)
		}
	})

	t.Run("rollback clears only an inside save", func(t *testing.T) {
		editor := NewEditor("abc")
		if _, _, ok := editor.TrackHistory(); !ok {
			t.Fatal("TrackHistory refused")
		}
		if !editor.EnableTentativeComposition() {
			t.Fatal("enable refused")
		}
		editor.SetSelection(Range{Start: 1, End: 2})
		compositionApply(t, editor, compositionUpdate("x", Range{}))
		editor.SetSavePoint()
		compositionApply(t, editor, CompositionEvent{Phase: CompositionCancel})
		if editor.AtSavePoint() {
			t.Fatal("rollback kept a public save boundary inside the removed suffix")
		}
		history := editor.journal.history
		if history.save != 2 || history.current != 1 {
			t.Fatalf("numeric producer save/current = %d/%d, want 2/1", history.save, history.current)
		}
	})
}

func TestCompositionHistoryActiveUpdateCrossesSavedTentative(t *testing.T) {
	editor := NewEditor("abc")
	if _, _, ok := editor.TrackHistory(); !ok {
		t.Fatal("TrackHistory refused")
	}
	if !editor.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	compositionApply(t, editor, compositionUpdate("x", Range{}))
	editor.SetSavePoint()
	compositionApply(t, editor, compositionUpdate("y", Range{}))
	if got := editor.String(); got != "abcy" {
		t.Fatalf("active update text = %q, want abcy", got)
	}
	history := editor.journal.history
	if history.current != 1 || history.save != -1 || history.detach != 0 {
		t.Fatalf("active update context = current/save/detach %d/%d/%d, want 1/-1/0",
			history.current, history.save, history.detach)
	}
}

func TestCompositionHistoryEventAccountingBalances(t *testing.T) {
	editor := NewEditor("abc")
	if _, _, ok := editor.TrackHistory(); !ok {
		t.Fatal("TrackHistory refused")
	}
	if !editor.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	compositionApply(t, editor, compositionUpdate("é𝄞", Range{}))
	compositionApply(t, editor, CompositionEvent{Phase: CompositionCancel})
	history := editor.journal.history
	want := historyHeaderRunes + cap(history.frames)*historyFrameRunes
	for index := range history.frames {
		want += cap(history.frames[index].Steps) * historyStepRunes
	}
	if history.cost != want {
		t.Fatalf("event frame accounting cost = %d, want header+backing %d", history.cost, want)
	}
	if editor.historyCost()-editor.historyRunes-editor.compositionCost() != want {
		t.Fatalf("historyCost producer portion = %d, want %d",
			editor.historyCost()-editor.historyRunes-editor.compositionCost(), want)
	}
}

func TestCompositionHistoryMetadataGrowthPreservesPhysicalRollback(t *testing.T) {
	const count = 150000
	editor := NewEditor("abc")
	cursor, _, ok := editor.TrackHistory()
	if !ok {
		t.Fatal("TrackHistory refused")
	}
	if !editor.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	if !editor.ApplyComposition(compositionUpdate(strings.Repeat("x", count), Range{Start: count, End: count})) {
		t.Fatal("physically retainable preedit was rejected because of metadata cost")
	}
	if editor.RuneCount() != count+3 {
		t.Fatalf("preedit was partially applied: %d runes", editor.RuneCount())
	}
	if !editor.ApplyComposition(CompositionEvent{Phase: CompositionCancel}) || editor.String() != "abc" {
		t.Fatalf("metadata growth destroyed physical rollback: %d runes", editor.RuneCount())
	}
	if frames, _, ok := editor.HistorySince(cursor); ok || frames != nil {
		t.Fatalf("unavailable metadata returned an applicable suffix: %+v", frames)
	}
}

func TestCompositionHistoryFrameRingAccounting(t *testing.T) {
	editor := NewEditor("abc")
	cursor, _, ok := editor.TrackHistory()
	if !ok {
		t.Fatal("TrackHistory refused")
	}
	if !editor.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	for index := 0; index < 300; index++ {
		compositionApply(t, editor, CompositionEvent{Phase: CompositionResult, Text: "x"})
		_, cursor = compositionHistoryFrames(t, editor, cursor)
	}
	history := editor.journal.history
	want := historyHeaderRunes + history.frameCapacity*historyFrameRunes
	for _, frame := range history.frames {
		want += cap(frame.Steps) * historyStepRunes
	}
	if history.cost != want {
		t.Fatalf("native frame ring charged=%d, retained=%d", history.cost, want)
	}
}

func TestCompositionHistoryNoByteTruncationTail(t *testing.T) {
	editor := NewEditor("abc")
	cursor, _, ok := editor.TrackHistory()
	if !ok {
		t.Fatal("TrackHistory refused")
	}
	if !editor.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	editor.Insert("d")
	_, cursor = compositionHistoryFrames(t, editor, cursor)
	if !editor.Undo() || !editor.CanRedo() {
		t.Fatal("redo-tail setup failed")
	}
	_, cursor = compositionHistoryFrames(t, editor, cursor)
	compositionApply(t, editor, CompositionEvent{Phase: CompositionEmptyCommit})
	if editor.CanRedo() {
		t.Fatal("empty commit failed to truncate redo")
	}
	frames, latest := compositionHistoryFrames(t, editor, cursor)
	if len(frames) != 0 || latest != cursor {
		t.Fatalf("no-callback truncation fabricated a frame: %+v/%+v", frames, latest)
	}
	editor.SetSavePoint()
	saveFrames, _ := compositionHistoryFrames(t, editor, cursor)
	if len(saveFrames) != 1 {
		t.Fatalf("Save after no-byte truncation produced %d frames", len(saveFrames))
	}
	frame := saveFrames[0]
	want := HistoryContext{Current: 0, Save: 0, Detach: -1, RetainedFrom: 0, RetainedTo: 0, Collecting: true}
	if len(frame.Steps) != 1 || frame.Steps[0].Operation != HistorySave || frame.Steps[0].Context != want {
		t.Fatalf("Save after no-byte truncation = %+v, want retained tail 0", frame)
	}
}

func TestCompositionHistoryCounterBalanceLifecycle(t *testing.T) {
	assertBalance := func(t *testing.T, stage string, editor *Editor) {
		t.Helper()
		for index := range editor.history {
			if editor.historyRunes < 0 {
				t.Fatalf("%s: historyRunes became negative before unit %d", stage, index)
			}
		}
		if got, want := editor.historyRunes, replayUnitRuneTotal(editor); got != want {
			t.Fatalf("%s: historyRunes=%d, want exact unit total %d", stage, got, want)
		}
		if editor.historyRunes < 0 {
			t.Fatalf("%s: historyRunes is negative: %d", stage, editor.historyRunes)
		}
	}

	editor := NewEditor("abc")
	if _, _, ok := editor.TrackHistory(); !ok {
		t.Fatal("TrackHistory refused")
	}
	if !editor.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	editor.SetSelection(Range{Start: 1, End: 2})
	compositionApply(t, editor, compositionUpdate("é𝄞", Range{Start: 2, End: 2}))
	assertBalance(t, "first update", editor)
	compositionApply(t, editor, compositionUpdate("x", Range{Start: 1, End: 1}))
	assertBalance(t, "active update rollback", editor)
	compositionApply(t, editor, CompositionEvent{Phase: CompositionCancel})
	assertBalance(t, "cancel", editor)

	editor.Insert("z")
	assertBalance(t, "ordinary append", editor)
	editor.SetSavePoint()
	assertBalance(t, "save", editor)
	if !editor.Undo() {
		t.Fatal("ordinary Undo refused")
	}
	editor.Insert("y")
	assertBalance(t, "discarded redo branch", editor)

	editor.ClearHistory()
	if editor.historyRunes != 0 || len(editor.history) != 0 {
		t.Fatalf("clear left historyRunes=%d units=%d", editor.historyRunes, len(editor.history))
	}
}

func TestCompositionHistoryCounterBalanceMixedBatchAndProducer(t *testing.T) {
	editor := NewEditor("abc")
	if _, _, ok := editor.TrackHistory(); !ok {
		t.Fatal("TrackHistory refused")
	}
	if !editor.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	if err := editor.ReplaceRanges([]Replacement{{Range: Range{Start: 0, End: 1}, Text: "aé"}}); err != nil {
		t.Fatal(err)
	}
	if got, want := editor.historyRunes, replayUnitRuneTotal(editor); got != want {
		t.Fatalf("batch historyRunes=%d, want %d", got, want)
	}
	editor.SetSelection(Range{Start: 1, End: 2})
	compositionApply(t, editor, compositionUpdate("é𝄞", Range{Start: 2, End: 2}))
	if got, want := editor.historyRunes, replayUnitRuneTotal(editor); got != want {
		t.Fatalf("mixed batch/tentative historyRunes=%d, want %d", got, want)
	}
	compositionApply(t, editor, CompositionEvent{Phase: CompositionCancel})
	if got, want := editor.historyRunes, replayUnitRuneTotal(editor); got != want {
		t.Fatalf("cancel mixed historyRunes=%d, want %d", got, want)
	}
	editor.ClearHistory()
	if editor.historyRunes != 0 || len(editor.history) != 0 {
		t.Fatalf("mixed clear left historyRunes=%d units=%d", editor.historyRunes, len(editor.history))
	}
}

func TestCompositionHistoryCounterBalanceMetadataInvalidation(t *testing.T) {
	editor := NewEditor("abc")
	if _, _, ok := editor.TrackHistory(); !ok {
		t.Fatal("TrackHistory refused")
	}
	if !editor.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	editor.SetSelection(Range{Start: 1, End: 2})
	compositionApply(t, editor, compositionUpdate("é𝄞", Range{Start: 2, End: 2}))
	history := editor.journal.history
	history.cost = maxSingleEditRunes
	compositionApply(t, editor, compositionUpdate("x", Range{Start: 1, End: 1}))
	if editor.historyRunes < 0 {
		t.Fatalf("metadata invalidation made historyRunes negative: %d", editor.historyRunes)
	}
	if got, want := editor.historyRunes, replayUnitRuneTotal(editor); got != want {
		t.Fatalf("metadata invalidation historyRunes=%d, want %d", got, want)
	}
	historyAssertNoCoalescedPrimitives(t, editor)
	compositionApply(t, editor, CompositionEvent{Phase: CompositionCancel})
	if got, want := editor.historyRunes, replayUnitRuneTotal(editor); got != want {
		t.Fatalf("post-invalidation cancel historyRunes=%d, want %d", got, want)
	}
}
