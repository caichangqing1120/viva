//go:build !android

package linux

import (
	"os"
	"strings"
	"time"

	"github.com/vivaui/viva/internal/fcitx"
	"github.com/vivaui/viva/internal/ibus"
	"github.com/vivaui/viva/internal/platform"
	"github.com/vivaui/viva/text"
)

// inputMethodDaemon is the connection to the input method framework the session
// runs, shared by the windows that take text. The X11 backend speaks the
// framework's own client protocol, so the same field code drives either.
type inputMethodDaemon interface {
	FD() int
	Dispatch() error
	Err() error
	Deadline() (time.Time, bool)
	Expire()
	Close()
	// open creates one window's input context, whose engine output goes to ime
	// while the epoch it was opened in is current.
	open(ime *linuxIME, current func() bool) (inputMethodContext, error)
}

// inputMethodDaemon probes once at startup. Reconnection runs off the loop.
func (application *linuxApplication) inputMethodDaemon() inputMethodDaemon {
	if !application.inputMethodTried {
		application.inputMethodTried = true
		application.inputMethod = dialInputMethod(os.Getenv)
	}
	return application.inputMethod
}

// dialInputMethod connects to the input method framework in use. XMODIFIERS is
// how an X11 session names it, so a desktop with several daemons installed uses
// the one the user configured; a Fcitx 4 session sets @im=fcitx even when an
// IBus daemon also runs. Without a name, IBus is tried first and Fcitx 4 second,
// as Fcitx 4 itself has no IBus frontend while Fcitx 5 is served by IBus.
func dialInputMethod(getenv func(string) string) inputMethodDaemon {
	return dialPreferred(inputMethodFramework(getenv), dialIBus, dialFcitx)
}

// dialPreferred connects to the daemon the session names, then to the other.
func dialPreferred(framework string, ibus, fcitx func() inputMethodDaemon) inputMethodDaemon {
	if framework == "fcitx" {
		if daemon := fcitx(); daemon != nil {
			return daemon
		}
	}
	if daemon := ibus(); daemon != nil {
		return daemon
	}
	return fcitx()
}

// inputMethodFramework is the XIM name XMODIFIERS selects, lowercased.
func inputMethodFramework(getenv func(string) string) string {
	fields := strings.FieldsFunc(getenv("XMODIFIERS"), func(r rune) bool { return r == ';' || r == ',' })
	for _, field := range fields {
		if value, ok := strings.CutPrefix(strings.TrimSpace(field), "@im="); ok {
			return strings.ToLower(strings.TrimSpace(value))
		}
	}
	return ""
}

func dialIBus() inputMethodDaemon {
	bus, err := ibus.Dial()
	if err != nil {
		return nil
	}
	return ibusDaemon{bus}
}

func dialFcitx() inputMethodDaemon {
	bus, err := fcitx.Dial()
	if err != nil {
		return nil
	}
	return fcitxDaemon{bus}
}

// ibusDaemon is the IBus client behind the daemon interface.
type ibusDaemon struct{ bus *ibus.Bus }

func (daemon ibusDaemon) FD() int                     { return daemon.bus.FD() }
func (daemon ibusDaemon) Dispatch() error             { return daemon.bus.Dispatch() }
func (daemon ibusDaemon) Err() error                  { return daemon.bus.Err() }
func (daemon ibusDaemon) Deadline() (time.Time, bool) { return daemon.bus.Deadline() }
func (daemon ibusDaemon) Expire()                     { daemon.bus.Expire() }
func (daemon ibusDaemon) Close()                      { daemon.bus.Close() }

func (daemon ibusDaemon) open(ime *linuxIME, current func() bool) (inputMethodContext, error) {
	context, err := daemon.bus.NewContext("viva", ime.ibusHandler(current))
	if err != nil {
		return nil, err
	}
	return ibusContext{context}, nil
}

func (ime *linuxIME) ibusHandler(current func() bool) ibus.Handler {
	return ibus.Handler{
		Commit: func(value string) {
			if current() {
				ime.commit(value)
			}
		},
		Preedit: func(event ibus.PreeditEvent) {
			if current() {
				ime.nativePreedit(event)
			}
		},
		Forward: func(keysym, _, state uint32) {
			if current() {
				ime.forward(keysym, state&ibus.ReleaseMask != 0, state)
			}
		},
		DeleteSurrounding: func(offset, count int) {
			if current() {
				ime.deleteSurrounding(offset, count)
			}
		},
	}
}

// ibusContext is an IBus context behind the daemon interface. IBus counts
// keycodes from the first non-modifier key and marks a release in the state
// mask; the window reports keys as the X server does.
type ibusContext struct{ *ibus.Context }

func (context ibusContext) ProcessKey(key inputMethodKey, done func(handled bool)) error {
	// IBus counts keycodes from the first non-modifier key on the keyboard, so
	// the X keycode less 8; Fcitx wants the X keycode itself.
	keycode := key.keycode
	if keycode >= 8 {
		keycode -= 8
	}
	state := key.state
	if key.release {
		state |= ibus.ReleaseMask
	}
	return context.Context.ProcessKey(key.keysym, keycode, state, done)
}

// fcitxDaemon is the Fcitx 4 client behind the daemon interface.
type fcitxDaemon struct{ bus *fcitx.Bus }

func (daemon fcitxDaemon) FD() int                     { return daemon.bus.FD() }
func (daemon fcitxDaemon) Dispatch() error             { return daemon.bus.Dispatch() }
func (daemon fcitxDaemon) Err() error                  { return daemon.bus.Err() }
func (daemon fcitxDaemon) Deadline() (time.Time, bool) { return daemon.bus.Deadline() }
func (daemon fcitxDaemon) Expire()                     { daemon.bus.Expire() }
func (daemon fcitxDaemon) Close()                      { daemon.bus.Close() }

func (daemon fcitxDaemon) open(ime *linuxIME, current func() bool) (inputMethodContext, error) {
	context, err := daemon.bus.NewContext("viva", ime.fcitxHandler(current))
	if err != nil {
		return nil, err
	}
	return fcitxContext{context}, nil
}

func (ime *linuxIME) fcitxHandler(current func() bool) fcitx.Handler {
	return fcitx.Handler{
		Commit: func(value string) {
			if current() {
				ime.commit(value)
			}
		},
		Preedit: func(event fcitx.PreeditEvent) {
			if !current() {
				return
			}
			if !event.Visible || event.Text == "" {
				ime.engineComposing = false
				if ime.text.state.Composing {
					ime.apply(platform.TextEvent{Kind: platform.TextCancelComposition})
				}
				return
			}
			ime.preedit(event.Text, event.Cursor, true, text.Range{Start: event.Selection.Start, End: event.Selection.End})
		},
		Forward: func(keysym, state uint32, release bool) {
			if current() {
				ime.forward(keysym, release, state)
			}
		},
		DeleteSurrounding: func(offset, count int) {
			if current() {
				ime.deleteSurrounding(offset, count)
			}
		},
	}
}

// fcitxContext is a Fcitx context behind the daemon interface. Fcitx wants the
// raw X keycode and the event time with each key.
type fcitxContext struct{ *fcitx.Context }

func (context fcitxContext) ProcessKey(key inputMethodKey, done func(handled bool)) error {
	return context.Context.ProcessKey(fcitx.Key{
		Keysym: key.keysym, Keycode: key.keycode, State: key.state, Time: key.time, Release: key.release,
	}, done)
}
