package text

import (
	"crypto/sha256"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
)

func compositionOverwriteEditor(t *testing.T, document string, selection Range) *Editor {
	t.Helper()
	editor := NewEditor(document)
	editor.SetSelection(selection)
	editor.SetOverwrite(true)
	if !editor.EnableTentativeComposition() {
		t.Fatal("EnableTentativeComposition refused an overwrite-mode source")
	}
	return editor
}

func compositionAssertUnit(t *testing.T, editor *Editor, index int, removed, inserted string) {
	t.Helper()
	if index < 0 || index >= len(editor.history) {
		t.Fatalf("unit %d missing; history has %d units", index, len(editor.history))
	}
	unit := &editor.history[index]
	if unit.kind != editSeparate {
		t.Fatalf("unit %d kind = %d, want editSeparate", index, unit.kind)
	}
	if got := string(unit.removed); got != removed {
		t.Fatalf("unit %d removed = %q, want %q", index, got, removed)
	}
	if got := string(unit.inserted); got != inserted {
		t.Fatalf("unit %d inserted = %q, want %q", index, got, inserted)
	}
	if len(unit.primitives) != 0 {
		t.Fatalf("unit %d retained %d producer-only primitives", index, len(unit.primitives))
	}
}

// TestCompositionOverwriteSourceScalarCases is the exact-Git Document/caller
// boundary table: overwrite deletes one Unicode code point, not a grapheme or
// the replacement width, and protects LF/CRLF and EOF.
func TestCompositionOverwriteSourceScalarCases(t *testing.T) {
	cases := []struct {
		name      string
		document  string
		selection Range
		text      string
		want      string
		removed   string
		inserted  string
	}{
		{"ascii-caret0", "abc", Range{Start: 0, End: 0}, "X", "Xbc", "a", "X"},
		{"bmp-caret0", "aéb", Range{Start: 0, End: 0}, "X", "Xéb", "a", "X"},
		{"bmp-caret1", "aéb", Range{Start: 1, End: 1}, "X", "aXb", "é", "X"},
		{"astral-caret0", "a𝄞b", Range{Start: 0, End: 0}, "X", "X𝄞b", "a", "X"},
		{"astral-caret1", "a𝄞b", Range{Start: 1, End: 1}, "X", "aXb", "𝄞", "X"},
		{"combining-caret1", "a\u0301b", Range{Start: 1, End: 1}, "X", "aXb", "\u0301", "X"},
		{"combining-caret0", "a\u0301b", Range{Start: 0, End: 0}, "X", "X\u0301b", "a", "X"},
		{"lf-line-end", "ab\ncd", Range{Start: 2, End: 2}, "X", "abX\ncd", "", "X"},
		{"crlf-line-end", "ab\r\ncd", Range{Start: 2, End: 2}, "X", "abX\r\ncd", "", "X"},
		{"eof", "abc", Range{Start: 3, End: 3}, "X", "abcX", "", "X"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			editor := compositionOverwriteEditor(t, test.document, test.selection)
			compositionApply(t, editor, compositionUpdate(test.text, Range{}))
			if got := editor.String(); got != test.want {
				t.Fatalf("text = %q, want %q", got, test.want)
			}
			if len(editor.history) != 1 {
				t.Fatalf("units = %d, want one ordinary scalar unit", len(editor.history))
			}
			compositionAssertUnit(t, editor, 0, test.removed, test.inserted)
			// Each overwrite scalar is its own ordinary Undo group even when it
			// only inserts at a protected line end/EOF. Public Undo still refuses
			// the live tentative boundary.
			if editor.Undo() {
				t.Fatal("public Undo crossed the live tentative boundary")
			}
			compositionApply(t, editor, CompositionEvent{Phase: CompositionCancel})
			if got := editor.String(); got != test.document {
				t.Fatalf("cancel = %q, want %q", got, test.document)
			}
			if len(editor.history) != 0 || editor.CanUndo() {
				t.Fatalf("cancel retained suffix history/Undo: %d/%t", len(editor.history), editor.CanUndo())
			}
		})
	}
}

