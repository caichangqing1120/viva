//go:build linux && vivagtk

package linux

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	fcitx "github.com/vivaui/viva/internal/fcitx5"
	"github.com/vivaui/viva/internal/platform"
	"github.com/vivaui/viva/text"
)

type gtkDirectFcitx struct {
	ime           *gtkIME
	client        *fcitx.Client
	generation    uint64
	owner, keyTag uint64
	keys          map[uint64]gdkEventKey
	task          uintptr
	source        uint32
	due           time.Time
	panel         *fcitx.Panel
	popup         gtkFcitxPopup
	failing       bool
}

type gtkDirectFcitxFailure struct {
	blocked bool
	owners  [2]string
}

func (failure gtkDirectFcitxFailure) canTry(owners [2]string) bool {
	if !failure.blocked {
		return true
	}
	for i, owner := range owners {
		if owner != "" && owner != failure.owners[i] {
			return true
		}
	}
	return false
}

func gtkDirectFcitxAllowed(override, module string) bool {
	return (override == "" || override == "1") && (module == "fcitx" || module == "fcitx5")
}

func gtkDirectFcitxField(input *platform.TextInput) bool {
	if input.Secure || input.Terminal || input.DisableIME {
		return false
	}
	// Owned single-line editors can use the ordered transport while keeping
	// legacy composition events, even if the window has no editable document.
	return input.State.TentativeComposition || !input.Multiline &&
		input.State.InputOwner != (platform.TextInputOwner{})
}

func (ime *gtkIME) startDirect() bool {
	w := ime.w
	override := os.Getenv("VIVA_GTK_FCITX_DIRECT")
	if !gtkDirectFcitxAllowed(override, "fcitx") || !ime.directFailure.canTry(w.app.fcitxWatch.owners) || !ime.focused || !w.text.enabled ||
		!gtkDirectFcitxField(&w.text.input) || w.nativeKeys || ime.active {
		return false
	}
	ime.realized()
	var contextID func(uintptr) uintptr
	if !ime.g.lookup(&contextID, "gtk_im_multicontext_get_context_id") {
		return false
	}
	if !gtkDirectFcitxAllowed(override, goString(contextID(ime.ctx))) {
		return false
	}
	w.app.watchFcitx(ime)
	display := "x11:"
	if w.app.wayland {
		display = "wayland:"
	}
	client, err := fcitx.Dial(filepath.Base(os.Args[0]), display)
	if err != nil {
		ime.directFailure = gtkDirectFcitxFailure{blocked: true, owners: w.app.fcitxWatch.owners}
		return false
	}
	ime.directFailure = gtkDirectFcitxFailure{}
	ime.direct = &gtkDirectFcitx{ime: ime, client: client, keys: make(map[uint64]gdkEventKey)}
	w.app.watchDirectFcitxModule(contextID)
	w.app.rebuildPoll()
	return true
}

func (d *gtkDirectFcitx) current() uint64 {
	ime, w := d.ime, d.ime.w
	if ime.direct != d || w.ime != ime || w.closed || w.nativeKeys || w.app.stopping || !ime.focused ||
		!w.text.enabled || !ime.sameOwner(&w.text.input) {
		return 0
	}
	return d.owner
}

func (d *gtkDirectFcitx) cancelPreedit() {
	ime := d.ime
	if d.current() != 0 && ime.session {
		ime.session, ime.engineComposing = false, false
		d.hidePopup()
		event := platform.TextEvent{Kind: platform.TextCancelComposition}
		if ime.tentative {
			event.CompositionPhase = text.CompositionCancel
		}
		ime.apply(event)
	}
}

func (d *gtkDirectFcitx) sync() {
	if d.failing {
		return
	}
	ime, w := d.ime, d.ime.w
	input := &w.text.input
	if w.text.enabled && !gtkDirectFcitxField(input) {
		d.close()
		ime.sync()
		return
	}
	if !ime.focused || !w.text.enabled || w.nativeKeys || w.app.saveDialogOwner == w {
		d.suspend()
		return
	}
	if d.owner == 0 || !ime.sameOwner(input) || ime.engineComposing && !w.text.state.Composing {
		ime.target, ime.editor = input.Target, input.State.InputOwner
		ime.owner, ime.tentative = input.State.CompositionIdentity, input.State.TentativeComposition
		ime.session, ime.engineComposing = false, false
		ime.haveCursor, ime.hasSources, ime.sent = false, false, false
		d.generation++
		d.owner, d.panel = d.generation, nil
		d.hidePopup()
		caps := fcitx.Preedit | fcitx.FormattedPreedit | fcitx.SurroundingText | fcitx.ReportKeyRepeat | fcitx.ClientSidePanel
		if w.app.wayland {
			caps |= fcitx.RelativeRect
		}
		if err := d.client.Focus(d.owner, caps); err != nil {
			d.receive(err)
			return
		}
	}
	d.updateCaret()
	d.placePopup()
	d.arm()
}

