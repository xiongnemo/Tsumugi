package media

import (
	"image"
	"image/color"
	"strings"
	"testing"

	"rsc.io/qr"
)

const testTokenURL = "tg://login?token=AQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

func TestQRBitmapGeometry(t *testing.T) {
	code, err := qr.Encode(testTokenURL, qr.M)
	if err != nil {
		t.Fatal(err)
	}
	img, err := QRBitmap(testTokenURL, QRQuietZone)
	if err != nil {
		t.Fatal(err)
	}

	want := code.Size + QRQuietZone*2
	bounds := img.Bounds()
	if bounds.Dx() != want || bounds.Dy() != want {
		t.Fatalf("bounds = %dx%d, want %dx%d (modules plus quiet zone, one pixel each)",
			bounds.Dx(), bounds.Dy(), want, want)
	}
}

// One pixel per module, offset by the quiet zone. Getting this wrong is invisible in a unit test
// that only checks the size, and produces an unscannable code.
func TestQRBitmapMapsModulesOneToOne(t *testing.T) {
	code, err := qr.Encode(testTokenURL, qr.M)
	if err != nil {
		t.Fatal(err)
	}
	img, err := QRBitmap(testTokenURL, QRQuietZone)
	if err != nil {
		t.Fatal(err)
	}
	gray, ok := img.(*image.Gray)
	if !ok {
		t.Fatalf("image type = %T, want *image.Gray", img)
	}

	for y := 0; y < code.Size; y++ {
		for x := 0; x < code.Size; x++ {
			got := gray.GrayAt(x+QRQuietZone, y+QRQuietZone)
			want := color.Gray{Y: 0xFF}
			if code.Black(x, y) {
				want = color.Gray{Y: 0x00}
			}
			if got != want {
				t.Fatalf("module (%d,%d): got %v, want %v", x, y, got, want)
			}
		}
	}
}

func TestQRBitmapQuietZoneIsWhite(t *testing.T) {
	img, err := QRBitmap(testTokenURL, QRQuietZone)
	if err != nil {
		t.Fatal(err)
	}
	gray := img.(*image.Gray)
	side := img.Bounds().Dx()

	for i := 0; i < side; i++ {
		for _, pt := range [][2]int{{i, 0}, {i, side - 1}, {0, i}, {side - 1, i}} {
			if got := gray.GrayAt(pt[0], pt[1]); got.Y != 0xFF {
				t.Fatalf("quiet zone pixel (%d,%d) = %v, want white", pt[0], pt[1], got)
			}
		}
	}
}

// Pins the upstream bug this code exists to avoid, so a future "just use Code.Image()"
// simplification fails loudly instead of shipping an unscannable code.
//
// codeImage.Bounds reports (Size+8)*Scale pixels per side, but At(x, y) passes raw coordinates to
// Black with no scale division and no quiet-zone offset. Every module therefore lands in the
// top-left corner of a canvas Scale times too large, and the rest of the image is uniform white.
func TestCodeImageIsBrokenAndMustNotBeUsed(t *testing.T) {
	code, err := qr.Encode(testTokenURL, qr.M)
	if err != nil {
		t.Fatal(err)
	}
	img := code.Image()
	bounds := img.Bounds()

	if bounds.Dx() == code.Size {
		t.Fatal("upstream Image() now reports the module count; QRBitmap's workaround can be revisited")
	}
	if bounds.Dx() != (code.Size+8)*code.Scale {
		t.Fatalf("bounds = %d, expected the documented (Size+8)*Scale = %d",
			bounds.Dx(), (code.Size+8)*code.Scale)
	}
	// Everything past the module grid is white, which is the visible symptom: the code occupies a
	// small patch in one corner.
	if r, _, _, _ := img.At(code.Size+1, code.Size+1).RGBA(); r != 0xFFFF {
		t.Fatal("upstream Image() now paints past the module grid; re-check the workaround")
	}
}

// The sizing is what makes the code scannable: passing the image's own width as maxCols leaves the
// fit a no-op, so each module is one column wide and half a row tall. Clamping maxCols to a pane
// width would silently destroy it.
func TestQRTerminalANSIDoesNotDownscale(t *testing.T) {
	img, err := QRBitmap(testTokenURL, QRQuietZone)
	if err != nil {
		t.Fatal(err)
	}
	side := img.Bounds().Dx()

	out := QRTerminalANSI(img)
	if out == "" {
		t.Fatal("empty render")
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if want := (side + 1) / 2; len(lines) != want {
		t.Fatalf("rows = %d, want %d (two module rows per text row)", len(lines), want)
	}
}

// A token code has to fit a bare 80x25 console, which is the size item 8 targets.
func TestQRFitsAnEightyByTwentyFiveConsole(t *testing.T) {
	img, err := QRBitmap(testTokenURL, QRQuietZone)
	if err != nil {
		t.Fatal(err)
	}
	side := img.Bounds().Dx()

	if side > 80 {
		t.Fatalf("QR is %d columns wide, want at most 80", side)
	}
	if rows := (side + 1) / 2; rows > 24 {
		t.Fatalf("QR is %d rows tall, want at most 24 to leave a line for the URL", rows)
	}
}

func TestQRBitmapRejectsNothing(t *testing.T) {
	// An empty string still encodes; the point is that it does not panic.
	if _, err := QRBitmap("", 0); err != nil {
		t.Fatalf("empty text: %v", err)
	}
}
