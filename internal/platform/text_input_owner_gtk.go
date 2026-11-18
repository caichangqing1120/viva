//go:build linux && vivagtk

package platform

import (
	"weak"

	"github.com/vivaui/viva/text"
)

// TextInputOwner identifies an editor without keeping its buffer or history alive.
type TextInputOwner = weak.Pointer[text.Editor]

// EditorInputOwner snapshots ownership for GTK's native input context.
func EditorInputOwner(editor *text.Editor) TextInputOwner { return weak.Make(editor) }
