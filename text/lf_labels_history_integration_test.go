package text

import (
	"fmt"
	"strings"
	"testing"
)

func lfLabelIntegrationEditor(t *testing.T, tracked, tentative bool) (*Editor, HistoryCursor) {
	t.Helper()
	editor := NewEditor("a\nb\nc")
	if !editor.TrackLFLabels([]byte{'W', 'L'}, 'M', nil) {
		t.Fatal("LF label setup failed")
	}
	var cursor HistoryCursor
	if tracked {
		var ok bool
		cursor, _, ok = editor.TrackHistory()
		if !ok {
			t.Fatal("history setup failed after labels")
		}
	}
	if tentative && !editor.EnableTentativeComposition() {
		t.Fatal("tentative setup failed after labels/history")
	}
	return editor, cursor
}

func TestLFLabelProducerInvalidationDoesNotDiscardLabels(t *testing.T) {
	editor, cursor := lfLabelIntegrationEditor(t, true, false)
	editor.SetSelection(Range{Start: 1, End: 4})
	editor.SetMarkedText("p\n", Range{Start: 2, End: 2})
	if _, _, ok := editor.HistorySince(cursor); ok {
		t.Fatal("legacy composition did not invalidate the producer fixture")
	}
	assertLFLabels(t, editor, []byte{'M'})
	if !editor.CancelComposition() || editor.String() != "a\nb\nc" {
		t.Fatal("legacy cancellation failed after producer invalidation")
	}
	assertLFLabels(t, editor, []byte{'W', 'L'})
	editor.ReplaceRange(Range{Start: 3, End: 4}, "")
	assertLFLabels(t, editor, []byte{'W'})
	if !editor.Undo() || editor.String() != "a\nb\nc" {
		t.Fatal("producer invalidation damaged subsequent label Undo")
	}
	assertLFLabels(t, editor, []byte{'W', 'L'})
}

func TestLFLabelProducerPrefixTrimDoesNotDiscardCurrentIdentity(t *testing.T) {
	editor, cursor := lfLabelIntegrationEditor(t, true, false)
	editor.MoveTo(editor.RuneCount(), false)
	for range 1200 {
		editor.Paste("x\n")
	}
	if _, _, ok := editor.HistorySince(cursor); ok {
		t.Fatal("fixture did not invalidate or expire history producer data")
	}
	assertLFLabels(t, editor, append([]byte{'W', 'L'}, bytesOfLabel('M', 1200)...))
	if !editor.Undo() {
		t.Fatal("prefix trim removed the latest actual unit")
	}
	assertLFLabels(t, editor, append([]byte{'W', 'L'}, bytesOfLabel('M', 1199)...))
	editor.ClearHistory()
	assertLFLabels(t, editor, append([]byte{'W', 'L'}, bytesOfLabel('M', 1199)...))
}

