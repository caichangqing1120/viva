//go:build linux && vivagtk

package app

import (
	"testing"

	"github.com/vivaui/viva/internal/platform"
	"github.com/vivaui/viva/text"
)

func TestGTKTextSnapshotDistinguishesIdenticalEditors(t *testing.T) {
	var editor text.Editor
	first := platformTextState(TextStateOf(&editor)).InputOwner
	if first == (platform.TextInputOwner{}) {
		t.Fatal("zero-value editor did not receive an owner")
	}
	if next := platformTextState(TextStateOf(&editor)).InputOwner; next != first {
		t.Fatal("a refresh changed the native owner")
	}
	other := text.NewEditor("")
	if next := platformTextState(TextStateOf(other)).InputOwner; next == first || next == (platform.TextInputOwner{}) {
		t.Fatal("an identical replacement editor did not receive its own native owner")
	}
}
