package scanner

import (
	"testing"
)

func TestParseDurationTag(t *testing.T) {
	if got := parseDurationTag("245000"); got != 245 {
		t.Fatalf("ms = %d, want 245", got)
	}
	if got := parseDurationTag("3:45"); got != 225 {
		t.Fatalf("hms = %d, want 225", got)
	}
}