// TestCompositionOverwriteFirstPreeditSelectionFlow covers the binding closure
// example: abcdef/select bc -> adef -> axef; cancel -> adef; Undo -> abcdef.
func TestCompositionOverwriteFirstPreeditSelectionFlow(t *testing.T) {
	t.Run("first scalar then cancel", func(t *testing.T) {
		editor := compositionOverwriteEditor(t, "abcdef", Range{Start: 1, End: 3})
		compositionApply(t, editor, compositionUpdate("x", Range{}))
		if got := editor.String(); got != "axef" {
			t.Fatalf("first overwrite preedit = %q, want axef", got)
		}
		if len(editor.history) != 2 {
			t.Fatalf("units = %d, want ordinary selection deletion plus scalar", len(editor.history))
		}
		compositionAssertUnit(t, editor, 0, "bc", "")
		compositionAssertUnit(t, editor, 1, "d", "x")
		compositionApply(t, editor, CompositionEvent{Phase: CompositionCancel})
		if got := editor.String(); got != "adef" {
			t.Fatalf("cancel = %q, want adef (pre-start ordinary deletion stays)", got)
		}
		if len(editor.history) != 1 || !editor.CanUndo() {
			t.Fatalf("cancel history = %d/%t, want one ordinary Undo", len(editor.history), editor.CanUndo())
		}
		if !editor.Undo() || editor.String() != "abcdef" {
			t.Fatalf("ordinary Undo after cancel = %q", editor.String())
		}
	})

	t.Run("active update yz", func(t *testing.T) {
		editor := compositionOverwriteEditor(t, "abcdef", Range{Start: 1, End: 3})
		compositionApply(t, editor, compositionUpdate("x", Range{}))
		compositionApply(t, editor, compositionUpdate("yz", Range{}))
		if got := editor.String(); got != "ayzf" {
			t.Fatalf("update yz = %q, want ayzf", got)
		}
		if len(editor.history) != 3 {
			t.Fatalf("units = %d, want deletion + independent y,z scalars", len(editor.history))
		}
		compositionAssertUnit(t, editor, 1, "d", "y")
		compositionAssertUnit(t, editor, 2, "e", "z")
		compositionApply(t, editor, CompositionEvent{Phase: CompositionCancel})
		if got := editor.String(); got != "adef" {
			t.Fatalf("cancel after yz = %q, want adef", got)
		}
		if !editor.Undo() || editor.String() != "abcdef" {
			t.Fatalf("Undo after yz cancel = %q", editor.String())
		}
	})

	t.Run("result RS keeps scalar units and Undo chain", func(t *testing.T) {
		editor := compositionOverwriteEditor(t, "abcdef", Range{Start: 1, End: 3})
		compositionApply(t, editor, compositionUpdate("x", Range{}))
		compositionApply(t, editor, CompositionEvent{Phase: CompositionResult, Text: "RS"})
		if got := editor.String(); got != "aRSf" {
			t.Fatalf("result RS = %q, want aRSf", got)
		}
		if len(editor.history) != 3 {
			t.Fatalf("units = %d, want deletion + R + S", len(editor.history))
		}
		for index, want := range []string{"aRef", "adef", "abcdef"} {
			if !editor.Undo() {
				t.Fatalf("Undo %d refused", index+1)
			}
			if got := editor.String(); got != want {
				t.Fatalf("Undo %d = %q, want %q", index+1, got, want)
			}
		}
	})
}

// TestCompositionOverwriteExplicitPostRollbackReplacement covers an explicit
// ordinary deletion after the preedit rollback; the following scalars still use
// the current overwrite flag and each owns a separate ordinary unit.
func TestCompositionOverwriteExplicitPostRollbackReplacement(t *testing.T) {
	editor := compositionOverwriteEditor(t, "abcdef", Range{Start: 1, End: 1})
	compositionApply(t, editor, compositionUpdate("x", Range{}))
	if got := editor.String(); got != "axcdef" {
		t.Fatalf("preedit x = %q, want axcdef", got)
	}
	compositionApply(t, editor, CompositionEvent{
		Phase: CompositionResult, Text: "XY", HasReplacement: true, Replacement: Range{Start: 1, End: 3},
	})
	if got := editor.String(); got != "aXYf" {
		t.Fatalf("explicit result = %q, want aXYf", got)
	}
	if len(editor.history) != 3 {
		t.Fatalf("units = %d, want explicit deletion + X + Y", len(editor.history))
	}
	compositionAssertUnit(t, editor, 0, "bc", "")
	compositionAssertUnit(t, editor, 1, "d", "X")
	compositionAssertUnit(t, editor, 2, "e", "Y")
	for index, want := range []string{"aXef", "adef", "abcdef"} {
		if !editor.Undo() {
			t.Fatalf("Undo %d refused", index+1)
		}
		if got := editor.String(); got != want {
			t.Fatalf("Undo %d = %q, want %q", index+1, got, want)
		}
	}
}

