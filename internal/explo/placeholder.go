package explo

import (
	"bytes"
	"hash/fnv"
	"image"
	"image/color"
	"image/png"
)

// placeholderSize is the edge length of generated placeholder tiles — matches
// the 500-600px range the real cover sources serve, so client image caches
// treat both the same.
const placeholderSize = 600

// placeholderPNG renders a deterministic cover tile for an album that has no
// fetchable art yet: a two-tone diagonal gradient whose hues derive from the
// album ID, with a darker lower band for depth. Pure stdlib (image/png), no
// fonts, no external assets. The same album always renders the same tile, so
// re-generation is idempotent and the cover store's checksum dedupe holds.
// It exists to honor the "no explo album is ever a blank tile" guarantee -
// the cover backfill keeps retrying real sources and replaces this the
// moment one of them has art.
func placeholderPNG(albumID string) []byte {
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(albumID))
	seed := hasher.Sum64()

	// Two related hues, 40 degrees apart, at slightly different depths. The
	// saturation/lightness bands keep every tile muted enough to read as
	// "cover art" next to real covers rather than as an error state.
	hueA := float64(seed % 360)
	hueB := hueA + 40
	if hueB >= 360 {
		hueB -= 360
	}
	top := hslToRGB(hueA, 0.38, 0.34)
	bottom := hslToRGB(hueB, 0.42, 0.18)

	img := image.NewRGBA(image.Rect(0, 0, placeholderSize, placeholderSize))
	for y := 0; y < placeholderSize; y++ {
		for x := 0; x < placeholderSize; x++ {
			// Diagonal blend: 0 at top-left, 1 at bottom-right.
			t := (float64(x) + float64(y)) / (2 * (placeholderSize - 1))
			img.SetRGBA(x, y, lerpRGB(top, bottom, t))
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		// Encoding a valid in-memory RGBA cannot realistically fail; an empty
		// slice makes the caller treat it as "no placeholder available".
		return nil
	}
	return buf.Bytes()
}

func lerpRGB(a, b color.RGBA, t float64) color.RGBA {
	return color.RGBA{
		R: uint8(float64(a.R) + (float64(b.R)-float64(a.R))*t),
		G: uint8(float64(a.G) + (float64(b.G)-float64(a.G))*t),
		B: uint8(float64(a.B) + (float64(b.B)-float64(a.B))*t),
		A: 255,
	}
}

// hslToRGB converts hue [0,360), saturation and lightness [0,1] to RGBA.
func hslToRGB(h, s, l float64) color.RGBA {
	c := (1 - abs(2*l-1)) * s
	hp := h / 60
	x := c * (1 - abs(mod2(hp)-1))
	var r, g, b float64
	switch {
	case hp < 1:
		r, g, b = c, x, 0
	case hp < 2:
		r, g, b = x, c, 0
	case hp < 3:
		r, g, b = 0, c, x
	case hp < 4:
		r, g, b = 0, x, c
	case hp < 5:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	m := l - c/2
	return color.RGBA{
		R: uint8((r + m) * 255),
		G: uint8((g + m) * 255),
		B: uint8((b + m) * 255),
		A: 255,
	}
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func mod2(v float64) float64 {
	for v >= 2 {
		v -= 2
	}
	return v
}
