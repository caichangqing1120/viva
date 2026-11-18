//go:build linux && vivagtk

package text

import "sync/atomic"

// EditorInputIdentity identifies one GTK text owner without retaining its editor.
// The zero identity denotes no editor.
type EditorInputIdentity struct{ id uint64 }

type editorInputOwner struct{ identity EditorInputIdentity }

var editorInputIdentities atomic.Uint64

// InputIdentity returns the stable GTK identity of the editor, or zero for nil.
// Like other Editor methods, it must be called on the editor's owning thread.
func (editor *Editor) InputIdentity() EditorInputIdentity {
	if editor == nil {
		return EditorInputIdentity{}
	}
	for editor.inputOwner.identity.id == 0 {
		editor.inputOwner.identity.id = editorInputIdentities.Add(1)
	}
	return editor.inputOwner.identity
}
