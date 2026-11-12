package platform

import (
	"strings"
	"testing"

	"github.com/vivaui/viva/text"
)

// The added provenance fields are only meaningful when the target editor has
// opted into tentative composition. A zero phase or a disabled editor keeps the
// complete legacy path, including eager rune replacement targeting.
func TestTextEventApplyTaggedCompositionRoutesOnlyWithCapability(t *testing.T) {
	for _, scenario := range []struct {
		name    string
		enabled bool
		phase   text.CompositionPhase
	}{
		{name: "disabled-tagged", phase: text.CompositionResult},
		{name: "disabled-zero"},
		{name: "enabled-zero", enabled: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			editor := text.NewEditor("abc")
			if scenario.enabled && !editor.EnableTentativeComposition() {
				t.Fatal("capability setup refused")
			}
			TextEvent{
				Kind: TextInsert, Text: "X", CompositionPhase: scenario.phase,
				HasReplacement: true, Replacement: text.Range{Start: 1, End: 2},
				// The raw form is deliberately invalid so a fallback that
				// accidentally used it would be visible.
				HasReplacementUTF16: true, ReplacementUTF16: text.UTF16Range{Start: 99, Length: 99},
			}.Apply(editor)
			if got := editor.String(); got != "aXc" {
				t.Fatalf("legacy rune targeting = %q, want aXc", got)
			}
			if editor.TentativeCompositionEnabled() != scenario.enabled {
				t.Fatalf("capability = %v, want %v", editor.TentativeCompositionEnabled(), scenario.enabled)
			}
			if EditorState(editor).TentativeComposition != scenario.enabled {
				t.Fatalf("state capability = %v, want %v", EditorState(editor).TentativeComposition, scenario.enabled)
			}
		})
	}
}

// An enabled tagged event is applied exactly once through ApplyComposition,
// including the raw UTF-16 replacement conversion after rollback.
func TestTextEventApplyTaggedCompositionForwardsUpdateAndResult(t *testing.T) {
	editor := text.NewEditor("abcd")
	if !editor.EnableTentativeComposition() {
		t.Fatal("capability setup refused")
	}
	cursor, _, ok := editor.TrackHistory()
	if !ok {
		t.Fatal("history setup refused")
	}
	editor.SetSelection(text.Range{Start: 1, End: 2})
	TextEvent{
		Kind: TextSetMarked, CompositionPhase: text.CompositionUpdate, Text: "é𝄞",
		MarkedSelection: text.Range{Start: 2, End: 2},
	}.Apply(editor)
	if got := editor.String(); got != "aé𝄞cd" {
		t.Fatalf("tagged Update = %q, want aé𝄞cd", got)
	}
	state := EditorState(editor)
	if !state.Composing || !state.TentativeComposition {
		t.Fatalf("tagged Update state = %+v, want live composing capability", state)
	}
	frames, cursor, ok := editor.HistorySince(cursor)
	if !ok || len(frames) != 1 || len(frames[0].Steps) != 3 {
		t.Fatalf("tagged Update frames = %d, want one complete three-scalar frame", len(frames))
	}

	TextEvent{
		Kind: TextInsert, CompositionPhase: text.CompositionResult, Text: "r",
		HasReplacement: true, Replacement: text.Range{Start: 0, End: 1},
		HasReplacementUTF16: true, ReplacementUTF16: text.UTF16Range{Start: 5, Length: 1},
	}.Apply(editor)
	if got := editor.String(); got != "acdr" {
		t.Fatalf("tagged raw Result = %q, want acdr", got)
	}
	state = EditorState(editor)
	if state.Composing || !state.TentativeComposition {
		t.Fatalf("tagged Result state = %+v, want idle enabled capability", state)
	}
	frames, _, ok = editor.HistorySince(cursor)
	if !ok || len(frames) != 1 || len(frames[0].Steps) != 3 ||
		frames[0].BeforeBytes != 9 || frames[0].AfterBytes != 4 {
		t.Fatalf("tagged Result frames = %+v, want one atomic post-rollback frame", frames)
	}
}

