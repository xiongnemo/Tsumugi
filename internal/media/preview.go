package media

// RasterPreviewOptions configures the pure Go half-block terminal image rasterizer.
type RasterPreviewOptions struct {
	MaxCols int
	MaxRows int
}

// PreviewMaxCols and PreviewMaxRows are the shared terminal budget for still and
// inline animated media previews (half-block renderer: each row covers 2px height).
const (
	PreviewMaxCols = 40
	PreviewMaxRows = 12
)

// RasterPreviewANSIToTview renders a still image with RenderTerminalPreview and converts SGR for tview.
// Result is safe to put in tview TextView with DynamicColors(true).
func RasterPreviewANSIToTview(path string, opts RasterPreviewOptions) string {
	if path == "" || opts.MaxCols <= 0 || opts.MaxRows <= 0 {
		return ""
	}
	raw := RenderTerminalPreview(path, opts.MaxCols, opts.MaxRows)
	return ANSISGRToTview(raw)
}
