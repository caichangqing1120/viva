package windows

import (
	"bytes"
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"github.com/vivaui/viva/internal/platform"
)

var rtfFormat = sync.OnceValue(func() uintptr {
	name, _ := syscall.UTF16PtrFromString("Rich Text Format")
	format, _, _ := registerClipboardFormat.Call(uintptr(unsafe.Pointer(name)))
	return format
})

func ClipboardSource(kind platform.ClipboardSourceFormat, limit int) ([]byte, error) {
	var format uintptr
	if kind == platform.ClipboardRTFSource {
		format = rtfFormat()
	} else {
		format = registeredHTMLFormat()
	}
	if format == 0 {
		return nil, fmt.Errorf("windows: register clipboard source format failed")
	}
	clipboardMu.Lock()
	defer clipboardMu.Unlock()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if opened, _, callErr := openClipboard.Call(0); opened == 0 {
		return nil, windowsCallError("open clipboard", callErr)
	}
	defer closeClipboard.Call()
	if available, _, _ := clipboardFormatAvailable.Call(format); available == 0 {
		return nil, platform.ErrNoClipboardSource
	}
	handle, _, callErr := clipboardData.Call(format)
	if handle == 0 {
		return nil, windowsCallError("read clipboard source", callErr)
	}
	size, _, callErr := globalSize.Call(handle)
	if size == 0 {
		return nil, windowsCallError("size clipboard source", callErr)
	}
	memory, _, callErr := globalLock.Call(handle)
	if memory == 0 {
		return nil, windowsCallError("lock clipboard source", callErr)
	}
	defer globalUnlock.Call(handle)
	return copyClipboardSource(unsafe.Slice((*byte)(pointer(memory)), min(size, uintptr(limit)+1)), limit)
}

// The native block can include a NUL terminator and allocator padding. Limit
// the scan to one more byte than the caller will accept.
func copyClipboardSource(block []byte, limit int) ([]byte, error) {
	if end := bytes.IndexByte(block, 0); end >= 0 {
		block = block[:end]
	}
	if len(block) == 0 {
		return nil, platform.ErrNoClipboardSource
	}
	if len(block) > limit {
		return nil, platform.ErrClipboardSourceTooLarge
	}
	return bytes.Clone(block), nil
}
