//go:build ignore

// Build the installer background image from a single square source PNG.
// Used by the package:macos:pkg:prepare task to create the logo shown
// in the installer window (referenced by distribution.xml for both the
// light and dark appearance).
//
// The source logo is resized to 256x256, drawn onto a 288x288
// transparent canvas anchored at the top-right (leaving a 32px margin
// on the left and bottom), and tagged with 144 DPI. The DPI makes the
// installer render the image at half size (144pt), and because the
// installer anchors the background at the bottom-left of the window,
// the transparent padding keeps the logo away from the window edges.
//
// This is plain Go instead of sips: sips resets the DPI when resizing,
// and its -p padding is always centered and filled with an opaque
// color, so it cannot produce the transparent asymmetric margin.
//
// Usage: go run make-background.go <source.png> <output.png>
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
)

const (
	// Size the source logo is scaled down to, in pixels.
	logoSize = 256
	// Transparent margin added on the left and bottom, in pixels.
	padding = 32
	// PNG pHYs value for 144 DPI: 144 dots/inch / 0.0254 m/inch.
	pixelsPerMetre = 5669
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "Usage: go run make-background.go <source.png> <output.png>")
		os.Exit(2)
	}

	src, err := os.ReadFile(os.Args[1])
	check(err)
	logoImg, err := png.Decode(bytes.NewReader(src))
	check(err)

	logo := resize(logoImg, logoSize, logoSize)

	// Draw the logo at the top-right of a transparent canvas that is
	// `padding` pixels wider and taller.
	canvas := image.NewRGBA(image.Rect(0, 0, logoSize+padding, logoSize+padding))
	draw.Draw(canvas, image.Rect(padding, 0, padding+logoSize, logoSize), logo, image.Point{}, draw.Src)

	var buf bytes.Buffer
	check(png.Encode(&buf, canvas))

	check(os.WriteFile(os.Args[2], withDPI(buf.Bytes()), 0o644))
}

// resize downscales src to w x h by averaging the covered source
// pixels (box filter), which is well suited for shrinking a logo.
// Color channels are weighted by alpha to avoid dark fringes around
// transparent edges.
func resize(src image.Image, w, h int) *image.RGBA {
	sb := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		sy0, sy1 := y*sb.Dy()/h, (y+1)*sb.Dy()/h
		for x := 0; x < w; x++ {
			sx0, sx1 := x*sb.Dx()/w, (x+1)*sb.Dx()/w
			var r, g, b, a, n uint64
			for sy := sy0; sy < max(sy1, sy0+1); sy++ {
				for sx := sx0; sx < max(sx1, sx0+1); sx++ {
					pr, pg, pb, pa := src.At(sb.Min.X+sx, sb.Min.Y+sy).RGBA()
					// RGBA() returns alpha-premultiplied values, which is
					// exactly the weighting we want for the color sums.
					r += uint64(pr)
					g += uint64(pg)
					b += uint64(pb)
					a += uint64(pa)
					n++
				}
			}
			if a == 0 {
				continue
			}
			// Convert the summed 16-bit premultiplied values back to an
			// 8-bit straight-alpha color.
			dst.SetRGBA(x, y, color.RGBA{
				R: uint8(min(r*255/a, 255)),
				G: uint8(min(g*255/a, 255)),
				B: uint8(min(b*255/a, 255)),
				A: uint8(a / n >> 8),
			})
		}
	}
	return dst
}

// withDPI inserts a pHYs chunk after the IHDR chunk (the encoding/png
// package cannot write DPI metadata itself).
func withDPI(data []byte) []byte {
	chunk := make([]byte, 21)
	binary.BigEndian.PutUint32(chunk[0:], 9) // data length
	copy(chunk[4:], "pHYs")
	binary.BigEndian.PutUint32(chunk[8:], pixelsPerMetre)  // x axis
	binary.BigEndian.PutUint32(chunk[12:], pixelsPerMetre) // y axis
	chunk[16] = 1                                          // unit: metre
	binary.BigEndian.PutUint32(chunk[17:], crc32.ChecksumIEEE(chunk[4:17]))

	// 8-byte signature + 25-byte IHDR chunk = offset 33.
	const ihdrEnd = 33
	out := make([]byte, 0, len(data)+len(chunk))
	out = append(out, data[:ihdrEnd]...)
	out = append(out, chunk...)
	return append(out, data[ihdrEnd:]...)
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "make-background:", err)
		os.Exit(1)
	}
}
