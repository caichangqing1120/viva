package text

import (
	"slices"
	"unsafe"
)

// History budgets use rune units. The entry reserve covers map capacity and
// allocator overhead; cells are conservatively charged per retained reference,
// including references also present in current text or another history unit.
const (
	lfLabelEntryBytes   = 512
	lfLabelCellBytes    = 16
	lfLabelPointerBytes = int(unsafe.Sizeof((*lfLabelCell)(nil)))
	lfLabelEditBytes    = int(unsafe.Sizeof(lfLabelEdit{}))
	lfLabelRuneBytes    = int(unsafe.Sizeof(rune(0)))
)

func lfLabelFragmentBytes(capacity int) int {
	return capacity * (lfLabelPointerBytes + lfLabelCellBytes)
}

func (entry lfLabelEntry) retainedRunes() int {
	if len(entry.edits) == 0 {
		return 0
	}
	bytes := lfLabelEntryBytes + cap(entry.edits)*lfLabelEditBytes + lfLabelFragmentBytes(entry.fragments)
	return (bytes + lfLabelRuneBytes - 1) / lfLabelRuneBytes
}

func lfLabelHistoryCost(state *lfLabelState, key *rune) int {
	return state.history[key].retainedRunes()
}

func lfLabelExtraCost(state *lfLabelState) int {
	return state.composition.extraRunes
}

func syncLFLabelExtraCost(state *lfLabelState) {
	composition := &state.composition
	cost := (lfLabelFragmentBytes(cap(composition.original)) + lfLabelRuneBytes - 1) / lfLabelRuneBytes
	if !composition.sharedHistory {
		cost += composition.heldRunes
	}
	state.owner.historyRunes += cost - composition.extraRunes
	composition.extraRunes = cost
}

// Rebuild after deletions so a small live history cannot retain an old map's
// oversized backing without paying for it. Rekeying an entry keeps its size.
func cloneLFLabelHistory(source map[*rune]lfLabelEntry) map[*rune]lfLabelEntry {
	if len(source) == 0 {
		return nil
	}
	result := make(map[*rune]lfLabelEntry, len(source))
	for key, entry := range source {
		result[key] = entry
	}
	return result
}

// The native planner groups physical scalars before any mutation. Simulate the
// same edit-slice growth and removed-fragment clone capacity without allocating
// cells or invoking policy. A grouped removal belongs to its first scalar.
func lfLabelPlannedUnitRunes(removedLF, insertedLF int) int {
	if removedLF == 0 && insertedLF == 0 {
		return 0
	}
	if removedLF > maxSingleEditRunes || insertedLF > maxSingleEditRunes {
		return maxSingleEditRunes + 1
	}
	editCount := max(insertedLF, 1)
	minimum := lfLabelEntryBytes + editCount*lfLabelEditBytes + lfLabelFragmentBytes(removedLF+insertedLF)
	if minimum/lfLabelRuneBytes > maxSingleEditRunes {
		return maxSingleEditRunes + 1
	}
	var edits []lfLabelEdit
	for range editCount {
		edits = append(edits, lfLabelEdit{})
	}
	fragments := insertedLF
	if removedLF != 0 {
		removed := make([]*lfLabelCell, removedLF)
		fragments += cap(slices.Clone(removed))
	}
	return (lfLabelEntry{edits: edits, fragments: fragments}).retainedRunes()
}
