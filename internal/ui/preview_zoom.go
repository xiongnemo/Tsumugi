package ui

import (
	"fmt"
	"math"

	"github.com/rivo/tview"

	"github.com/nemo/Tsumugi/internal/i18n"
	"github.com/nemo/Tsumugi/internal/telegram"
)

const (
	// Zoom is expressed in steps rather than absolute sizes, because the useful range depends on
	// the pane, which depends on the terminal.
	previewZoomMin = -4
	previewZoomMax = 8
	// 1.3 per step: small enough that a step is a refinement rather than a jump, large enough
	// that four steps out and eight steps in cover the whole useful range.
	previewZoomStep = 1.3
	// Floors, so zooming out stays a picture instead of collapsing to a couple of blocks.
	previewZoomMinCols = 8
	previewZoomMinRows = 4
)

// previewZoomBudget converts a zoom level into a render budget in terminal cells.
//
// Zoom 0 is fit-to-pane, which is what the preview has always done. The ceiling exists to bound
// allocation, not appearance: RenderTerminalImage runs the budget through fitPreviewSize, which
// only ever shrinks, so asking for more cells than the source has pixels simply plateaus at a 1:1
// mapping rather than inventing detail.
func previewZoomBudget(paneCols, paneRows, zoom int) (int, int) {
	if paneCols <= 0 || paneRows <= 0 {
		return 0, 0
	}
	factor := math.Pow(previewZoomStep, float64(zoom))
	cols := int(float64(paneCols) * factor)
	rows := int(float64(paneRows) * factor)
	if cols < previewZoomMinCols {
		cols = previewZoomMinCols
	}
	if rows < previewZoomMinRows {
		rows = previewZoomMinRows
	}
	if max := paneCols * 4; cols > max {
		cols = max
	}
	if max := paneRows * 4; rows > max {
		rows = max
	}
	return cols, rows
}

// previewZoomPercent renders a zoom level for the pane title.
func previewZoomPercent(zoom int) int {
	return int(math.Round(math.Pow(previewZoomStep, float64(zoom)) * 100))
}

// adjustPreviewZoom changes the preview scale. Reports whether the key was consumed, so the
// overlay capture can pass it on when there is no zoomable preview.
func (a *App) adjustPreviewZoom(delta int) bool {
	if a.msgActionPreview == nil || !a.msgActionZoomable {
		return false
	}
	next := a.msgActionZoom + delta
	if next < previewZoomMin {
		next = previewZoomMin
	}
	if next > previewZoomMax {
		next = previewZoomMax
	}
	// Consumed either way: at the limit the key still belongs to the preview, and letting it
	// fall through would be worse than doing nothing.
	if next == a.msgActionZoom {
		return true
	}
	a.msgActionZoom = next
	a.applyPreviewTitle()
	a.renderPreviewAsync(a.msgActionMsg, a.msgActionPreview, a.msgActionSeq)
	return true
}

// applyPreviewTitle shows the current scale and how to change it.
func (a *App) applyPreviewTitle() {
	if a.msgActionPreview == nil {
		return
	}
	title := " " + i18n.T(i18n.KeyUIPreview) + " "
	if a.msgActionZoomable {
		title = fmt.Sprintf(" %s %d%% · %s ",
			i18n.T(i18n.KeyUIPreview), previewZoomPercent(a.msgActionZoom), i18n.T(i18n.KeyUIPreviewZoomHint))
	}
	a.msgActionPreview.SetTitle(title)
}

// renderPreviewAsync rasterises the preview off the UI goroutine.
//
// Decoding and scaling an image is far too slow to do inside an event handler, and the pane's size
// is only knowable from the UI goroutine — hence the QueueUpdate hop to read the rect and the
// zoom, which also keeps both off a racy read from here.
func (a *App) renderPreviewAsync(msg telegram.Message, view *tview.TextView, seq int) {
	if view == nil {
		return
	}
	go func() {
		// One draw first so the Flex has assigned the pane a size to measure.
		a.app.QueueUpdateDraw(func() {})
		var paneCols, paneRows, zoom int
		a.app.QueueUpdate(func() {
			_, _, paneCols, paneRows = view.GetInnerRect()
			zoom = a.msgActionZoom
		})
		if paneCols < previewZoomMinCols || paneRows < previewZoomMinRows {
			return
		}
		cols, rows := previewZoomBudget(paneCols, paneRows, zoom)
		text := a.messageActionMediaPreview(msg, cols, rows)
		a.app.QueueUpdateDraw(func() {
			// The overlay may have been closed or reopened for another message while this ran.
			if a.msgActionSeq != seq || a.msgActionPreview != view {
				return
			}
			if text == "" {
				view.SetText(i18n.T(i18n.KeyUINoPreview))
				return
			}
			view.SetText(text)
			// A zoomed-in image is wider and taller than the pane, so land at the top-left
			// rather than wherever the previous scale happened to leave the offset.
			view.ScrollTo(0, 0)
			a.applyPreviewTitle()
		})
	}()
}
