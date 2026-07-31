package libraryroots_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/bouliehaan/samo-server/internal/libraryroots"
	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
)

func newResolver(t *testing.T, root string) *libraryroots.Resolver {
	t.Helper()
	db := storagetest.Open(t)
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO libraries (id, name, kind, path) VALUES (?, ?, ?, ?)`,
		"library_test", "Test", "music", root); err != nil {
		t.Fatal(err)
	}
	return libraryroots.New(db)
}

func TestValidateAcceptsFileInsideRoot(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "track.mp3")
	if err := os.WriteFile(path, []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}

	resolved, info, err := newResolver(t, root).Validate(context.Background(), path)
	if err != nil {
		t.Fatalf("expected the file to validate: %v", err)
	}
	if info.Size() != 5 {
		t.Fatalf("unexpected size %d", info.Size())
	}
	if resolved == "" {
		t.Fatal("expected a resolved path")
	}
}

func TestValidateRejectsPathOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := newResolver(t, root).Validate(context.Background(), outside)
	if !errors.Is(err, libraryroots.ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

// The check the catalog's delete path used to lack. A symlink sitting inside a
// library whose target is outside it must not validate — otherwise a delete
// resolves the link and removes something that was never in the library.
func TestValidateRejectsSymlinkEscapingRoot(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()
	target := filepath.Join(outsideDir, "outside.mp3")
	if err := os.WriteFile(target, []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "escape.mp3")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	_, _, err := newResolver(t, root).Validate(context.Background(), link)
	if !errors.Is(err, libraryroots.ErrForbidden) {
		t.Fatalf("symlink escaping the library root must be forbidden, got %v", err)
	}
}

// The inverse bug, and the one that actually bit: when the library root is
// itself a symlink, media recorded under the resolved spelling must still
// validate. The old delete-path copy recorded only the unresolved root, so it
// silently skipped every file on such a setup and reported success.
func TestValidateAcceptsBothSpellingsOfASymlinkedRoot(t *testing.T) {
	realDir := t.TempDir()
	linkParent := t.TempDir()
	linkedRoot := filepath.Join(linkParent, "music")
	if err := os.Symlink(realDir, linkedRoot); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.WriteFile(filepath.Join(realDir, "track.mp3"), []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Library configured via the symlink.
	resolver := newResolver(t, linkedRoot)
	ctx := context.Background()

	if _, _, err := resolver.Validate(ctx, filepath.Join(linkedRoot, "track.mp3")); err != nil {
		t.Fatalf("path under the configured (symlink) root must validate: %v", err)
	}
	if _, _, err := resolver.Validate(ctx, filepath.Join(realDir, "track.mp3")); err != nil {
		t.Fatalf("path under the resolved root must validate too: %v", err)
	}
}

// "/mnt/media-other" must not pass as a child of "/mnt/media".
func TestContainsComparesBySegmentNotPrefix(t *testing.T) {
	roots := []string{"/mnt/media"}
	if libraryroots.Contains(roots, "/mnt/media-other/track.mp3") {
		t.Fatal("sibling directory sharing a name prefix must not be contained")
	}
	if !libraryroots.Contains(roots, "/mnt/media/album/track.mp3") {
		t.Fatal("genuine child must be contained")
	}
	if !libraryroots.Contains(roots, "/mnt/media") {
		t.Fatal("the root itself must be contained")
	}
}

func TestValidateReportsMissingSeparatelyFromForbidden(t *testing.T) {
	root := t.TempDir()
	_, _, err := newResolver(t, root).Validate(context.Background(), filepath.Join(root, "gone.mp3"))
	if !errors.Is(err, libraryroots.ErrMissing) {
		t.Fatalf("expected ErrMissing for an absent file inside a root, got %v", err)
	}
}

func TestInvalidateForcesReload(t *testing.T) {
	root := t.TempDir()
	resolver := newResolver(t, root)
	ctx := context.Background()

	first, err := resolver.Roots(ctx)
	if err != nil {
		t.Fatal(err)
	}
	resolver.Invalidate()
	second, err := resolver.Roots(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != len(second) {
		t.Fatalf("root set changed across an invalidate: %v vs %v", first, second)
	}
}

// A path outside every root that does not exist must read as forbidden, not
// missing. Reporting "missing" would let a caller probe for the existence of
// files outside the sandbox purely from the error it gets back.
func TestValidateDoesNotLeakExistenceOutsideRoots(t *testing.T) {
	root := t.TempDir()
	resolver := newResolver(t, root)

	_, _, err := resolver.Validate(context.Background(), filepath.Join(t.TempDir(), "nope.mp3"))
	if !errors.Is(err, libraryroots.ErrForbidden) {
		t.Fatalf("expected ErrForbidden for a nonexistent path outside the sandbox, got %v", err)
	}
}