func TestLFLabelTentativeLifecycleKeepsOrdinarySelectionDeletion(t *testing.T) {
	for _, tracked := range []bool{false, true} {
		for _, ending := range []string{"cancel", "generic-cancel", "empty-update", "result", "commit"} {
			t.Run(fmt.Sprintf("tracked-%t/%s", tracked, ending), func(t *testing.T) {
				editor, _ := lfLabelIntegrationEditor(t, tracked, true)
				editor.SetSelection(Range{Start: 1, End: 2})
				compositionApply(t, editor, compositionUpdate("p\nq\n", Range{Start: 4, End: 4}))
				assertLFLabels(t, editor, []byte{'M', 'M', 'L'})
				switch ending {
				case "cancel":
					compositionApply(t, editor, CompositionEvent{Phase: CompositionCancel})
				case "generic-cancel":
					if !editor.CancelComposition() {
						t.Fatal("generic cancel refused owned composition")
					}
				case "empty-update":
					compositionApply(t, editor, compositionUpdate("", Range{}))
				case "result":
					compositionApply(t, editor, CompositionEvent{Phase: CompositionResult, Text: "z\n"})
					if editor.String() != "az\nb\nc" {
						t.Fatalf("result text = %q", editor.String())
					}
					assertLFLabels(t, editor, []byte{'M', 'L'})
					if !editor.Undo() {
						t.Fatal("result Undo failed")
					}
				case "commit":
					compositionApply(t, editor, CompositionEvent{Phase: CompositionCommit})
					if editor.String() != "ap\nq\nb\nc" {
						t.Fatalf("commit text = %q", editor.String())
					}
					assertLFLabels(t, editor, []byte{'M', 'M', 'L'})
					if !editor.Undo() {
						t.Fatal("committed preedit Undo failed")
					}
				}
				if editor.String() != "ab\nc" {
					t.Fatalf("ending restored ordinary selection deletion: %q", editor.String())
				}
				assertLFLabels(t, editor, []byte{'L'})
				if !editor.Undo() || editor.String() != "a\nb\nc" || editor.CanUndo() {
					t.Fatal("ordinary selection deletion lost its own Undo")
				}
				assertLFLabels(t, editor, []byte{'W', 'L'})
				if !editor.Redo() || editor.String() != "ab\nc" {
					t.Fatal("ordinary selection deletion lost Redo")
				}
				assertLFLabels(t, editor, []byte{'L'})
				editor.ClearHistory()
				if editor.historyRunes != 0 || len(editor.lfLabelState().history) != 0 || len(editor.lfLabelState().pending) != 0 {
					t.Fatal("tentative lifecycle retained forgotten label fragments")
				}
			})
		}
	}
}

func TestLFLabelTentativeRepeatedUpdatesAndRollbackRelease(t *testing.T) {
	editor, _ := lfLabelIntegrationEditor(t, true, true)
	editor.SetSelection(Range{Start: 2, End: 2})
	for index := range 30 {
		compositionApply(t, editor, compositionUpdate(fmt.Sprintf("%04d\n", index), Range{Start: 5, End: 5}))
		assertLFLabels(t, editor, []byte{'W', 'M', 'L'})
	}
	compositionApply(t, editor, CompositionEvent{Phase: CompositionCancel})
	if editor.String() != "a\nb\nc" || editor.CanUndo() || len(editor.lfLabelState().history) != 0 || editor.historyRunes != 0 {
		t.Fatal("rollback retained provisional identities/history ownership")
	}
	assertLFLabels(t, editor, []byte{'W', 'L'})
}

func TestLFLabelProducerEqualReplacementOwnsNewIdentity(t *testing.T) {
	editor, _ := lfLabelIntegrationEditor(t, true, false)
	editor.ReplaceRange(Range{End: editor.RuneCount()}, editor.String())
	if !editor.CanUndo() || !editor.SetLFLabels([]byte{'M', 'M'}, 'M') {
		t.Fatal("producer equal replacement/relabel fixture failed")
	}
	if !editor.Undo() {
		t.Fatal("equal physical replacement lost Undo")
	}
	assertLFLabels(t, editor, []byte{'W', 'L'})
	if !editor.Redo() {
		t.Fatal("equal physical replacement lost Redo")
	}
	assertLFLabels(t, editor, []byte{'M', 'M'})
}

func TestLFLabelTentativeLargeInsertionRekeysOwnedText(t *testing.T) {
	editor, _ := lfLabelIntegrationEditor(t, true, true)
	editor.SetSelection(Range{Start: 2, End: 2})
	value := strings.Repeat("x\n", 100)
	compositionApply(t, editor, compositionUpdate(value, Range{Start: len(value), End: len(value)}))
	assertLFLabels(t, editor, append(append([]byte{'W'}, bytesOfLabel('M', 100)...), 'L'))
	compositionApply(t, editor, CompositionEvent{Phase: CompositionCancel})
	if editor.String() != "a\nb\nc" || editor.CanUndo() || editor.historyRunes != 0 || len(editor.lfLabelState().history) != 0 {
		t.Fatal("owned-text compaction lost rollback identity or retained dead label units")
	}
	assertLFLabels(t, editor, []byte{'W', 'L'})
}

