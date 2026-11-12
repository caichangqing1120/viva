package text

import (
	"reflect"
	"testing"
	"unsafe"
)

// historyPrivateField returns an addressable view of an unexported
// historyJournal field. The test uses reflection so it can observe the private
// producer boundary without widening the public History API.
func historyPrivateField(history *historyJournal, name string) (reflect.Value, bool) {
	typ := reflect.TypeOf(*history)
	field, ok := typ.FieldByName(name)
	if !ok {
		return reflect.Value{}, false
	}
	address := unsafe.Pointer(uintptr(unsafe.Pointer(history)) + field.Offset)
	return reflect.NewAt(field.Type, address).Elem(), true
}

func historyOpsInstalled(value reflect.Value) bool {
	switch value.Kind() {
	case reflect.Struct:
		for index := 0; index < value.NumField(); index++ {
			field := value.Field(index)
			if field.Kind() == reflect.Func && !field.IsNil() {
				return true
			}
		}
	case reflect.Pointer:
		if !value.IsNil() {
			return historyOpsInstalled(value.Elem())
		}
	}
	return false
}

// TestHistoryProducerPrivateBoundaryIsOwnedAndCharged pins the 132.4A private
// boundary: ordinary editors allocate no producer, TrackHistory installs a
// producer-side owner plus function table, and the enabled header charge covers
// that retained structure (including a separately allocated table if one is
// ever introduced).
func TestHistoryProducerPrivateBoundaryIsOwnedAndCharged(t *testing.T) {
	ordinary := NewEditor("abc")
	ordinary.Insert("x")
	ordinary.SetSavePoint()
	ordinary.Undo()
	ordinary.Redo()
	ordinary.ClearHistory()
	if ordinary.journal != nil && ordinary.journal.history != nil {
		t.Fatal("ordinary editor retained a history producer")
	}

	editor := NewEditor("abc")
	cursor, bootstrap, ok := editor.TrackHistory()
	if !ok || bootstrap != 3 {
		t.Fatalf("TrackHistory = %+v/%d/%t, want cursor/bootstrap 3/true", cursor, bootstrap, ok)
	}
	history := editor.journal.history
	if history == nil || !history.available {
		t.Fatal("TrackHistory did not retain an available producer")
	}
	owner, ok := historyPrivateField(history, "owner")
	if !ok {
		t.Fatal("historyJournal has no private producer owner")
	}
	if got, ok := owner.Interface().(*Editor); !ok || got != editor {
		t.Fatalf("historyJournal owner = %v, want the tracked editor", owner)
	}
	ops, ok := historyPrivateField(history, "ops")
	if !ok {
		t.Fatal("historyJournal has no private producer function table")
	}
	if !historyOpsInstalled(ops) {
		t.Fatal("historyJournal private function table is empty")
	}

	minimum := int(unsafe.Sizeof(*history))
	if ops.Kind() == reflect.Pointer && !ops.IsNil() {
		minimum += int(ops.Type().Elem().Size())
	}
	charged := historyHeaderRunes * int(unsafe.Sizeof(rune(0)))
	if charged < minimum {
		t.Fatalf("history header charge %d bytes does not cover retained boundary %d bytes", charged, minimum)
	}

	editor.SetMarkedText("x", Range{})
	if history.available {
		t.Fatal("composition invalidation left the producer available")
	}
}

// TestHistoryUnavailableGuardRemainsChargedAndRefuses pins the retained
// unavailable guard found by Codex storage review. Invalidating a live producer
// releases frames and primitive backing but keeps a distinct refusal guard; that
// owned allocation must stay charged, survive subsequent lifecycle calls, and
// never allow a guessed bootstrap.
func TestHistoryUnavailableGuardRemainsChargedAndRefuses(t *testing.T) {
	editor := NewEditor("abc")
	cursor, _ := historyMustTrack(t, editor)
	editor.invalidateHistory()
	history := editor.journal.history
	if history == nil || history.available {
		t.Fatalf("unavailable guard = %+v, want a retained unavailable journal", history)
	}
	check := func(stage string) {
		t.Helper()
		if history.cost != historyHeaderRunes {
			t.Fatalf("%s: unavailable guard cost = %d runes, want header %d", stage, history.cost, historyHeaderRunes)
		}
		charged := (editor.historyCost() - editor.historyRunes) * int(unsafe.Sizeof(rune(0)))
		if charged < int(unsafe.Sizeof(*history)) {
			t.Fatalf("%s: unavailable guard charge = %d bytes, retained structure is %d", stage, charged, unsafe.Sizeof(*history))
		}
		if frames, _, ok := editor.HistorySince(cursor); ok || frames != nil {
			t.Fatalf("%s: unavailable guard served frames", stage)
		}
		if got, bytes, ok := editor.TrackHistory(); ok || got != (HistoryCursor{}) || bytes != 0 {
			t.Fatalf("%s: TrackHistory re-enabled the unavailable guard: %+v/%d/%t", stage, got, bytes, ok)
		}
	}
	check("invalidate")
	actions := []struct {
		name string
		run  func()
	}{
		{"Unmark", editor.Unmark},
		{"Insert", func() { editor.Insert("x") }},
		{"Save", editor.SetSavePoint},
		{"Undo", func() { editor.Undo() }},
		{"Redo", func() { editor.Redo() }},
		{"Clear", editor.ClearHistory},
	}
	for _, action := range actions {
		action.run()
		check(action.name)
	}
}
