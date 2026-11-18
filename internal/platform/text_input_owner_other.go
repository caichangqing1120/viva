//go:build !linux || !vivagtk

package platform

import "github.com/vivaui/viva/text"

// TextInputOwner carries no state on platforms that do not reuse a GTK context.
type TextInputOwner struct{}

func EditorInputOwner(*text.Editor) TextInputOwner { return TextInputOwner{} }
