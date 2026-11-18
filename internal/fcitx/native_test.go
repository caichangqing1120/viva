//go:build linux

package fcitx

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vivaui/viva/internal/dbus"
)

// The test harness starts its own Fcitx daemon and session bus. Never change
// the input method on a user's desktop merely because a daemon is running.
func TestNativeOrderedHandoff(t *testing.T) {
	policy := os.Getenv("VIVA_TEST_FCITX_SHARE")
	if policy == "" {
		t.Skip("requires a private Fcitx daemon fixture")
	}
	if !strings.HasPrefix(os.Getenv("DBUS_SESSION_BUS_ADDRESS"), "unix:path=/tmp/") {
		t.Fatal("expected a private test session bus")
	}
	c, err := Dial("viva-native-order-test", "wayland:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	const caps = Preedit | FormattedPreedit | SurroundingText | RelativeRect | ClientSidePanel
	if err := c.Focus(1, caps); err != nil {
		t.Fatal(err)
	}
	wait := func(accept func(Update) bool) {
		t.Helper()
		end := time.Now().Add(4 * time.Second)
		for time.Now().Before(end) {
			if err := c.Dispatch(); err != nil {
				t.Fatal(err)
			}
			for _, u := range c.Updates() {
				if accept(u) {
					return
				}
			}
			if err := c.Expire(); err != nil {
				t.Fatal(err)
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatal("timed out waiting for native Fcitx update")
	}
	wait(func(u Update) bool { return u.Owner == 1 && u.Result == nil && u.Event == nil })
	controller, err := dbus.DialSession()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(controller.Close)
	var e dbus.Encoder
	e.String("pinyin")
	for _, method := range []struct {
		name, sig string
		body      []byte
	}{{"SetCurrentIM", "s", e.Bytes()}, {"Activate", "", nil}} {
		reply, err := controller.Call(service, "/controller", "org.fcitx.Fcitx.Controller1", method.name, method.sig, method.body)
		if err != nil {
			t.Fatal(err)
		}
		reply.CloseFDs()
	}
	key := func(owner uint64, symbol, code uint32) {
		t.Helper()
		if err := c.Key(owner, Key{Symbol: symbol, Code: code}); err != nil {
			t.Fatal(err)
		}
	}
	key(1, 'n', 57)
	key(1, 'i', 31)
	if err := c.Focus(2, caps); err != nil {
		t.Fatal(err)
	}
	key(2, 'n', 57)
	if err := c.Suspend(); err != nil {
		t.Fatal(err)
	}
	if err := c.Focus(3, caps); err != nil {
		t.Fatal(err)
	}
	key(3, 'n', 57)
	key(3, 'i', 31)
	key(3, 0xff1b, 9)
	key(3, 'n', 57)
	key(3, 'i', 31)
	key(3, ' ', 65)
	var replies []Update
	var commits []string
	var lastPanel *Panel
	wait(func(u Update) bool {
		if u.Event != nil {
			switch u.Event.Kind {
			case Commit:
				commits = append(commits, u.Event.Text)
			case UpdatePanel:
				if len(u.Event.Panel.Candidates) != 0 {
					lastPanel = u.Event.Panel
				}
			}
		}
		if u.Result != nil {
			replies = append(replies, u)
		}
		return len(replies) == 9
	})
	for i, owner := range []uint64{1, 1, 2, 3, 3, 3, 3, 3, 3} {
		if replies[i].Owner != owner {
			t.Fatalf("reply %d owner=%d, want %d", i, replies[i].Owner, owner)
		}
	}
	if len(commits) != 0 {
		t.Fatalf("unsolicited commit from retired owner: %q", commits)
	}
	if policy == "No" {
		if replies[2].Result.Delivery != Declined || replies[3].Result.Delivery != Declined {
			t.Fatalf("independent mode was not honored: %+v / %+v", *replies[2].Result, *replies[3].Result)
		}
		return
	}
	for _, index := range []int{0, 2, 3} {
		r := replies[index].Result
		if r.Delivery != Handled || len(r.Events) == 0 || r.Events[0].Kind != UpdatePreedit || len(r.Events[0].Preedit) != 1 || r.Events[0].Preedit[0].Text != "n" {
			t.Fatalf("key %d didn't start marked text: %+v", index, *r)
		}
	}
	if r := replies[5].Result; len(r.Events) != 1 || r.Events[0].Kind != UpdatePreedit || len(r.Events[0].Preedit) != 0 {
		t.Fatalf("Escape didn't clear marked text: %+v", *r)
	}
	if r := replies[8].Result; len(r.Events) != 2 || r.Events[0].Kind != Commit || r.Events[0].Text != "\u4f60" || r.Events[1].Kind != UpdatePreedit || len(r.Events[1].Preedit) != 0 {
		t.Fatalf("confirmation didn't commit before clearing preedit: %+v", *r)
	}
	if lastPanel == nil || lastPanel.Candidates[0].Text != "\u4f60" {
		t.Fatalf("expected native candidate panel: %+v", lastPanel)
	}
	if err := c.SetCursor(3, 40, 90, 2, 20, 1); err != nil {
		t.Fatal(err)
	}
	if err := c.SetSurrounding(3, "\u4f60", 1, 1); err != nil {
		t.Fatal(err)
	}
	key(3, 'n', 57)
	key(3, 'i', 31)
	if err := c.SelectCandidate(2, 0); err != ErrFocus {
		t.Fatalf("stale pane could select a candidate: %v", err)
	}
	if err := c.SelectCandidate(3, 0); err != nil {
		t.Fatal(err)
	}
	wait(func(u Update) bool {
		if u.Event == nil || u.Event.Kind != Commit {
			return false
		}
		if u.Owner != 3 || u.Event.Text != "\u4f60" {
			t.Fatalf("selection reached the wrong editor: %+v, event=%+v", u, *u.Event)
		}
		return true
	})
	t.Logf("native policy %s preserved both first keys and produced %d ordered replies", policy, len(replies))
}
