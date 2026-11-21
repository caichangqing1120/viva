//go:build darwin || windows || linux

package app

import (
	"errors"
	"testing"
)

func TestClipboardSourceReadsOnlyTheRequestedFlavor(t *testing.T) {
	driver, err := NewDriver(Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer driver.Close()

	const html = "<b>\u6587</b>\r\n"
	const rtf = `{\rtf1\ansi \u25991?}`
	check := func(format ClipboardSourceFormat, limit int, want string, wantErr error) {
		t.Helper()
		got, err := ClipboardSource(format, limit)
		if string(got) != want || !errors.Is(err, wantErr) {
			t.Fatalf("format %d, limit %d: got %q, %v; want %q, %v", format, limit, got, err, want, wantErr)
		}
	}
	driver.SetClipboardHTML(html, "html fallback")
	check(ClipboardHTMLSource, len(html), html, nil)
	check(ClipboardHTMLSource, len(html)-1, "", ErrClipboardSourceTooLarge)
	check(ClipboardRTFSource, 100, "", ErrNoClipboardSource)

	driver.SetClipboardRTF(rtf, "rtf fallback")
	check(ClipboardRTFSource, len(rtf), rtf, nil)
	check(ClipboardHTMLSource, 100, "", ErrNoClipboardSource)
	if plain, err := ClipboardText(); plain != "rtf fallback" || err != nil {
		t.Fatalf("plain paste = %q, %v", plain, err)
	}
	if err := SetClipboardText("plain"); err != nil {
		t.Fatal(err)
	}
	check(ClipboardRTFSource, 100, "", ErrNoClipboardSource)
}

func TestClipboardSourceRejectsAnUnknownFormatOrInvalidLimit(t *testing.T) {
	for _, input := range []struct {
		format ClipboardSourceFormat
		limit  int
	}{{0, 100}, {ClipboardHTMLSource, 0}, {ClipboardRTFSource, -1}} {
		if got, err := ClipboardSource(input.format, input.limit); got != nil || err == nil {
			t.Fatalf("invalid input %+v got %q, %v", input, got, err)
		}
	}
}
