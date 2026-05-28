package media

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"strings"

	"golang.org/x/image/webp"
)

// RenderTerminalPreview renders a still image into ANSI true-color half blocks.
// It is intentionally small and dependency-light so media still has an inline
// terminal path without native dependencies.
func RenderTerminalPreview(path string, maxCols, maxRows int) string {
	if maxCols <= 0 || maxRows <= 0 {
		return ""
	}
	img, err := decodePreviewImage(path)
	if err != nil {
		return ""
	}
	return RenderTerminalImage(img, maxCols, maxRows)
}

// RenderTerminalImage renders a still image into ANSI true-color half blocks.
func RenderTerminalImage(img image.Image, maxCols, maxRows int) string {
	if maxCols <= 0 || maxRows <= 0 || img == nil {
		return ""
	}
	bounds := img.Bounds()
	if bounds.Empty() {
		return ""
	}
	targetW, targetH := fitPreviewSize(bounds.Dx(), bounds.Dy(), maxCols, maxRows*2)
	if targetW <= 0 || targetH <= 0 {
		return ""
	}
	var b strings.Builder
	for y := 0; y < targetH; y += 2 {
		for x := 0; x < targetW; x++ {
			top := sampleImage(img, bounds, x, y, targetW, targetH)
			bottom := top
			if y+1 < targetH {
				bottom = sampleImage(img, bounds, x, y+1, targetW, targetH)
			}
			fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀", top.R, top.G, top.B, bottom.R, bottom.G, bottom.B)
		}
		b.WriteString("\x1b[0m")
		if y+2 < targetH {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func decodePreviewImage(path string) (image.Image, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if img, err := png.Decode(file); err == nil {
		return img, nil
	}
	if _, err := file.Seek(0, 0); err != nil {
		return nil, err
	}
	if img, err := jpeg.Decode(file); err == nil {
		return img, nil
	}
	if _, err := file.Seek(0, 0); err != nil {
		return nil, err
	}
	if img, err := gif.Decode(file); err == nil {
		return img, nil
	}
	if _, err := file.Seek(0, 0); err != nil {
		return nil, err
	}
	return webp.Decode(file)
}

// FitRasterPreviewCells maps image pixel dimensions to terminal character columns and rows
// for the Go half-block renderer (each terminal row covers two image pixel rows).
func FitRasterPreviewCells(maxCols, maxRows, imgW, imgH int) (cols, rows int) {
	if maxCols <= 0 || maxRows <= 0 || imgW <= 0 || imgH <= 0 {
		return 0, 0
	}
	tw, th := fitPreviewSize(imgW, imgH, maxCols, maxRows*2)
	rows = (th + 1) / 2 // ceil(th/2) terminal rows for half-block output
	cols = tw
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	return cols, rows
}

// PreviewStillImageBounds returns width and height of a still image readable by the preview path.
func PreviewStillImageBounds(path string) (w, h int, err error) {
	img, err := decodePreviewImage(path)
	if err != nil || img == nil {
		return 0, 0, err
	}
	b := img.Bounds()
	if b.Empty() {
		return 0, 0, errors.New("empty image bounds")
	}
	return b.Dx(), b.Dy(), nil
}

func fitPreviewSize(width, height, maxW, maxH int) (int, int) {
	if width <= 0 || height <= 0 {
		return 0, 0
	}
	outW, outH := width, height
	if outW > maxW {
		outH = outH * maxW / outW
		outW = maxW
	}
	if outH > maxH {
		outW = outW * maxH / outH
		outH = maxH
	}
	if outW < 1 {
		outW = 1
	}
	if outH < 1 {
		outH = 1
	}
	return outW, outH
}

func sampleImage(img image.Image, bounds image.Rectangle, x, y, targetW, targetH int) color.RGBA {
	srcX := bounds.Min.X + x*bounds.Dx()/targetW
	srcY := bounds.Min.Y + y*bounds.Dy()/targetH
	r, g, b, a := img.At(srcX, srcY).RGBA()
	if a == 0 {
		return color.RGBA{}
	}
	alpha := float64(a) / 0xffff
	return color.RGBA{
		R: uint8((float64(r) / 0xffff) * alpha * 255),
		G: uint8((float64(g) / 0xffff) * alpha * 255),
		B: uint8((float64(b) / 0xffff) * alpha * 255),
		A: 255,
	}
}
