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
// document, the caret or hit-to-byte behaviour.
func TestCodeEditorChangeDecorationsRespectGeometry(t *testing.T) {
	t.Run("bars use the dedicated lane and keep text in place", func(t *testing.T) {
		fixture := changeFixture("one\ntwo\nthree\n")
		fixture.snapshot.LineNumbers = true
		fixture.paint()
		contentWithoutLane := fixture.editor.content
		fixture.snapshot.ChangeLines = []CodeEditorChangeLine{{Line: 1, Kind: ChangeModified}}
		fixture.paint()
		laneWidth := fixture.editor.content.X - contentWithoutLane.X
		if laneWidth <= 0 || !near(contentWithoutLane.Width-fixture.editor.content.Width, laneWidth) {
			t.Fatalf("change lane delta = %v, content %+v -> %+v", laneWidth, contentWithoutLane, fixture.editor.content)
		}
		bars := fixture.fills(fixture.snapshot.ChangeColors[ChangeModified-1])
		if len(bars) != 1 {
			t.Fatalf("modified bar count = %d", len(bars))
		}
		_, _, lineHeight := fixture.cell()
		row := fixture.rowOfLine(1)
		if row < 0 {
			t.Fatal("line 1 is not visible")
		}
		wantY := fixture.editor.content.Y + float32(row)*lineHeight
		if !near(bars[0].Y, wantY) || !near(bars[0].Height, lineHeight) {
			t.Fatalf("bar = %+v, want y=%v height=%v", bars[0], wantY, lineHeight)
		}
		laneOrigin := fixture.bounds.X + fixture.theme.Metrics.TextInsetX()
		if bars[0].X < laneOrigin || bars[0].X > laneOrigin+8 {
			t.Fatalf("bar x = %v, want the leftmost lane at %v", bars[0].X, laneOrigin)
		}
		if bars[0].X+bars[0].Width >= fixture.editor.content.X {
			t.Fatalf("bar overlaps code: %+v, content %+v", bars[0], fixture.editor.content)
		}
	})

	t.Run("wrapped line paints its bar on the first row", func(t *testing.T) {
		fixture := changeFixture("a very long first line that wraps and wraps\nsecond\n")
		fixture.snapshot.Wrap = true
		fixture.snapshot.ChangeLines = []CodeEditorChangeLine{{Line: 0, Kind: ChangeSaved}}
		fixture.paint()
		bars := fixture.fills(fixture.snapshot.ChangeColors[ChangeSaved-1])
		if len(bars) != 1 {
			t.Fatalf("wrapped line painted %d bars", len(bars))
		}
		_, _, lineHeight := fixture.cell()
		firstRow := fixture.rowOfLine(0)
		if !near(bars[0].Y, fixture.editor.content.Y+float32(firstRow)*lineHeight) {
			t.Fatalf("wrapped bar y = %v, want the first row %v", bars[0].Y, firstRow)
		}
		lastRow := fixture.lastRowOfLine(0)
		if lastRow <= firstRow {
			t.Fatalf("fixture did not wrap: first %d last %d", firstRow, lastRow)
		}
	})

	t.Run("wrapped line keeps its bottom deletion point on the last row", func(t *testing.T) {
		fixture := changeFixture("a very long first line that wraps and wraps\nsecond\n")
		fixture.snapshot.Wrap = true
		fixture.snapshot.ChangeLines = []CodeEditorChangeLine{{Line: 0, DeletedBelow: true}}
		fixture.paint()
		points := fixture.fills(fixture.snapshot.ChangeDeletionColor)
		if len(points) != 1 {
			t.Fatalf("deletion points = %d", len(points))
		}
		_, _, lineHeight := fixture.cell()
		lastRow := fixture.lastRowOfLine(0)
		wantY := fixture.editor.content.Y + float32(lastRow)*lineHeight + lineHeight - points[0].Height
		if !near(points[0].Y, wantY) {
			t.Fatalf("bottom point y = %v, want %v (last row %d)", points[0].Y, wantY, lastRow)
		}
		// Scroll the first row out of view; the last row and its point stay.
		fixture.editor.scrollY = lineHeight
		fixture.paint()
		if after := fixture.fills(fixture.snapshot.ChangeDeletionColor); len(after) != 1 {
			t.Fatalf("scrolled bottom point = %d", len(after))
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

	t.Run("horizontal scroll leaves the lane in place", func(t *testing.T) {
		fixture := changeFixture(strings.Repeat("x", 200) + "\ntwo\n")
		fixture.snapshot.HorizontalScrollbarID = gfx.Item(9)
		fixture.snapshot.ChangeLines = []CodeEditorChangeLine{{Line: 0, Kind: ChangeModified}}
		fixture.paint()
		before := fixture.fills(fixture.snapshot.ChangeColors[ChangeModified-1])
		fixture.editor.scrollX = 40
		fixture.paint()
		after := fixture.fills(fixture.snapshot.ChangeColors[ChangeModified-1])
		if len(before) != 1 || len(after) != 1 || !near(before[0].X, after[0].X) {
			t.Fatalf("horizontal scroll moved the lane: %+v -> %+v", before, after)
		}
	})

	t.Run("lane width follows the font metrics", func(t *testing.T) {
		small := changeFixture("one\ntwo\n")
		small.snapshot.FontSize = 11
		small.snapshot.ChangeLines = []CodeEditorChangeLine{{Line: 0, Kind: ChangeModified}}
		small.paint()
		large := changeFixture("one\ntwo\n")
		large.snapshot.FontSize = 22
		large.snapshot.ChangeLines = []CodeEditorChangeLine{{Line: 0, Kind: ChangeModified}}
		large.paint()
		smallBar, largeBar := small.fills(small.snapshot.ChangeColors[ChangeModified-1]), large.fills(large.snapshot.ChangeColors[ChangeModified-1])
		if len(smallBar) != 1 || len(largeBar) != 1 {
			t.Fatalf("font metrics bars = %d/%d", len(smallBar), len(largeBar))
		}
		smallLane := small.editor.content.X - (small.bounds.X + small.theme.Metrics.TextInsetX())
		largeLane := large.editor.content.X - (large.bounds.X + large.theme.Metrics.TextInsetX())
		if largeLane <= smallLane || largeBar[0].Width < smallBar[0].Width {
			t.Fatalf("lane did not scale with the font: %v/%v bars %+v/%+v", smallLane, largeLane, smallBar[0], largeBar[0])
		}
	})

	t.Run("split views decorate independently", func(t *testing.T) {
		left := changeFixture("one\ntwo\n")
		left.snapshot.ChangeLines = []CodeEditorChangeLine{{Line: 0, Kind: ChangeModified}}
		left.paint()
		right := changeFixture("one\ntwo\n")
		right.snapshot.ChangeLines = []CodeEditorChangeLine{{Line: 1, Kind: ChangeSaved}}
		right.paint()
		if bars := left.fills(left.snapshot.ChangeColors[ChangeModified-1]); len(bars) != 1 {
			t.Fatalf("left bars = %d", len(bars))
		}
		if bars := right.fills(right.snapshot.ChangeColors[ChangeSaved-1]); len(bars) != 1 {
			t.Fatalf("right bars = %d", len(bars))
		}
		if bars := left.fills(left.snapshot.ChangeColors[ChangeSaved-1]); len(bars) != 0 {
			t.Fatalf("left view painted the right view's decoration")
		}
	})

	t.Run("FoldLast keeps the change lane leftmost", func(t *testing.T) {
		fixture := changeFixture("one\ntwo\n")
		fixture.snapshot.FoldGutter = true
		fixture.snapshot.LineNumbers = true
		fixture.snapshot.ChangeLines = []CodeEditorChangeLine{{Line: 0, Kind: ChangeModified}}
		fixture.snapshot.Gutter = &CodeEditorGutter{Change: 6, Marker: 8, Bookmark: 8, Fold: 16, LineNumberPadding: 4, FoldLast: true}
		fixture.paint()
		bars := fixture.fills(fixture.snapshot.ChangeColors[ChangeModified-1])
		if len(bars) != 1 {
			t.Fatalf("bars = %d", len(bars))
		}
		// This custom gutter fixes the inset at zero, so the lane starts at bounds.
		laneOrigin := fixture.bounds.X + fixture.snapshot.Gutter.Inset
		if bars[0].X < laneOrigin || bars[0].X+bars[0].Width > laneOrigin+6 {
			t.Fatalf("change lane is not leftmost with FoldLast: %+v (lane %v)", bars[0], laneOrigin)
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
		if len(bars) != 1 || bars[0].Width <= 0 || bars[0].Width >= 14 {
			t.Fatalf("custom lane bar = %+v, want a bar inside the 14 px lane", bars)
		}
	})

	t.Run("narrow custom lanes never paint inside a negative or zero width", func(t *testing.T) {
		for _, width := range []float32{1, 2, 3} {
			fixture := changeFixture("one\ntwo\n")
			fixture.snapshot.ChangeLines = []CodeEditorChangeLine{{Line: 0, Kind: ChangeModified, DeletedBelow: true}}
			fixture.snapshot.Gutter = &CodeEditorGutter{Change: width, Marker: 8, Bookmark: 8, LineNumberPadding: 4, Fold: 16}
			fixture.paint()
			for _, rectangle := range fixture.canvas.Rectangles() {
				if rectangle.Bounds.Width < 0 || rectangle.Bounds.Height < 0 {
					t.Fatalf("lane width %v painted a negative rectangle: %+v", width, rectangle.Bounds)
				}
			}
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
		// First line's bottom point ends at the first row's bottom; the second
		// line's top point starts at the second row's top: the same coordinate.
		firstRow := fixture.rowOfLine(0)
		rowY := fixture.editor.content.Y + float32(firstRow)*lineHeight
		if !near(top.Y+top.Height, rowY+lineHeight) || !near(bottom.Y, rowY+lineHeight) {
			t.Fatalf("deletion points are not adjacent to the row boundary: %+v %+v (row y %v)", top, bottom, rowY)
		}
	})

	t.Run("unknown kinds paint no bar but keep the deletion points", func(t *testing.T) {
		for _, kind := range []CodeEditorChangeKind{ChangeNone, CodeEditorChangeKind(5), CodeEditorChangeKind(255)} {
			fixture := changeFixture("one\ntwo\n")
			fixture.snapshot.ChangeLines = []CodeEditorChangeLine{{Line: 0, Kind: kind, DeletedAbove: true}}
			fixture.paint() // must not panic
			barColor := fixture.snapshot.ChangeColors[0]
			if bars := fixture.fills(barColor); len(bars) != 0 {
				t.Fatalf("kind %d painted a bar: %+v", kind, bars)
			}
			if points := fixture.fills(fixture.snapshot.ChangeDeletionColor); len(points) != 1 {
				t.Fatalf("kind %d lost its deletion point: %d", kind, len(points))
			}
		}
	})

	t.Run("decorations are a pure function of the snapshot", func(t *testing.T) {
		fixture := changeFixture("one\ntwo\nthree\n")
		fixture.paint()
		content := fixture.editor.content
		pointer := gfx.PointerEvent{Kind: gfx.PointerDown, Button: gfx.PointerButtonPrimary, X: fixture.editor.content.X + 8, Y: fixture.editor.content.Y + 6}
		before := fixture.editor.Pointer(pointer)
		fixture.snapshot.ChangeLines = []CodeEditorChangeLine{
			{Line: 0, Kind: ChangeReverted, DeletedBelow: true},
			{Line: 1, Kind: ChangeModified},
			{Line: 2, Kind: ChangeSaved, DeletedAbove: true},
		}
		fixture.paint()
		first := fixture.fills(fixture.snapshot.ChangeColors[ChangeModified-1])
		if after := fixture.editor.Pointer(pointer); after != before {
			t.Fatalf("decorations changed hit-to-byte: %+v -> %+v", before, after)
		}
		if fixture.editor.content.Width >= content.Width || !near(content.Width-fixture.editor.content.Width, fixture.editor.content.X-content.X) {
			t.Fatalf("decorations changed more than the lane: %+v -> %+v", content, fixture.editor.content)
		}
		fixture.paint()
		second := fixture.fills(fixture.snapshot.ChangeColors[ChangeModified-1])
		if len(second) != 1 || !near(first[0].X, second[0].X) || !near(first[0].Y, second[0].Y) {
			t.Fatalf("decoration paint is not idempotent: %+v -> %+v", first, second)
		}
	})
}

// rowOfLine returns the first visible row index that paints the logical line.
func (f *codeFixture) rowOfLine(line int) int {
	for index, row := range f.editor.rows {
		if int(row.line) == line && f.editor.firstOfLine(index) {
			return index
		}
	}
	return -1
}

// lastRowOfLine returns the last visible row index of the logical line.
func (f *codeFixture) lastRowOfLine(line int) int {
	last := -1
	for index, row := range f.editor.rows {
		if int(row.line) == line {
			last = index
		}
	}
	return last
}
