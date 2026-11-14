package text

import "testing"

func TestCompositionIdentityTracksOnlyAnEnabledCapability(t *testing.T) {
	var absent *Editor
	first, second := NewEditor("same"), NewEditor("same")
	if absent.TentativeCompositionIdentity() != (CompositionIdentity{}) || first.TentativeCompositionIdentity() != (CompositionIdentity{}) || first.journal != nil {
		t.Fatal("reading default identity enabled or retained optional state")
	}
	if !first.EnableTentativeComposition() || !second.EnableTentativeComposition() {
		t.Fatal("capability setup refused")
	}
	identity := first.TentativeCompositionIdentity()
	if identity == (CompositionIdentity{}) || identity == second.TentativeCompositionIdentity() {
		t.Fatal("different enabled capabilities have no distinct identities")
	}
	compositionApply(t, first, CompositionEvent{Phase: CompositionUpdate, Text: "x"})
	before, selection := first.String(), first.Selection()
	if allocations := testing.AllocsPerRun(100, func() {
		if first.TentativeCompositionIdentity() != identity {
			t.Fatal("an active snapshot changed ownership")
		}
	}); allocations != 0 {
		t.Fatalf("identity snapshots allocate: %v", allocations)
	}
	if first.String() != before || first.Selection() != selection || !first.CompositionActive() {
		t.Fatal("identity lookup changed the live composition")
	}
	compositionApply(t, first, CompositionEvent{Phase: CompositionCancel})
	first.ClearHistory()
	if first.TentativeCompositionIdentity() != identity {
		t.Fatal("clearing history changed the enabled editor's ownership")
	}
	if !first.DisableTentativeComposition() || first.TentativeCompositionIdentity() != (CompositionIdentity{}) {
		t.Fatal("disabled capability retained a visible identity")
	}
}
