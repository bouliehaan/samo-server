// Package artwork renders the cover tiles Samo generates for itself.
//
// Everything here is deterministic in a key and pure stdlib: no fonts, no
// external assets, no network. The same key always renders the same bytes,
// which is what lets the cover store dedupe by checksum and re-generate a tile
// for free rather than keeping one alive forever.
//
// It lives in its own package because two unrelated things need tiles — explo
// drops with no fetchable art, and channels with no uploaded art — and neither
// should have to depend on the other to get one.
package artwork

import (
	"bytes"
	"hash/fnv"
	"image"
	"image/color"
	"image/png"
	"math"
)

// TileSize is the edge length of every generated tile — inside the 500–600px
// range the real cover sources serve, so client image caches treat a generated
// tile and a real cover the same.
const TileSize = 600

// GradientTile renders a two-tone diagonal gradient whose hues derive from
// [key], with a darker lower band for depth. This is the explo drop tile.
//
// The saturation and lightness bands are deliberately muted: the tile has to
// read as cover art sitting next to real covers, not as an error state.
func GradientTile(key string) []byte {
	seed := seedOf(key)

	hueA := hueOf(seed)
	hueB := hueA + 40
	if hueB >= 360 {
		hueB -= 360
	}
	top := hslToRGB(hueA, 0.38, 0.34)
	bottom := hslToRGB(hueB, 0.42, 0.18)

	return renderTile(math.Pi/4, func(t float64) (float64, float64, float64) {
		return lerp(float64(top.R), float64(bottom.R), t),
			lerp(float64(top.G), float64(bottom.G), t),
			lerp(float64(top.B), float64(bottom.B), t)
	})
}

// ChannelTile renders a channel's gradient: two colours melting into each
// other, swept across the tile at an angle of the channel's own.
//
// Interpolated in Oklab, where a straight line between two colours is what the
// eye reads as an even blend. The two obvious alternatives both fail visibly at
// this size: RGB runs the middle of a wide hue sweep through mud (teal to amber
// passes through brown), and HSL puts a bright ridge across the tile, because
// equal lightness is not equal brightness — yellow at L=0.5 is far brighter
// than blue at L=0.5. Oklab's L IS perceived lightness, so a linear ramp down
// it just gets darker, evenly, whatever the hue is doing.
func ChannelTile(key string) []byte {
	seed := seedOf(key)

	// Far enough apart to be two colours rather than one colour twice.
	span := 62.0 + float64(seed>>8%88)
	if seed>>29&1 == 1 {
		span = -span
	}
	hueA := hueOf(seed) * math.Pi / 180
	hueB := hueA + span*math.Pi/180

	// Light to dark, but not to black: both ends have to keep their colour.
	// Chroma rises slightly into the dark end, which stops deep colours going
	// grey the way they do when only lightness moves.
	const lightA, lightB = 0.58, 0.32
	const chromaA, chromaB = 0.110, 0.135

	// Strictly linear, no easing anywhere.
	//
	// Easing is tempting here: a straight sweep across a square gives each end
	// only a corner triangle while the middle gets the broad band, so the ends
	// look under-represented. But holding the ends means moving faster through
	// the middle, and a faster middle is a VISIBLE EDGE — the tile stops
	// reading as two colours melting into each other and starts reading as two
	// colours meeting at a line. Under-represented corners are the better
	// trade: nothing about them looks like a mistake.
	return renderTile(angleOf(seed), func(t float64) (float64, float64, float64) {
		// Chroma dips through the middle of the sweep.
		//
		// Even with lightness falling monotonically, the mid-tile can still
		// LOOK like a bright band, because a saturated colour reads as brighter
		// than its luminance says — most of all in the blues and cyans a wide
		// hue sweep tends to pass through. Measuring the tile says there is no
		// ridge; looking at it says there is. Easing the chroma off where the
		// two colours hand over settles it, and costs nothing at the ends,
		// which is where the colour is supposed to be doing the work.
		damping := 1 - 0.24*math.Sin(math.Pi*t)
		return oklchToRGB(
			lerp(lightA, lightB, t),
			lerp(chromaA, chromaB, t)*damping,
			lerp(hueA, hueB, t),
		)
	})
}

// angleOf picks the direction the gradient runs in.
//
// One of twelve, rather than anything the seed likes, because a gradient a few
// degrees off horizontal looks like a mistake while one at a clean angle looks
// chosen. Twelve is enough that a rack of channels does not read as a set of
// identical diagonals.
func angleOf(seed uint64) float64 {
	return float64(seed>>17%12) * (math.Pi / 6)
}

// renderTile sweeps `colourAt` across the tile along `angle` and encodes it.
//
// The sweep is normalised to the tile's own corners, so every angle spans the
// full range of the gradient rather than clipping it — a 30° gradient covers as
// much ground as a 45° one.
func renderTile(angle float64, colourAt func(t float64) (r, g, b float64)) []byte {
	cos, sin := math.Cos(angle), math.Sin(angle)
	edge := float64(TileSize - 1)
	// Projections of the four corners; the two that matter are whichever are
	// smallest and largest for this angle.
	lo := math.Min(0, math.Min(cos*edge, math.Min(sin*edge, cos*edge+sin*edge)))
	hi := math.Max(0, math.Max(cos*edge, math.Max(sin*edge, cos*edge+sin*edge)))
	span := hi - lo
	if span == 0 {
		span = 1
	}

	// Precomputed per step of t, not per pixel: the colour is constant along
	// each line perpendicular to the sweep, and the conversion out of Oklab is
	// the expensive part of drawing one of these.
	const steps = 2048
	var ramp [steps][3]float64
	for i := range ramp {
		r, g, b := colourAt(float64(i) / float64(steps-1))
		ramp[i] = [3]float64{r, g, b}
	}

	img := image.NewRGBA(image.Rect(0, 0, TileSize, TileSize))
	for y := 0; y < TileSize; y++ {
		for x := 0; x < TileSize; x++ {
			t := (float64(x)*cos + float64(y)*sin - lo) / span
			// Read BETWEEN ramp entries rather than snapping to the nearest.
			// Snapping quietly re-introduced the thing the ramp was not
			// supposed to affect at all: neighbouring pixels landing on the
			// same index draw flat strips, which is banding again, just at the
			// ramp's resolution instead of the gradient's.
			position := t * float64(steps-1)
			index := int(position)
			if index >= steps-1 {
				index = steps - 2
			}
			frac := position - float64(index)
			low, high := ramp[index], ramp[index+1]
			colour := [3]float64{
				lerp(low[0], high[0], frac),
				lerp(low[1], high[1], frac),
				lerp(low[2], high[2], frac),
			}
			img.SetRGBA(x, y, quantize(colour, x, y))
		}
	}

	return encodePNG(img)
}