func TestLFLabelGenericCancelRefusesUnownedTentativeResidue(t *testing.T) {
	editor, _ := lfLabelIntegrationEditor(t, true, true)
	compositionApply(t, editor, compositionUpdate("x\n", Range{}))
	editor.Restrict(nil, 0)
	before, labels := editor.String(), []byte{'W', 'L', 'M'}
	if editor.CancelComposition() || editor.String() != before {
		t.Fatal("generic cancel rewrote an unowned tentative residue")
	}
	assertLFLabels(t, editor, labels)
	editor.Unmark()
	if !editor.Undo() || editor.String() != "a\nb\nc" {
		t.Fatal("unowned tentative residue lost ordinary Undo")
	}
	assertLFLabels(t, editor, []byte{'W', 'L'})
}

func TestLFLabelLegacyMovementSealsBeforeOrdinaryInsert(t *testing.T) {
	for _, movement := range []string{"selection", "move-to", "left", "right", "delete-mark"} {
		t.Run(movement, func(t *testing.T) {
			editor, _ := lfLabelIntegrationEditor(t, true, false)
			editor.SetSelection(Range{Start: 1, End: 2})
			editor.SetMarkedText("x\n", Range{Start: 2, End: 2})
			if movement == "delete-mark" {
				if !editor.DeleteBackward() {
					t.Fatal("marked deletion failed")
				}
			} else {
				switch movement {
				case "selection":
					editor.SetSelection(Range{Start: editor.RuneCount(), End: editor.RuneCount()})
				case "move-to":
					editor.MoveTo(editor.RuneCount(), false)
				case "left":
					editor.MoveLeft(false)
				case "right":
					editor.MoveRight(false)
				}
				editor.Insert("\n")
			}
			if !editor.Undo() || editor.String() != "ax\nb\nc" {
				t.Fatal("independent edit did not restore its marked-text basis")
			}
			assertLFLabels(t, editor, []byte{'M', 'L'})
			if !editor.Undo() || editor.String() != "a\nb\nc" || editor.CanUndo() {
				t.Fatal("movement lost earlier composition Undo")
			}
			assertLFLabels(t, editor, []byte{'W', 'L'})
		})
	}
}

func TestLFLabelLegacyCancelAfterClearWithTentativeCapability(t *testing.T) {
	editor, _ := lfLabelIntegrationEditor(t, true, true)
	editor.SetSelection(Range{Start: 1, End: 4})
	editor.SetMarkedText("p\n", Range{})
	editor.ClearHistory()
	if !editor.CancelComposition() || editor.String() != "a\nb\nc" || editor.CanUndo() || editor.CanRedo() || editor.historyRunes != 0 {
		t.Fatal("legacy cancellation lost its source identity after Clear")
	}
	assertLFLabels(t, editor, []byte{'W', 'L'})
}

func TestLFLabelTentativeBudgetIncludesPhysicalMetadataBeforeMutation(t *testing.T) {
	const breaks = 30000
	editor, cursor := lfLabelIntegrationEditor(t, true, true)
	editor.SetOverwrite(true)
	beforeCost := editor.historyCost()
	if editor.ApplyComposition(compositionUpdate(strings.Repeat("\n", breaks), Range{})) {
		t.Fatal("oversized LF metadata was accepted by tentative physical preflight")
	}
	if editor.String() != "a\nb\nc" || editor.CanUndo() || editor.CanRedo() || editor.historyCost() != beforeCost || editor.CompositionActive() {
		t.Fatal("LF budget refusal changed text, history, composition or cost")
	}
	assertLFLabels(t, editor, []byte{'W', 'L'})
	if frames, latest, ok := editor.HistorySince(cursor); !ok || len(frames) != 0 || latest != cursor {
		t.Fatal("LF physical budget refusal published producer state")
	}
}

