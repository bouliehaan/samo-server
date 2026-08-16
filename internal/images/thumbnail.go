// Package images renders and caches downscaled variants of catalog artwork.
//
// Cover art is stored at whatever resolution it arrived in — commonly 1400px,
// often 3000px. Clients render most of it into 48-200px slots, so serving the
// original means shipping (and decoding) two orders of magnitude more pixels
// than end up on screen: a single measured cover was 1.48 MB for a 48px
// sidebar row. Downscaling once, on the server, and caching the result turns
// that into a few KB per request forever after.
package images

import (
	"errors"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif" // registered so DecodeConfig recognises GIF sources
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"crypto/sha256"
	"encoding/hex"

	"golang.org/x/sync/singleflight"
)

var (
	// ErrUnsupported means the source is in a format this package cannot
	// decode (WebP and AVIF need decoders outside the standard library).
	ErrUnsupported = errors.New("image format not supported for resizing")
	// ErrNotSmaller means a variant would be no smaller than the source, so
	// there is nothing to gain by generating one. Artwork is never upscaled.
	ErrNotSmaller = errors.New("source is already at or below the target size")
)

// Widths is the ladder a requested width snaps up to.
//
// Requests are quantised rather than honoured exactly for two reasons: an
// unbounded width parameter lets any caller fill the disk with near-identical
// variants, and neighbouring sizes share a cache entry instead of each paying
// its own decode. The rungs cover the real slots — sidebar rows and player
// thumbs at the bottom, grid tiles in the middle, detail-page headers on top —
// and anything above the last rung falls through to the original file.
var Widths = []int{64, 128, 256, 384, 512, 768, 1024}

// maxSourcePixels caps what will be decoded into memory. A source is held as
// RGBA while it is resized, so this bounds one request at roughly 256 MB and
// declines to expand anything more absurd than that.
const maxSourcePixels = 64 << 20

// jpegQuality is high enough that a downscaled cover shows no visible
// artefacts at the sizes above, and well below the point where file size stops
// buying anything.
const jpegQuality = 85

type Service struct {
	dir   string
	group singleflight.Group
}

func New(dir string) (*Service, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, errors.New("thumbnail directory is required")
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absolute, 0o755); err != nil {
		return nil, fmt.Errorf("create thumbnail directory: %w", err)
	}
	return &Service{dir: absolute}, nil
}

// Dir is the directory variants are cached in.
func (s *Service) Dir() string {
	if s == nil {
		return ""
	}
	return s.dir
}

// NormalizeWidth snaps a requested width up to the next rung of the ladder.
// It reports false when the request is not a resize instruction at all — an
// absent, unparseable, non-positive, or larger-than-the-top-rung value — in
// which case the caller should serve the original bytes.
func NormalizeWidth(raw string) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	requested, err := strconv.Atoi(raw)
	if err != nil || requested <= 0 {
		return 0, false
	}
	for _, width := range Widths {
		if requested <= width {
			return width, true
		}
	}
	return 0, false
}

// Variant returns the path to a cached downscale of sourcePath at the given
// width, rendering it first if it does not exist yet.
//
// modTimeUnix and size participate in the cache key so replaced artwork at an
// unchanged path produces a different key rather than serving stale pixels.
func (s *Service) Variant(sourcePath string, modTimeUnix, size int64, width int) (string, error) {
	if s == nil {
		return "", errors.New("thumbnail service is not configured")
	}
	if width <= 0 {
		return "", ErrNotSmaller
	}

	key := variantKey(sourcePath, modTimeUnix, size, width)

	// singleflight collapses concurrent misses for the same variant. A page
	// of artwork routinely shows the same cover in two places at once, and
	// decoding a 3000px JPEG twice to write identical bytes is pure waste.
	path, err, _ := s.group.Do(key, func() (any, error) {
		if existing, ok := s.lookup(key); ok {
			return existing, nil
		}
		return s.render(sourcePath, key, width)
	})
	if err != nil {
		return "", err
	}
	return path.(string), nil
}

func variantKey(sourcePath string, modTimeUnix, size int64, width int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d|%d", sourcePath, modTimeUnix, size, width)))
	return hex.EncodeToString(sum[:12])
}

// lookup finds an already-rendered variant. The extension is not part of the
// key, so both candidates are probed.
func (s *Service) lookup(key string) (string, bool) {
	for _, extension := range []string{".jpg", ".png"} {
		candidate := filepath.Join(s.dir, key+extension)
		if info, err := os.Stat(candidate); err == nil && info.Size() > 0 {
			return candidate, true
		}
	}
	return "", false
}

