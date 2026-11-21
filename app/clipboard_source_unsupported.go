//go:build !darwin && !windows && !linux

package app

func nativeClipboardSource(ClipboardSourceFormat, int) ([]byte, error) { return nil, ErrUnsupported }
