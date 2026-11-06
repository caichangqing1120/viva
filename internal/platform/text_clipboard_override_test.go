package platform

import (
	"errors"
	"testing"

	"github.com/vivaui/viva/text"
)

func TestTextInputClipboardStateUsesExplicitCopyOverride(t *testing.T) {
	input := TextInput{State: TextState{Value: "abc", Selection: text.Range{Start: 1, End: 1}}, CopyOverride: true, CopyText: "one\ntwo"}
	state := input.ClipboardState()
	if state.Value != "one\ntwo" || state.Selection != (text.Range{Start: 0, End: 7}) {
		t.Fatalf("override state = %+v", state)
	}
	input.CopyOverride = false
	if state = input.ClipboardState(); state != input.State {
		t.Fatalf("ordinary state changed: %+v", state)
	}
	input.CopyOverride = true
	problem := errors.New("clipboard busy")
	if _, changed, err := TextClipboardEvent(input.ClipboardState(), false, 'x', nil, func(string) error { return problem }); changed || !errors.Is(err, problem) {
		t.Fatalf("failed custom cut changed editor: changed=%v err=%v", changed, err)
	}
	if _, changed, err := TextClipboardEvent(input.ClipboardState(), true, 'c', nil, func(string) error { t.Fatal("secure copy"); return nil }); changed || err != nil {
		t.Fatalf("secure override copied: changed=%v err=%v", changed, err)
	}
}
