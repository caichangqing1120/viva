package linux

import (
	"os"
	"sync"
	"time"

	"github.com/vivaui/viva/gfx"
	"github.com/vivaui/viva/internal/platform"
	"github.com/vivaui/viva/text"
)

type imeReconnect struct {
	ime         *linuxIME
	span, epoch uint64
	target      gfx.ID
	owner       platform.TextInputOwner
	identity    text.CompositionIdentity
	tentative   bool
	// Negotiation can dispatch messages on the worker before publication.
	// After publication only the event loop touches the bus and this flag.
	adopted bool

	// The worker owns the bus until publication. Shutdown and the event loop
	// compete with publication only through this slot, never through the bus.
	mu       sync.Mutex
	canceled bool
	ready    imeConnection
}

type imeConnection struct {
	daemon  inputMethodDaemon
	context inputMethodContext
	phases  bool
}

const (
	imeRetryMin = 250 * time.Millisecond
	imeRetryMax = 2 * time.Second
)

func (app *linuxApplication) setInputMethodFocus(ime *linuxIME) {
	if app.inputMethodFocus != ime {
		app.inputMethodFocus = ime
		app.inputMethodRetryAt = time.Time{}
		app.inputMethodRetryDelay = 0
	}
}

func (app *linuxApplication) inputMethodRetryDeadline() (time.Time, bool) {
	ime := app.inputMethodFocus
	return app.inputMethodRetryAt, app.inputMethodReconnect == nil && ime != nil && ime.focused && ime.dead &&
		!ime.text.closed && ime.text.enabled && !app.quit && app.firstErr == nil
}

func (app *linuxApplication) delayInputMethodRetry() {
	app.inputMethodRetryDelay = max(imeRetryMin, min(imeRetryMax, 2*app.inputMethodRetryDelay))
	app.inputMethodRetryAt = app.timeNow().Add(app.inputMethodRetryDelay)
}

func (app *linuxApplication) startInputMethodReconnect() {
	if deadline, wanted := app.inputMethodRetryDeadline(); !wanted || app.timeNow().Before(deadline) {
		return
	}
	ime := app.inputMethodFocus
	if app.inputMethod != nil {
		// Another window already restored the shared connection.
		ime.dead = false
		ime.sync()
		if ime.dead {
			app.delayInputMethodRetry()
		}
		return
	}
	state := ime.text.state
	attempt := &imeReconnect{ime: ime, span: ime.focusSpan, epoch: ime.epoch, target: ime.text.input.Target,
		owner: state.InputOwner, identity: state.CompositionIdentity, tentative: state.TentativeComposition}
	app.inputMethodReconnect = attempt
	go func() {
		connection := attempt.connect(dialInputMethod(os.Getenv))
		attempt.mu.Lock()
		if attempt.canceled {
			attempt.mu.Unlock()
			connection.close()
			return
		}
		attempt.ready = connection
		attempt.mu.Unlock()
		app.Post(func() { app.finishInputMethodReconnect(attempt) })
	}()
}

func (attempt *imeReconnect) acceptsCallbacks() bool {
	return attempt.adopted && attempt.ime.contextCurrent(attempt.epoch+1)
}

func (attempt *imeReconnect) connect(daemon inputMethodDaemon) imeConnection {
	if daemon == nil {
		return imeConnection{}
	}
	context, err := daemon.open(attempt.ime, attempt.acceptsCallbacks)
	if err == nil {
		negotiator, supportsPhases := context.(tentativeInputMethod)
		phases := attempt.tentative && supportsPhases && negotiator.SetClientCommitPreedit(true) == nil
		if daemon.Err() == nil {
			return imeConnection{daemon: daemon, context: context, phases: phases}
		}
	}
	daemon.Close()
	return imeConnection{}
}

func (attempt *imeReconnect) matches(ime *linuxIME) bool {
	if ime != attempt.ime || !ime.focused || !ime.dead || ime.text.closed || !ime.text.enabled ||
		ime.focusSpan != attempt.span || ime.epoch != attempt.epoch || ime.text.input.Target != attempt.target {
		return false
	}
	state := ime.text.state
	return state.InputOwner == attempt.owner && state.CompositionIdentity == attempt.identity &&
		state.TentativeComposition == attempt.tentative
}

func (app *linuxApplication) finishInputMethodReconnect(attempt *imeReconnect) {
	connection := attempt.take()
	if app.inputMethodReconnect != attempt {
		connection.close()
		return
	}
	app.inputMethodReconnect = nil
	ime := app.inputMethodFocus
	if ime == nil || !attempt.matches(ime) || app.inputMethod != nil || app.quit || app.firstErr != nil {
		connection.close()
		return
	}
	if connection.daemon == nil {
		app.delayInputMethodRetry()
		return
	}
	app.inputMethodRetryAt = time.Time{}
	app.inputMethodRetryDelay = 0
	app.inputMethod = connection.daemon
	ime.bindOwner()
	if !connection.phases {
		ime.phase = nil
	}
	ime.epoch++
	ime.dead, ime.context = false, connection.context
	attempt.adopted = true
	app.rebuildPoll()
	ime.sync()
}

func (app *linuxApplication) closeInputMethod() {
	if attempt := app.inputMethodReconnect; attempt != nil {
		app.inputMethodReconnect = nil
		attempt.take().close()
	}
	app.setInputMethodFocus(nil)
	if app.inputMethod != nil {
		app.inputMethod.Close()
		app.inputMethod = nil
	}
}

func (attempt *imeReconnect) take() imeConnection {
	attempt.mu.Lock()
	defer attempt.mu.Unlock()
	attempt.canceled = true
	ready := attempt.ready
	attempt.ready = imeConnection{}
	return ready
}

func (connection imeConnection) close() {
	if connection.daemon != nil {
		connection.daemon.Close()
	}
}
