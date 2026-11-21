package darwin

/*
#include <stdlib.h>
#include "system_darwin.h"
*/
import "C"

import (
	"unsafe"

	"github.com/vivaui/viva/internal/platform"
)

func ClipboardSource(format platform.ClipboardSourceFormat, limit int) ([]byte, error) {
	return pasteboardSource(nil, format, limit)
}

func pasteboardSource(name *C.char, format platform.ClipboardSourceFormat, limit int) ([]byte, error) {
	var data *C.uint8_t
	var length C.size_t
	var failure *C.char
	rtf := C.int(0)
	if format == platform.ClipboardRTFSource {
		rtf = 1
	}
	switch C.viva_system_clipboard_source(name, rtf, C.size_t(limit), &data, &length, &failure) {
	case 0:
		defer C.viva_system_free(unsafe.Pointer(data))
		return C.GoBytes(unsafe.Pointer(data), C.int(length)), nil
	case 1:
		return nil, platform.ErrNoClipboardSource
	case 2:
		return nil, platform.ErrClipboardSourceTooLarge
	default:
		return nil, systemError("read clipboard source", failure)
	}
}

func privatePasteboardSource(name string, format platform.ClipboardSourceFormat, limit int) ([]byte, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	return pasteboardSource(cName, format, limit)
}
