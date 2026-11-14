//go:build linux && vivagtk

package linux

import (
	"fmt"
	"os"
	"slices"
	"sync"
	"testing"

	"github.com/ebitengine/purego"
	"github.com/vivaui/viva/gfx"
	"github.com/vivaui/viva/internal/platform"
	"github.com/vivaui/viva/text"
)

type gtkTentativeProbeKeys struct {
	newEvent  func(int32) uintptr
	freeEvent func(uintptr)
	setDevice func(uintptr, uintptr)
	keyboard  func(uintptr) uintptr
	ref       func(uintptr) uintptr
	entries   func(uintptr, uint32, *uintptr, *int32) int32
	translate func(uintptr, uint32, uint32, int32, *uint32, *int32, *int32, *uint32) int32
}

type gtkTentativeProbeKey struct {
	Code         uint32
	Group, Level int32
}

type gtkTentativeProbeTrial struct {
	name, value string
	signals     []string
	events      []string
	failures    []string
}

type gtkTentativeProbe struct {
	w                         *gtkWindow
	keys                      gtkTentativeProbeKeys
	editor                    *text.Editor
	context                   uintptr
	signals                   []string
	events                    []platform.TextEvent
	hook                      func(platform.TextEvent)
	trials                    []gtkTentativeProbeTrial
	tentative, composingTrial bool
}

var gtkTentativeProbeActive *gtkTentativeProbe
var gtkTentativeProbeOnce sync.Once
var gtkTentativeProbeSignals struct{ start, changed, end, commit uintptr }

func (p *gtkTentativeProbe) observe() {
	gtkTentativeProbeOnce.Do(func() {
		gtkTentativeProbeSignals.start = purego.NewCallback(func(ctx, _ uintptr) uintptr {
			if p := gtkTentativeProbeActive; p != nil && p.context == ctx {
				p.signals = append(p.signals, "start")
			}
			return 0
		})
		gtkTentativeProbeSignals.changed = purego.NewCallback(func(ctx, _ uintptr) uintptr {
			if p := gtkTentativeProbeActive; p != nil && p.context == ctx {
				var value, attrs uintptr
				var cursor int32
				p.w.g.imGetPreedit(ctx, &value, &attrs, &cursor)
				p.signals = append(p.signals, "preedit:"+goString(value))
				if value != 0 {
					p.w.g.gFree(value)
				}
				if attrs != 0 {
					p.w.g.attrListUnref(attrs)
				}
			}
			return 0
		})
		gtkTentativeProbeSignals.end = purego.NewCallback(func(ctx, _ uintptr) uintptr {
			if p := gtkTentativeProbeActive; p != nil && p.context == ctx {
				p.signals = append(p.signals, "end")
			}
			return 0
		})
		gtkTentativeProbeSignals.commit = purego.NewCallback(func(ctx, value, _ uintptr) uintptr {
			if p := gtkTentativeProbeActive; p != nil && p.context == ctx {
				p.signals = append(p.signals, "commit:"+goString(value))
			}
			return 0
		})
	})
	ctx := p.w.ime.ctx
	if ctx != p.context {
		p.w.g.connect(ctx, "preedit-start", gtkTentativeProbeSignals.start, 0)
		p.w.g.connect(ctx, "preedit-changed", gtkTentativeProbeSignals.changed, 0)
		p.w.g.connect(ctx, "preedit-end", gtkTentativeProbeSignals.end, 0)
		p.w.g.connect(ctx, "commit", gtkTentativeProbeSignals.commit, 0)
		p.context = ctx
	}
}

func (p *gtkTentativeProbe) install(editor *text.Editor) {
	p.editor = editor
	field := platform.TextInput{
		Target: gfx.Item(1), X: 20, Y: 20, Width: 300, Height: 100, FontSize: 16,
		Multiline: true, Color: platform.Color{Alpha: 1}, State: platform.EditorState(editor),
		OnEvent: func(event platform.TextEvent) (platform.TextState, error) {
			p.events = append(p.events, event)
			event.Apply(editor)
			next := platform.EditorState(editor)
			if p.hook != nil {
				p.hook(event)
			}
			return next, nil
		},
	}
	p.w.present(platform.Frame{TextInput: platform.TextInputUpdate{Changed: true, Input: &field}}, nil)
	p.w.ime.setFocused(true)
	p.observe()
	p.signals, p.events = nil, nil
}

