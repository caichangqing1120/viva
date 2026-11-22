package linux

import (
	"os"
	"strings"
	"testing"
	"time"
	"unsafe"
)

func TestTimedOutClipboardReplyCannotSatisfyTheNextRead(t *testing.T) {
	if os.Getenv("VIVA_LINUX_XCB_SMOKE") != "1" || os.Getenv("DISPLAY") == "" {
		t.Skip("requires an isolated X11 display")
	}
	api, connection, screen, err := openSystemConnection()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { api.disconnect(connection) })
	window := api.generateID(connection)
	api.createWindow(connection, xcbCopyFromParent, window, screen.Root, 0, 0, 1, 1, 0, xcbWindowClassInput, screen.RootVisual, 0, nil)
	clipboard, err := api.atom(connection, "CLIPBOARD")
	if err != nil {
		t.Fatal(err)
	}
	api.setSelectionOwner(connection, window, clipboard, 0)
	api.flush(connection)
	if owner, err := selectionOwner(api, connection, clipboard); err != nil || owner != window {
		t.Fatalf("clipboard owner = %d, %v; want %d", owner, err, window)
	}

	nextRequest := func() xcbSelectionRequestEvent {
		t.Helper()
		deadline := time.Now().Add(clipboardWait)
		file := pollFD{FD: api.fileDescriptor(connection), Events: xcbPollInput}
		for time.Now().Before(deadline) {
			for event := api.pollForEvent(connection); event != nil; event = api.pollForEvent(connection) {
				if (*xcbGenericEvent)(event).ResponseType&0x7f == xcbSelectionRequest {
					request := *(*xcbSelectionRequestEvent)(event)
					api.free(event)
					return request
				}
				api.free(event)
			}
			api.poll(&file, 1, 20)
		}
		t.Fatal("clipboard owner did not receive a request")
		return xcbSelectionRequestEvent{}
	}
	reply := func(request xcbSelectionRequestEvent, text string) {
		api.changeProperty(connection, xcbPropertyReplace, request.Requestor, request.Property, request.Target, 8, uint32(len(text)), unsafe.Pointer(unsafe.StringData(text)))
		notify := xcbSelectionNotifyWire{xcbSelectionNotifyEvent: xcbSelectionNotifyEvent{
			ResponseType: xcbSelectionNotify, Time: request.Time, Requestor: request.Requestor,
			Selection: request.Selection, Target: request.Target, Property: request.Property,
		}}
		api.sendEvent(connection, 0, request.Requestor, 0, (*byte)(unsafe.Pointer(&notify)))
		api.flush(connection)
	}
	type result struct {
		text string
		err  error
	}
	read := func() <-chan result {
		done := make(chan result, 1)
		go func() {
			text, err := readClipboardX11()
			done <- result{text, err}
		}()
		return done
	}
	await := func(done <-chan result) result {
		t.Helper()
		select {
		case got := <-done:
			return got
		case <-time.After(2 * clipboardWait):
			t.Fatal("clipboard read did not finish")
			return result{}
		}
	}
	first := read()
	stale := nextRequest()
	if got := await(first); got.err == nil || !strings.Contains(got.err.Error(), "did not respond") {
		t.Fatalf("first read = %q, %v; want a response timeout", got.text, got.err)
	}
	second := read()
	fresh := nextRequest()
	reply(stale, "expired clipboard value")
	select {
	case got := <-second:
		t.Fatalf("later read consumed a timed-out reply: %q, %v", got.text, got.err)
	case <-time.After(100 * time.Millisecond):
	}
	reply(fresh, "current clipboard value")
	if got := await(second); got.err != nil || got.text != "current clipboard value" {
		t.Fatalf("later read = %q, %v; want current clipboard value", got.text, got.err)
	}
}
