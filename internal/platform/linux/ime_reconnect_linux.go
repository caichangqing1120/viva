package linux

import (
	"sync"
	"time"

	"github.com/vivaui/viva/gfx"
	"github.com/vivaui/viva/internal/ibus"
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
	bus     *ibus.Bus
	context *ibus.Context
	phases  bool
}

const (
	imeRetryMin = 250 * time.Millisecond
	imeRetryMax = 2 * time.Second
)

func (app *linuxApplication) setInputMethodFocus(ime *linuxIME) {
	if app.ibusResumeFocus != ime {
		app.ibusResumeFocus = ime
		app.ibusRetryAt = time.Time{}
		app.ibusRetryDelay = 0
	}
}

func (app *linuxApplication) inputMethodRetryDeadline() (time.Time, bool) {
	ime := app.ibusResumeFocus
	return app.ibusRetryAt, app.ibusReconnect == nil && ime != nil && ime.focused && ime.dead &&
		!ime.text.closed && ime.text.enabled && !app.quit && app.firstErr == nil
}

func (app *linuxApplication) delayInputMethodRetry() {
	app.ibusRetryDelay = max(imeRetryMin, min(imeRetryMax, 2*app.ibusRetryDelay))
	app.ibusRetryAt = app.timeNow().Add(app.ibusRetryDelay)
}

func (app *linuxApplication) startInputMethodReconnect() {
	if deadline, wanted := app.inputMethodRetryDeadline(); !wanted || app.timeNow().Before(deadline) {
		return
	}
	ime := app.ibusResumeFocus
	if app.ibus != nil {
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
	app.ibusReconnect = attempt
	handler := ime.handler(attempt.epoch+1, func() bool { return attempt.adopted })
	go func() {
		var connection imeConnection
		if bus, err := ibus.Dial(); err == nil {
			if context, err := bus.NewContext("viva", handler); err == nil {
				phases := attempt.tentative && context.SetClientCommitPreedit(true) == nil
				if bus.Err() == nil {
					connection = imeConnection{bus: bus, context: context, phases: phases}
				}
			}
			if connection.bus == nil {
				bus.Close()
			}
		}
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
	if app.ibusReconnect != attempt {
		connection.close()
		return
	}
	app.ibusReconnect = nil
	ime := app.ibusResumeFocus
	if ime == nil || !attempt.matches(ime) || app.ibus != nil || app.quit || app.firstErr != nil {
		connection.close()
		return
	}
	if connection.bus == nil {
		app.delayInputMethodRetry()
		return
	}
	app.ibusRetryAt = time.Time{}
	app.ibusRetryDelay = 0
	app.ibus = connection.bus
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
	if attempt := app.ibusReconnect; attempt != nil {
		app.ibusReconnect = nil
		attempt.take().close()
	}
	app.setInputMethodFocus(nil)
	if app.ibus != nil {
		app.ibus.Close()
		app.ibus = nil
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
	if connection.bus != nil {
		connection.bus.Close()
	}
}