func (p *gtkTentativeProbe) press(ctx uintptr, symbol, state uint32) error {
	g := p.w.g
	keymap := g.gdkKeymapDefault()
	var entries uintptr
	var count int32
	if p.keys.entries(keymap, symbol, &entries, &count) == 0 || entries == 0 || count == 0 {
		return fmt.Errorf("no native keymap entry for %#x", symbol)
	}
	key := *(*gtkTentativeProbeKey)(unsafePointer(entries))
	g.gFree(entries)
	var value, consumed uint32
	var group, level int32
	if p.keys.translate(keymap, key.Code, state, key.Group, &value, &group, &level, &consumed) == 0 {
		return fmt.Errorf("native keymap did not translate %#x", symbol)
	}
	window := g.widgetGetWindow(p.w.toplevel)
	device := p.keys.keyboard(g.gdkDisplaySeat(g.gdkDisplayDefault()))
	if window == 0 || device == 0 || ctx == 0 {
		return fmt.Errorf("native window/keyboard/context unavailable")
	}
	event := p.keys.newEvent(int32(gdkKeyPress))
	defer p.keys.freeEvent(event)
	record := (*gdkEventKey)(unsafePointer(event))
	record.Window, record.SendEvent = p.keys.ref(window), 1
	record.State, record.Keyval = state, value
	record.HardwareKeycode, record.Group = uint16(key.Code), uint8(group)
	p.keys.setDevice(event, device)
	handled := false
	if ctx == p.w.ime.ctx {
		handled = p.w.ime.filter(event)
	} else {
		handled = g.imFilterKeypress(ctx, event) != 0
	}
	if !handled {
		return fmt.Errorf("native GTK did not consume key %#x (translated %#x)", symbol, value)
	}
	return nil
}

func (p *gtkTentativeProbe) compose() error {
	for _, symbol := range []uint32{'U', '3', 'A', '9'} {
		if err := p.press(p.w.ime.ctx, symbol, (1<<2)|(1<<0)); err != nil {
			return err
		}
	}
	return nil
}

func (p *gtkTentativeProbe) record(name string, failures ...string) {
	trial := gtkTentativeProbeTrial{name: name, value: p.editor.String(), signals: append([]string(nil), p.signals...), failures: failures}
	for _, event := range p.events {
		trial.events = append(trial.events, fmt.Sprintf("%d/%d:%q", event.Kind, event.CompositionPhase, event.Text))
		want := text.CompositionNone
		if p.tentative {
			switch event.Kind {
			case platform.TextSetMarked:
				want = text.CompositionUpdate
			case platform.TextInsert:
				if p.composingTrial {
					want = text.CompositionResult
				}
			case platform.TextCancelComposition:
				want = text.CompositionCancel
			default:
				trial.failures = append(trial.failures, fmt.Sprintf("unexpected tentative event kind %d", event.Kind))
			}
		}
		if event.CompositionPhase != want {
			trial.failures = append(trial.failures, fmt.Sprintf("event %d phase=%d, want %d", event.Kind, event.CompositionPhase, want))
		}
	}
	if p.tentative && !p.editor.TentativeCompositionEnabled() {
		trial.failures = append(trial.failures, "native ingress disabled the requested tentative capability")
	}
	if mirror := p.w.text.state; mirror != platform.EditorState(p.editor).WithoutRedraw() {
		trial.failures = append(trial.failures, fmt.Sprintf("model/mirror differ: model=%+v mirror=%+v", platform.EditorState(p.editor), mirror))
	}
	p.trials = append(p.trials, trial)
}

