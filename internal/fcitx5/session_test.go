//go:build linux

package fcitx5

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/vivaui/viva/internal/dbus"
)

type sent struct {
	dest, path, iface, name, sig string
	body                         []byte
}

type wire struct {
	sent   []sent
	inbox  []*dbus.Message
	closed bool
}

func (w *wire) FD() int { return 42 }
func (w *wire) Close()  { w.closed = true }
func (w *wire) Send(dest, path, iface, name, sig string, body []byte) (uint32, error) {
	w.sent = append(w.sent, sent{dest, path, iface, name, sig, append([]byte(nil), body...)})
	return uint32(len(w.sent)), nil
}
func (w *wire) Receive() ([]*dbus.Message, error) {
	messages := w.inbox
	w.inbox = nil
	return messages, nil
}

func step(t *testing.T, client *Client, w *wire, name, signature string, body []byte) {
	t.Helper()
	if got := w.sent[len(w.sent)-1]; got.name != name || got.dest != ":1.7" {
		t.Fatalf("latest call = %+v, want %s to :1.7", got, name)
	}
	w.inbox = append(w.inbox, &dbus.Message{Type: dbus.TypeReturn, ReplySerial: uint32(len(w.sent)), Sender: ":1.7", Signature: signature, Body: body})
	if err := client.Dispatch(); err != nil {
		t.Fatal(err)
	}
}

func contextReply(path string) []byte {
	var e dbus.Encoder
	e.String(path)
	mark := e.BeginArray(1)
	for i := range 16 {
		e.Byte(byte(i))
	}
	e.EndArray(mark)
	return e.Bytes()
}

func commitBody(value string) []byte {
	var e dbus.Encoder
	e.String(value)
	return e.Bytes()
}

func batchBody(value string, handled bool) []byte {
	var e dbus.Encoder
	mark := e.BeginArray(8)
	if value != "" {
		e.Uint32(0)
		e.Variant("s")
		e.String(value)
	}
	e.EndArray(mark)
	e.Bool(handled)
	return e.Bytes()
}

func fixture() (*Client, *wire) {
	w := new(wire)
	return newClient(w, service, ":1.7", "vivapad", "wayland:", time.Now), w
}

