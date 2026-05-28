package covers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

const defaultUploadMaxBytes = 5 << 20

// StoreFromUpload persists an admin-uploaded image (radio thumbnails, etc.)
// under the cover cache directory and records it in extracted_covers.
// sourceKey must be a stable logical owner string, e.g.
// "internet-radio:station-id".
func (s *Service) StoreFromUpload(ctx context.Context, sourceKey, contentType string, r io.Reader) (*catalog.Image, error) {
	if s == nil || s.db == nil {
		return nil, ErrDisabled
	}
	sourceKey = strings.TrimSpace(sourceKey)
	if sourceKey == "" {
		return nil, ErrInvalidPath
	}
	mime := cleanMimeType(contentType)
	if !strings.HasPrefix(strings.ToLower(mime), "image/") {
		return nil, ErrUnsupportedType
	}

	id := uploadCoverID(sourceKey)
	extension := extensionForContentType(mime, nil)
	destPath := filepath.Join(s.coverDir, id+extension)
	tempPath := destPath + ".tmp"

	out, err := os.Create(tempPath)
	if err != nil {
		return nil, fmt.Errorf("create cover file: %w", err)
	}

	hasher := sha256.New()
	reader := io.LimitReader(r, defaultUploadMaxBytes+1)
	written, err := io.Copy(io.MultiWriter(out, hasher), reader)
	closeErr := out.Close()
	if err != nil {
		_ = os.Remove(tempPath)
		return nil, fmt.Errorf("write cover: %w", err)
	}
	if closeErr != nil {
		_ = os.Remove(tempPath)
		return nil, fmt.Errorf("flush cover: %w", closeErr)
	}
	if written > defaultUploadMaxBytes {
		_ = os.Remove(tempPath)
		return nil, ErrTooLarge
	}
	if written == 0 {
		_ = os.Remove(tempPath)
		return nil, fmt.Errorf("cover file was empty")
	}
	if err := os.Rename(tempPath, destPath); err != nil {
		_ = os.Remove(tempPath)
		return nil, fmt.Errorf("install cover file: %w", err)
	}

	checksum := hex.EncodeToString(hasher.Sum(nil))
	image := catalog.Image{
		ID:       id,
		Path:     destPath,
		MimeType: mime,
	}
	if err := s.upsert(ctx, sourceKey, checksum, image); err != nil {
		return nil, err
	}
	return &image, nil
}

func uploadCoverID(sourceKey string) string {
	hash := sha256.New()
	hash.Write([]byte("upload:"))
	hash.Write([]byte(strings.ToLower(strings.TrimSpace(sourceKey))))
	return "cover_" + hex.EncodeToString(hash.Sum(nil)[:12])
}
