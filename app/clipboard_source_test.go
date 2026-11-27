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

func TestClipboardSourceAndFilePromisesReplaceEachOther(t *testing.T) {
	for _, mode := range []string{"application", "driver"} {
		t.Run(mode, func(t *testing.T) {
			driver := newPromiseDriver(t)
			files := promiseTree(t)
			const rtf = `{\rtf1\ansi handoff}`
			driver.SetClipboardRTF(rtf, "rich text")
			released := 0
			if mode == "application" {
				if err := SetClipboardFilePromises(files, func() { released++ }); err != nil {
					t.Fatal(err)
				}
			} else {
				driver.SetClipboardFilePromises(files)
			}
			if got, err := ClipboardSource(ClipboardRTFSource, len(rtf)); got != nil || !errors.Is(err, ErrNoClipboardSource) {
				t.Errorf("file promises left RTF source %q, %v", got, err)
			}
			if got := driver.ClipboardFilePromises(); len(got) != len(files) || released != 0 {
				t.Fatalf("installed %d promises and released %d before replacement", len(got), released)
			}
			const html = "<b>replacement</b>"
			driver.SetClipboardHTML(html, "replacement")
			if got, err := ClipboardSource(ClipboardHTMLSource, len(html)); string(got) != html || err != nil {
				t.Fatalf("HTML replacing file promises = %q, %v", got, err)
			}
			if len(driver.ClipboardFilePromises()) != 0 {
				t.Fatal("HTML replacement left file promises")
			}
			wantReleased := 0
			if mode == "application" {
				wantReleased = 1
			}
			if released != wantReleased {
				t.Fatalf("released %d times, want %d", released, wantReleased)
			}
			driver.SetClipboardRTF(rtf, "again")
			if released != wantReleased {
				t.Fatalf("released %d times after another write, want %d", released, wantReleased)
			}
		})
	}
}
