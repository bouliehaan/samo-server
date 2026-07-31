package files

import (
	"context"
	"errors"
	"os"

	"github.com/bouliehaan/samo-server/internal/libraryroots"
)

// validateReadablePath delegates the library-root sandbox to
// internal/libraryroots, which is the single owner of that invariant, and maps
// its sentinels onto this package's public error values so callers (and
// writeFilesError) are unaffected.
func validateReadablePath(ctx context.Context, roots *libraryroots.Resolver, path string) (string, os.FileInfo, error) {
	resolved, info, err := roots.Validate(ctx, path)
	if err != nil {
		return "", nil, mapRootError(err)
	}
	return resolved, info, nil
}

func mapRootError(err error) error {
	switch {
	case errors.Is(err, libraryroots.ErrForbidden):
		return ErrForbidden
	case errors.Is(err, libraryroots.ErrMissing):
		return ErrMissing
	case errors.Is(err, libraryroots.ErrInvalidPath):
		return ErrInvalidPath
	default:
		return err
	}
}
