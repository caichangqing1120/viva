//go:build linux && vivagtk

package linux

import (
	"strings"
	"sync"

	"github.com/ebitengine/purego"
	fcitx "github.com/vivaui/viva/internal/fcitx5"
)

type gtkFcitxPopup struct {
	widget  uintptr
	owner   uint64
	actions map[uintptr]int32
	move    func(uintptr, *[4]int32, int32, int32, uint32, int32, int32)
	anchor  [4]int32
	width   int32
}

const (
	fcitxPagePrevious int32 = -1
	fcitxPageNext     int32 = -2
)

var gtkFcitxPopupOnce sync.Once
var gtkFcitxPopupHandler uintptr

func gtkFcitxPopupCallback() uintptr {
	gtkFcitxPopupOnce.Do(func() {
		gtkFcitxPopupHandler = purego.NewCallback(func(widget, id uintptr) uintptr {
			w := gtkWindowByID(id)
			if w == nil || w.ime == nil || w.ime.direct == nil {
				return 0
			}
			d := w.ime.direct
			owner := d.popup.owner
			if owner == 0 || d.current() != owner {
				return 0
			}
			index, ok := d.popup.actions[widget]
			if !ok {
				return 0
			}
			switch index {
			case fcitxPagePrevious, fcitxPageNext:
				d.receive(d.client.PageCandidates(owner, index == fcitxPageNext))
			default:
				d.receive(d.client.SelectCandidate(owner, index))
			}
			return 0
		})
	})
	return gtkFcitxPopupHandler
}

func (d *gtkDirectFcitx) showPopup() {
	d.hidePopup()
	panel := d.panel
	owner := d.current()
	if panel == nil || owner == 0 || len(panel.Candidates) == 0 && len(panel.Above) == 0 && len(panel.Below) == 0 {
		return
	}
	w := d.ime.w
	g := w.g
	var move func(uintptr, *[4]int32, int32, int32, uint32, int32, int32)
	var boxNew func(int32, int32) uintptr
	var buttonNew, labelNew func(*byte) uintptr
	var labelWidth, labelEllipsize func(uintptr, int32)
	var buttonImage func(*byte, int32) uintptr
	var tooltip func(uintptr, *byte)
	var buttonChild func(uintptr) uintptr
	var stateFlags func(uintptr, uint32, int32)
	var preferredSize func(uintptr, *[2]int32, *[2]int32)
	if !(g.lookup(&move, "gdk_window_move_to_rect") && g.lookup(&boxNew, "gtk_box_new") &&
		g.lookup(&buttonNew, "gtk_button_new_with_label") && g.lookup(&labelNew, "gtk_label_new") &&
		g.lookup(&labelWidth, "gtk_label_set_max_width_chars") && g.lookup(&labelEllipsize, "gtk_label_set_ellipsize") &&
		g.lookup(&buttonImage, "gtk_button_new_from_icon_name") && g.lookup(&tooltip, "gtk_widget_set_tooltip_text") &&
		g.lookup(&buttonChild, "gtk_bin_get_child") && g.lookup(&stateFlags, "gtk_widget_set_state_flags") &&
		g.lookup(&preferredSize, "gtk_widget_get_preferred_size")) {
		return
	}
	widget := g.windowNew(1) // GTK_WINDOW_POPUP
	if widget == 0 {
		return
	}
	d.popup = gtkFcitxPopup{widget: widget, owner: owner, move: move, actions: map[uintptr]int32{}}
	g.windowSetDecorated(widget, 0)
	g.windowAcceptFocus(widget, 0)
	g.windowSetTypeHint(widget, 8) // GDK_WINDOW_TYPE_HINT_POPUP_MENU
	g.windowSetTransient(widget, w.toplevel)
	g.widgetSetCanFocus(widget, 0)
	box := boxNew(1, 2) // GTK_ORIENTATION_VERTICAL
	g.containerAdd(widget, box)
	addLabel := func(parts []fcitx.FormattedText) {
		var message strings.Builder
		for _, part := range parts {
			message.WriteString(strings.ReplaceAll(part.Text, "\x00", ""))
		}
		if message.Len() == 0 {
			return
		}
		label := labelNew(&cString(message.String())[0])
		labelWidth(label, 32)
		labelEllipsize(label, 3) // PANGO_ELLIPSIZE_END
		g.containerAdd(box, label)
	}
	addButton := func(parent, button uintptr, index int32) {
		g.widgetSetCanFocus(button, 0)
		d.popup.actions[button] = index
		g.connect(button, "clicked", gtkFcitxPopupCallback(), w.id)
		g.containerAdd(parent, button)
	}
	addLabel(panel.Above)
	columns := 1
	if panel.Layout == 2 || w.height < 480 {
		columns = min(4, max(1, int(w.width)/200))
	}
	row := box
	for index, candidate := range panel.Candidates {
		if columns > 1 && index%columns == 0 {
			row = boxNew(0, 2)
			g.containerAdd(box, row)
		}
		name := strings.ReplaceAll(candidate.Label+candidate.Text, "\x00", "")
		button := buttonNew(&cString(name)[0])
		if label := buttonChild(button); label != 0 {
			labelWidth(label, 16)
			labelEllipsize(label, 3)
		}
		if int32(index) == panel.Selected {
			stateFlags(button, 1<<2, 0) // GTK_STATE_FLAG_SELECTED
		}
		addButton(row, button, int32(index))
	}
	if panel.Previous || panel.Next {
		nav := boxNew(0, 2)
		g.containerAdd(box, nav)
		if panel.Previous {
			button := buttonImage(&cString("go-previous-symbolic")[0], 1)
			tooltip(button, &cString("Previous candidates")[0])
			addButton(nav, button, fcitxPagePrevious)
		}
		if panel.Next {
			button := buttonImage(&cString("go-next-symbolic")[0], 1)
			tooltip(button, &cString("Next candidates")[0])
			addButton(nav, button, fcitxPageNext)
		}
	}
	addLabel(panel.Below)
	g.widgetShowAll(box)
	var minimum, natural [2]int32
	preferredSize(box, &minimum, &natural)
	d.popup.width = max(natural[0], 1)
	g.windowSetDefault(widget, d.popup.width, max(natural[1], 1))
	g.widgetRealize(widget)
	g.gdkWindowTransient(g.widgetGetWindow(widget), g.widgetGetWindow(w.toplevel))
	d.placePopup()
}

func (d *gtkDirectFcitx) placePopup() {
	if d.popup.widget == 0 {
		return
	}
	x, y, width, height := d.ime.caretRectangle()
	w := d.ime.w
	var left, top int32
	w.g.widgetTranslate(w.area, w.toplevel, int32(x*w.ratio), int32(y*w.ratio), &left, &top)
	left = min(left, max(0, w.g.widgetAllocW(w.toplevel)-d.popup.width))
	rect := [4]int32{left, top, max(1, int32(width*w.ratio)), max(1, int32(height*w.ratio))}
	if rect == d.popup.anchor {
		return
	}
	d.popup.anchor = rect
	widget := d.popup.widget
	w.g.widgetHide(widget)
	const southWest, northWest, slideX, flipY = 7, 1, 1 << 2, 1 << 1
	d.popup.move(w.g.widgetGetWindow(widget), &rect, southWest, northWest, slideX|flipY, 0, 0)
	w.g.widgetShowAll(widget)
}

func (d *gtkDirectFcitx) hidePopup() {
	if widget := d.popup.widget; widget != 0 {
		d.popup = gtkFcitxPopup{}
		d.ime.w.g.widgetDestroy(widget)
	}
}
