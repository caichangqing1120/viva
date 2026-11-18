//go:build linux && vivagtk

package linux

import (
	"reflect"
	"testing"

	"github.com/vivaui/viva/internal/fcitx"
)

func TestGtkFcitxDeliveryChecksOwnerAfterEveryEvent(t *testing.T) {
	owner := uint64(3)
	var got []string
	deliver := func(e fcitx.Event) {
		got = append(got, e.Text)
		owner++ // A returned frame moved focus during dispatch.
	}
	verdict := gtkFcitxDeliver(fcitx.Update{Owner: 2, Event: &fcitx.Event{Text: "stale"}}, func() uint64 { return owner }, deliver)
	if verdict || len(got) != 0 {
		t.Fatalf("stale update reached the new editor: verdict=%t events=%v", verdict, got)
	}
	result := &fcitx.Result{Delivery: fcitx.Declined, Events: []fcitx.Event{{Text: "accepted"}, {Text: "crossed"}}}
	verdict = gtkFcitxDeliver(fcitx.Update{Owner: 3, Result: result}, func() uint64 { return owner }, deliver)
	if verdict || !reflect.DeepEqual(got, []string{"accepted"}) {
		t.Fatalf("batch crossed focus boundary: verdict=%t events=%v", verdict, got)
	}
}

func TestGtkFcitxDeliveryNeverReplaysAnUncertainOrForwardedKey(t *testing.T) {
	for _, result := range []struct {
		status   fcitx.Delivery
		events   []fcitx.Event
		fallback bool
	}{
		{status: fcitx.Unsent, fallback: true},
		{status: fcitx.Declined, fallback: true},
		{status: fcitx.Handled},
		{status: fcitx.Unknown},
		{status: fcitx.Declined, events: []fcitx.Event{{Kind: fcitx.Forward}}},
	} {
		u := fcitx.Update{Owner: 1, Result: &fcitx.Result{Delivery: result.status, Events: result.events}}
		if got := gtkFcitxDeliver(u, func() uint64 { return 1 }, func(fcitx.Event) {}); got != result.fallback {
			t.Fatalf("status %v events %v fallback=%t want %t", result.status, result.events, got, result.fallback)
		}
	}
}
