//go:build js && wasm

package web

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/png"
	"syscall/js"
	"testing"
	"time"

	"github.com/vivaui/viva/internal/platform"
)

// installClipboard replaces navigator.clipboard with a stub whose read() resolves
// to one item offering the given types, or rejects with the named error.
func installClipboard(t *testing.T, script string) {
	t.Helper()
	stub := js.Global().Get("Function").New(script).Invoke()
	navigator := js.Global().Get("Object").New()
	navigator.Set("clipboard", stub)
	js.Global().Get("Object").Call("defineProperty", js.Global(), "navigator",
		map[string]any{"value": navigator, "configurable": true, "writable": true})
}

func readImage(t *testing.T) ([]byte, error) {
	t.Helper()
	type result struct {
		png []byte
		err error
	}
	done := make(chan result, 1)
	ReadClipboardImagePNG(func(png []byte, err error) { done <- result{png, err} })
	select {
	case got := <-done:
		return got.png, got.err
	case <-time.After(5 * time.Second):
		t.Fatal("the clipboard read never completed")
		return nil, nil
	}
}

func TestReadClipboardImagePNG(t *testing.T) {
	var fixture bytes.Buffer
	if err := png.Encode(&fixture, image.NewNRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(fixture.Bytes())
	installClipboard(t, `return { read: () => Promise.resolve([
		{ types: ["text/plain"], getType: () => Promise.reject(new Error("text")) },
		{ types: ["image/png"], getType: () => Promise.resolve(new Blob([Uint8Array.from(atob("`+encoded+`"), c => c.charCodeAt(0))])) },
	]) }`)
	got, err := readImage(t)
	if err != nil || !bytes.Equal(got, fixture.Bytes()) {
		t.Fatalf("PNG readback = %d bytes, %v; want %d unchanged bytes", len(got), err, fixture.Len())
	}
}

func TestReadClipboardImageReportsEachFailure(t *testing.T) {
	installClipboard(t, `return { read: () => Promise.resolve([{ types: ["text/plain"] }]) }`)
	if _, err := readImage(t); !errors.Is(err, platform.ErrNoClipboardImage) {
		t.Fatalf("no image: error = %v", err)
	}
	installClipboard(t, `return { read: () => Promise.resolve([
		{ types: ["image/png"], getType: () => Promise.resolve(new Blob([new Uint8Array(12)])) }]) }`)
	if _, err := readImage(t); err == nil {
		t.Fatal("bytes that are not a PNG were accepted")
	}
	installClipboard(t, `return { read: () => Promise.reject(Object.assign(new Error("no"), { name: "NotAllowedError" })) }`)
	if _, err := readImage(t); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("denied: error = %v", err)
	}
	installClipboard(t, `return {}`)
	if _, err := readImage(t); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("no API: error = %v", err)
	}
}