// TestCompositionOverwriteExplicitEmptyAndUTF16 keeps explicit empty ranges and
// raw post-rollback UTF-16 conversion distinct from the overwrite scalar that
// follows them.
func TestCompositionOverwriteExplicitEmptyAndUTF16(t *testing.T) {
	t.Run("empty explicit range still overwrites next scalar", func(t *testing.T) {
		editor := compositionOverwriteEditor(t, "abc", Range{Start: 1, End: 1})
		compositionApply(t, editor, compositionUpdate("x", Range{}))
		compositionApply(t, editor, CompositionEvent{
			Phase: CompositionResult, Text: "Y", HasReplacement: true, Replacement: Range{Start: 1, End: 1},
		})
		if got := editor.String(); got != "aYc" {
			t.Fatalf("empty explicit overwrite = %q, want aYc", got)
		}
		if len(editor.history) != 1 {
			t.Fatalf("units = %d, want one Y scalar unit", len(editor.history))
		}
		compositionAssertUnit(t, editor, 0, "b", "Y")
	})
	t.Run("raw UTF16 replacement after rollback", func(t *testing.T) {
		editor := compositionOverwriteEditor(t, "abcd", Range{Start: 1, End: 2})
		compositionApply(t, editor, compositionUpdate("é𝄞", Range{}))
		if got := editor.String(); got != "aé𝄞" {
			t.Fatalf("preedit = %q, want aé𝄞", got)
		}
		compositionApply(t, editor, CompositionEvent{
			Phase: CompositionResult, Text: "r",
			HasReplacementUTF16: true, ReplacementUTF16: UTF16Range{Start: 3, Length: 1},
		})
		// Result rollback excludes the ordinary pre-start b deletion, so the raw
		// start 3 is the document end of "acd"; the explicit range is empty and
		// the overwrite scalar appends r without deleting a previous EOF rune.
		if got := editor.String(); got != "acdr" {
			t.Fatalf("raw UTF16 overwrite = %q, want acdr", got)
		}
		if len(editor.history) != 2 {
			t.Fatalf("units = %d, want pre-start deletion plus r", len(editor.history))
		}
		compositionAssertUnit(t, editor, 0, "b", "")
		compositionAssertUnit(t, editor, 1, "", "r")
	})
}

// TestCompositionOverwriteEqualScalarRetainsUnit keeps a real equal overwrite
// action (x over x) as one ordinary unit without a save point or producer.
func TestCompositionOverwriteEqualScalarRetainsUnit(t *testing.T) {
	editor := compositionOverwriteEditor(t, "axb", Range{Start: 1, End: 1})
	compositionApply(t, editor, compositionUpdate("x", Range{}))
	if got := editor.String(); got != "axb" {
		t.Fatalf("equal overwrite = %q, want axb", got)
	}
	if len(editor.history) != 1 || !editor.CanUndo() {
		t.Fatalf("equal overwrite units/Undo = %d/%t, want 1/true", len(editor.history), editor.CanUndo())
	}
	compositionApply(t, editor, CompositionEvent{Phase: CompositionCancel})
	if editor.String() != "axb" || len(editor.history) != 0 {
		t.Fatalf("equal overwrite cancel = %q/%d", editor.String(), len(editor.history))
	}
}

