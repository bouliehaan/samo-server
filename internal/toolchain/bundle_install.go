//go:build bundled

package toolchain

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed all:assets
var bundledAssets embed.FS

func installFromBundle(dataDir, name string) (string, error) {
	destDir := filepath.Join(dataDir, "tools", bundleVersion, platformKey())
	dest := filepath.Join(destDir, binaryName(name))

	if path, err := validateExecutable(dest); err == nil {
		return path, nil
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("create bundled tool dir: %w", err)
	}

	assetRoot := filepath.Join("assets", platformKey())
	entries, err := fs.ReadDir(bundledAssets, assetRoot)
	if err != nil {
		return "", fmt.Errorf("read bundled assets for %s: %w", platformKey(), err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		src := filepath.Join(assetRoot, entry.Name())
		data, err := bundledAssets.ReadFile(src)
		if err != nil {
			return "", fmt.Errorf("read bundled asset %q: %w", entry.Name(), err)
		}
		target := filepath.Join(destDir, entry.Name())
		if err := os.WriteFile(target, data, 0o755); err != nil {
			return "", fmt.Errorf("write bundled tool %q: %w", entry.Name(), err)
		}
	}

	return validateExecutable(dest)
}