var errGtkFcitxModuleChanged = errors.New("linux: GTK input module changed")

func (d *gtkDirectFcitx) refreshModule(contextID func(uintptr) uintptr) {
	ime := d.ime
	// The getter reports the last GTK context. Reattach its window so GTK
	// resolves the newly selected module before we inspect it.
	ime.realized()
	if ime.direct == d && !gtkDirectFcitxAllowed(os.Getenv("VIVA_GTK_FCITX_DIRECT"), goString(contextID(ime.ctx))) {
		d.receive(errGtkFcitxModuleChanged)
	}
}

func (d *gtkDirectFcitx) suspend() {
	if d.owner == 0 {
		return
	}
	ime, w := d.ime, d.ime.w
	wasComposing := ime.session
	d.owner, d.panel = 0, nil
	ime.session, ime.engineComposing = false, false
	d.hidePopup()
	err := d.client.Suspend()
	// The unfocused transport cannot deliver the old context's preedit end.
	if wasComposing && !ime.focused && w.ime == ime && ime.direct == d &&
		!w.closed && w.text.enabled && w.text.state.Composing && ime.sameOwner(&w.text.input) {
		event := platform.TextEvent{Kind: platform.TextCancelComposition}
		if ime.tentative {
			event.CompositionPhase = text.CompositionCancel
		}
		ime.apply(event)
	}
	d.receive(err)
}

func (d *gtkDirectFcitx) updateCaret() {
	ime, w := d.ime, d.ime.w
	x, y, width, height := ime.caretRectangle()
	var left, top int32
	w.g.widgetTranslate(w.area, w.toplevel, int32(x*w.ratio), int32(y*w.ratio), &left, &top)
	if !w.app.wayland {
		var wx, wy int32
		w.g.windowGetPosition(w.toplevel, &wx, &wy)
		left, top = left+wx, top+wy
	}
	location := [4]int32{left, top, max(1, int32(width*w.ratio)), max(1, int32(height*w.ratio))}
	if !ime.haveCursor || location != ime.cursor {
		ime.cursor, ime.haveCursor = location, true
		if err := d.client.SetCursor(d.owner, location[0], location[1], location[2], location[3], float64(max(w.scale, 1))); err != nil {
			d.receive(err)
			return
		}
	}
	ime.updateSurrounding()
	if !ime.sent {
		s := ime.window
		ime.sent = true
		if err := d.client.SetSurrounding(d.owner, s.Text, uint32(s.Cursor), uint32(s.Anchor)); err != nil {
			d.receive(err)
		}
	}
}

func (d *gtkDirectFcitx) queueKey(event *gdkEventKey) bool {
	owner := d.current()
	if owner == 0 {
		return false
	}
	d.keyTag++
	key := fcitx.Key{Symbol: event.Keyval, Code: uint32(event.HardwareKeycode), State: event.State & 0xff,
		Release: event.Type == gdkKeyRelease, Time: event.Time, Tag: d.keyTag}
	if !key.Release && d.ime.w.keys.down[event.HardwareKeycode] {
		key.State |= 1 << 31
	}
	if err := d.client.Key(owner, key); err != nil {
		d.receive(err)
		return false
	}
	copy := *event
	copy.Window, copy.String, copy.Length = 0, 0, 0
	d.keys[key.Tag] = copy
	d.receive(nil)
	return true
}

func gtkFcitxDeliver(update fcitx.Update, current func() uint64, deliver func(fcitx.Event)) bool {
	owned := func() bool { return update.Owner != 0 && update.Owner == current() }
	if !owned() {
		return false
	}
	if update.Event != nil {
		deliver(*update.Event)
	}
	if update.Result == nil {
		return false
	}
	consumed := false
	for _, e := range update.Result.Events {
		if !owned() {
			return false
		}
		deliver(e)
		consumed = consumed || e.Kind == fcitx.Forward || e.Kind == fcitx.Commit
	}
	return owned() && !consumed && (update.Result.Delivery == fcitx.Declined || update.Result.Delivery == fcitx.Unsent)
}

