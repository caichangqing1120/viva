package components

import (
	"strings"
	"testing"

	"github.com/vivaui/viva/gfx"
)

func changeFixture(value string) *codeFixture {
	fixture := newCodeFixture(value)
	fixture.bounds.Width = fixture.widthFor(12)
	fixture.bounds.Height = 600
	fixture.snapshot.ChangeColors = [4]gfx.Color{
		gfx.RGB(200, 120, 40), // reverted
		gfx.RGB(120, 160, 60), // saved
		gfx.RGB(60, 100, 200), // modified
		gfx.RGB(160, 60, 160), // reverted to modified
	}
	fixture.snapshot.ChangeDeletionColor = gfx.RGB(220, 40, 40)
	return fixture
}

// H05: the change margin must respect the editor's geometry and never change the
// document or layout of the text area.
func TestCodeEditorChangeDecorationsRespectGeometry(t *testing.T) {
	t.Run("bars use the dedicated lane and keep text in place", func(t *testing.T) {
		fixture := changeFixture("one\ntwo\nthree\n")
		fixture.snapshot.LineNumbers = true
		fixture.paint()
		contentWithoutLane := fixture.editor.content
		fixture.snapshot.ChangeLines = []CodeEditorChangeLine{{Line: 1, Kind: ChangeModified}}
		fixture.paint()
		if fixture.editor.content.X <= contentWithoutLane.X {
			t.Fatalf("change lane did not widen the gutter: %v -> %v", contentWithoutLane.X, fixture.editor.content.X)
		}
		bars := fixture.fills(fixture.snapshot.ChangeColors[ChangeModified-1])
		if len(bars) != 1 {
			t.Fatalf("modified bar count = %d", len(bars))
		}
		_, _, lineHeight := fixture.cell()
		if !near(bars[0].Height, lineHeight) || bars[0].X+bars[0].Width >= fixture.editor.content.X {
			t.Fatalf("bar geometry = %+v, content %+v", bars[0], fixture.editor.content)
		}
	})

	t.Run("wrapped line paints once on its first row", func(t *testing.T) {
		fixture := changeFixture("a very long first line that wraps\nsecond\nthird")
		fixture.snapshot.Wrap = true
		fixture.snapshot.ChangeLines = []CodeEditorChangeLine{{Line: 0, Kind: ChangeSaved}}
		fixture.paint()
		if bars := fixture.fills(fixture.snapshot.ChangeColors[ChangeSaved-1]); len(bars) != 1 {
			t.Fatalf("wrapped line painted %d bars", len(bars))
		}
	})

	t.Run("hidden fold rows are not painted", func(t *testing.T) {
		fixture := changeFixture("zero\none\ntwo\nthree\nfour\n")
		fixture.snapshot.FoldedLines = []CodeEditorFoldRange{{Header: 1, LastLine: 3}}
		fixture.snapshot.ChangeLines = []CodeEditorChangeLine{
			{Line: 1, Kind: ChangeModified},
			{Line: 2, Kind: ChangeModified},
			{Line: 4, Kind: ChangeModified},
		}
		fixture.paint()
		if bars := fixture.fills(fixture.snapshot.ChangeColors[ChangeModified-1]); len(bars) != 2 {
			t.Fatalf("folded decoration painted %d bars, want the header and the visible line", len(bars))
		}
	})

	t.Run("vertical scroll moves bars with their rows", func(t *testing.T) {
		fixture := changeFixture(strings.Repeat("row\n", 60))
		fixture.snapshot.ChangeLines = []CodeEditorChangeLine{{Line: 5, Kind: ChangeModified}}
		fixture.paint()
		before := fixture.fills(fixture.snapshot.ChangeColors[ChangeModified-1])
		if len(before) != 1 {
			t.Fatalf("precondition bars = %d", len(before))
		}
		_, _, lineHeight := fixture.cell()
		fixture.editor.scrollY = lineHeight
		fixture.paint()
		after := fixture.fills(fixture.snapshot.ChangeColors[ChangeModified-1])
		if len(after) != 1 {
			t.Fatalf("scrolled bars = %d", len(after))
		}
		if !near(after[0].Y, before[0].Y-lineHeight) || !near(after[0].X, before[0].X) {
			t.Fatalf("bar did not follow the row: %+v -> %+v", before[0], after[0])
		}
	})

	t.Run("line numbers toggle keeps the lane", func(t *testing.T) {
		fixture := changeFixture("one\ntwo\n")
		fixture.snapshot.LineNumbers = true
		fixture.snapshot.ChangeLines = []CodeEditorChangeLine{{Line: 1, Kind: ChangeReverted}}
		fixture.paint()
		withNumbers := fixture.fills(fixture.snapshot.ChangeColors[ChangeReverted-1])
		fixture.snapshot.LineNumbers = false
		fixture.paint()
		withoutNumbers := fixture.fills(fixture.snapshot.ChangeColors[ChangeReverted-1])
		if len(withNumbers) != 1 || len(withoutNumbers) != 1 {
			t.Fatalf("lane bars with/without line numbers = %d/%d", len(withNumbers), len(withoutNumbers))
		}
		if !near(withNumbers[0].X, withoutNumbers[0].X) {
			t.Fatalf("hiding line numbers moved the change lane: %+v -> %+v", withNumbers[0], withoutNumbers[0])
		}
	})

	t.Run("custom lane width is honoured", func(t *testing.T) {
		fixture := changeFixture("one\ntwo\n")
		fixture.snapshot.ChangeLines = []CodeEditorChangeLine{{Line: 0, Kind: ChangeModified}}
		fixture.snapshot.Gutter = &CodeEditorGutter{Change: 14, Marker: 8, Bookmark: 8, LineNumberPadding: 4, Fold: 16}
		fixture.paint()
		bars := fixture.fills(fixture.snapshot.ChangeColors[ChangeModified-1])
		if len(bars) != 1 || !near(bars[0].Width, 12) {
			t.Fatalf("custom lane bar = %+v, want a 12 px bar inside the 14 px lane", bars)
		}
	})

	t.Run("deletion points sit at the top and bottom of the line", func(t *testing.T) {
		fixture := changeFixture("one\ntwo\n")
		fixture.snapshot.ChangeLines = []CodeEditorChangeLine{
			{Line: 0, DeletedBelow: true},
			{Line: 1, DeletedAbove: true},
		}
		fixture.paint()
		points := fixture.fills(fixture.snapshot.ChangeDeletionColor)
		if len(points) != 2 {
			t.Fatalf("deletion points = %d", len(points))
		}
		_, _, lineHeight := fixture.cell()
		top, bottom := points[0], points[1]
		if top.Y > bottom.Y {
			top, bottom = bottom, top
		}
		if !near(bottom.Y+lineHeight, top.Y+lineHeight) && bottom.Y < top.Y {
			t.Fatalf("deletion points are not ordered: %+v %+v", top, bottom)
		}
		if !near(top.Y+top.Height, fixture.editor.content.Y+lineHeight) {
			t.Fatalf("bottom deletion point is not at the line's bottom: %+v", top)
		}
		if !near(bottom.Y, fixture.editor.content.Y+lineHeight) {
			t.Fatalf("top deletion point of the next line is not at its top: %+v", bottom)
		}
	})

	t.Run("decorations are visual only and idempotent", func(t *testing.T) {
		fixture := changeFixture("one\ntwo\nthree\n")
		fixture.paint()
		before := fixture.editor.content
		fixture.snapshot.ChangeLines = []CodeEditorChangeLine{
			{Line: 0, Kind: ChangeReverted, DeletedBelow: true},
			{Line: 1, Kind: ChangeModified},
			{Line: 2, Kind: ChangeSaved, DeletedAbove: true},
		}
		fixture.paint()
		first := fixture.fills(fixture.snapshot.ChangeColors[ChangeModified-1])
		if len(first) != 1 {
			t.Fatalf("modified band = %d", len(first))
		}
		laneWidth := fixture.editor.content.X - before.X
		if laneWidth <= 0 || !near(before.Width-fixture.editor.content.Width, laneWidth) {
			t.Fatalf("text area changed by more than the change lane: %+v -> %+v", before, fixture.editor.content)
		}
		// A second paint must produce exactly the same decoration geometry: the
		// paint is a pure function of the snapshot.
		fixture.paint()
		second := fixture.fills(fixture.snapshot.ChangeColors[ChangeModified-1])
		if len(second) != len(first) || !near(second[0].X, first[0].X) || !near(second[0].Y, first[0].Y) {
			t.Fatalf("decoration paint is not idempotent: %+v -> %+v", first, second)
		}
	})
}
