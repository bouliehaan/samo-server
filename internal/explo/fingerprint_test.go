package explo

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// fakeFpcalc writes a stand-in fpcalc script so tests don't depend on the
// real chromaprint binary or a real audio file. script is the full shell
// script body (without the shebang line).
func fakeFpcalc(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fpcalc")
	content := "#!/bin/sh\n" + script + "\n"
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFingerprintFileParsesFpcalcJSON(t *testing.T) {
	fpcalc := fakeFpcalc(t, `echo '{"duration": 5.00, "fingerprint": "AQAAT0mUaEkSRZEmJZ"}'`)
	fp, err := fingerprintFile(context.Background(), fpcalc, "/music/explo/track.mp3")
	if err != nil {
		t.Fatal(err)
	}
	if fp.DurationSeconds != 5 {
		t.Fatalf("duration = %d, want 5", fp.DurationSeconds)
	}
	if fp.Value != "AQAAT0mUaEkSRZEmJZ" {
		t.Fatalf("fingerprint = %q", fp.Value)
	}
}

func TestFingerprintFileRoundsDuration(t *testing.T) {
	fpcalc := fakeFpcalc(t, `echo '{"duration": 238.7, "fingerprint": "AQAA"}'`)
	fp, err := fingerprintFile(context.Background(), fpcalc, "/music/explo/track.mp3")
	if err != nil {
		t.Fatal(err)
	}
	if fp.DurationSeconds != 239 {
		t.Fatalf("duration = %d, want 239", fp.DurationSeconds)
	}
}

func TestFingerprintFileFailsOnNonZeroExit(t *testing.T) {
	fpcalc := fakeFpcalc(t, `echo "corrupt file" >&2; exit 1`)
	if _, err := fingerprintFile(context.Background(), fpcalc, "/music/explo/bad.mp3"); err == nil {
		t.Fatal("expected error from failed fpcalc run")
	}
}

func TestFingerprintFileFailsOnEmptyFingerprint(t *testing.T) {
	fpcalc := fakeFpcalc(t, `echo '{"duration": 5.00, "fingerprint": ""}'`)
	if _, err := fingerprintFile(context.Background(), fpcalc, "/music/explo/silent.mp3"); err == nil {
		t.Fatal("expected error for empty fingerprint")
	}
}

func TestFingerprintFileRequiresPath(t *testing.T) {
	if _, err := fingerprintFile(context.Background(), "", "/music/explo/track.mp3"); err == nil {
		t.Fatal("expected error for unconfigured fpcalc path")
	}
}
