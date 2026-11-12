package text

import (
	"testing"
	"unsafe"
)

var historyJournalBenchmarkEditor *Editor

func BenchmarkHistoryDefaultConstruction(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		historyJournalBenchmarkEditor = NewEditor("base")
	}
}

func BenchmarkHistoryDefaultTyping(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		editor := NewEditor("")
		for _, value := range []string{"h", "e", "\n", "l", "l", "o"} {
			editor.Insert(value)
		}
		editor.Undo()
		editor.Redo()
	}
}

func BenchmarkHistoryOptInTyping(b *testing.B) {
	b.ReportAllocs()
	b.ReportMetric(float64(unsafe.Sizeof(Editor{})), "editor-B")
	b.ReportMetric(float64(unsafe.Sizeof(historyJournal{})), "history-B")
	for b.Loop() {
		editor := NewEditor("")
		editor.TrackHistory()
		for _, value := range []string{"h", "e", "\n", "l", "l", "o"} {
			editor.Insert(value)
		}
		editor.SetSavePoint()
		historyJournalBenchmarkEditor = editor
	}
}

func BenchmarkHistoryForwardFrames(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		editor := NewEditor("")
		editor.TrackHistory()
		for range 16 {
			editor.BreakUndoGroup()
			editor.Insert("x")
		}
		historyJournalBenchmarkEditor = editor
	}
	b.ReportMetric(16, "frames/op")
}

func BenchmarkHistoryUndoRedoFrames(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		editor := NewEditor("")
		editor.TrackHistory()
		for range 8 {
			editor.BreakUndoGroup()
			editor.Insert("x")
		}
		for range 8 {
			editor.Undo()
		}
		for range 8 {
			editor.Redo()
		}
		historyJournalBenchmarkEditor = editor
	}
	b.ReportMetric(16, "replay-frames/op")
}