func (s *Service) render(sourcePath, key string, width int) (string, error) {
	file, err := os.Open(sourcePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	config, format, err := image.DecodeConfig(file)
	if err != nil {
		// An unregistered format (WebP, AVIF) lands here, as does a
		// truncated file. Either way the original is the only thing that
		// can be served.
		return "", ErrUnsupported
	}
	if config.Width <= 0 || config.Height <= 0 {
		return "", ErrUnsupported
	}
	if int64(config.Width)*int64(config.Height) > maxSourcePixels {
		return "", ErrUnsupported
	}
	// The longest edge defines the size, so non-square art keeps its aspect
	// ratio and still fits the slot the client asked for.
	if config.Width <= width && config.Height <= width {
		return "", ErrNotSmaller
	}

	if _, err := file.Seek(0, 0); err != nil {
		return "", err
	}
	source, _, err := image.Decode(file)
	if err != nil {
		return "", ErrUnsupported
	}

	targetW, targetH := fitWithin(config.Width, config.Height, width)
	resized := downscale(source, targetW, targetH)

	extension := ".jpg"
	if format == "png" {
		// PNG sources round-trip as PNG: album art rarely has alpha, but
		// flattening the ones that do would put a background colour behind
		// artwork that was drawn to sit on the app's own.
		extension = ".png"
	}

	destination := filepath.Join(s.dir, key+extension)
	temp, err := os.CreateTemp(s.dir, key+".*.tmp")
	if err != nil {
		return "", err
	}
	tempName := temp.Name()

	if extension == ".png" {
		err = png.Encode(temp, resized)
	} else {
		err = jpeg.Encode(temp, resized, &jpeg.Options{Quality: jpegQuality})
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(tempName)
		return "", err
	}

	// Rename last so a reader never observes a partially written variant.
	if err := os.Rename(tempName, destination); err != nil {
		_ = os.Remove(tempName)
		return "", err
	}
	return destination, nil
}

// fitWithin scales (w, h) so the longest edge is exactly longest, preserving
// aspect ratio and never returning a zero dimension.
func fitWithin(w, h, longest int) (int, int) {
	if w >= h {
		scaled := int(float64(h) * float64(longest) / float64(w))
		return longest, max(scaled, 1)
	}
	scaled := int(float64(w) * float64(longest) / float64(h))
	return max(scaled, 1), longest
}

// downscale area-averages src into a dstW x dstH image.
//
// Averaging every source pixel that lands in a destination pixel is the right
// filter for the ratios here — 3000px down to 128px is a 23x reduction, where
// point sampling or bilinear would read a handful of scattered pixels per
// output and alias badly on the fine detail covers are full of (text, grain,
// thin borders). It is also single-pass over the source, so it costs the same
// as the cheaper filters it beats.
//
// The source is drawn into RGBA first so the inner loop indexes a flat byte
// slice: image/draw has assembly-assisted fast paths for the YCbCr that every
// JPEG decodes into, which is far quicker than several million At() calls
// through the image.Image interface.
func downscale(src image.Image, dstW, dstH int) *image.RGBA {
	bounds := src.Bounds()
	flat, ok := src.(*image.RGBA)
	if !ok || flat.Bounds() != bounds {
		flat = image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
		draw.Draw(flat, flat.Bounds(), src, bounds.Min, draw.Src)
	}

	srcW := flat.Bounds().Dx()
	srcH := flat.Bounds().Dy()
	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))

	for dy := range dstH {
		// Integer edges keep the bands exactly adjacent: every source row
		// belongs to one destination row, with none dropped or counted twice.
		sy0 := dy * srcH / dstH
		sy1 := (dy + 1) * srcH / dstH
		if sy1 <= sy0 {
			sy1 = sy0 + 1
		}
		for dx := range dstW {
			sx0 := dx * srcW / dstW
			sx1 := (dx + 1) * srcW / dstW
			if sx1 <= sx0 {
				sx1 = sx0 + 1
			}

			var r, g, b, a uint64
			for sy := sy0; sy < sy1; sy++ {
				row := flat.PixOffset(sx0, sy)
				for sx := sx0; sx < sx1; sx++ {
					r += uint64(flat.Pix[row])
					g += uint64(flat.Pix[row+1])
					b += uint64(flat.Pix[row+2])
					a += uint64(flat.Pix[row+3])
					row += 4
				}
			}

			count := uint64((sy1 - sy0) * (sx1 - sx0))
			out := dst.PixOffset(dx, dy)
			dst.Pix[out] = uint8(r / count)
			dst.Pix[out+1] = uint8(g / count)
			dst.Pix[out+2] = uint8(b / count)
			dst.Pix[out+3] = uint8(a / count)
		}
	}
	return dst
}
