package app

import (
	"fmt"
	"math"

	"github.com/vivaui/viva/internal/platform"
)

// ClipboardSourceFormat selects one native clipboard representation.
type ClipboardSourceFormat = platform.ClipboardSourceFormat

const (
	ClipboardHTMLSource = platform.ClipboardHTMLSource
	ClipboardRTFSource  = platform.ClipboardRTFSource
)

var (
	// ErrNoClipboardSource reports that the selected format has no source bytes.
	ErrNoClipboardSource = platform.ErrNoClipboardSource
	// ErrClipboardSourceTooLarge reports a source larger than the caller's limit.
	ErrClipboardSourceTooLarge = platform.ErrClipboardSourceTooLarge
)

// ClipboardSource reads the selected native HTML or RTF representation without
// decoding it or falling back to plain text. Windows includes the CF_HTML
// header and omits the native NUL terminator. The returned bytes are owned by
// the caller. limit must be positive and less than 2 GiB. Oversized sources are
// refused before copying into Go. Web and unsupported desktops return
// ErrUnsupported. A system clipboard read can wait on its owner; call it in
// response to a user action rather than during rendering.
func ClipboardSource(format ClipboardSourceFormat, limit int) ([]byte, error) {
	if format != ClipboardHTMLSource && format != ClipboardRTFSource {
		return nil, fmt.Errorf("app: invalid clipboard source format %d", format)
	}
	if limit <= 0 || limit >= math.MaxInt32 {
		return nil, fmt.Errorf("app: invalid clipboard source byte limit %d", limit)
	}
	if bytes, err, handled := driverClipboardSource(format, limit); handled {
		return bytes, err
	}
	return nativeClipboardSource(format, limit)
}
