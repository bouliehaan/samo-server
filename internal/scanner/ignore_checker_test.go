package scanner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIgnoreCheckerNdignore(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".ndignore"), []byte("*.tmp\nignored/**\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "ignored")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "nested.flac"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	ic := newIgnoreChecker()
	ic.resetForDir(root, root)
	if !ic.shouldIgnore("skip.tmp") {
		t.Fatal("expected *.tmp to be ignored")
	}
	if !ic.shouldIgnore("ignored/nested.flac") {
		t.Fatal("expected ignored/** to be ignored")
	}
	if ic.shouldIgnore("album/track.flac") {
		t.Fatal("expected normal track path not to be ignored")
	}
}

func TestIgnoreCheckerSkipDirPattern(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".ndignore"), []byte("skip/**\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ic := newIgnoreChecker()
	ic.pushDir(root)
	if !ic.shouldIgnore("skip") {
		t.Fatal("expected skip directory to be ignored")
	}
	if !ic.shouldIgnore("skip/nested/track.flac") {
		t.Fatal("expected path under skip to be ignored")
	}
}

func TestMatchIgnorePatternSkipGlob(t *testing.T) {
	if !matchIgnorePattern("skip/**", "skip") {
		t.Fatal("skip/** should match skip directory")
	}
	if !matchIgnorePattern("skip/**", "skip/nested/hidden.flac") {
		t.Fatal("skip/** should match paths under skip")
	}
}

func TestShouldIgnoreLibraryPath(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".ndignore"), []byte("private/**\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(root, "private", "secret.flac")
	if err := os.MkdirAll(filepath.Dir(private), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(private, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	public := filepath.Join(root, "public.flac")
	if err := os.WriteFile(public, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	if !ShouldIgnoreLibraryPath(root, private) {
		t.Fatal("expected private path to be ignored")
	}
	if ShouldIgnoreLibraryPath(root, public) {
		t.Fatal("expected public path not to be ignored")
	}
}