// Commit, Cancel and EmptyCommit each consume the live tentative frame once and
// never replay the event text through the legacy switch.
func TestTextEventApplyTaggedCompositionTerminalPhases(t *testing.T) {
	for _, scenario := range []struct {
		name  string
		phase text.CompositionPhase
		want  string
	}{
		{name: "commit", phase: text.CompositionCommit, want: "abcx"},
		{name: "cancel", phase: text.CompositionCancel, want: "abc"},
		{name: "empty-commit", phase: text.CompositionEmptyCommit, want: "abc"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			editor := text.NewEditor("abc")
			if !editor.EnableTentativeComposition() {
				t.Fatal("capability setup refused")
			}
			TextEvent{Kind: TextSetMarked, CompositionPhase: text.CompositionUpdate, Text: "x"}.Apply(editor)
			TextEvent{Kind: TextInsert, CompositionPhase: scenario.phase, Text: "ignored"}.Apply(editor)
			if got := editor.String(); got != scenario.want {
				t.Fatalf("%v = %q, want %q", scenario.phase, got, scenario.want)
			}
			state := EditorState(editor)
			if state.Composing || !state.TentativeComposition {
				t.Fatalf("%v state = %+v, want idle enabled capability", scenario.phase, state)
			}
		})
	}
}

// A rejected tagged event returns through the composition branch and never
// retries the legacy mutation, including invalid phases, invalid raw ranges and
// hard-capacity refusal.
func TestTextEventApplyTaggedCompositionRejectsWithoutFallback(t *testing.T) {
	editor := text.NewEditor("abc")
	if !editor.EnableTentativeComposition() {
		t.Fatal("capability setup refused")
	}
	editor.SetSelection(text.Range{Start: 1, End: 1})
	before := EditorState(editor)
	cursor, _, ok := editor.TrackHistory()
	if !ok {
		t.Fatal("history setup refused")
	}
	oversized := strings.Repeat("x", 4*1024*1024+1)
	for name, event := range map[string]TextEvent{
		"invalid phase": {Kind: TextInsert, Text: "BAD", CompositionPhase: text.CompositionPhase(255), HasReplacement: true, Replacement: text.Range{Start: 0, End: 3}},
		"negative raw":  {Kind: TextInsert, Text: "BAD", CompositionPhase: text.CompositionResult, HasReplacementUTF16: true, ReplacementUTF16: text.UTF16Range{Start: -1}},
		"capacity":      {Kind: TextInsert, Text: oversized, CompositionPhase: text.CompositionUpdate},
	} {
		t.Run(name, func(t *testing.T) {
			event.Apply(editor)
			if got := editor.String(); got != "abc" {
				t.Fatalf("rejected event changed text (len %d, want 3)", len(got))
			}
			if state := EditorState(editor); state != before {
				t.Fatalf("rejected event state = %+v, want %+v", state, before)
			}
			if editor.CanUndo() {
				t.Fatal("rejected event left an Undo unit")
			}
			if frames, latest, ok := editor.HistorySince(cursor); !ok || len(frames) != 0 || latest != cursor {
				t.Fatal("rejected event changed history metadata")
			}
		})
	}
}

// The capability snapshot is derived only from the editor and stays distinct
// from the current UI composing flag, including a passive disabled owner.
func TestEditorStateTentativeCompositionSnapshot(t *testing.T) {
	editor := text.NewEditor("abc")
	if EditorState(editor).TentativeComposition {
		t.Fatal("default editor reported the opt-in capability")
	}
	if !editor.EnableTentativeComposition() || !EditorState(editor).TentativeComposition {
		t.Fatal("capability snapshot did not follow opt-in")
	}
	TextEvent{Kind: TextSetMarked, CompositionPhase: text.CompositionUpdate, Text: "x"}.Apply(editor)
	if state := EditorState(editor); !state.Composing || !state.TentativeComposition {
		t.Fatalf("live state = %+v, want composing enabled", state)
	}
	TextEvent{Kind: TextInsert, CompositionPhase: text.CompositionCommit}.Apply(editor)
	if state := EditorState(editor); state.Composing || !state.TentativeComposition {
		t.Fatalf("idle state = %+v, want idle enabled", state)
	}
	if !editor.DisableTentativeComposition() {
		t.Fatal("disable refused")
	}
	if state := EditorState(editor); state.Composing || state.TentativeComposition {
		t.Fatalf("passive disabled state = %+v, want no capability", state)
	}
}
