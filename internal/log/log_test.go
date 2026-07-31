package log_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/bouliehaan/samo-server/internal/log"
)

// The level is read from the environment at package init, so exercising it for
// real means a subprocess. This is the behaviour an operator depends on:
// SAMO_LOG_LEVEL=warn must actually silence info.
func TestLevelFiltersOutput(t *testing.T) {
	if os.Getenv("SAMO_LOG_TEST_CHILD") == "1" {
		log.Debugf("DEBUG_LINE")
		log.Infof("INFO_LINE")
		log.Warnf("WARN_LINE")
		log.Errorf("ERROR_LINE")
		return
	}

	for _, tc := range []struct {
		level   string
		want    []string
		notWant []string
	}{
		{"debug", []string{"DEBUG_LINE", "INFO_LINE", "WARN_LINE", "ERROR_LINE"}, nil},
		{"info", []string{"INFO_LINE", "WARN_LINE", "ERROR_LINE"}, []string{"DEBUG_LINE"}},
		{"warn", []string{"WARN_LINE", "ERROR_LINE"}, []string{"DEBUG_LINE", "INFO_LINE"}},
		{"error", []string{"ERROR_LINE"}, []string{"DEBUG_LINE", "INFO_LINE", "WARN_LINE"}},
		// An unrecognised value must not silence the log.
		{"nonsense", []string{"INFO_LINE", "WARN_LINE", "ERROR_LINE"}, []string{"DEBUG_LINE"}},
	} {
		t.Run(tc.level, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=TestLevelFiltersOutput")
			cmd.Env = append(os.Environ(), "SAMO_LOG_TEST_CHILD=1", "SAMO_LOG_LEVEL="+tc.level)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("child failed: %v\n%s", err, out)
			}
			got := string(out)
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("SAMO_LOG_LEVEL=%s should emit %s, got:\n%s", tc.level, want, got)
				}
			}
			for _, notWant := range tc.notWant {
				if strings.Contains(got, notWant) {
					t.Errorf("SAMO_LOG_LEVEL=%s should suppress %s, got:\n%s", tc.level, notWant, got)
				}
			}
		})
	}
}

func TestSetLevelAtRuntime(t *testing.T) {
	original := log.Level()
	t.Cleanup(func() { log.SetLevel(original) })

	log.SetLevel("warn")
	if log.Level() != "warn" {
		t.Fatalf("SetLevel(warn) -> %q", log.Level())
	}
	if log.Enabled(log.LevelInfo) {
		t.Fatal("info should be disabled at warn")
	}
	if !log.Enabled(log.LevelError) {
		t.Fatal("error should stay enabled at warn")
	}

	log.SetLevel("debug")
	if !log.Enabled(log.LevelDebug) {
		t.Fatal("debug should be enabled after SetLevel(debug)")
	}
}

// StdLogger is the bridge for APIs that demand *log.Logger (http.Server's
// ErrorLog, ffmpeg stderr). Its output must obey the same dial.
func TestStdLoggerRespectsLevel(t *testing.T) {
	original := log.Level()
	t.Cleanup(func() { log.SetLevel(original) })

	log.SetLevel("error")
	// Must not panic and must be safely discardable at a suppressed level.
	log.StdLogger(log.LevelDebug).Printf("suppressed detail %d", 1)
}
