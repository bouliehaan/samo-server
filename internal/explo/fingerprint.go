package explo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	cmd := exec.CommandContext(ctx, fpcalcPath, "-json", filePath)
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
