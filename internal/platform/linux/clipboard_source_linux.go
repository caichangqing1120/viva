package linux

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/vivaui/viva/internal/platform"
)

func ClipboardSource(format platform.ClipboardSourceFormat, limit int) ([]byte, error) {
	if hooks := shellHooks.Load(); hooks != nil {
		if hooks.clipboardSource == nil {
			return nil, ErrUnsupported
		}
		return hooks.clipboardSource(format, limit)
	}
	if win := activeWindow.Load(); win != nil {
		win.mu.Lock()
		owned := activeWindow.Load() == win && win.connection != 0 && win.ownsClipboard
		html := win.clipboardHTML
		win.mu.Unlock()
		if owned {
			switch {
			case format != platform.ClipboardHTMLSource || html == "":
				return nil, platform.ErrNoClipboardSource
			case len(html) > limit:
				return nil, platform.ErrClipboardSourceTooLarge
			default:
				return []byte(html), nil
			}
		}
	}
	api, conn, requestor, clipboard, owner, err := openClipboardRequest()
	if err != nil {
		return nil, err
	}
	defer closeClipboardRequest()
	if owner == xcbAtomNone {
		return nil, platform.ErrNoClipboardSource
	}
	targets, err := api.atom(conn, "TARGETS")
	if err != nil {
		return nil, err
	}
	property, err := api.atom(conn, "VIVA_CLIPBOARD_SOURCE")
	if err != nil {
		return nil, err
	}
	offered, kind, ok, err := readSelectionBytes(api, conn, requestor, clipboard, targets, property, 1<<16)
	if errors.Is(err, platform.ErrClipboardImageTooLarge) {
		return nil, fmt.Errorf("linux: clipboard target list exceeds the byte limit")
	}
	if err != nil {
		return nil, err
	}
	if !ok || kind != xcbAtomAtom || len(offered)%4 != 0 {
		return nil, platform.ErrNoClipboardSource
	}
	for _, name := range format.Targets() {
		target, err := api.atom(conn, name)
		if err != nil {
			return nil, err
		}
		for offset := 0; offset < len(offered); offset += 4 {
			if binary.NativeEndian.Uint32(offered[offset:]) != target {
				continue
			}
			data, _, accepted, err := readSelectionBytes(api, conn, requestor, clipboard, target, property, limit)
			if errors.Is(err, platform.ErrClipboardImageTooLarge) {
				return nil, platform.ErrClipboardSourceTooLarge
			}
			if err != nil {
				return nil, err
			}
			if !accepted {
				return nil, fmt.Errorf("linux: clipboard owner refused %s", name)
			}
			if len(data) == 0 {
				return nil, platform.ErrNoClipboardSource
			}
			return data, nil
		}
	}
	return nil, platform.ErrNoClipboardSource
}
