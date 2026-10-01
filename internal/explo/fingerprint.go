package explo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/bits"
	"os/exec"
	"strings"
)

// Fingerprint is a chromaprint acoustic fingerprint for one audio file,
// produced by shelling out to fpcalc. It identifies what a track SOUNDS
// like, so it still works on files with no (or wrong) tags - unlike
// filename parsing, a fingerprint can't be fooled by a misleading name.
type Fingerprint struct {
	DurationSeconds int
	Value           string
}

// fpcalcOutput mirrors `fpcalc -json`'s exact shape, verified against a real
// build: {"duration": 5.00, "fingerprint": "AQAA..."}.
type fpcalcOutput struct {
	Duration    float64 `json:"duration"`
	Fingerprint string  `json:"fingerprint"`
}

func fingerprintFile(ctx context.Context, fpcalcPath, filePath string) (Fingerprint, error) {
	fpcalcPath = strings.TrimSpace(fpcalcPath)
	if fpcalcPath == "" {
		return Fingerprint{}, fmt.Errorf("fpcalc path is not configured")
	}
	// -ignore-errors: fpcalc 1.5.1 exits 3 when it cannot decode the last
	// frame of a file, after printing a complete fingerprint of the rest.
	// Without the flag samo threw that fingerprint away and called the file
	// unidentifiable — a YouTube rip of Quangou's "puer aeternus" sat in
	// identify retries that way. A file with nothing decodable still fails:
	// fpcalc then prints no fingerprint at all.
	cmd := exec.CommandContext(ctx, fpcalcPath, "-ignore-errors", "-json", filePath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return Fingerprint{}, fmt.Errorf("fpcalc failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var out fpcalcOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return Fingerprint{}, fmt.Errorf("decode fpcalc output: %w", err)
	}
	if strings.TrimSpace(out.Fingerprint) == "" {
		return Fingerprint{}, fmt.Errorf("fpcalc produced no fingerprint")
	}
	return Fingerprint{
		DurationSeconds: int(out.Duration + 0.5),
		Value:           out.Fingerprint,
	}, nil
}

// rawFingerprint is a file's fingerprint as chromaprint's 32-bit frames
// (fpcalc -raw), the form two files can be compared in, with the length
// fpcalc decoded. Identification sends the compressed form to AcoustID;
// this one never leaves the box.
func rawFingerprint(ctx context.Context, fpcalcPath, filePath string) ([]uint32, int, error) {
	fpcalcPath = strings.TrimSpace(fpcalcPath)
	if fpcalcPath == "" {
		return nil, 0, fmt.Errorf("fpcalc path is not configured")
	}
	cmd := exec.CommandContext(ctx, fpcalcPath, "-ignore-errors", "-raw", "-json", filePath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, 0, fmt.Errorf("fpcalc failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var out struct {
		Duration    float64  `json:"duration"`
		Fingerprint []uint32 `json:"fingerprint"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return nil, 0, fmt.Errorf("decode fpcalc output: %w", err)
	}
	if len(out.Fingerprint) == 0 {
		return nil, 0, fmt.Errorf("fpcalc produced no fingerprint")
	}
	return out.Fingerprint, int(out.Duration + 0.5), nil
}

// Two raw fingerprints are compared frame by frame at every alignment within
// fingerprintMaxOffset frames (about ten seconds: a rip with a longer or
// shorter lead-in), over at least fingerprintMinOverlap frames (about five).
const (
	fingerprintMaxOffset  = 80
	fingerprintMinOverlap = 40
)

// sameAudioSimilarity is the share of matching fingerprint bits at which two
// files are the same recording. Two encodes of one recording score above 0.9;
// different songs score near 0.5, chance — measured on the box, Quangou's
// tracks against one another came out 0.44–0.53.
const sameAudioSimilarity = 0.8

// fingerprintSimilarity is the share of bits two raw fingerprints have in
// common at their best alignment: 1 for identical audio, about 0.5 for
// unrelated audio, 0 when they never overlap enough to say.
func fingerprintSimilarity(a, b []uint32) float64 {
	best := 0.0
	for offset := -fingerprintMaxOffset; offset <= fingerprintMaxOffset; offset++ {
		errors, frames := 0, 0
		for i, frame := range a {
			j := i + offset
			if j < 0 || j >= len(b) {
				continue
			}
			errors += bits.OnesCount32(frame ^ b[j])
			frames++
		}
		if frames >= fingerprintMinOverlap {
			best = max(best, 1-float64(errors)/float64(32*frames))
		}
	}
	return best
}