// TestCompositionOverwriteFlagChangesDuringTentative proves SetOverwrite is a
// pure flag change: rollback uses the units actually recorded and the next event
// uses the new flag.
func TestCompositionOverwriteFlagChangesDuringTentative(t *testing.T) {
	t.Run("insertion to overwrite", func(t *testing.T) {
		editor := NewEditor("abc")
		editor.SetSelection(Range{Start: 0, End: 0})
		if !editor.EnableTentativeComposition() {
			t.Fatal("enable refused")
		}
		compositionApply(t, editor, compositionUpdate("x", Range{}))
		if got := editor.String(); got != "xabc" {
			t.Fatalf("insertion preedit = %q, want xabc", got)
		}
		editor.SetOverwrite(true)
		if !editor.TentativeCompositionEnabled() {
			t.Fatal("flag change disabled the capability")
		}
		compositionApply(t, editor, compositionUpdate("yz", Range{}))
		if got := editor.String(); got != "yzc" {
			t.Fatalf("overwrite update yz = %q, want yzc", got)
		}
		compositionApply(t, editor, CompositionEvent{Phase: CompositionCancel})
		if got := editor.String(); got != "abc" {
			t.Fatalf("cancel = %q, want abc", got)
		}
	})

	t.Run("overwrite to insertion", func(t *testing.T) {
		editor := compositionOverwriteEditor(t, "abc", Range{Start: 0, End: 0})
		compositionApply(t, editor, compositionUpdate("x", Range{}))
		if got := editor.String(); got != "xbc" {
			t.Fatalf("overwrite preedit = %q, want xbc", got)
		}
		editor.SetOverwrite(false)
		if !editor.TentativeCompositionEnabled() {
			t.Fatal("flag change disabled the capability")
		}
		compositionApply(t, editor, compositionUpdate("yz", Range{}))
		if got := editor.String(); got != "yzabc" {
			t.Fatalf("insertion update yz = %q, want yzabc", got)
		}
		if len(editor.history) != 1 {
			t.Fatalf("insertion update units = %d, want one coalesced unit", len(editor.history))
		}
		compositionApply(t, editor, CompositionEvent{Phase: CompositionCancel})
		if got := editor.String(); got != "abc" {
			t.Fatalf("cancel = %q, want abc", got)
		}
	})

	t.Run("commit after toggle keeps units", func(t *testing.T) {
		editor := compositionOverwriteEditor(t, "abc", Range{Start: 0, End: 0})
		compositionApply(t, editor, compositionUpdate("x", Range{}))
		editor.SetOverwrite(false)
		compositionApply(t, editor, CompositionEvent{Phase: CompositionCommit})
		if got := editor.String(); got != "xbc" {
			t.Fatalf("commit = %q, want xbc", got)
		}
		if !editor.Undo() || editor.String() != "abc" {
			t.Fatalf("Undo after commit = %q", editor.String())
		}
	})
}

// TestCompositionOverwriteMixedWin32LogicalResults covers the logical native
// arx/ary sequence (not insertion-mode arxc/aryc).
func TestCompositionOverwriteMixedWin32LogicalResults(t *testing.T) {
	editor := compositionOverwriteEditor(t, "abc", Range{Start: 1, End: 2})
	compositionApply(t, editor, CompositionEvent{Phase: CompositionResult, Text: "r"})
	if got := editor.String(); got != "arc" {
		t.Fatalf("selected result r = %q, want arc", got)
	}
	compositionApply(t, editor, compositionUpdate("x", Range{}))
	if got := editor.String(); got != "arx" {
		t.Fatalf("preedit x = %q, want arx", got)
	}
	compositionApply(t, editor, CompositionEvent{Phase: CompositionResult, Text: "y"})
	if got := editor.String(); got != "ary" {
		t.Fatalf("next result y = %q, want ary (not aryc)", got)
	}
}

