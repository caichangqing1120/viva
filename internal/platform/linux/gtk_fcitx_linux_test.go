//go:build linux && vivagtk

package linux

import (
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"

	"github.com/vivaui/viva/internal/fcitx"
	"github.com/vivaui/viva/text"
)

func TestGtkDirectFcitxDefaultsToMatchingModule(t *testing.T) {
	for _, test := range []struct {
		override, module string
		want             bool
	}{
		{"", "fcitx", true},
		{"", "fcitx5", true},
		{"1", "fcitx", true},
		{"0", "fcitx", false},
		{"invalid", "fcitx", false},
		{"", "ibus", false},
		{"1", "xim", false},
		{"", "", false},
	} {
		if got := gtkDirectFcitxAllowed(test.override, test.module); got != test.want {
			t.Errorf("override %q module %q: allowed=%t, want %t", test.override, test.module, got, test.want)
		}
	}
}

func TestGtkFcitxCancelsPreeditBeforeUnsentFallback(t *testing.T) {
	var events []string
	updates := []fcitx.Update{
		{Owner: 1, Event: &fcitx.Event{Kind: fcitx.Commit, Text: "confirmed"}},
		{Owner: 1, Result: &fcitx.Result{Delivery: fcitx.Unknown}},
		{Owner: 2, Result: &fcitx.Result{Delivery: fcitx.Unsent}},
		{Owner: 1, Result: &fcitx.Result{Delivery: fcitx.Unsent}},
		{Owner: 1, Result: &fcitx.Result{Delivery: fcitx.Unsent}},
	}
	gtkFcitxDrain(updates, true, func() uint64 { return 1 }, func(event fcitx.Event) {
		events = append(events, event.Text)
	}, func() {
		events = append(events, "cancel")
	}, func(_ fcitx.Update, fallback bool) {
		if fallback {
			events = append(events, "key")
		}
	})
	if want := []string{"confirmed", "cancel", "key", "key"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("fallback ordering = %v, want %v", events, want)
	}
}

func TestGtkFcitxDeliveryChecksOwnerAfterEveryEvent(t *testing.T) {
	owner := uint64(3)
	var got []string
	deliver := func(e fcitx.Event) {
		got = append(got, e.Text)
		owner++ // A returned frame moved focus during dispatch.
	}
	verdict := gtkFcitxDeliver(fcitx.Update{Owner: 2, Event: &fcitx.Event{Text: "stale"}}, func() uint64 { return owner }, deliver)
	if verdict || len(got) != 0 {
		t.Fatalf("stale update reached the new editor: verdict=%t events=%v", verdict, got)
	}
	result := &fcitx.Result{Delivery: fcitx.Declined, Events: []fcitx.Event{{Text: "accepted"}, {Text: "crossed"}}}
	verdict = gtkFcitxDeliver(fcitx.Update{Owner: 3, Result: result}, func() uint64 { return owner }, deliver)
	if verdict || !reflect.DeepEqual(got, []string{"accepted"}) {
		t.Fatalf("batch crossed focus boundary: verdict=%t events=%v", verdict, got)
	}
}

func TestGtkFcitxDeliveryNeverReplaysAnUncertainOrForwardedKey(t *testing.T) {
	for _, result := range []struct {
		status   fcitx.Delivery
		events   []fcitx.Event
		fallback bool
	}{
		{status: fcitx.Unsent, fallback: true},
		{status: fcitx.Declined, fallback: true},
		{status: fcitx.Handled},
		{status: fcitx.Unknown},
		{status: fcitx.Declined, events: []fcitx.Event{{Kind: fcitx.Forward}}},
	} {
		u := fcitx.Update{Owner: 1, Result: &fcitx.Result{Delivery: result.status, Events: result.events}}
		if got := gtkFcitxDeliver(u, func() uint64 { return 1 }, func(fcitx.Event) {}); got != result.fallback {
			t.Fatalf("status %v events %v fallback=%t want %t", result.status, result.events, got, result.fallback)
		}
	}
}

func TestGTKNativeDirectFcitxHotkeyOnFieldHandoff(t *testing.T) {
	testGTKNativeDirectFcitxFieldHotkey(t, true)
}

func TestGTKNativeDirectFcitxHotkeyOnFirstField(t *testing.T) {
	testGTKNativeDirectFcitxFieldHotkey(t, false)
}

