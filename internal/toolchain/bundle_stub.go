//go:build !bundled

package toolchain

import "fmt"

func installFromBundle(dataDir, name string) (string, error) {
	_ = dataDir
	_ = name
	return "", fmt.Errorf("server built without bundled ffmpeg assets")
}
