//go:build linux

package fcitx

import (
	"errors"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/vivaui/viva/internal/dbus"
)

var errArgument = errors.New("fcitx: invalid input context argument")

// SetCursor sends the caret rectangle in the coordinates specified by the
// context's capabilities. Scale converts logical units to device pixels.
func (c *Client) SetCursor(owner uint64, x, y, width, height int32, scale float64) error {
	if width < 0 || height < 0 || scale <= 0 || math.IsNaN(scale) || math.IsInf(scale, 0) {
		return errArgument
	}
	var e dbus.Encoder
	for _, v := range []int32{x, y, width, height} {
		e.Int32(v)
	}
	e.Float64(scale)
	return c.control(owner, "SetCursorRectV2", "iiiid", e.Bytes())
}

// SetSurrounding sends a UTF-8 window with cursor and anchor as rune offsets.
func (c *Client) SetSurrounding(owner uint64, text string, cursor, anchor uint32) error {
	if len(text) > maxBody || !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		return errArgument
	}
	count := uint32(utf8.RuneCountInString(text))
	if cursor > count || anchor > count {
		return errArgument
	}
	var e dbus.Encoder
	e.String(text)
	e.Uint32(cursor)
	e.Uint32(anchor)
	return c.control(owner, "SetSurroundingText", "suu", e.Bytes())
}

func (c *Client) SelectCandidate(owner uint64, index int32) error {
	if index < 0 {
		return errArgument
	}
	var e dbus.Encoder
	e.Int32(index)
	return c.control(owner, "SelectCandidate", "i", e.Bytes())
}

func (c *Client) PageCandidates(owner uint64, next bool) error {
	method := "PrevPage"
	if next {
		method = "NextPage"
	}
	return c.control(owner, method, "", nil)
}