func testGTKNativeDirectFcitxFieldHotkey(t *testing.T, withDocument bool) {
	t.Helper()
	if os.Getenv("VIVA_GTK_FCITX_HANDOFF") != "1" || os.Getenv("VIVA_IBUS_FRONTEND") != "fcitx5" || os.Getenv("VIVA_IBUS_ENGINE") != "pinyin" {
		t.Skip("requires the private Fcitx Pinyin handoff fixture")
	}
	t.Setenv("VIVA_GTK_FCITX_DIRECT", "")
	testGTKNativeTentativeProbe(t, 1, func(p *gtkTentativeProbe) {
		p.asynchronous, p.keyIngress = true, true
		document, field := text.NewEditor("body"), text.NewEditor("")
		finish := func(failure ...string) {
			p.record("immediate Fcitx hotkey in a single-line field", failure...)
			p.w.destroy()
		}
		probe := &gtkFcitxProbe{p: p}
		deadline := time.Now().Add(3 * time.Second)
		var await func(func() bool, func(), string)
		await = func(check func() bool, then func(), description string) {
			switch {
			case check():
				then()
			case time.Now().After(deadline):
				finish(description)
			default:
				probe.later(10*time.Millisecond, func() { await(check, then, description) })
			}
		}
		typeQuery := func() {
			p.installField(field, false)
			keys := []struct{ symbol, state uint32 }{{' ', gdkControlMask}, {'n', 0}, {'i', 0}, {'h', 0}, {'a', 0}, {'o', 0}, {' ', 0}}
			// Do not pump GTK between the handoff and the hotkey/typing sequence.
			for _, key := range keys {
				if err := p.press(p.w.ime.ctx, key.symbol, key.state); err != nil {
					finish(err.Error())
					return
				}
				if err := p.release(key.symbol, key.state); err != nil {
					finish(err.Error())
					return
				}
			}
			await(func() bool { return field.String() == "\u4f60\u597d" && !field.CompositionActive() }, func() {
				if document.String() != "body" || field.TentativeCompositionEnabled() {
					finish("handoff changed the outgoing document or the field's composition mode")
					return
				}
				finish()
			}, "the field did not confirm Chinese from the immediate hotkey/typing sequence")
		}
		if !withDocument {
			typeQuery()
			return
		}
		if !document.EnableTentativeComposition() {
			finish("document did not enable tentative composition")
			return
		}
		p.install(document)
		await(func() bool {
			engine, err := nativeFcitxCommand("--check", "-n")
			return err == nil && engine != ""
		}, func() {
			if err := selectNativeFcitxEngine("pinyin"); err != nil {
				finish(err.Error())
				return
			}
			typeQuery()
		}, "Fcitx did not focus the initial document")
	})
}

func TestGTKNativeDirectFcitxSwitchesModulesDuringPreedit(t *testing.T) {
	if os.Getenv("VIVA_IBUS_FRONTEND") != "fcitx5" || os.Getenv("GTK_IM_MODULE") != "" {
		t.Skip("requires a private Fcitx fixture and an unforced GTK input module")
	}
	t.Setenv("VIVA_GTK_FCITX_DIRECT", "")
	testGTKNativeTentativeProbe(t, 1, func(p *gtkTentativeProbe) {
		p.asynchronous, p.keyIngress, p.tentative = true, true, true
		editor := text.NewEditor("abc")
		editor.MoveTo(3, false)
		editor.EnableTentativeComposition()
		p.editor = editor
		finish := func(failures ...string) {
			p.record("GTK input module change", failures...)
			p.w.destroy()
		}
		var setString func(*gValue, *byte)
		var setProperty func(uintptr, *byte, *gValue)
		g := p.w.g
		if !g.lookup(&setString, "g_value_set_string") || !g.lookup(&setProperty, "g_object_set_property") {
			finish("GTK settings setter unavailable")
			return
		}
		settings := g.settingsDefault()
		setModule := func(name string) {
			var value gValue
			g.valueInit(&value, gTypeString)
			defer g.valueUnset(&value)
			setString(&value, &cString(name)[0])
			setProperty(settings, &cString("gtk-im-module")[0], &value)
		}
		setModule("fcitx")
		p.install(editor)
		if p.w.ime.direct == nil {
			finish("Fcitx did not start by default")
			return
		}
		probe := &gtkFcitxProbe{p: p}
		await := func(ready func() bool, then func()) {
			deadline := time.Now().Add(3 * time.Second)
			var poll func()
			poll = func() {
				if ready() {
					then()
				} else if time.Now().After(deadline) {
					finish("timed out waiting for input method state")
				} else {
					probe.later(10*time.Millisecond, poll)
				}
			}
			poll()
		}
		await(func() bool {
			if d := p.w.ime.direct; d != nil {
				_, pending := d.client.Deadline()
				return !pending
			}
			return false
		}, func() {
			for _, args := range [][]string{{"-s", "pinyin"}, {"-o"}} {
				if out, err := exec.Command("fcitx5-remote", args...).CombinedOutput(); err != nil {
					finish("could not activate test Fcitx engine: " + string(out))
					return
				}
			}
			for _, symbol := range []uint32{'n', 'i'} {
				if err := p.press(p.w.ime.ctx, symbol, 0); err != nil {
					finish(err.Error())
					return
				}
			}
			await(func() bool { return editor.String() == "abcni" && editor.CommittedString() == "abc" }, func() {
				setModule("gtk-im-context-simple")
				if p.w.ime.direct != nil || editor.TentativeCompositionActive() || editor.String() != "abc" {
					finish("module change did not cancel and release direct Fcitx")
					return
				}
				if err := p.press(p.w.ime.ctx, 'x', 0); err != nil {
					finish(err.Error())
					return
				}
				await(func() bool { return editor.CommittedString() == "abcx" }, func() { finish() })
			})
		})
	})
}
