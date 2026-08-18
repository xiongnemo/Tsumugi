package media

import (
	"image"
	"image/color"

	"rsc.io/qr"
)

// QRQuietZone is the white margin around a QR code, in modules.
//
// The spec asks for 4. Scanners in practice manage with 2, and 2 keeps a token code inside 80
// columns with room to spare, which matters on a bare console.
const QRQuietZone = 2

// QRBitmap renders text as a QR code image, one pixel per module plus a quiet zone.
//
// Built from Code.Black rather than Code.Image, which is unusable here: codeImage.Bounds reports
// (Size+8)*Scale pixels per side while At(x, y) passes raw coordinates straight to Black with no
// scale division and no quiet-zone offset. The modules therefore occupy one pixel each in the
// top-left corner of a canvas eight times too large, and piping that into a terminal renderer
// produces an unscannable smudge. (Code.PNG scales correctly; Code.Image does not.)
//
// Level M rather than H: the higher correction level inflates the version, and therefore the
// module count, for no on-screen benefit — a terminal is not a crumpled receipt.
func QRBitmap(text string, quietZone int) (image.Image, error) {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return nil, err
	}
	if quietZone < 0 {
		quietZone = 0
	}
	side := code.Size + quietZone*2
	img := image.NewGray(image.Rect(0, 0, side, side))
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			shade := color.Gray{Y: 0xFF}
			if code.Black(x-quietZone, y-quietZone) {
				shade = color.Gray{Y: 0x00}
			}
			img.SetGray(x, y, shade)
		}
	}
	return img, nil
}

// QRTerminalANSI renders a QR code as half-block rows ready for a terminal.
//
// The sizing is the whole trick: passing the image's own width as maxCols makes the fit a no-op
// and the sampler an identity map, so every module is exactly one column wide and half a row
// tall. On a cell that is roughly 1:2 that is square, which is the standard scannable half-block
// layout. Clamping maxCols to a pane width later would silently destroy the code, which is why
// the no-downscale property has its own test.
func QRTerminalANSI(img image.Image) string {
	bounds := img.Bounds()
	cols := bounds.Dx()
	rows := (bounds.Dy() + 1) / 2
	return RenderTerminalImage(img, cols, rows)
}
