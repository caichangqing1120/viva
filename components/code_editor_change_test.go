package components

import (
	"strings"
	"testing"

	"github.com/vivaui/viva/gfx"
)

func TestCodeEditorChangeLaneCoexistsWithDecorationsAndSelection(t *testing.T) {
	fixture := newCodeFixture("first\nsecond")
	fixture.snapshot.Gutter = &CodeEditorGutter{Inset: 2}
	decorationColor := gfx.RGB(180, 40, 60)
	changeColor := gfx.RGB(30, 150, 80)
	indicatorColor := gfx.RGB(40, 70, 220)
	fixture.snapshot.Decorations = &CodeEditorDecorations{
		GutterWidth: 9,
		GutterBars:  []CodeEditorGutterBar{{Line: 0, Fill: decorationColor, Stroke: decorationColor}},
	}
	fixture.snapshot.SelectionStart, fixture.snapshot.SelectionEnd = 0, 3
	fixture.paint()
	withoutChanges := fixture.editor.content.X

	fixture.snapshot.Gutter.Change = 7
	fixture.snapshot.ChangeLines = []CodeEditorChangeLine{{Line: 0, Kind: ChangeModified}}
	fixture.snapshot.ChangeIndicators = []CodeEditorChangeLine{{Line: 0, Kind: ChangeSaved}}
	fixture.snapshot.ChangeColors[ChangeModified-1] = changeColor
	fixture.snapshot.ChangeColors[ChangeSaved-1] = indicatorColor
	fixture.paint()
	if got := fixture.editor.content.X; !near(got, withoutChanges+7) {
		t.Fatalf("change lane shifted content from %v to %v, want +7", withoutChanges, got)
	}
	decorations := fixture.fills(decorationColor)
	changes := fixture.fills(changeColor)
	indicators := fixture.fills(indicatorColor)
	if len(decorations) != 1 || len(changes) != 1 || len(indicators) != 1 {
		t.Fatalf("decorations=%+v, changes=%+v, indicators=%+v", decorations, changes, indicators)
	}
	if !(decorations[0].X < changes[0].X && changes[0].X+changes[0].Width <= fixture.editor.content.X) {
		t.Fatalf("lanes overlap text or each other: decoration=%+v, change=%+v, content=%+v", decorations[0], changes[0], fixture.editor.content)
	}
	if !near(indicators[0].X, fixture.editor.content.X+1) {
		t.Fatalf("indicator %+v is not anchored in text area %+v", indicators[0], fixture.editor.content)
	}
	var selectionOrder, indicatorOrder uint32
	for _, rectangle := range fixture.canvas.Rectangles() {
		if rectangle.Color == fixture.theme.Selection {
			selectionOrder = rectangle.Order
		}
		if rectangle.Color == indicatorColor {
			indicatorOrder = rectangle.Order
		}
	}
	if selectionOrder == 0 || indicatorOrder <= selectionOrder {
		t.Fatalf("indicator must paint above selection: selection=%d, indicator=%d", selectionOrder, indicatorOrder)
	}
}

func TestCodeEditorOversizedChangeLaneClipsFoldPaintingAndHits(t *testing.T) {
	var canvas gfx.Canvas
	var editor CodeEditor
	fold := gfx.Item(17)
	editor.Paint(&canvas, gfx.LightTheme(), gfx.Rect{Width: 100, Height: 200}, CodeEditorSnapshot{
		Value:       "left",
		Gutter:      &CodeEditorGutter{Change: 120, Fold: 16},
		FoldMarkers: []CodeEditorFoldMarker{{Line: 0, ID: fold}},
	})
	for _, triangle := range canvas.Triangles() {
		if !triangle.HasClip || triangle.ClipOf.Width > 0 && triangle.ClipOf.X+triangle.ClipOf.Width > 100 {
			t.Fatalf("fold chevron escaped editor: %+v", triangle)
		}
	}
	for _, hit := range canvas.Hits() {
		if hit.ID == fold {
			t.Fatalf("invisible fold button remained clickable outside editor: %+v", hit.Bounds)
		}
	}
	if len(canvas.Clips()) != 0 {
		t.Fatal("editor left an unbalanced clip")
	}
}

