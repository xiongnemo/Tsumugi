package ui

import (
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/nemo/Tsumugi/internal/render"
	"github.com/nemo/Tsumugi/internal/telegram"
)

// Cell geometry. A cell is the thumbnail plus one caption row, with a one-column gutter
// between cells so adjacent thumbnails do not touch.
const (
	inlineCellWidth   = telegram.InlineThumbCols
	inlineCellHeight  = telegram.InlineThumbRows + 1
	inlineCellGutterX = 1
	inlineCellGutterY = 1
)

// inlineResultGrid tiles inline bot results as thumbnails, the way graphical clients present
// GIF results. A vertical list cannot show what you are choosing between when the bot returns
// no titles, which is the normal case for GIF bots.
type inlineResultGrid struct {
	*tview.Box

	results  []telegram.InlineResultSuggestion
	previews map[string]string
	selected int

	onAccept func(index int)
}

func newInlineResultGrid(theme Theme) *inlineResultGrid {
	g := &inlineResultGrid{
		Box:      tview.NewBox(),
		previews: map[string]string{},
	}
	g.SetBorder(true)
	g.SetBorderColor(theme.Border)
	return g
}

func (g *inlineResultGrid) SetResults(results []telegram.InlineResultSuggestion) {
	g.results = results
	if g.selected >= len(results) {
		g.selected = len(results) - 1
	}
	if g.selected < 0 {
		g.selected = 0
	}
	// Drop previews for results that are no longer on screen so a long typing session does
	// not accumulate rendered thumbnails for every query the user passed through.
	keep := make(map[string]string, len(results))
	for _, r := range results {
		if preview, ok := g.previews[r.ID]; ok {
			keep[r.ID] = preview
		}
	}
	g.previews = keep
}

func (g *inlineResultGrid) SetPreview(resultID, preview string) {
	if resultID == "" || preview == "" {
		return
	}
	g.previews[resultID] = preview
}

func (g *inlineResultGrid) HasPreview(resultID string) bool {
	_, ok := g.previews[resultID]
	return ok
}

func (g *inlineResultGrid) Selected() int { return g.selected }

func (g *inlineResultGrid) SetSelected(index int) {
	if index < 0 || index >= len(g.results) {
		return
	}
	g.selected = index
}

// Columns reports how many cells fit across the given inner width.
func inlineGridColumns(innerWidth int) int {
	if innerWidth <= 0 {
		return 1
	}
	cols := (innerWidth + inlineCellGutterX) / (inlineCellWidth + inlineCellGutterX)
	if cols < 1 {
		return 1
	}
	return cols
}

// GridRows reports how many cell rows the results need at the given inner width.
func inlineGridRows(count, innerWidth int) int {
	if count <= 0 {
		return 1
	}
	cols := inlineGridColumns(innerWidth)
	rows := (count + cols - 1) / cols
	if rows < 1 {
		return 1
	}
	return rows
}

// PanelHeight is the total height the panel needs, including its border.
func inlineGridPanelHeight(count, innerWidth int) int {
	rows := inlineGridRows(count, innerWidth)
	return rows*inlineCellHeight + (rows-1)*inlineCellGutterY + 2
}

// MoveSelection moves the highlight by whole cells. Vertical movement steps by a full row so
// arrow keys behave like a grid rather than a list.
func (g *inlineResultGrid) MoveSelection(dx, dy int) {
	if len(g.results) == 0 {
		return
	}
	_, _, innerWidth, _ := g.GetInnerRect()
	cols := inlineGridColumns(innerWidth)
	next := g.selected + dx + dy*cols
	if next < 0 || next >= len(g.results) {
		return
	}
	g.selected = next
}

func (g *inlineResultGrid) Draw(screen tcell.Screen) {
	g.Box.DrawForSubclass(screen, g)
	x, y, width, height := g.GetInnerRect()
	if width <= 0 || height <= 0 || len(g.results) == 0 {
		return
	}
	cols := inlineGridColumns(width)
	for i, result := range g.results {
		col := i % cols
		row := i / cols
		cellX := x + col*(inlineCellWidth+inlineCellGutterX)
		cellY := y + row*(inlineCellHeight+inlineCellGutterY)
		if cellY+inlineCellHeight > y+height {
			break
		}
		g.drawCell(screen, result, cellX, cellY, i == g.selected)
	}
}

func (g *inlineResultGrid) drawCell(screen tcell.Screen, result telegram.InlineResultSuggestion, x, y int, selected bool) {
	preview := g.previews[result.ID]
	if preview == "" {
		// Placeholder keeps the cell the same size while the thumbnail downloads, so the
		// grid does not reflow as previews land one by one.
		g.drawPlaceholder(screen, x, y, selected)
	} else {
		lines := strings.Split(tview.TranslateANSI(preview), "\n")
		for row := 0; row < telegram.InlineThumbRows; row++ {
			line := ""
			if row < len(lines) {
				line = lines[row]
			}
			tview.Print(screen, line, x, y+row, inlineCellWidth, tview.AlignLeft, tcell.ColorWhite)
		}
	}
	caption := render.InlineResultDetail(result)
	if caption == "" {
		caption = result.Type
	}
	color := tcell.ColorGray
	if selected {
		color = tcell.ColorYellow
		caption = "[" + caption + "]"
	}
	tview.Print(screen, render.Truncate(caption, inlineCellWidth), x, y+telegram.InlineThumbRows, inlineCellWidth, tview.AlignLeft, color)
}

func (g *inlineResultGrid) drawPlaceholder(screen tcell.Screen, x, y int, selected bool) {
	color := tcell.ColorDarkGray
	if selected {
		color = tcell.ColorGray
	}
	fill := strings.Repeat("░", inlineCellWidth)
	for row := 0; row < telegram.InlineThumbRows; row++ {
		tview.Print(screen, fill, x, y+row, inlineCellWidth, tview.AlignLeft, color)
	}
}

// MouseHandler lets a click pick a cell, so the grid is usable without the keyboard.
func (g *inlineResultGrid) MouseHandler() func(tview.MouseAction, *tcell.EventMouse, func(tview.Primitive)) (bool, tview.Primitive) {
	return g.WrapMouseHandler(func(action tview.MouseAction, event *tcell.EventMouse, _ func(tview.Primitive)) (bool, tview.Primitive) {
		if action != tview.MouseLeftClick && action != tview.MouseLeftDoubleClick {
			return false, nil
		}
		mx, my := event.Position()
		if !g.InRect(mx, my) {
			return false, nil
		}
		x, y, width, _ := g.GetInnerRect()
		cols := inlineGridColumns(width)
		col := (mx - x) / (inlineCellWidth + inlineCellGutterX)
		row := (my - y) / (inlineCellHeight + inlineCellGutterY)
		if col < 0 || col >= cols || row < 0 {
			return true, nil
		}
		index := row*cols + col
		if index < 0 || index >= len(g.results) {
			return true, nil
		}
		g.selected = index
		if g.onAccept != nil {
			g.onAccept(index)
		}
		return true, nil
	})
}
