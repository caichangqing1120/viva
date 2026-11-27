package platform

import "errors"

type ClipboardSourceFormat uint8

const (
	ClipboardHTMLSource ClipboardSourceFormat = iota + 1
	ClipboardRTFSource
)

var (
	ErrNoClipboardSource       = errors.New("clipboard does not hold the requested source format")
	ErrClipboardSourceTooLarge = errors.New("clipboard source exceeds the byte limit")
)

func (format ClipboardSourceFormat) Targets() []string {
	if format == ClipboardRTFSource {
		return []string{"text/rtf", "application/rtf"}
	}
	return []string{"text/html"}
}
