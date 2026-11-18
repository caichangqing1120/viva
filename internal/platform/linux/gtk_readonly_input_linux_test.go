//go:build linux && vivagtk

package linux

import (
	"fmt"
	"testing"

	"github.com/vivaui/viva/gfx"
	"github.com/vivaui/viva/internal/platform"
	"github.com/vivaui/viva/text"
)

func TestGTKNativeRefreshReadOnlyTextInput(t *testing.T) {
	t.Setenv("GTK_IM_MODULE", "gtk-im-context-simple")
	testGTKNativeTentativeProbe(t, 4, func(p *gtkTentativeProbe) {
		w := p.w
		p.keyIngress = true
		for _, tc := range []struct {
			name            string
			refocus         bool
			readOnlyFocused bool
			writable        bool
			want            string
		}{
			{"permission restored with focus", false, true, true, "xq"},
			{"permission restored on activation", true, true, true, "xq"},
			{"file remains read-only", false, true, false, "x"},
			{"focus outside editor", false, false, true, "x"},
		} {
			editor := text.NewEditor("x")
			editor.MoveTo(1, false)
			w.present(platform.Frame{TextInput: platform.TextInputUpdate{Changed: true}}, nil)
			w.setKeyboard(false)
			w.config.ReadOnlyTextFocused = func() bool { return tc.readOnlyFocused }
			writable := false
			redraws := 0
			w.config.OnRedraw = func() (platform.Frame, error) {
				redraws++
				if !writable {
					return platform.Frame{}, nil
				}
				field := platform.TextInput{
					Target: gfx.Item(1), X: 20, Y: 20, Width: 300, Height: 100, FontSize: 16,
					Color: platform.Color{Alpha: 1}, State: platform.EditorState(editor),
					OnEvent: func(event platform.TextEvent) (platform.TextState, error) {
						event.Apply(editor)
						return platform.EditorState(editor), nil
					},
				}
				return platform.Frame{TextInput: platform.TextInputUpdate{Changed: true, Input: &field}}, nil
			}
			w.setKeyboard(true)
			redraws = 0
			writable = tc.writable
			var failures []string
			if tc.refocus {
				w.setKeyboard(false)
				w.setKeyboard(true)
				if !w.text.enabled {
					failures = append(failures, "activation did not publish the writable editor")
				}
			}
			if err := p.press(w.ime.ctx, 'q', 0); err != nil {
				failures = append(failures, err.Error())
			}
			if got := editor.String(); got != tc.want {
				failures = append(failures, fmt.Sprintf("first key left text %q, want %q", got, tc.want))
			}
			if !tc.readOnlyFocused && redraws != 0 {
				failures = append(failures, "key outside the read-only editor triggered a redraw")
			}
			p.trials = append(p.trials, gtkTentativeProbeTrial{name: tc.name, value: editor.String(), failures: failures})
		}
	})
}