// TestCompositionOverwriteSeparateScalarUnitsAndProducerShape keeps raw action
// counts separate from ordinary unit counts: a two-scalar overwrite event is two
// units and four source actions, while insertion mode remains one coalesced unit.
func TestCompositionOverwriteSeparateScalarUnitsAndProducerShape(t *testing.T) {
	overwrite := NewEditor("abc")
	cursor, _ := historyMustTrack(t, overwrite)
	overwrite.SetSelection(Range{Start: 0, End: 0})
	overwrite.SetOverwrite(true)
	if !overwrite.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	compositionApply(t, overwrite, compositionUpdate("XY", Range{}))
	if got := overwrite.String(); got != "XYc" {
		t.Fatalf("overwrite XY = %q, want XYc", got)
	}
	if len(overwrite.history) != 2 {
		t.Fatalf("overwrite units = %d, want 2", len(overwrite.history))
	}
	compositionApply(t, overwrite, CompositionEvent{Phase: CompositionCommit})
	frames, _, ok := overwrite.HistorySince(cursor)
	if !ok || len(frames) != 1 {
		t.Fatalf("overwrite frames = %d/%t, want one update frame (commit adds none)", len(frames), ok)
	}
	update := frames[0]
	if len(update.Steps) != 4 {
		t.Fatalf("overwrite update steps = %d, want 4 (delete+insert per scalar)", len(update.Steps))
	}
	for index, step := range update.Steps {
		want := HistoryDelete
		if index%2 == 1 {
			want = HistoryInsert
		}
		if step.Operation != HistoryForward || step.Primitive != want {
			t.Fatalf("step %d = %d/%d, want forward/%d", index, step.Operation, step.Primitive, want)
		}
		if step.Context.Current != index+1 {
			t.Fatalf("step %d Current = %d, want %d", index, step.Context.Current, index+1)
		}
	}

	insert := NewEditor("abc")
	insertCursor, _ := historyMustTrack(t, insert)
	insert.SetSelection(Range{Start: 0, End: 0})
	if !insert.EnableTentativeComposition() {
		t.Fatal("enable refused")
	}
	compositionApply(t, insert, compositionUpdate("XY", Range{}))
	if got := insert.String(); got != "XYabc" {
		t.Fatalf("insertion XY = %q, want XYabc", got)
	}
	if len(insert.history) != 1 {
		t.Fatalf("insertion units = %d, want one coalesced unit", len(insert.history))
	}
	compositionApply(t, insert, CompositionEvent{Phase: CompositionCommit})
	frames, _, ok = insert.HistorySince(insertCursor)
	if !ok || len(frames) != 1 || len(frames[0].Steps) != 2 {
		t.Fatalf("insertion update shape = %d frames/%d steps/%t", len(frames), func() int {
			if len(frames) > 0 {
				return len(frames[0].Steps)
			}
			return -1
		}(), ok)
	}
}

// TestCompositionCommittedStringOwned derives the committed snapshot from real
// tentative rollback units, not the unused legacy compositionOriginal field.
func TestCompositionCommittedStringOwned(t *testing.T) {
	t.Run("overwrite update and cancel", func(t *testing.T) {
		editor := compositionOverwriteEditor(t, "abc", Range{Start: 1, End: 1})
		compositionApply(t, editor, compositionUpdate("xy", Range{}))
		if got := editor.String(); got != "axy" {
			t.Fatalf("preedit = %q, want axy", got)
		}
		for index := 0; index < 2; index++ {
			if got := editor.CommittedString(); got != "abc" {
				t.Fatalf("snapshot %d = %q, want abc", index+1, got)
			}
		}
		compositionApply(t, editor, CompositionEvent{Phase: CompositionCancel})
		if got := editor.String(); got != "abc" {
			t.Fatalf("cancel = %q, want abc", got)
		}
	})
	t.Run("selected predelete excludes ordinary deletion", func(t *testing.T) {
		editor := compositionOverwriteEditor(t, "abcdef", Range{Start: 1, End: 3})
		compositionApply(t, editor, compositionUpdate("x", Range{}))
		if got := editor.CommittedString(); got != "adef" {
			t.Fatalf("committed after predelete = %q, want adef", got)
		}
		compositionApply(t, editor, CompositionEvent{Phase: CompositionCancel})
		if editor.String() != "adef" {
			t.Fatalf("cancel = %q, want adef", editor.String())
		}
		if !editor.Undo() || editor.String() != "abcdef" {
			t.Fatalf("Undo = %q, want abcdef", editor.String())
		}
	})
	t.Run("insertion and mode toggle", func(t *testing.T) {
		editor := NewEditor("abc")
		editor.SetSelection(Range{Start: 1, End: 1})
		if !editor.EnableTentativeComposition() {
			t.Fatal("enable refused")
		}
		compositionApply(t, editor, compositionUpdate("x", Range{}))
		if got := editor.CommittedString(); got != "abc" {
			t.Fatalf("insertion committed = %q, want abc", got)
		}
		editor.SetOverwrite(true)
		compositionApply(t, editor, compositionUpdate("y", Range{}))
		if got := editor.CommittedString(); got != "abc" {
			t.Fatalf("toggled committed = %q, want abc", got)
		}
		compositionApply(t, editor, CompositionEvent{Phase: CompositionCancel})
		if editor.String() != "abc" {
			t.Fatalf("cancel = %q, want abc", editor.String())
		}
	})
	t.Run("producer unavailable", func(t *testing.T) {
		editor := compositionOverwriteEditor(t, "abc", Range{Start: 1, End: 1})
		compositionApply(t, editor, compositionUpdate("xy", Range{}))
		editor.invalidateHistory()
		if got := editor.CommittedString(); got != "abc" {
			t.Fatalf("unavailable committed = %q, want abc", got)
		}
		compositionApply(t, editor, CompositionEvent{Phase: CompositionCancel})
		if editor.String() != "abc" {
			t.Fatalf("unavailable cancel = %q", editor.String())
		}
	})
	t.Run("legacy marked text unchanged", func(t *testing.T) {
		editor := NewEditor("abc")
		editor.SetMarkedText("xy", Range{Start: 1, End: 1})
		if got := editor.CommittedString(); got != "abc" {
			t.Fatalf("legacy committed = %q, want abc", got)
		}
	})
	t.Run("producer and commit", func(t *testing.T) {
		editor := NewEditor("abc")
		if _, _, ok := editor.TrackHistory(); !ok {
			t.Fatal("TrackHistory refused")
		}
		editor.SetSelection(Range{Start: 1, End: 1})
		editor.SetOverwrite(true)
		if !editor.EnableTentativeComposition() {
			t.Fatal("enable refused")
		}
		compositionApply(t, editor, compositionUpdate("xy", Range{}))
		if got := editor.CommittedString(); got != "abc" {
			t.Fatalf("producer committed = %q, want abc", got)
		}
		compositionApply(t, editor, CompositionEvent{Phase: CompositionCommit})
		if got := editor.CommittedString(); got != "axy" {
			t.Fatalf("committed after Commit = %q, want axy", got)
		}
	})
}

