package ui

import (
	"testing"

	"github.com/nemo/Tsumugi/internal/telegram"
)

func TestInlineGridLayoutMath(t *testing.T) {
	// One cell plus gutter per column; a width that fits exactly three must not claim four.
	width := inlineCellWidth*3 + inlineCellGutterX*2
	if cols := inlineGridColumns(width); cols != 3 {
		t.Fatalf("columns = %d, want 3", cols)
	}
	if cols := inlineGridColumns(inlineCellWidth - 1); cols != 1 {
		t.Fatalf("columns = %d, want at least 1 for a narrow panel", cols)
	}
	if rows := inlineGridRows(7, width); rows != 3 {
		t.Fatalf("rows = %d, want 3 for 7 results across 3 columns", rows)
	}
	// Panel height must account for every row, or the bottom row is clipped.
	want := 3*inlineCellHeight + 2*inlineCellGutterY + 2
	if got := inlineGridPanelHeight(7, width); got != want {
		t.Fatalf("panel height = %d, want %d", got, want)
	}
}

func TestInlineGridMoveSelectionStaysInRange(t *testing.T) {
	g := newInlineResultGrid(DefaultTheme())
	results := make([]telegram.InlineResultSuggestion, 5)
	for i := range results {
		results[i].ID = string(rune('a' + i))
	}
	g.SetResults(results)
	g.SetRect(0, 0, inlineCellWidth*2+inlineCellGutterX+2, 20)

	g.MoveSelection(1, 0)
	if g.Selected() != 1 {
		t.Fatalf("selected = %d, want 1", g.Selected())
	}
	// Down moves by a whole row (2 columns here), not by one cell.
	g.MoveSelection(0, 1)
	if g.Selected() != 3 {
		t.Fatalf("selected = %d, want 3", g.Selected())
	}
	// Moving past either end is a no-op rather than wrapping into a surprising cell.
	g.MoveSelection(0, -5)
	if g.Selected() != 3 {
		t.Fatalf("selected = %d, want unchanged", g.Selected())
	}
	g.MoveSelection(0, 5)
	if g.Selected() != 3 {
		t.Fatalf("selected = %d, want unchanged", g.Selected())
	}
}

func TestInlineGridDropsPreviewsForGoneResults(t *testing.T) {
	g := newInlineResultGrid(DefaultTheme())
	g.SetResults([]telegram.InlineResultSuggestion{{ID: "a"}, {ID: "b"}})
	g.SetPreview("a", "raster")
	g.SetPreview("b", "raster")

	// A new query must not keep rendered thumbnails for results that are gone.
	g.SetResults([]telegram.InlineResultSuggestion{{ID: "b"}})
	if g.HasPreview("a") {
		t.Fatal("preview for a stale result was retained")
	}
	if !g.HasPreview("b") {
		t.Fatal("preview for a still-present result was dropped")
	}
}