func gtkFcitxDrain(updates []fcitx.Update, disconnected bool, current func() uint64, deliver func(fcitx.Event), cancel func(), resolve func(fcitx.Update, bool)) {
	for _, update := range updates {
		fallback := gtkFcitxDeliver(update, current, deliver)
		if disconnected && fallback && cancel != nil {
			// Resolve the old composition before the first unfiltered input.
			cancel()
			cancel = nil
			fallback = current() == update.Owner
		}
		resolve(update, fallback)
	}
	if disconnected && cancel != nil {
		cancel()
	}
}

func (d *gtkDirectFcitx) receive(err error) {
	if d.ime.direct != d || d.failing {
		return
	}
	if err != nil {
		d.failing = true
		d.client.Close()
	}
	gtkFcitxDrain(d.client.Updates(), err != nil, d.current, d.event, d.cancelPreedit, func(update fcitx.Update, fallback bool) {
		if update.Result != nil {
			key, found := d.keys[update.Result.Key.Tag]
			delete(d.keys, update.Result.Key.Tag)
			if fallback && found {
				d.ime.w.routeKey(&key, 0, false)
			}
		}
	})
	if err == nil {
		d.arm()
		return
	}
	ime := d.ime
	if !errors.Is(err, errGtkFcitxModuleChanged) {
		owners := ime.w.app.fcitxWatch.owners
		name, owner := d.client.ServiceOwner()
		for i, service := range gtkFcitxServices {
			if service == name {
				// The watcher may already have reported the replacement before
				// this client received NameOwnerChanged on its own connection.
				owners[i] = owner
			}
		}
		ime.directFailure = gtkDirectFcitxFailure{blocked: true, owners: owners}
	}
	d.close()
	ime.sync()
}

func (d *gtkDirectFcitx) event(event fcitx.Event) {
	ime := d.ime
	switch event.Kind {
	case fcitx.Commit:
		ime.commit(event.Text)
	case fcitx.UpdatePreedit:
		var value strings.Builder
		var highlight text.Range
		for _, part := range event.Preedit {
			if part.Flags&8 != 0 {
				start := utf8.RuneCountInString(value.String())
				highlight = text.Range{Start: start, End: start + utf8.RuneCountInString(part.Text)}
			}
			value.WriteString(part.Text)
		}
		if value.Len() == 0 {
			d.cancelPreedit()
			return
		}
		if highlight.Start == highlight.End {
			cursor := utf8.RuneCountInString(value.String()[:min(max(int(event.Cursor), 0), value.Len())])
			highlight = text.Range{Start: cursor, End: cursor}
		}
		ime.session, ime.engineComposing = true, true
		update := platform.TextEvent{Kind: platform.TextSetMarked, Text: value.String(), MarkedSelection: highlight}
		if ime.tentative {
			update.CompositionPhase = text.CompositionUpdate
		} else {
			update.CancelOnHandoff = true
		}
		ime.apply(update)
	case fcitx.Forward:
		kind := gdkKeyPress
		if event.Key.Release {
			kind = gdkKeyRelease
		}
		key := gdkEventKey{Type: kind, Keyval: event.Key.Symbol, State: event.Key.State}
		ime.w.routeKey(&key, 0, false)
	case fcitx.DeleteSurrounding:
		if r, ok := ime.window.fieldRange(int(event.Offset), int(event.Count)); ok {
			ime.apply(platform.TextEvent{Kind: platform.TextInsert, Replacement: r, HasReplacement: true})
		}
	case fcitx.UpdatePanel:
		d.panel = event.Panel
		d.showPopup()
	}
}

func (d *gtkDirectFcitx) arm() {
	if d.ime.direct != d {
		return
	}
	due, pending := d.client.Deadline()
	if d.source != 0 && pending && due == d.due {
		return
	}
	d.removeTimer()
	if !pending {
		return
	}
	a := d.ime.w.app
	d.due = due
	d.task = a.tasks.add(func() {
		d.source, d.task = 0, 0
		if d.ime.direct == d {
			d.receive(d.client.Expire())
		}
	})
	ms := max(1, (time.Until(due)+time.Millisecond-1)/time.Millisecond)
	d.source = a.g.timeoutAdd(uint32(ms), callbacks().task, d.task)
}

func (d *gtkDirectFcitx) removeTimer() {
	if d.source != 0 {
		a := d.ime.w.app
		a.g.sourceRemove(d.source)
		a.tasks.take(d.task)
		d.source, d.task = 0, 0
	}
}

func (d *gtkDirectFcitx) close() {
	if d.ime.direct != d {
		return
	}
	d.removeTimer()
	d.ime.direct = nil
	d.hidePopup()
	d.ime.session, d.ime.engineComposing = false, false
	d.client.Close()
	d.ime.w.app.rebuildPoll()
}
