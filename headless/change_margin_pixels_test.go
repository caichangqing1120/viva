//go:build darwin && vivahead

package headless

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vivaui/viva/app"
	"github.com/vivaui/viva/components"
	"github.com/vivaui/viva/gfx"
)

func TestChangeMarginPaintsPixelsAtDesktopAndCompactSizes(t *testing.T) {
	if !rendererAvailable {
		t.Skip("screenshots need the offscreen renderer (-tags vivahead)")
	}
	theme := gfx.LightTheme()
	colors := [4]gfx.Color{
		gfx.RGB(230, 150, 60),
		gfx.RGB(90, 160, 60),
		gfx.RGB(60, 120, 210),
		gfx.RGB(150, 90, 190),
	}
	deletion := gfx.RGB(210, 70, 70)
	for _, size := range []struct {
		name          string
		width, height int
	}{{"desktop", 1200, 800}, {"compact", 640, 400}} {
		t.Run(size.name, func(t *testing.T) {
			var editor components.CodeEditor
			painted := 0
			config := app.Config{
				Title: size.name, Width: size.width, Height: size.height,
				ClearColor: app.Color{Red: 1, Green: 1, Blue: 1, Alpha: 1},
				Paint: func(canvas *gfx.Canvas, frame gfx.Size) {
					painted++
					editor.Paint(canvas, theme, gfx.Rect{Width: frame.Width, Height: frame.Height}, components.CodeEditorSnapshot{
						ID: gfx.Item(1), Label: "Text editor", Value: "one\ntwo\nthree\nfour\nfive\n",
						LineNumbers: true, ShowCaret: true,
						ChangeColors: colors, ChangeDeletionColor: deletion,
						ChangeLines: []components.CodeEditorChangeLine{
							{Line: 0, Kind: components.ChangeModified},
							{Line: 1, Kind: components.ChangeRevertedModified},
							{Line: 2, Kind: components.ChangeSaved, DeletedBelow: true},
							{Line: 3, Kind: components.ChangeReverted},
						},
					})
				},
			}
			session, err := Open(config, nil, Options{Scale: 1})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(session.Close)
			picture, err := session.Image()
			if err != nil {
				t.Fatal(err)
			}
			if painted == 0 || picture.Bounds().Dx() != size.width || picture.Bounds().Dy() != size.height {
				t.Fatalf("missing %dx%d painted image: painted=%d bounds=%v", size.width, size.height, painted, picture.Bounds())
			}
			content := decorationContentBounds(t, session.Snapshot())
			if content.Width <= 0 || content.X < 2 || content.X >= float32(size.width) {
				t.Fatalf("invalid text bounds: %+v", content)
			}
			seen := map[[3]uint8]int{}
			for y := 0; y < picture.Bounds().Dy(); y++ {
				for x := 0; x < picture.Bounds().Dx(); x++ {
					red, green, blue := pixelAt(picture, x, y)
					color := [3]uint8{red, green, blue}
					for _, want := range [][3]uint8{{60, 120, 210}, {150, 90, 190}, {90, 160, 60}, {230, 150, 60}, {210, 70, 70}} {
						if color == want {
							if float32(x) >= content.X {
								t.Fatalf("change margin color %v bled into text at (%d, %d), content=%+v", want, x, y, content)
							}
							seen[want]++
						}
					}
				}
			}
			for _, want := range [][3]uint8{{60, 120, 210}, {150, 90, 190}, {90, 160, 60}, {230, 150, 60}, {210, 70, 70}} {
				if seen[want] == 0 {
					t.Fatalf("%s: change margin is missing color %v", size.name, want)
				}
			}
			if output := os.Getenv("VIVA_CHANGE_MARGIN_SHOTS"); output != "" {
				if err := os.MkdirAll(output, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := session.Screenshot(filepath.Join(output, "change-margin-"+size.name+".png")); err != nil {
					t.Fatal(err)
				}
			}
			t.Logf("%dx%d: rendered change bars and deletion mark: %v", size.width, size.height, seen)
		})
	}
}
