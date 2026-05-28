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

func TestRasterPreviewANSIToTviewPNG(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	path := filepath.Join(t.TempDir(), "x.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	got := RasterPreviewANSIToTview(path, RasterPreviewOptions{MaxCols: 8, MaxRows: 4})
	if got == "" {
		t.Fatal("expected non-empty preview")
	}
	if strings.Contains(got, "\x1b[") {
		t.Fatalf("expected tview tags, raw ESC in output: %q", got)
	}
}
