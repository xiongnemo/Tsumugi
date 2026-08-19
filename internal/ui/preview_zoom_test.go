package ui

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rivo/tview"

	"github.com/nemo/Tsumugi/internal/media"
)

func tviewTextViewForTest() *tview.TextView {
	v := tview.NewTextView().SetDynamicColors(true).SetWrap(false)
	v.SetBorder(true)
	return v
}

// Zoom 0 has to be exactly what the preview has always shown, or opening a message looks different
// than before for no reason.
func TestPreviewZoomBudgetAtZeroIsFitToPane(t *testing.T) {
	cols, rows := previewZoomBudget(80, 20, 0)
	if cols != 80 || rows != 20 {
		t.Fatalf("budget = %dx%d, want the pane size 80x20", cols, rows)
	}
}

func TestPreviewZoomBudgetGrowsAndShrinks(t *testing.T) {
	base, baseRows := previewZoomBudget(80, 20, 0)

	inCols, inRows := previewZoomBudget(80, 20, 1)
	if inCols <= base || inRows <= baseRows {
		t.Fatalf("one step in = %dx%d, want larger than %dx%d", inCols, inRows, base, baseRows)
	}
	outCols, outRows := previewZoomBudget(80, 20, -1)
	if outCols >= base || outRows >= baseRows {
		t.Fatalf("one step out = %dx%d, want smaller than %dx%d", outCols, outRows, base, baseRows)
	}
}

// Monotonic across the whole range, so a keypress never fails to change anything mid-range.
func TestPreviewZoomBudgetIsMonotonic(t *testing.T) {
	prevCols, prevRows := 0, 0
	for zoom := previewZoomMin; zoom <= previewZoomMax; zoom++ {
		cols, rows := previewZoomBudget(120, 30, zoom)
		if cols < prevCols || rows < prevRows {
			t.Fatalf("zoom %d gave %dx%d after %dx%d; must not go backwards", zoom, cols, rows, prevCols, prevRows)
		}
		prevCols, prevRows = cols, rows
	}
}

// Zooming out must leave a picture, not a couple of blocks.
func TestPreviewZoomBudgetHasFloors(t *testing.T) {
	cols, rows := previewZoomBudget(80, 20, previewZoomMin)
	if cols < previewZoomMinCols || rows < previewZoomMinRows {
		t.Fatalf("fully zoomed out = %dx%d, want at least %dx%d",
			cols, rows, previewZoomMinCols, previewZoomMinRows)
	}
}

// The ceiling bounds allocation. Appearance is already bounded by the renderer, which only ever
// shrinks the source, but an unbounded budget would still allocate.
func TestPreviewZoomBudgetIsBounded(t *testing.T) {
	cols, rows := previewZoomBudget(80, 20, previewZoomMax)
	if cols > 80*4 || rows > 20*4 {
		t.Fatalf("fully zoomed in = %dx%d, want bounded to 4x the pane", cols, rows)
	}
}

func TestPreviewZoomBudgetWithNoGeometry(t *testing.T) {
	if cols, rows := previewZoomBudget(0, 0, 2); cols != 0 || rows != 0 {
		t.Fatalf("got %dx%d, want zeros before the pane has a size", cols, rows)
	}
}

func TestPreviewZoomPercent(t *testing.T) {
	if got := previewZoomPercent(0); got != 100 {
		t.Fatalf("zoom 0 = %d%%, want 100%%", got)
	}
	if previewZoomPercent(1) <= 100 {
		t.Fatal("one step in should read above 100%")
	}
	if previewZoomPercent(-1) >= 100 {
		t.Fatal("one step out should read below 100%")
	}
}

func TestAdjustPreviewZoomClampsAndReportsConsumed(t *testing.T) {
	app := newSuggestionTestApp()
	app.msgActionPreview = tviewTextViewForTest()
	app.msgActionZoomable = true

	for i := 0; i < 50; i++ {
		if !app.adjustPreviewZoom(1) {
			t.Fatal("zoom in should stay consumed while a preview is open")
		}
	}
	if app.msgActionZoom != previewZoomMax {
		t.Fatalf("zoom = %d, want clamped to %d", app.msgActionZoom, previewZoomMax)
	}
	for i := 0; i < 50; i++ {
		app.adjustPreviewZoom(-1)
	}
	if app.msgActionZoom != previewZoomMin {
		t.Fatalf("zoom = %d, want clamped to %d", app.msgActionZoom, previewZoomMin)
	}
}

// With nothing zoomable the key must fall through, so it keeps whatever other meaning it has.
func TestAdjustPreviewZoomIgnoredWithoutARaster(t *testing.T) {
	app := newSuggestionTestApp()
	app.msgActionPreview = tviewTextViewForTest()
	app.msgActionZoomable = false

	if app.adjustPreviewZoom(1) {
		t.Fatal("zoom should not consume the key for a non-raster preview")
	}
	app.msgActionPreview = nil
	app.msgActionZoomable = true
	if app.adjustPreviewZoom(1) {
		t.Fatal("zoom should not consume the key with no preview open")
	}
}

