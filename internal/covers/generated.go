package covers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// StoreGenerated persists a server-generated image (e.g. the explo placeholder
// tile) into the cover store, keyed by a caller-chosen stable key, and returns
// the stored image. Idempotent: the id derives from the key, so re-storing the
// same key with the same bytes returns the existing entry without rewriting.
// The synthetic "generated:" source path keeps these rows distinguishable from
// file-extracted and URL-downloaded covers in extracted_covers.
func (s *Service) StoreGenerated(ctx context.Context, key string, data []byte, mimeType string) (*catalog.Image, error) {
	if s == nil || s.db == nil {
		return nil, ErrDisabled
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, fmt.Errorf("generated cover key is required")
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("generated cover is empty")
	}

	sourcePath := "generated:" + key
	id := coverID(sourcePath)
	checksum := sha256.Sum256(data)
	checksumHex := hex.EncodeToString(checksum[:])

	if existing, err := s.loadByID(ctx, id); err == nil &&
		existing.sourceChecksum == checksumHex && fileExists(existing.path) {
		image := existing.image
		return &image, nil
	}

	extension := ".png"
	if strings.EqualFold(strings.TrimSpace(mimeType), "image/jpeg") {
		extension = ".jpg"
	}
	destPath := filepath.Join(s.coverDir, id+extension)
	tempPath := destPath + ".tmp"
	if err := os.WriteFile(tempPath, data, 0o644); err != nil {
		return nil, fmt.Errorf("write generated cover: %w", err)
	}
	if err := os.Rename(tempPath, destPath); err != nil {
		_ = os.Remove(tempPath)
		return nil, fmt.Errorf("install generated cover: %w", err)
	}

	image := catalog.Image{
		ID:       id,
		Path:     destPath,
		MimeType: cleanMimeType(mimeType),
	}
	if err := s.upsert(ctx, sourcePath, checksumHex, image); err != nil {
		return nil, err
	}
	return &image, nil
}
