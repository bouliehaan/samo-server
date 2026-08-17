package artwork

import (
	"bytes"
	"image/png"
	"math"
	"testing"
)

// Determinism is not a nicety here: the cover store derives an id from the key
// and dedupes by checksum, so a generator that drifted would rewrite every
// tile on every list and change artwork under a client's cache.
func TestTilesAreDeterministic(t *testing.T) {
	for _, key := range []string{"jake", "late-night", ""} {
		if !bytes.Equal(ChannelTile(key), ChannelTile(key)) {
			t.Fatalf("ChannelTile(%q) is not stable across calls", key)
		}
		if !bytes.Equal(GradientTile(key), GradientTile(key)) {
			t.Fatalf("GradientTile(%q) is not stable across calls", key)
		}
	}
}

func TestTilesDifferByKey(t *testing.T) {
	// Two channels sitting next to each other in a rack must not render the
	// same tile, or the mark stops identifying anything.
	if bytes.Equal(ChannelTile("jake"), ChannelTile("late-night")) {
		t.Fatal("two different channels rendered identical tiles")
	}
}

func TestChannelTileIsNotTheExploTile(t *testing.T) {
	// Channels and explo drops render from different generators on purpose.
	// explo's is the older two-hue-40-degrees-apart tile, which reads as one
	// colour fading out; a channel gets a real two-colour sweep. Same key, so
	// a match here would mean channels had been quietly folded back onto it.
	if bytes.Equal(ChannelTile("jake"), GradientTile("jake")) {
		t.Fatal("channel tile fell back to the explo gradient")
	}
}

// Oklab's lightness is the reason the sweep does not band: a linear ramp down
// it has to get evenly darker whatever the hue is doing. If a hue ever came
// back brighter than the step before it, that is a bright ridge across the
// tile — the exact artefact this replaced.
func TestLightnessFallsEvenlyAcrossTheWheel(t *testing.T) {
	for hue := 0.0; hue < 2*math.Pi; hue += math.Pi / 12 {
		previous := math.Inf(1)
		for step := 0; step <= 40; step++ {
			t2 := float64(step) / 40
			r, g, b := oklchToRGB(lerp(0.58, 0.32, t2), lerp(0.110, 0.135, t2), hue)
			// Rec. 709 luminance, as a stand-in for what the eye reads.
			got := (0.2126*r + 0.7152*g + 0.0722*b) / 255
			if got > previous+0.005 {
				t.Fatalf("hue %.2f brightened at step %d (%.3f after %.3f)",
					hue, step, got, previous)
			}
			previous = got
		}
	}
}

func TestTilesDecodeAtTheExpectedSize(t *testing.T) {
	for name, data := range map[string][]byte{
		"channel":  ChannelTile("jake"),
		"gradient": GradientTile("jake"),
	} {
		image, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("%s tile is not decodable PNG: %v", name, err)
		}
		bounds := image.Bounds()
		if bounds.Dx() != TileSize || bounds.Dy() != TileSize {
			t.Fatalf("%s tile is %dx%d, want %dx%d",
				name, bounds.Dx(), bounds.Dy(), TileSize, TileSize)
		}
	}
}