func TestCodeEditorChangeGeometry(t *testing.T) {
	t.Run("wrapped deletion remains at line end after scrolling", func(t *testing.T) {
		fixture := newCodeFixture("a very long first line that wraps and wraps\nsecond\nthird\nfourth\n")
		fixture.bounds.Width = fixture.widthFor(8)
		fixture.bounds.Height = 600
		fixture.snapshot.Wrap = true
		status, deletion := gfx.RGB(60, 100, 200), gfx.RGB(220, 40, 40)
		fixture.snapshot.ChangeColors[ChangeModified-1] = status
		fixture.snapshot.ChangeDeletionColor = deletion
		fixture.snapshot.ChangeLines = []CodeEditorChangeLine{{Line: 0, Kind: ChangeModified, DeletedBelow: true}}
		fixture.paint()
		firstLineRows := 0
		for _, row := range fixture.editor.rows {
			if row.line == 0 {
				firstLineRows++
			}
		}
		if firstLineRows < 2 {
			t.Fatal("fixture did not wrap the changed line")
		}
		_, _, lineHeight := fixture.cell()
		bars, points := fixture.fills(status), fixture.fills(deletion)
		if len(bars) != 1 || len(points) != 1 ||
			!near(bars[0].Y, fixture.editor.content.Y) ||
			!near(points[0].Y+points[0].Height, fixture.editor.content.Y+float32(firstLineRows)*lineHeight) {
			t.Fatalf("wrapped change belongs to first/last rows: bars=%+v points=%+v", bars, points)
		}
		fixture.bounds.Height = float32(firstLineRows)*lineHeight + 2*codeEditorPadY
		fixture.paint()
		fixture.editor.SetScrollY(lineHeight)
		fixture.paint()
		if bars := fixture.fills(status); len(bars) != 0 {
			t.Fatalf("scrolled-away first row kept its bar: %+v", bars)
		}
		if points := fixture.fills(deletion); len(points) != 1 ||
			points[0].Y < fixture.editor.content.Y || points[0].Y+points[0].Height > fixture.editor.content.Y+fixture.editor.content.Height {
			t.Fatalf("visible last row lost its deletion point: %+v", points)
		}
	})

	t.Run("folded lines do not paint changes", func(t *testing.T) {
		fixture := newCodeFixture("zero\none\ntwo\nthree\nfour")
		fixture.snapshot.FoldedLines = []CodeEditorFoldRange{{Header: 1, LastLine: 3}}
		fixture.snapshot.ChangeLines = []CodeEditorChangeLine{
			{Line: 1, Kind: ChangeModified},
			{Line: 2, Kind: ChangeSaved},
			{Line: 4, Kind: ChangeReverted},
		}
		fixture.snapshot.ChangeIndicators = fixture.snapshot.ChangeLines
		header, hidden, after := gfx.RGB(60, 100, 200), gfx.RGB(120, 160, 60), gfx.RGB(200, 120, 40)
		fixture.snapshot.ChangeColors[ChangeModified-1] = header
		fixture.snapshot.ChangeColors[ChangeSaved-1] = hidden
		fixture.snapshot.ChangeColors[ChangeReverted-1] = after
		fixture.paint()
		if len(fixture.fills(header)) != 2 || len(fixture.fills(hidden)) != 0 || len(fixture.fills(after)) != 2 {
			t.Fatalf("folded changes painted: header=%+v hidden=%+v after=%+v", fixture.fills(header), fixture.fills(hidden), fixture.fills(after))
		}
	})

	t.Run("invalid kinds keep deletion points and disabled colors", func(t *testing.T) {
		fixture := newCodeFixture("one\ntwo")
		status, deletion := gfx.RGB(60, 100, 200), gfx.RGB(220, 40, 40)
		fixture.snapshot.ChangeColors[ChangeModified-1] = status
		fixture.snapshot.ChangeDeletionColor = deletion
		fixture.snapshot.ChangeLines = []CodeEditorChangeLine{
			{Line: 0, DeletedAbove: true}, {Line: 1, Kind: ChangeModified},
		}
		fixture.snapshot.ChangeIndicators = []CodeEditorChangeLine{
			{Line: 0, DeletedAbove: true}, {Line: 1, Kind: ChangeModified},
		}
		fixture.snapshot.Disabled = true
		dimStatus, dimDeletion := status, deletion
		dimStatus.Alpha, dimDeletion.Alpha = 0.5, 0.5
		for _, kind := range []CodeEditorChangeKind{ChangeNone, CodeEditorChangeKind(255)} {
			fixture.snapshot.ChangeLines[0].Kind = kind
			fixture.snapshot.ChangeIndicators[0].Kind = kind
			fixture.paint()
			if len(fixture.fills(dimStatus)) != 2 || len(fixture.fills(dimDeletion)) != 2 ||
				len(fixture.fills(status)) != 0 || len(fixture.fills(deletion)) != 0 {
				t.Fatalf("kind %d: status=%+v deletion=%+v", kind, fixture.fills(dimStatus), fixture.fills(dimDeletion))
			}
			if fixture.snapshot.ChangeColors[ChangeModified-1] != status || fixture.snapshot.ChangeDeletionColor != deletion {
				t.Fatal("disabled paint changed caller-owned colors")
			}
		}
	})
}