func TestLFLabelTentativeAffordableCoalescedBreaksRemainAccepted(t *testing.T) {
	const breaks = 30000
	editor, _ := lfLabelIntegrationEditor(t, true, true)
	compositionApply(t, editor, compositionUpdate(strings.Repeat("\n", breaks), Range{}))
	assertLFLabels(t, editor, append([]byte{'W', 'L'}, bytesOfLabel('M', breaks)...))
	if editor.historyCost() > maxSingleEditRunes {
		t.Fatal("accepted coalesced LF event exceeds its physical budget")
	}
	compositionApply(t, editor, CompositionEvent{Phase: CompositionCancel})
	if editor.String() != "a\nb\nc" || editor.CanUndo() || editor.historyRunes != 0 || len(editor.lfLabelState().history) != 0 {
		t.Fatal("affordable coalesced event lost rollback or retained label fragments")
	}
	assertLFLabels(t, editor, []byte{'W', 'L'})
}

func TestLFLabelTentativeIdleStorageKeepsOrdinaryHistoryIdentity(t *testing.T) {
	for _, tracked := range []bool{false, true} {
		for _, operation := range []string{"insert", "backspace", "replace"} {
			t.Run(fmt.Sprintf("tracked-%t/%s", tracked, operation), func(t *testing.T) {
				editor, _ := lfLabelIntegrationEditor(t, tracked, true)
				want := []byte{'L'}
				switch operation {
				case "insert":
					editor.Insert("\n")
					want = []byte{'W', 'L', 'M'}
				case "backspace":
					editor.SetSelection(Range{Start: 1, End: 2})
					if !editor.DeleteBackward() {
						t.Fatal("selected LF deletion failed")
					}
				case "replace":
					editor.ReplaceRange(Range{Start: 1, End: 2}, "")
				}
				assertLFLabels(t, editor, want)
				if !editor.Undo() || editor.String() != "a\nb\nc" || editor.CanUndo() {
					t.Fatal("idle tentative storage lost ordinary Undo")
				}
				assertLFLabels(t, editor, []byte{'W', 'L'})
				if !editor.Redo() {
					t.Fatal("idle tentative storage lost ordinary Redo")
				}
				assertLFLabels(t, editor, want)
			})
		}
	}
}

func TestLFLabelLegacyCompactionPreservesOlderUnitIdentity(t *testing.T) {
	for _, tracked := range []bool{false, true} {
		for _, cancel := range []bool{false, true} {
			t.Run(fmt.Sprintf("tracked-%t/cancel-%t", tracked, cancel), func(t *testing.T) {
				editor, _ := lfLabelIntegrationEditor(t, tracked, false)
				editor.Insert("\n")
				if !editor.EnableTentativeComposition() {
					t.Fatal("tentative enable refused older ordinary history")
				}
				editor.MoveTo(0, false)
				editor.SetMarkedText("x\n", Range{})
				assertLFLabels(t, editor, []byte{'M', 'W', 'L', 'M'})
				if cancel {
					if !editor.CancelComposition() {
						t.Fatal("legacy cancellation failed")
					}
				} else {
					editor.Unmark()
					if !editor.Undo() {
						t.Fatal("committed legacy composition lost Undo")
					}
				}
				assertLFLabels(t, editor, []byte{'W', 'L', 'M'})
				if !editor.Undo() || editor.String() != "a\nb\nc" || editor.CanUndo() {
					t.Fatal("legacy compaction lost its older ordinary unit")
				}
				assertLFLabels(t, editor, []byte{'W', 'L'})
			})
		}
	}
}