func TestRepeatedHandoffOrdersKeysAndRejectsRetiredSignals(t *testing.T) {
	c, w := fixture()
	const left = "/org/freedesktop/portal/inputcontext/1"
	const right = "/org/freedesktop/portal/inputcontext/2"
	const returned = "/org/freedesktop/portal/inputcontext/3"
	for _, owner := range []uint64{1, 2, 3} {
		if err := c.Focus(owner, Preedit|FormattedPreedit); err != nil {
			t.Fatal(err)
		}
		if err := c.Key(owner, Key{Symbol: 'n', Code: 57}); err != nil {
			t.Fatal(err)
		}
	}
	if len(w.sent) != 1 {
		t.Fatalf("calls before first ready = %v", w.sent)
	}
	change := w.sent[0]
	options := dbus.NewDecoder(change.body)
	end := options.ArrayLength(8)
	var properties []string
	for options.Err() == nil && options.Pos() < end {
		options.Align(8)
		properties = append(properties, options.String(), options.String())
	}
	if got := []string{change.path, change.iface, change.sig}; !reflect.DeepEqual(got, []string{methodPath, methodInterface, "a(ss)"}) ||
		options.Err() != nil || options.Pos() != end || !reflect.DeepEqual(properties, []string{"program", "vivapad", "display", "wayland:"}) {
		t.Fatalf("create call = %+v, options %q", change, properties)
	}
	step(t, c, w, "CreateInputContext", "oay", contextReply(left))
	step(t, c, w, "SetCapability", "", nil)
	step(t, c, w, "FocusIn", "", nil)
	step(t, c, w, "ProcessKeyEventBatch", "a(uv)b", batchBody("left", true))
	step(t, c, w, "FocusOut", "", nil)
	w.inbox = []*dbus.Message{
		{Type: dbus.TypeSignal, Sender: ":1.7", Path: left, Interface: contextInterface, Member: "CommitString", Signature: "s", Body: commitBody("stale")},
		{Type: dbus.TypeReturn, Sender: ":1.8", ReplySerial: uint32(len(w.sent)), Signature: "oay", Body: contextReply("/forged")},
	}
	step(t, c, w, "CreateInputContext", "oay", contextReply(right))
	step(t, c, w, "SetCapability", "", nil)
	step(t, c, w, "FocusIn", "", nil)
	if got := w.sent[len(w.sent)-1].path; got != left {
		t.Fatalf("destroyed %s, want old %s", got, left)
	}
	step(t, c, w, "DestroyIC", "", nil)
	step(t, c, w, "ProcessKeyEventBatch", "a(uv)b", batchBody("", false))
	step(t, c, w, "FocusOut", "", nil)
	step(t, c, w, "CreateInputContext", "oay", contextReply(returned))
	step(t, c, w, "SetCapability", "", nil)
	step(t, c, w, "FocusIn", "", nil)
	step(t, c, w, "DestroyIC", "", nil)
	step(t, c, w, "ProcessKeyEventBatch", "a(uv)b", batchBody("returned", true))
	w.inbox = []*dbus.Message{
		{Type: dbus.TypeSignal, Sender: ":1.7", Path: right, Interface: contextInterface, Member: "CommitString", Signature: "s", Body: commitBody("stale")},
		{Type: dbus.TypeSignal, Sender: ":1.7", Path: returned, Interface: contextInterface, Member: "CommitString", Signature: "s", Body: commitBody("candidate")},
	}
	if err := c.Dispatch(); err != nil {
		t.Fatal(err)
	}
	var owners []uint64
	var statuses []Delivery
	var commits []string
	for _, update := range c.Updates() {
		if update.Result != nil {
			owners = append(owners, update.Owner)
			statuses = append(statuses, update.Result.Delivery)
			for _, event := range update.Result.Events {
				commits = append(commits, event.Text)
			}
		}
		if update.Event != nil {
			commits = append(commits, update.Event.Text)
		}
	}
	if !reflect.DeepEqual(owners, []uint64{1, 2, 3}) || !reflect.DeepEqual(statuses, []Delivery{Handled, Declined, Handled}) ||
		!reflect.DeepEqual(commits, []string{"left", "returned", "candidate"}) {
		t.Fatalf("ordered updates: owners=%v deliveries=%v commits=%q", owners, statuses, commits)
	}
}

func TestServiceLossDistinguishesInFlightAndUnsentKeys(t *testing.T) {
	c, w := fixture()
	if err := c.Focus(1, Preedit); err != nil {
		t.Fatal(err)
	}
	step(t, c, w, "CreateInputContext", "oay", contextReply("/ctx/1"))
	step(t, c, w, "SetCapability", "", nil)
	step(t, c, w, "FocusIn", "", nil)
	if err := c.Key(1, Key{Symbol: 'n'}); err != nil {
		t.Fatal(err)
	}
	if err := c.Key(1, Key{Symbol: 'i'}); err != nil {
		t.Fatal(err)
	}
	var e dbus.Encoder
	e.String(service)
	e.String(":1.7")
	e.String(":1.8")
	w.inbox = []*dbus.Message{
		{Type: dbus.TypeSignal, Sender: busService, Path: busPath, Interface: busService, Member: "NameOwnerChanged", Signature: "sss", Body: e.Bytes()},
		{Type: dbus.TypeReturn, Sender: ":1.7", ReplySerial: uint32(len(w.sent)), Signature: "a(uv)b", Body: batchBody("stale", true)},
	}
	if err := c.Dispatch(); !errors.Is(err, ErrServiceChanged) || !w.closed {
		t.Fatalf("loss: %v, closed %v", err, w.closed)
	}
	var statuses []Delivery
	for _, u := range c.Updates() {
		if u.Result != nil {
			statuses = append(statuses, u.Result.Delivery)
		}
	}
	if !reflect.DeepEqual(statuses, []Delivery{Unknown, Unsent}) || len(c.Updates()) != 0 {
		t.Fatalf("loss results = %v", statuses)
	}
}

