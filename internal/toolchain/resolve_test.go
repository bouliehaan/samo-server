package toolchain

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveUsesBinNextToRepoRoot(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ffmpeg := filepath.Join(binDir, "ffmpeg")
	ffprobe := filepath.Join(binDir, "ffprobe")
	if err := os.WriteFile(ffmpeg, []byte("#!/bin/sh\necho ffmpeg\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ffprobe, []byte("#!/bin/sh\necho ffprobe\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	defer t.Chdir(wd)

	if _, err := os.Stat("go.mod"); os.IsNotExist(err) {
		if err := os.WriteFile("go.mod", []byte("module test\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tools, err := Resolve(Options{DataDir: filepath.Join(root, "data")})
	if err != nil {
		t.Fatal(err)
	}
	if tools.FFmpeg != ffmpeg {
		t.Fatalf("ffmpeg = %q, want %q", tools.FFmpeg, ffmpeg)
	}
	if tools.FFprobe != ffprobe {
		t.Fatalf("ffprobe = %q, want %q", tools.FFprobe, ffprobe)
	}
}

func TestResolveHonorsExplicitOverride(t *testing.T) {
	root := t.TempDir()
	ffmpeg := filepath.Join(root, "custom-ffmpeg")
	if err := os.WriteFile(ffmpeg, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SAMO_FFMPEG_PATH", ffmpeg)
	t.Setenv("SAMO_FFPROBE_PATH", ffmpeg)

	tools, err := Resolve(Options{DataDir: root})
	if err != nil {
		t.Fatal(err)
	}
	if tools.FFmpeg != ffmpeg || tools.FFprobe != ffmpeg {
		t.Fatalf("tools = %+v, want %q for both", tools, ffmpeg)
	}
}