func TestLFLabelProducerEqualValueUnitsKeepSeparateIdentity(t *testing.T) {
	for _, operation := range []string{"direct", "batch", "transaction", "zero-net"} {
		t.Run(operation, func(t *testing.T) {
			editor := NewEditor("a\nb")
			if !editor.TrackLFLabels([]byte{'M'}, 'M', nil) {
				t.Fatal("label setup failed")
			}
			if _, _, ok := editor.TrackHistory(); !ok {
				t.Fatal("producer setup failed")
			}
			switch operation {
			case "direct":
				editor.ReplaceRange(Range{End: editor.RuneCount()}, editor.String())
			case "batch":
				if err := editor.ReplaceRanges([]Replacement{{Range: Range{End: editor.RuneCount()}, Text: editor.String()}}); err != nil {
					t.Fatal(err)
				}
			default:
				if err := editor.Transact(func(draft *Editor) error {
					if operation == "zero-net" {
						draft.ReplaceRange(Range{End: draft.RuneCount()}, "other\ntext")
					}
					draft.ReplaceRange(Range{End: draft.RuneCount()}, "a\nb")
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			if !editor.CanUndo() || !editor.SetLFLabels([]byte{'W'}, 'W') || !editor.Undo() || editor.String() != "a\nb" || editor.CanUndo() {
				t.Fatal("producer physical equal unit lost Undo or relabel")
			}
			assertLFLabels(t, editor, []byte{'M'})
			if !editor.Redo() || editor.String() != "a\nb" {
				t.Fatal("producer equal-value unit lost Redo")
			}
			assertLFLabels(t, editor, []byte{'W'})
		})
	}
}

func TestLFLabelIndependentReplacementSealsTentativeOwner(t *testing.T) {
	editor, _ := lfLabelIntegrationEditor(t, true, true)
	editor.SetSelection(Range{Start: 1, End: 2})
	compositionApply(t, editor, compositionUpdate("x\n", Range{}))
	editor.ReplaceRange(Range{Start: editor.RuneCount(), End: editor.RuneCount()}, "tail\n")
	if editor.CompositionActive() || editor.String() != "ax\nb\nctail\n" {
		t.Fatal("independent replacement left a live tentative owner")
	}
	assertLFLabels(t, editor, []byte{'M', 'L', 'M'})
	compositionApply(t, editor, CompositionEvent{Phase: CompositionCancel})
	if editor.String() != "ax\nb\nctail\n" || !editor.Undo() || editor.String() != "ax\nb\nc" {
		t.Fatal("late Cancel consumed an independent accepted replacement")
	}
	assertLFLabels(t, editor, []byte{'M', 'L'})
}

func TestLFLabelTentativeEqualResultKeepsSeparateIdentity(t *testing.T) {
	editor := NewEditor("a\nb")
	if !editor.TrackLFLabels([]byte{'M'}, 'M', nil) || !editor.EnableTentativeComposition() {
		t.Fatal("label/tentative setup failed")
	}
	editor.SetSelection(Range{Start: 1, End: 2})
	compositionApply(t, editor, CompositionEvent{Phase: CompositionResult, Text: "\n"})
	if !editor.CanUndo() || !editor.SetLFLabels([]byte{'W'}, 'M') || !editor.Undo() {
		t.Fatal("equal Result lost its physical unit")
	}
	assertLFLabels(t, editor, []byte{'M'})
	if !editor.Redo() {
		t.Fatal("equal Result lost Redo")
	}
	assertLFLabels(t, editor, []byte{'W'})
}

func TestLFLabelTentativeSavedGrowthAccountsForCoalescedCapacity(t *testing.T) {
	for _, saved := range []bool{false, true} {
		t.Run(fmt.Sprintf("saved-%t", saved), func(t *testing.T) {
			editor := NewEditor("")
			if !editor.TrackLFLabels(nil, 'M', nil) || !editor.EnableTentativeComposition() {
				t.Fatal("label/tentative setup failed")
			}
			if saved {
				editor.SetSavePoint()
			}
			event := compositionUpdate(strings.Repeat("\n", 1025), Range{})
			plan, ok := editor.planCompositionEvent(editor.compositionState(), event, []rune(event.Text))
			if !ok {
				t.Fatal("small saved LF event refused")
			}
			compositionApply(t, editor, event)
			actual := 0
			for index := editor.compositionState().eventStart; index < len(editor.history); index++ {
				actual += editor.lfLabelState().ops.cost(editor.lfLabelState(), lfLabelHistoryKey(&editor.history[index]))
			}
			if plan.labels < actual {
				t.Fatalf("coalesced LF capacity underestimated: plan=%d actual=%d", plan.labels, actual)
			}
			compositionApply(t, editor, CompositionEvent{Phase: CompositionCancel})
			if editor.String() != "" || editor.CanUndo() || editor.historyRunes != 0 {
				t.Fatal("saved LF event failed to release provisional history")
			}
		})
	}
}

func TestLFLabelTentativeSavedGrowthRejectsOversizedPhysicalEvent(t *testing.T) {
	editor := NewEditor("")
	if !editor.TrackLFLabels(nil, 'M', nil) || !editor.EnableTentativeComposition() {
		t.Fatal("label/tentative setup failed")
	}
	editor.SetSavePoint()
	event := compositionUpdate(strings.Repeat("\n", 187831), Range{})
	// Inspect preflight directly so the regression never executes a known
	// oversized scalar loop when the guard is broken.
	if plan, ok := editor.planCompositionEvent(editor.compositionState(), event, []rune(event.Text)); ok {
		t.Fatalf("oversized saved LF event accepted by preflight: %+v", plan)
	}
}

func TestLFLabelCodeTransformRefusalPreservesTentativeOwner(t *testing.T) {
	editor, _ := lfLabelIntegrationEditor(t, true, true)
	editor.SetSelection(Range{Start: 1, End: 2})
	compositionApply(t, editor, compositionUpdate("x\n", Range{}))
	before, selection, cost := editor.String(), editor.Selection(), editor.historyCost()
	editor.IndentLinesCode(CodeOptions{})
	if !editor.CompositionActive() || editor.String() != before || editor.Selection() != selection || editor.historyCost() != cost {
		t.Fatal("refused code transform changed its tentative owner")
	}
	compositionApply(t, editor, CompositionEvent{Phase: CompositionCancel})
	if editor.String() != "ab\nc" {
		t.Fatal("refused code transform damaged tentative cancellation")
	}
	assertLFLabels(t, editor, []byte{'L'})
}

func TestLFLabelRejectedIndependentEditPreservesTentativeOwner(t *testing.T) {
	reject := false
	editor := NewEditor("a\nb\nc")
	if !editor.TrackLFLabels([]byte{'W', 'L'}, 'M', func(change LFLabelReplacement) []byte {
		if reject {
			return nil
		}
		return bytesOfLabel(change.Default, strings.Count(change.Inserted, "\n"))
	}) || !editor.EnableTentativeComposition() {
		t.Fatal("label/tentative setup failed")
	}
	editor.SetSelection(Range{Start: 1, End: 2})
	compositionApply(t, editor, compositionUpdate("x\n", Range{End: 2}))
	before, selection, cost := editor.String(), editor.Selection(), editor.historyCost()
	reject = true
	panicked := false
	func() {
		defer func() { panicked = recover() != nil }()
		editor.ReplaceRange(Range{Start: editor.RuneCount(), End: editor.RuneCount()}, "tail\n")
	}()
	if !panicked || !editor.CompositionActive() || editor.String() != before || editor.Selection() != selection || editor.historyCost() != cost {
		t.Fatal("rejected mapper changed source or sealed its tentative owner")
	}
	compositionApply(t, editor, CompositionEvent{Phase: CompositionCancel})
	if editor.String() != "ab\nc" {
		t.Fatal("rejected edit damaged tentative cancellation")
	}
	assertLFLabels(t, editor, []byte{'L'})
}
