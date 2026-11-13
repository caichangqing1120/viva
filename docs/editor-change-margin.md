# Code editor change margin

`components.CodeEditorSnapshot.ChangeLines` paints the change-history margin that
the original editor family shows left of the line numbers. It is a generic lane,
not a change-history implementation: the caller decides what each line means.

## Input

```go
type CodeEditorChangeKind uint8
const (
    ChangeNone CodeEditorChangeKind = iota
    ChangeReverted           // change reverted to the loaded text
    ChangeSaved              // changed line, change written to disk
    ChangeModified           // changed line, unsaved
    ChangeRevertedModified   // reverted to a previously modified state
)

type CodeEditorChangeLine struct {
    Line         int                   // zero-based logical line
    Kind         CodeEditorChangeKind
    DeletedAbove bool                  // deletion point at the line's top
    DeletedBelow bool                  // deletion point at the line's bottom
}
```

- Entries are sorted by `Line` and unique; a wrapped line paints its bar once, on
  its first row.
- `ChangeColors[0..3]` is the bar color for kinds 1..4; `ChangeDeletionColor`
  paints deletion points. A zero-alpha color is invisible.
- `CodeEditorGutter.Change` fixes the lane width (logical pixels). Without a
  custom gutter a lane appears only when `ChangeLines` is non-empty and is sized
  from the cell width (3-8 px), left of every other lane; with one, the caller's
  width wins and an empty lane reserves nothing.

## Contract

- Purely visual: the lane never changes the caret, selection, hit-to-byte map or
  document state, and a paint is a pure function of the snapshot.
- Layout: adding the lane narrows the text area by exactly the lane width; hiding
  line numbers does not move the lane.
- Hidden fold rows are not painted; the fold header keeps its bar.
- Deletion points are drawn at the line's top (a deletion at the line start) and
  bottom (a deletion at the end of the previous visible line, including the empty
  final line).

Geometry coverage: `components/code_editor_change_test.go`
(`TestCodeEditorChangeDecorationsRespectGeometry`) covers the lane width, wrapped
first/continuation rows, hidden folds, vertical/horizontal geometry, the
line-number toggle, a custom gutter, deletion points and paint idempotence.