// dither is why these tiles show no contour LINES, which is the one thing a
// gradient must not do. A 600px sweep between two close colours crosses only a
// handful of 8-bit values, so rounding lays down wide flat bands with hard
// edges between them — and the eye exaggerates those edges further. Spending
// the rounding error as sub-pixel noise instead makes the bands disappear.
//
// Hash noise rather than an ordered (Bayer) matrix. Bayer is the textbook
// choice and it is wrong here: its 8×8 cell has a strong diagonal structure,
// and across the slow-changing part of a gradient that structure stops
// cancelling out and shows through as a faint diagonal weave — trading contour
// lines for lines of a different kind. Unstructured noise has no pattern to
// show. Still fully deterministic: it is a hash of the coordinates, not a
// random number.
func dither(x, y int) float64 {
	h := uint32(x)*374761393 + uint32(y)*668265263
	h = (h ^ (h >> 13)) * 1274126177
	h ^= h >> 16
	return float64(h%2048)/2048 - 0.5
}

func quantize(colour [3]float64, x, y int) color.RGBA {
	// One 8-bit step of noise: enough to break the bands, not enough to read
	// as grain.
	noise := dither(x, y)
	return color.RGBA{
		R: clamp8(colour[0] + noise),
		G: clamp8(colour[1] + noise),
		B: clamp8(colour[2] + noise),
		A: 255,
	}
}

func clamp8(v float64) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(v + 0.5)
}

func seedOf(key string) uint64 {
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(key))
	return hasher.Sum64()
}

func hueOf(seed uint64) float64 { return float64(seed % 360) }

func encodePNG(img *image.RGBA) []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		// Encoding a valid in-memory RGBA cannot realistically fail; an empty
		// slice makes the caller treat it as "no placeholder available".
		return nil
	}
	return buf.Bytes()
}

func lerp(a, b, t float64) float64 { return a + (b-a)*t }

// ----- colour ----------------------------------------------------------

// oklchToRGB converts Oklab's polar form — lightness, chroma, hue in radians —
// to sRGB channels in 0..255.
//
// Out-of-gamut colours have their chroma walked down rather than their channels
// clipped: clipping a channel changes the hue, which on a gradient shows up as
// a streak of the wrong colour exactly where the sweep leaves the gamut.
func oklchToRGB(lightness, chroma, hue float64) (float64, float64, float64) {
	if fits(lightness, chroma, hue) {
		r, g, b := oklabToLinearRGB(lightness, chroma*math.Cos(hue), chroma*math.Sin(hue))
		return encodeSRGB(r), encodeSRGB(g), encodeSRGB(b)
	}

	// Binary search for the most chroma this hue can hold at this lightness.
	// Stepping down by a fixed amount instead — the obvious way — quantises the
	// result, and neighbouring points on the sweep landing on different steps
	// is a visible band of its own. Twenty halvings put the boundary well
	// inside a single 8-bit value, so the reduction reads as continuous.
	low, high := 0.0, chroma
	for i := 0; i < 20; i++ {
		mid := (low + high) / 2
		if fits(lightness, mid, hue) {
			low = mid
		} else {
			high = mid
		}
	}
	r, g, b := oklabToLinearRGB(lightness, low*math.Cos(hue), low*math.Sin(hue))
	return encodeSRGB(r), encodeSRGB(g), encodeSRGB(b)
}

func fits(lightness, chroma, hue float64) bool {
	r, g, b := oklabToLinearRGB(lightness, chroma*math.Cos(hue), chroma*math.Sin(hue))
	return inGamut(r) && inGamut(g) && inGamut(b)
}

func inGamut(v float64) bool { return v >= -0.0005 && v <= 1.0005 }

func oklabToLinearRGB(lightness, a, b float64) (float64, float64, float64) {
	l := lightness + 0.3963377774*a + 0.2158037573*b
	m := lightness - 0.1055613458*a - 0.0638541728*b
	s := lightness - 0.0894841775*a - 1.2914855480*b
	l, m, s = l*l*l, m*m*m, s*s*s
	return 4.0767416621*l - 3.3077115913*m + 0.2309699292*s,
		-1.2684380046*l + 2.6097574011*m - 0.3413193965*s,
		-0.0041960863*l - 0.7034186147*m + 1.7076147010*s
}

// encodeSRGB applies the sRGB transfer function and scales to 0..255.
func encodeSRGB(v float64) float64 {
	if v <= 0 {
		return 0
	}
	if v >= 1 {
		return 255
	}
	if v <= 0.0031308 {
		return v * 12.92 * 255
	}
	return (1.055*math.Pow(v, 1/2.4) - 0.055) * 255
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
