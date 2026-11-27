//go:build linux && vivagtk

package linux

import (
	"math/bits"
	"sync"

	"github.com/ebitengine/purego"
	"github.com/vivaui/viva/internal/platform"
	"github.com/vivaui/viva/text"
)

var gtkFcitxServices = [...]string{"org.fcitx.Fcitx5", "org.freedesktop.portal.Fcitx"}

type gtkFcitxWatch struct {
	contextID func(uintptr) uintptr
	unwatch   func(uint32)
	ids       [2]uint32
	owners    [2]string
	present   uintptr
	checked   bool
	settings  uintptr
	moduleID  uint64
}

var gtkFcitxCallbacks struct {
	sync.Once
	appeared, vanished uintptr
}

var gtkDirectFcitxModuleOnce sync.Once
var gtkDirectFcitxModuleChanged uintptr

func (a *gtkApplication) watchDirectFcitxModule(contextID func(uintptr) uintptr) {
	watch := &a.fcitxWatch
	if watch.moduleID != 0 {
		return
	}
	watch.settings = a.g.settingsDefault()
	if watch.settings == 0 {
		return
	}
	watch.contextID, watch.checked = contextID, true
	gtkDirectFcitxModuleOnce.Do(func() {
		gtkDirectFcitxModuleChanged = purego.NewCallback(func(_, _, _ uintptr) uintptr {
			if app := gtkRunning.Load(); app != nil && !app.stopping {
				for _, w := range app.windows {
					if !w.closed && w.ime != nil && w.ime.direct != nil && !w.ime.direct.failing {
						w.ime.direct.refreshModule(app.fcitxWatch.contextID)
					}
				}
			}
			return 0
		})
	})
	// GtkIMMulticontext invalidates its global context ID in a notify handler.
	// Read the effective context only after GTK has handled the notification.
	watch.moduleID = a.g.signalConnect(watch.settings, &cString("notify::gtk-im-module")[0], gtkDirectFcitxModuleChanged, 0, 0, 1)
}

func (a *gtkApplication) watchFcitx(ime *gtkIME) {
	state := &a.fcitxWatch
	if state.ids != [2]uint32{} {
		return
	}
	if !state.checked {
		state.checked = true
		a.g.lookup(&state.contextID, "gtk_im_multicontext_get_context_id")
	}
	if !state.matches(ime) {
		return
	}
	var watch func(bus int32, name *byte, flags uint32, appeared, vanished, data, destroy uintptr) uint32
	if !a.g.lookup(&watch, "g_bus_watch_name") || !a.g.lookup(&state.unwatch, "g_bus_unwatch_name") {
		return
	}
	gtkFcitxCallbacks.Do(func() {
		gtkFcitxCallbacks.appeared = purego.NewCallback(func(_, _, nameOwner, bit uintptr) {
			if app := gtkRunning.Load(); app != nil {
				app.fcitxWatch.present |= bit
				app.fcitxWatch.owners[bits.TrailingZeros(uint(bit))] = goString(nameOwner)
			}
		})
		gtkFcitxCallbacks.vanished = purego.NewCallback(func(_, _, bit uintptr) {
			if app := gtkRunning.Load(); app != nil {
				app.fcitxVanished(bit)
			}
		})
	})
	// The GTK module can use the main or portal service. Do not activate either.
	const sessionBus = 2 // G_BUS_TYPE_SESSION
	for index, name := range gtkFcitxServices {
		state.ids[index] = watch(sessionBus, &cString(name)[0], 0, gtkFcitxCallbacks.appeared, gtkFcitxCallbacks.vanished, 1<<index, 0)
	}
}

func (watch *gtkFcitxWatch) matches(ime *gtkIME) bool {
	if watch.contextID == nil || ime.ctx == 0 {
		return false
	}
	switch goString(watch.contextID(ime.ctx)) {
	case "fcitx", "fcitx5":
		return true
	}
	return false
}

func (a *gtkApplication) closeFcitxWatch() {
	if watch := &a.fcitxWatch; watch.moduleID != 0 {
		a.g.signalDisc(watch.settings, watch.moduleID)
	}
	for _, id := range a.fcitxWatch.ids {
		if id != 0 {
			a.fcitxWatch.unwatch(id)
		}
	}
	a.fcitxWatch = gtkFcitxWatch{}
}

func (a *gtkApplication) fcitxVanished(bit uintptr) {
	watch := &a.fcitxWatch
	wasPresent := watch.present
	watch.present &^= bit
	watch.owners[bits.TrailingZeros(uint(bit))] = ""
	if wasPresent == 0 || watch.present != 0 || a.stopping {
		return
	}
	// A cancellation can call back into the app and replace another window's context.
	type target struct {
		ime     *gtkIME
		context uintptr
	}
	var affected []target
	for _, window := range a.windows {
		if ime := window.ime; ime != nil && ime.direct == nil && ime.ownsServicePreedit(ime.ctx) && watch.matches(ime) {
			affected = append(affected, target{ime, ime.ctx})
		}
	}
	for _, target := range affected {
		target.ime.serviceLost(target.context)
	}
}

func (ime *gtkIME) ownsServicePreedit(context uintptr) bool {
	if !ime.current(context) {
		return false
	}
	if ime.tentative {
		return ime.session
	}
	return ime.engineComposing && ime.w.text.state.Composing
}

func (ime *gtkIME) serviceLost(context uintptr) {
	if !ime.ownsServicePreedit(context) {
		return
	}
	w, revision := ime.w, ime.w.text.stateRevision
	event := platform.TextEvent{Kind: platform.TextCancelComposition}
	if ime.tentative {
		event.CompositionPhase = text.CompositionCancel
	}
	// Retire first: GTK can relay an old result while it gives up focus.
	ime.close()
	if !w.app.stopping && !w.closed && w.ime == ime && ime.ctx == 0 && w.text.enabled &&
		w.text.stateRevision == revision && ime.sameOwner(&w.text.input) {
		ime.apply(event)
	}
	if !w.app.stopping && !w.closed && w.ime == ime && ime.ctx == 0 {
		ime.open()
		ime.sync()
	}
}
