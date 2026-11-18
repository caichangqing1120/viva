//go:build linux

package fcitx

import (
	"strings"
	"unicode/utf8"

	"github.com/vivaui/viva/internal/dbus"
)

const (
	maxBody     = 1 << 20
	maxElements = 256
)

type EventKind uint8

const (
	Commit EventKind = iota
	UpdatePreedit
	Forward
	DeleteSurrounding
	UpdatePanel
	FocusLost
)

type FormattedText struct {
	Text  string
	Flags int32
}

type Candidate struct{ Label, Text string }

type Panel struct {
	Preedit, Above, Below    []FormattedText
	Cursor, Selected, Layout int32
	Candidates               []Candidate
	Previous, Next           bool
}

type Event struct {
	Kind    EventKind
	Text    string
	Preedit []FormattedText
	Cursor  int32 // UTF-8 byte offset, or -1 if not displayed
	Key     Key
	Offset  int32
	Count   uint32
	Panel   *Panel
}

func decodeContext(m *dbus.Message) (string, bool) {
	if m.Signature != "oay" || len(m.Body) > 512 {
		return "", false
	}
	d := m.Args()
	path := d.String()
	end := d.ArrayLength(1)
	if !strings.HasPrefix(path, "/") || path == "/" || strings.HasSuffix(path, "/") || strings.Contains(path, "//") || end-d.Pos() != 16 {
		return "", false
	}
	for _, c := range path {
		if c != '/' && c != '_' && !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') && !(c >= '0' && c <= '9') {
			return "", false
		}
	}
	for range 16 {
		d.Byte()
	}
	return path, end == d.Pos() && d.Done() && d.Err() == nil
}

func decodeBatch(m *dbus.Message) (Result, bool) {
	if m.Signature != "a(uv)b" || len(m.Body) > maxBody {
		return Result{}, false
	}
	d := m.Args()
	end := d.ArrayLength(8)
	var result Result
	for d.Err() == nil && d.Pos() < end {
		if len(result.Events) == maxElements {
			return Result{}, false
		}
		d.Align(8)
		kind, sig := d.Uint32(), d.Signature()
		if kind > uint32(DeleteSurrounding) {
			return Result{}, false
		}
		event, valid := decodeEvent(EventKind(kind), sig, d)
		if !valid || d.Pos() > end {
			return Result{}, false
		}
		result.Events = append(result.Events, event)
	}
	if end != d.Pos() {
		return Result{}, false
	}
	handled := d.Uint32()
	if handled > 1 || !d.Done() || d.Err() != nil {
		return Result{}, false
	}
	result.Delivery = Declined
	if handled != 0 {
		result.Delivery = Handled
	}
	return result, true
}

func decodeSignal(m *dbus.Message) (Event, bool) {
	if len(m.Body) > maxBody {
		return Event{}, false
	}
	var kind EventKind
	switch m.Member {
	case "CommitString":
		kind = Commit
	case "UpdateFormattedPreedit":
		kind = UpdatePreedit
	case "ForwardKey":
		kind = Forward
	case "DeleteSurroundingText":
		kind = DeleteSurrounding
	case "UpdateClientSideUI":
		kind = UpdatePanel
	case "NotifyFocusOut":
		kind = FocusLost
	default:
		return Event{}, false
	}
	d := m.Args()
	event, ok := decodeEvent(kind, m.Signature, d)
	return event, ok && d.Done() && d.Err() == nil
}

func decodeEvent(kind EventKind, sig string, d *dbus.Decoder) (Event, bool) {
	e := Event{Kind: kind}
	switch kind {
	case Commit:
		if sig != "s" {
			return Event{}, false
		}
		e.Text = d.String()
		if !utf8.ValidString(e.Text) {
			return Event{}, false
		}
	case UpdatePreedit:
		if sig == "(a(si)i)" {
			d.Align(8)
		} else if sig != "a(si)i" {
			return Event{}, false
		}
		var ok bool
		e.Preedit, ok = formatted(d)
		if !ok {
			return Event{}, false
		}
		e.Cursor = d.Int32()
	case Forward:
		if sig == "(uub)" {
			d.Align(8)
		} else if sig != "uub" {
			return Event{}, false
		}
		e.Key.Symbol, e.Key.State = d.Uint32(), d.Uint32()
		release := d.Uint32()
		if release > 1 {
			return Event{}, false
		}
		e.Key.Release = release == 1
	case DeleteSurrounding:
		if sig == "(iu)" {
			d.Align(8)
		} else if sig != "iu" {
			return Event{}, false
		}
		e.Offset, e.Count = d.Int32(), d.Uint32()
	case UpdatePanel:
		if sig != "a(si)ia(si)a(si)a(ss)iibb" {
			return Event{}, false
		}
		panel := new(Panel)
		var ok bool
		if panel.Preedit, ok = formatted(d); !ok {
			return Event{}, false
		}
		panel.Cursor = d.Int32()
		if panel.Above, ok = formatted(d); !ok {
			return Event{}, false
		}
		if panel.Below, ok = formatted(d); !ok {
			return Event{}, false
		}
		end := d.ArrayLength(8)
		for d.Err() == nil && d.Pos() < end {
			if len(panel.Candidates) == maxElements {
				return Event{}, false
			}
			d.Align(8)
			label, text := d.String(), d.String()
			if !utf8.ValidString(label) || !utf8.ValidString(text) {
				return Event{}, false
			}
			panel.Candidates = append(panel.Candidates, Candidate{label, text})
		}
		if d.Pos() != end {
			return Event{}, false
		}
		panel.Selected, panel.Layout = d.Int32(), d.Int32()
		previous, next := d.Uint32(), d.Uint32()
		if previous > 1 || next > 1 {
			return Event{}, false
		}
		panel.Previous, panel.Next = previous == 1, next == 1
		e.Panel = panel
	case FocusLost:
		if sig != "" {
			return Event{}, false
		}
	default:
		return Event{}, false
	}
	return e, d.Err() == nil
}

func formatted(d *dbus.Decoder) ([]FormattedText, bool) {
	end := d.ArrayLength(8)
	var parts []FormattedText
	for d.Err() == nil && d.Pos() < end {
		if len(parts) == maxElements {
			return nil, false
		}
		d.Align(8)
		value, flags := d.String(), d.Int32()
		if !utf8.ValidString(value) {
			return nil, false
		}
		parts = append(parts, FormattedText{value, flags})
	}
	return parts, d.Err() == nil && d.Pos() == end
}
