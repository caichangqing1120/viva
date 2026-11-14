//go:build !darwin && !windows && !linux && !(js && wasm)

package app

import "net/url"

func nativeSystemProxy(*url.URL) (*url.URL, error) { return nil, ErrSystemProxyUnavailable }