func TestCodeEditorChangeNarrowCustomLaneFitsBarAndDeletion(t *testing.T) {
	for _, width := range []float32{0.5, 1, 2, 3} {
		fixture := newCodeFixture("one\ntwo")
		status, deletion := gfx.RGB(60, 100, 200), gfx.RGB(220, 40, 40)
		fixture.snapshot.ChangeColors[ChangeModified-1] = status
		fixture.snapshot.ChangeDeletionColor = deletion
		fixture.snapshot.ChangeLines = []CodeEditorChangeLine{{Line: 0, Kind: ChangeModified, DeletedBelow: true}}
		fixture.snapshot.Gutter = &CodeEditorGutter{Change: width}
		fixture.paint()
		lane := max(width, changeLaneMinimum)
		bars, points := fixture.fills(status), fixture.fills(deletion)
		if len(bars) != 1 || len(points) != 1 {
			t.Fatalf("width %v: bars=%+v points=%+v", width, bars, points)
		}
		laneX := fixture.bounds.X + fixture.snapshot.Gutter.Inset
		if !near(fixture.editor.content.X, laneX+lane) {
			t.Fatalf("width %v: content starts at %v, want %v", width, fixture.editor.content.X, laneX+lane)
		}
		for _, rect := range []gfx.Rect{bars[0], points[0]} {
			if rect.Width <= 0 || rect.X < laneX || rect.X+rect.Width > laneX+lane+0.01 {
				t.Fatalf("width %v: change mark %+v escaped lane [%v, %v]", width, rect, laneX, laneX+lane)
			}
		}
	}
}

func TestCodeEditorChangeLaneHorizontalScrollAndSplitViews(t *testing.T) {
	left := newCodeFixture(strings.Repeat("x", 200) + "\ntwo\n")
	left.bounds.Width = left.widthFor(12)
	left.snapshot.HorizontalScrollbarID = gfx.Item(9)
	left.snapshot.LineNumbers = true
	left.snapshot.ChangeLines = []CodeEditorChangeLine{{Line: 0, Kind: ChangeModified}}
	modified := gfx.RGB(60, 100, 200)
	left.snapshot.ChangeColors[ChangeModified-1] = modified
	left.paint()
	before := left.fills(modified)
	if len(before) != 1 {
		t.Fatalf("initial change bar = %+v", before)
	}
	left.editor.SetScrollX(40)
	left.paint()
	after := left.fills(modified)
	if left.editor.ScrollX() == 0 || len(after) != 1 || !near(after[0].X, before[0].X) {
		t.Fatalf("horizontal scroll moved change lane: before=%+v after=%+v scroll=%v", before, after, left.editor.ScrollX())
	}
	left.snapshot.LineNumbers = false
	left.paint()
	if bars := left.fills(modified); len(bars) != 1 || !near(bars[0].X, before[0].X) {
		t.Fatalf("hiding line numbers moved change lane: %+v", bars)
	}

	right := newCodeFixture("one\ntwo\n")
	saved := gfx.RGB(120, 160, 60)
	right.snapshot.ChangeColors[ChangeSaved-1] = saved
	right.snapshot.ChangeLines = []CodeEditorChangeLine{{Line: 1, Kind: ChangeSaved}}
	right.paint()
	if len(right.fills(saved)) != 1 || len(right.fills(modified)) != 0 || len(left.fills(saved)) != 0 {
		t.Fatalf("split views shared change marks: left=%+v right=%+v", left.fills(saved), right.fills(saved))
	}
}