func TestPreviewTitleShowsScaleAndHint(t *testing.T) {
	app := newSuggestionTestApp()
	view := tviewTextViewForTest()
	app.msgActionPreview = view
	app.msgActionZoomable = true
	app.msgActionZoom = 2

	app.applyPreviewTitle()

	title := view.GetTitle()
	if !strings.Contains(title, "%") {
		t.Fatalf("title = %q, want the scale", title)
	}
	if !strings.Contains(title, "-/=") {
		t.Fatalf("title = %q, want the zoom keys advertised", title)
	}
}

// A non-raster preview gets the plain title: advertising zoom keys that do nothing is worse than
// saying nothing.
func TestPreviewTitlePlainWhenNotZoomable(t *testing.T) {
	app := newSuggestionTestApp()
	view := tviewTextViewForTest()
	app.msgActionPreview = view
	app.msgActionZoomable = false

	app.applyPreviewTitle()

	if strings.Contains(view.GetTitle(), "-/=") {
		t.Fatalf("title = %q, want no zoom hint", view.GetTitle())
	}
}

// The renderer only ever shrinks the source, so a budget larger than the image plateaus rather
// than inventing detail. That is what makes the zoom ceiling a memory bound and not a visual one.
func TestZoomBeyondSourceResolutionPlateaus(t *testing.T) {
	path := writeTestPNG(t, 24, 24)

	small := media.RasterPreviewANSIToTview(path, media.RasterPreviewOptions{MaxCols: 24, MaxRows: 12})
	huge := media.RasterPreviewANSIToTview(path, media.RasterPreviewOptions{MaxCols: 400, MaxRows: 200})

	if small == "" || huge == "" {
		t.Fatal("empty render")
	}
	if strings.Count(small, "\n") != strings.Count(huge, "\n") {
		t.Fatalf("row counts differ (%d vs %d); the render should plateau at the source size",
			strings.Count(small, "\n"), strings.Count(huge, "\n"))
	}
}

// Zooming in makes the image wider than its pane, so the view must not wrap it. SetWordWrap(false)
// is not enough — it only disables wrapping at word boundaries.
func TestPreviewViewDoesNotWrap(t *testing.T) {
	app := newSuggestionTestApp()
	view := tviewTextViewForTest()
	app.msgActionPreview = view

	wide := strings.Repeat("X", 400)
	view.SetText(wide)

	// A wrapping view reports more lines than it was given.
	if got := strings.Count(view.GetText(true), "\n"); got != 0 {
		t.Fatalf("stored text gained %d newlines", got)
	}
}

func writeTestPNG(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 8), G: uint8(y * 8), B: 128, A: 255})
		}
	}
	path := filepath.Join(t.TempDir(), "test.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	return path
}

// The old height estimate reserved two rows per action for what tview draws as a single row of
// buttons, and every over-reserved row came out of the preview above it.
func TestMessageActionFormHeightIsMinimalWhenButtonsFitOneLine(t *testing.T) {
	labels := []string{"Reply", "Delete", "Copy"}

	if got := messageActionFormHeight(labels, 120); got != formBorderedBaseHeight {
		t.Fatalf("height = %d, want the single-row base %d — three short buttons fit 120 columns",
			got, formBorderedBaseHeight)
	}
}

// tview silently drops buttons that do not fit unless the form wraps, so the height has to account
// for the wrapping it now does.
func TestMessageActionFormHeightGrowsOnANarrowTerminal(t *testing.T) {
	labels := []string{
		"Reply", "Delete", "Copy", "Open media", "Download media",
		"React", "Forward this message", "Mark for forwarding (v)", "Cancel",
	}

	wide := messageActionFormHeight(labels, 200)
	narrow := messageActionFormHeight(labels, 80)

	if narrow <= wide {
		t.Fatalf("80 columns needed %d rows and 200 needed %d; narrower must need more", narrow, wide)
	}
	// It must still be well under the old two-rows-per-action estimate, or the preview goes back
	// to being squeezed.
	if narrow >= len(labels)*2 {
		t.Fatalf("height = %d for %d buttons, no better than the old estimate", narrow, len(labels))
	}
}

func TestMessageActionFormHeightHandlesNoGeometry(t *testing.T) {
	if got := messageActionFormHeight([]string{"Reply"}, 0); got < formBorderedBaseHeight {
		t.Fatalf("height = %d, want at least %d with no width known", got, formBorderedBaseHeight)
	}
	if got := messageActionFormHeight(nil, 80); got != formBorderedBaseHeight {
		t.Fatalf("height = %d for no buttons, want %d", got, formBorderedBaseHeight)
	}
}

// CJK labels are double-width, so counting runes would under-reserve and clip the bar.
func TestMessageActionFormHeightUsesDisplayWidth(t *testing.T) {
	cjk := []string{"转发这条消息", "选中以便转发（v）", "回复", "删除", "复制"}

	narrow := messageActionFormHeight(cjk, 40)
	if narrow < formBorderedBaseHeight+formWrapRows {
		t.Fatalf("rows = %d; double-width labels must not be counted as single cells", narrow)
	}
}
