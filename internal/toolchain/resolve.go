package toolchain

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const bundleVersion = "linux-gpl-latest"

var ErrNotFound = errors.New("bundled ffmpeg tools not found")

type Tools struct {
	FFmpeg  string
	FFprobe string
}

type Options struct {
	DataDir string
}

func Resolve(options Options) (Tools, error) {
	dataDir := strings.TrimSpace(options.DataDir)
	if dataDir == "" {
		dataDir = "data"
	}

	ffmpeg, err := resolveTool("ffmpeg", "SAMO_FFMPEG_PATH", dataDir)
	if err != nil {
		return Tools{}, err
	}
	ffprobe, err := resolveTool("ffprobe", "SAMO_FFPROBE_PATH", dataDir)
	if err != nil {
		return Tools{}, err
	}
	return Tools{FFmpeg: ffmpeg, FFprobe: ffprobe}, nil
}

func resolveTool(name, envKey, dataDir string) (string, error) {
	if override := strings.TrimSpace(os.Getenv(envKey)); override != "" {
		return validateExecutable(override)
	}

	candidates := []string{
		filepath.Join(executableDir(), "bin", binaryName(name)),
		filepath.Join(dataDir, "tools", bundleVersion, platformKey(), binaryName(name)),
		filepath.Join(repoRoot(), "bin", binaryName(name)),
	}

	for _, candidate := range candidates {
		if path, err := validateExecutable(candidate); err == nil {
			return path, nil
		}
	}

	if path, err := installFromBundle(dataDir, name); err == nil {
		return path, nil
	}

	return "", fmt.Errorf(
		"%w: %s (deploy bin/ffmpeg and bin/ffprobe beside samo-server on Ubuntu, or run ./scripts/bundle-ffmpeg.sh on Linux)",
		ErrNotFound,
		name,
	)
}

func validateExecutable(path string) (string, error) {
	absolute, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%q is a directory", absolute)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%q is not a regular file", absolute)
	}
	if info.Size() == 0 {
		return "", fmt.Errorf("%q is empty", absolute)
	}
	if info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("%q is not executable", absolute)
	}
	return absolute, nil
}

func executableDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return filepath.Dir(exe)
	}
	return filepath.Dir(exe)
}

func repoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
