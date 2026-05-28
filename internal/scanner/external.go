package scanner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const scanSubprocessEnv = "SAMO_SCAN_SUBPROCESS"

// ScanWithProgressExternal runs the scan in a child process to isolate memory use
// (Navidrome DevExternalScanner).
func (s *Scanner) ScanWithProgressExternal(ctx context.Context, libraries []Library, opts ScanOptions) (ScanStats, error) {
	exe, err := os.Executable()
	if err != nil {
		return ScanStats{}, fmt.Errorf("resolve executable for external scan: %w", err)
	}
	payload := subprocessPayload{
		Mode:      normalizeScanMode(opts.Mode),
		Subpaths:  opts.Subpaths,
		Libraries: libraries,
		JobID:     opts.JobID,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return ScanStats{}, err
	}
	tmp, err := os.CreateTemp("", "samo-scan-*.json")
	if err != nil {
		return ScanStats{}, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return ScanStats{}, err
	}
	if err := tmp.Close(); err != nil {
		return ScanStats{}, err
	}

	cmd := exec.CommandContext(ctx, exe, "--scan-subprocess", "--payload", tmpPath)
	cmd.Env = append(os.Environ(),
		scanSubprocessEnv+"=1",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return ScanStats{}, fmt.Errorf("external scan subprocess: %w", err)
	}
	if ctx.Err() != nil {
		return ScanStats{}, ctx.Err()
	}
	// Subprocess writes stats to sidecar file.
	statsPath := tmpPath + ".stats"
	defer os.Remove(statsPath)
	raw, err := os.ReadFile(statsPath)
	if err != nil {
		return ScanStats{}, fmt.Errorf("read external scan stats: %w", err)
	}
	var stats ScanStats
	if err := json.Unmarshal(raw, &stats); err != nil {
		return ScanStats{}, err
	}
	return stats, nil
}

type subprocessPayload struct {
	Mode      string    `json:"mode"`
	Subpaths  []string  `json:"subpaths"`
	Libraries []Library `json:"libraries"`
	JobID     string    `json:"jobId,omitempty"`
}

// RunSubprocessScan is the entry point for --scan-subprocess (called from main).
func RunSubprocessScan(ctx context.Context, s *Scanner, payloadPath string) error {
	raw, err := os.ReadFile(payloadPath)
	if err != nil {
		return err
	}
	var payload subprocessPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return err
	}
	s.externalScanner = false
	progress := newSubprocessProgress(s.db, payload.JobID)
	progress.Start()
	defer progress.Stop()
	stats, scanErr := s.ScanWithProgress(ctx, payload.Libraries, ScanOptions{
		Mode:           payload.Mode,
		Subpaths:       payload.Subpaths,
		JobID:          payload.JobID,
		OnFileSeen:     progress.SetSeen,
		OnWalkProgress: progress.SetSeen,
		OnActivity:     progress.SetActivity,
		OnFileActive: func(path string) {
			progress.SetActivity("probing " + filepath.Base(path))
		},
	})
	progress.Flush()
	if writeErr := writeSubprocessStats(payloadPath, stats); writeErr != nil && scanErr == nil {
		scanErr = writeErr
	}
	return scanErr
}

func writeSubprocessStats(payloadPath string, stats ScanStats) error {
	data, err := json.Marshal(stats)
	if err != nil {
		return err
	}
	return os.WriteFile(payloadPath+".stats", data, 0o644)
}

func IsScanSubprocess() bool {
	return strings.TrimSpace(os.Getenv(scanSubprocessEnv)) == "1"
}

func PayloadPathFromArgs(args []string) string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--payload" {
			return filepath.Clean(args[i+1])
		}
	}
	return ""
}
