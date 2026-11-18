//go:build linux && vivagtk

package platform

import "github.com/vivaui/viva/text"

// TextInputOwner identifies an editor without keeping its buffer or history alive.
type TextInputOwner = text.EditorInputIdentity

// EditorInputOwner snapshots ownership for GTK's native input context.
func EditorInputOwner(editor *text.Editor) TextInputOwner { return editor.InputIdentity() }