func (p *gtkTentativeProbe) run() {
	for _, tentative := range []bool{false, true} {
		p.tentative, p.composingTrial = tentative, true
		for _, ending := range []string{"commit", "escape", "reset", "focus-out", "window-focus-out"} {
			editor := text.NewEditor("abc")
			editor.SetSelection(text.Range{Start: 1, End: 2})
			if tentative && !editor.EnableTentativeComposition() {
				p.trials = append(p.trials, gtkTentativeProbeTrial{name: ending, failures: []string{"enable refused"}})
				return
			}
			p.install(editor)
			if err := p.compose(); err != nil {
				p.record(fmt.Sprintf("tentative=%t/%s", tentative, ending), err.Error())
				continue
			}
			var failures []string
			prefix := []string{"start", "preedit:u", "preedit:u3", "preedit:u3A", "preedit:u3A9"}
			if !slices.Equal(p.signals, prefix) || editor.String() != "au3A9c" {
				failures = append(failures, fmt.Sprintf("preedit signals=%q text=%q", p.signals, editor.String()))
			}
			switch ending {
			case "commit":
				if err := p.press(p.w.ime.ctx, ' ', (1<<2)|(1<<0)); err != nil {
					failures = append(failures, err.Error())
				}
			case "escape":
				if err := p.press(p.w.ime.ctx, 0xff1b, (1<<2)|(1<<0)); err != nil {
					failures = append(failures, err.Error())
				}
			case "reset":
				p.w.g.imReset(p.w.ime.ctx)
			case "focus-out":
				p.w.g.imFocusOut(p.w.ime.ctx)
				if !slices.Equal(p.signals, prefix) || editor.String() != "au3A9c" {
					failures = append(failures, "GtkIMContextSimple focus-out unexpectedly settled preedit")
				}
				p.w.g.imReset(p.w.ime.ctx)
			case "window-focus-out":
				p.w.ime.setFocused(false)
			}
			wantSignals := append(prefix, "preedit:", "end")
			wantText := "ac"
			if ending == "commit" {
				wantSignals = append(wantSignals, "commit:\u03a9")
				wantText = "a\u03a9c"
			}
			if !slices.Equal(p.signals, wantSignals) || editor.String() != wantText || p.w.text.state.Composing {
				failures = append(failures, fmt.Sprintf("ending signals=%q text=%q composing=%t", p.signals, editor.String(), p.w.text.state.Composing))
			}
			p.record(fmt.Sprintf("tentative=%t/%s", tentative, ending), failures...)
			if !editor.Undo() || ending == "commit" && editor.String() != "ac" || ending != "commit" && editor.String() != "abc" {
				p.trials[len(p.trials)-1].failures = append(p.trials[len(p.trials)-1].failures, fmt.Sprintf("first Undo=%q", editor.String()))
			}
			if ending == "commit" && (!editor.Undo() || editor.String() != "abc") {
				p.trials[len(p.trials)-1].failures = append(p.trials[len(p.trials)-1].failures, fmt.Sprintf("second Undo=%q", editor.String()))
			}
		}
		p.composingTrial = false
		plain := text.NewEditor("abc")
		if tentative {
			plain.EnableTentativeComposition()
		}
		p.install(plain)
		var failures []string
		for _, symbol := range []uint32{'r', 's'} {
			if err := p.press(p.w.ime.ctx, symbol, 0); err != nil {
				failures = append(failures, err.Error())
			}
		}
		if plain.String() != "abcrs" || !slices.Equal(p.signals, []string{"commit:r", "commit:s"}) {
			failures = append(failures, "ordinary native commits changed text or signal order")
		}
		p.record(fmt.Sprintf("tentative=%t/plain", tentative), failures...)
		if !plain.Undo() || plain.String() != "abc" {
			p.trials[len(p.trials)-1].failures = append(p.trials[len(p.trials)-1].failures, "ordinary GTK commits stopped coalescing")
		}
	}
	old := text.NewEditor("abc")
	old.EnableTentativeComposition()
	p.install(old)
	oldContext := p.keys.ref(p.w.ime.ctx)
	defer p.w.g.objectUnref(oldContext)
	next := text.NewEditor("abc")
	next.EnableTentativeComposition()
	p.install(next)
	p.w.g.imReset(oldContext)
	var failures []string
	if err := p.press(oldContext, 'z', 0); err != nil {
		failures = append(failures, err.Error())
	}
	if p.w.ime.ctx == oldContext || next.String() != "abc" || len(p.events) != 0 {
		failures = append(failures, fmt.Sprintf("old native context reached replacement input: reused=%t text=%q events=%d", p.w.ime.ctx == oldContext, next.String(), len(p.events)))
	}
	p.record("same-target replacement", failures...)
}

func TestGTKNativeTentativeComposition(t *testing.T) {
	if os.Getenv("VIVA_GTK_IME_NATIVE") != "1" {
		t.Skip("native GTK IME probe requires VIVA_GTK_IME_NATIVE=1 and a private display")
	}
	p := &gtkTentativeProbe{}
	gtkTentativeProbeActive = p
	defer func() { gtkTentativeProbeActive = nil }()
	err := RunGTKApplication(func(application platform.Application) error {
		a := application.(*gtkApplication)
		for _, binding := range []struct {
			function any
			name     string
		}{
			{&p.keys.newEvent, "gdk_event_new"}, {&p.keys.freeEvent, "gdk_event_free"},
			{&p.keys.setDevice, "gdk_event_set_device"}, {&p.keys.keyboard, "gdk_seat_get_keyboard"},
			{&p.keys.ref, "g_object_ref"}, {&p.keys.entries, "gdk_keymap_get_entries_for_keyval"},
			{&p.keys.translate, "gdk_keymap_translate_keyboard_state"},
		} {
			if !a.g.lookup(binding.function, binding.name) {
				return fmt.Errorf("native probe cannot bind %s", binding.name)
			}
		}
		window, err := application.OpenWindow(platform.Config{
			Title: "Viva native IME probe", Width: 360, Height: 160,
			ClearColor:       platform.Color{Red: 1, Green: 1, Blue: 1, Alpha: 1},
			DynamicTextInput: true, OnRedraw: func() (platform.Frame, error) { return platform.Frame{}, nil },
		})
		if err != nil {
			return err
		}
		p.w = window.(*gtkWindow)
		a.enqueue(func() {
			p.run()
			p.w.destroy()
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.trials) != 13 {
		t.Fatalf("native trials=%d, want 13", len(p.trials))
	}
	for _, trial := range p.trials {
		t.Logf("%s signals=%q text=%q events=%q", trial.name, trial.signals, trial.value, trial.events)
		for _, failure := range trial.failures {
			t.Errorf("%s: %s", trial.name, failure)
		}
	}
}