// TestCompositionOverwriteReplacementBudget bounds active replacement planning
// by the post-rollback event, not the replaced old generation, and keeps total
// allocation linear rather than copying the full buffer per rolled-back unit.
func TestCompositionOverwriteReplacementBudget(t *testing.T) {
	const count = 80000
	initial := strings.Repeat("a", count)
	editor := compositionOverwriteEditor(t, initial, Range{Start: 0, End: 0})
	compositionApply(t, editor, compositionUpdate(strings.Repeat("b", count), Range{}))
	if len(editor.history) != count {
		t.Fatalf("first units = %d, want %d", len(editor.history), count)
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	compositionApply(t, editor, compositionUpdate(strings.Repeat("c", count), Range{}))
	if got := editor.String(); got != strings.Repeat("c", count) {
		t.Fatalf("replacement text length = %d", len(got))
	}
	if len(editor.history) != count {
		t.Fatalf("replacement units = %d, want %d", len(editor.history), count)
	}
	compositionApply(t, editor, CompositionEvent{Phase: CompositionCancel})
	if editor.String() != initial {
		t.Fatal("replacement Cancel lost the initial document")
	}
	runtime.ReadMemStats(&after)
	// The old per-unit full-buffer copy path allocates tens of gigabytes for
	// this workload. A generous linear bound still catches that regression.
	const limit = 256 << 20
	if delta := after.TotalAlloc - before.TotalAlloc; delta > limit {
		t.Fatalf("replacement planning allocated %d bytes, want <= %d", delta, limit)
	} else {
		t.Logf("replacement planning allocation = %d bytes", delta)
	}
}

// TestCompositionOverwriteSourceReferenceHashes keeps the exact-Git overwrite
// probe source/reference read-only and byte-identical to Codex-verified data.
func TestCompositionOverwriteSourceReferenceHashes(t *testing.T) {
	cases := []struct {
		path string
		want string
		size int
	}{
		{"testdata/composition-overwrite-probe.cxx", "3fcabe7723718edd6c00ecb87aadab235c57db7cfe8d3518b8b202480cbfb265", 0},
		{"testdata/composition-overwrite-reference.txt", "d68a93d105fcb416c6512ce90f1248d98f81d0fb5bd1e2bd76267356c547d72b", 5122},
	}
	for _, test := range cases {
		data, err := os.ReadFile(test.path)
		if err != nil {
			t.Fatalf("read %s: %v", test.path, err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != test.want {
			t.Fatalf("%s SHA-256 = %s, want %s", test.path, got, test.want)
		}
		if test.size > 0 && len(data) != test.size {
			t.Fatalf("%s size = %d, want %d", test.path, len(data), test.size)
		}
	}
}