func TestMalformedBatchDoesNotExposePartialCommit(t *testing.T) {
	c, w := fixture()
	if err := c.Focus(1, Preedit); err != nil {
		t.Fatal(err)
	}
	step(t, c, w, "CreateInputContext", "oay", contextReply("/ctx/1"))
	step(t, c, w, "SetCapability", "", nil)
	step(t, c, w, "FocusIn", "", nil)
	if err := c.Key(1, Key{Symbol: 'n'}); err != nil {
		t.Fatal(err)
	}
	body := batchBody("not a partial commit", true)
	w.inbox = []*dbus.Message{{Type: dbus.TypeReturn, Sender: ":1.7", ReplySerial: uint32(len(w.sent)), Signature: "a(uv)b", Body: body[:len(body)-1]}}
	if err := c.Dispatch(); err == nil {
		t.Fatal("accepted a truncated reply")
	}
	updates := c.Updates()
	last := updates[len(updates)-1]
	if last.Result == nil || last.Result.Delivery != Unknown || len(last.Result.Events) != 0 {
		t.Fatalf("malformed batch exposed content: %+v", last)
	}
}

func TestSuspendKeepsUnfocusedModeUntilSuccessor(t *testing.T) {
	c, w := fixture()
	if err := c.Focus(1, Preedit); err != nil {
		t.Fatal(err)
	}
	step(t, c, w, "CreateInputContext", "oay", contextReply("/ctx/1"))
	step(t, c, w, "SetCapability", "", nil)
	step(t, c, w, "FocusIn", "", nil)
	c.Updates()
	if err := c.Suspend(); err != nil {
		t.Fatal(err)
	}
	step(t, c, w, "FocusOut", "", nil)
	if len(w.sent) != 4 {
		t.Fatal("suspend destroyed the mode donor")
	}
	if err := c.Key(1, Key{Symbol: 'n'}); err != ErrFocus {
		t.Fatalf("suspended context accepted a key: %v", err)
	}
	w.inbox = []*dbus.Message{{Type: dbus.TypeSignal, Sender: ":1.7", Path: "/ctx/1", Interface: contextInterface,
		Member: "CommitString", Signature: "s", Body: commitBody("stale")}}
	if err := c.Dispatch(); err != nil {
		t.Fatal(err)
	}
	if len(c.Updates()) != 0 {
		t.Fatal("suspended context delivered text")
	}
	if err := c.Focus(2, Preedit); err != nil {
		t.Fatal(err)
	}
	step(t, c, w, "CreateInputContext", "oay", contextReply("/ctx/2"))
	step(t, c, w, "SetCapability", "", nil)
	step(t, c, w, "FocusIn", "", nil)
	if got := w.sent[len(w.sent)-1].path; got != "/ctx/1" {
		t.Fatalf("destroyed %s, want the mode donor", got)
	}
	step(t, c, w, "DestroyIC", "", nil)
	if err := c.Suspend(); err != nil {
		t.Fatal(err)
	}
	step(t, c, w, "FocusOut", "", nil)
	if err := c.Focus(0, 0); err != nil {
		t.Fatal(err)
	}
	step(t, c, w, "DestroyIC", "", nil)
}

func TestStalledFocusReleasesOnlyAcceptedUnsentKeys(t *testing.T) {
	w := new(wire)
	now := time.Unix(10, 0)
	c := newClient(w, service, ":1.7", "vivapad", "wayland:", func() time.Time { return now })
	if err := c.Focus(1, Preedit); err != nil {
		t.Fatal(err)
	}
	for i := range queueLimit {
		if err := c.Key(1, Key{Time: uint32(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Key(1, Key{Symbol: 'x'}); err != ErrQueueFull {
		t.Fatalf("overflow was accepted: %v", err)
	}
	now = now.Add(requestTimeout)
	if err := c.Expire(); err != ErrTimeout || !w.closed || len(w.sent) != 1 {
		t.Fatalf("stalled focus: err=%v, closed=%t, sends=%d", err, w.closed, len(w.sent))
	}
	updates := c.Updates()
	if len(updates) != queueLimit {
		t.Fatalf("fallback count=%d, want %d", len(updates), queueLimit)
	}
	for i, u := range updates {
		if u.Owner != 1 || u.Result == nil || u.Result.Delivery != Unsent || u.Result.Key.Time != uint32(i) {
			t.Fatalf("fallback %d: %+v", i, u)
		}
	}
}
