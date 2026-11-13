//go:build darwin && vivahead

package headless

import (
	"testing"

	"github.com/vivaui/viva/app"
	"github.com/vivaui/viva/components"
	"github.com/vivaui/viva/gfx"
)

// H05: the change margin must paint real, non-empty pixels at a desktop size and
// at the 640x400 compact size, carrying the edition colours and a deletion mark.
func TestChangeMarginPaintsPixelsAtDesktopAndCompactSizes(t *testing.T) {
	if !rendererAvailable {
		t.Skip("screenshots need the offscreen renderer (-tags vivahead)")
	}
	theme := gfx.LightTheme()
	colors := [4]gfx.Color{
		gfx.RGB(230, 150, 60), // reverted
		gfx.RGB(90, 160, 60),  // saved
		gfx.RGB(60, 120, 210), // modified
		gfx.RGB(150, 90, 190), // reverted to modified
	}
	deletion := gfx.RGB(210, 70, 70)
	editor := &components.CodeEditor{}
	for _, size := range []struct {
		name          string
		width, height int
	}{
		{"desktop", 1200, 800},
		{"compact", 640, 400},
	} {
		t.Run(size.name, func(t *testing.T) {
			painted := 0
			config := app.Config{
				Title: size.name, Width: size.width, Height: size.height,
				ClearColor: app.Color{Red: 1, Green: 1, Blue: 1, Alpha: 1},
			}
			config.Paint = func(canvas *gfx.Canvas, frame gfx.Size) {
				painted++
				editor.Paint(canvas, theme, gfx.Rect{Width: frame.Width, Height: frame.Height}, components.CodeEditorSnapshot{
					ID: gfx.Item(1), Label: "Text editor", Value: "one\ntwo\nthree\nfour\n",
					LineNumbers: true, ShowCaret: true,
					ChangeColors: colors, ChangeDeletionColor: deletion,
					ChangeLines: []components.CodeEditorChangeLine{
						{Line: 0, Kind: components.ChangeModified},
						{Line: 2, Kind: components.ChangeSaved, DeletedBelow: true},
						{Line: 3, Kind: components.ChangeReverted},
					},
				})
			}
			session, err := Open(config, nil, Options{Scale: 1})
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			t.Cleanup(session.Close)
			picture, err := session.Image()
			if err != nil {
				t.Fatalf("image: %v", err)
			}
			if painted == 0 {
				t.Fatal("the scene was never painted")
			}
			// The margin sits at the left edge: count the lane's coloured rows and
			// require each edition colour and the deletion mark to be present.
			seen := map[[3]uint8]int{}
			rows := 0
			for y := 0; y < picture.Bounds().Dy(); y++ {
				rowHasLane := false
				for x := 0; x < 30; x++ {
					red, green, blue := pixelAt(picture, x, y)
					if red == green && green == blue {
						continue
					}
					seen[[3]uint8{red, green, blue}]++
					rowHasLane = true
				}
				if rowHasLane {
					rows++
				}
			}
			if rows < 4 {
				t.Fatalf("%s: the margin painted only %d rows", size.name, rows)
			}
			// The offscreen renderer keeps the requested colours exactly.
			for _, want := range []([3]uint8){{60, 120, 210}, {90, 160, 60}, {230, 150, 60}, {210, 70, 70}} {
				if seen[want] == 0 {
					t.Fatalf("%s: the margin is missing the %v colour", size.name, want)
				}
			}
			// The text area starts to the right of the lane and carries no lane colour.
			contentX := 40
			for y := 0; y < 40; y++ {
				for x := contentX; x < contentX+20; x++ {
					red, green, blue := pixelAt(picture, x, y)
					if red == 60 && green == 120 && blue == 210 {
						t.Fatalf("%s: the modified bar bleeds into the text area at (%d, %d)", size.name, x, y)
					}
				}
			}
		})
	}
}
