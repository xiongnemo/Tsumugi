package media

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderTerminalPreviewUsesHalfBlockANSI(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	img.Set(1, 0, color.RGBA{G: 255, A: 255})
	img.Set(0, 1, color.RGBA{B: 255, A: 255})
	img.Set(1, 1, color.RGBA{R: 255, G: 255, A: 255})

	path := filepath.Join(t.TempDir(), "preview.png")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, img); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	got := RenderTerminalPreview(path, 4, 2)
	if !strings.Contains(got, "\x1b[38;2;") || !strings.Contains(got, "▀") {
		t.Fatalf("preview does not look like half-block ANSI: %q", got)
	}
}

func TestFitRasterPreviewCells(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		maxCols    int
		maxRows    int
		imgW, imgH int
		wantCols   int
		wantRows   int
	}{
		{"square_in_box", 40, 10, 100, 100, 20, 10},
		{"wide_image", 40, 10, 200, 50, 40, 5},
		{"tall_image", 40, 10, 50, 200, 5, 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cols, rows := FitRasterPreviewCells(tt.maxCols, tt.maxRows, tt.imgW, tt.imgH)
			if cols != tt.wantCols || rows != tt.wantRows {
				t.Fatalf("got %dx%d want %dx%d", cols, rows, tt.wantCols, tt.wantRows)
			}
		})
	}
}
