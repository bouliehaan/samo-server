package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Config is read once at boot and every deployment depends on it. The failure
// mode that matters is silent misreading: a value the operator set that the
// server quietly ignores, or a malformed value that becomes something
// surprising rather than an error. These tests pin that behaviour.

func TestLoadEnvRequiresDSN(t *testing.T) {
	t.Setenv("SAMO_DB_DSN", "")
	if _, err := LoadEnv(); err == nil {
		t.Fatal("a missing SAMO_DB_DSN must be a hard error, not a default")
	}
}

func TestLoadEnvRejectsRetiredSQLiteBackend(t *testing.T) {
	t.Setenv("SAMO_DB_DSN", "postgres://x/y")
	t.Setenv("SAMO_DB_BACKEND", "sqlite")
	_, err := LoadEnv()
	if err == nil {
		t.Fatal("SAMO_DB_BACKEND=sqlite must fail loudly rather than silently using Postgres")
	}
	if !strings.Contains(err.Error(), "Postgres-only") {
		t.Fatalf("the error should tell the operator what happened, got: %v", err)
	}
}

func TestLoadEnvAcceptsPostgresBackendSpelling(t *testing.T) {
	t.Setenv("SAMO_DB_DSN", "postgres://x/y")
	for _, spelling := range []string{"postgres", "postgresql", ""} {
		t.Setenv("SAMO_DB_BACKEND", spelling)
		if _, err := LoadEnv(); err != nil {
			t.Fatalf("SAMO_DB_BACKEND=%q should be accepted, got %v", spelling, err)
		}
	}
}

func TestEnvBoolAcceptsCommonSpellings(t *testing.T) {
	for _, truthy := range []string{"1", "true", "TRUE", "yes", "on", " true "} {
		t.Setenv("SAMO_TEST_BOOL", truthy)
		if !envBool("SAMO_TEST_BOOL", false) {
			t.Errorf("%q should read as true", truthy)
		}
	}
	for _, falsy := range []string{"0", "false", "no", "off", "nonsense"} {
		t.Setenv("SAMO_TEST_BOOL", falsy)
		if envBool("SAMO_TEST_BOOL", true) {
			t.Errorf("%q should read as false", falsy)
		}
	}
	t.Setenv("SAMO_TEST_BOOL", "")
	if !envBool("SAMO_TEST_BOOL", true) {
		t.Error("an unset value must fall back to the default")
	}
}

// A typo in a duration must not silently become zero — that would turn a
// poll interval into a hot loop.
func TestEnvDurationFallsBackOnGarbage(t *testing.T) {
	t.Setenv("SAMO_TEST_DUR", "every 5 minutes")
	if got := envDuration("SAMO_TEST_DUR", time.Minute); got != time.Minute {
		t.Fatalf("malformed duration should fall back to the default, got %v", got)
	}
	t.Setenv("SAMO_TEST_DUR", "90s")
	if got := envDuration("SAMO_TEST_DUR", time.Minute); got != 90*time.Second {
		t.Fatalf("valid duration misparsed: %v", got)
	}
}

// Same reasoning for byte caps: a negative or unparseable cache limit must not
// become "cache nothing" or "cache everything".
func TestEnvInt64RejectsNegativeAndGarbage(t *testing.T) {
	for _, bad := range []string{"-1", "lots", "1.5"} {
		t.Setenv("SAMO_TEST_INT", bad)
		if got := envInt64("SAMO_TEST_INT", 42); got != 42 {
			t.Errorf("%q should fall back to the default, got %d", bad, got)
		}
	}
	t.Setenv("SAMO_TEST_INT", "1024")
	if got := envInt64("SAMO_TEST_INT", 42); got != 1024 {
		t.Fatalf("valid int misparsed: %d", got)
	}
}

// Media folders are split on the OS path separator, not on commas, so a folder
// whose name contains a comma is one path and not two.
func TestEnvPathListSplitsOnPathSeparatorNotComma(t *testing.T) {
	withComma := "/mnt/Best of 2020, Vol. 2"
	other := "/mnt/music"
	t.Setenv("SAMO_TEST_PATHS", withComma+string(filepath.ListSeparator)+other)

	got := envPathList("SAMO_TEST_PATHS")
	if len(got) != 2 {
		t.Fatalf("expected 2 paths, got %d: %v", len(got), got)
	}
	if got[0] != withComma {
		t.Fatalf("a comma in a folder name must not split it: %q", got[0])
	}
}

func TestEnvPathListSkipsEmptySegments(t *testing.T) {
	sep := string(filepath.ListSeparator)
	t.Setenv("SAMO_TEST_PATHS", sep+"/mnt/a"+sep+sep+"/mnt/b"+sep)
	got := envPathList("SAMO_TEST_PATHS")
	if len(got) != 2 || got[0] != "/mnt/a" || got[1] != "/mnt/b" {
		t.Fatalf("empty segments should be dropped, got %v", got)
	}
}

// An explicitly set provider list replaces the defaults rather than appending
// to them — otherwise an operator could never turn a provider off.
func TestEnvCSVOrDefaultReplacesRatherThanAppends(t *testing.T) {
	t.Setenv("SAMO_TEST_CSV", "musicbrainz")
	got := envCSVOrDefault("SAMO_TEST_CSV", []string{"audible", "itunes"})
	if len(got) != 1 || got[0] != "musicbrainz" {
		t.Fatalf("explicit value must replace the defaults, got %v", got)
	}

	t.Setenv("SAMO_TEST_CSV", "")
	got = envCSVOrDefault("SAMO_TEST_CSV", []string{"audible", "itunes"})
	if len(got) != 2 {
		t.Fatalf("unset should yield the defaults, got %v", got)
	}
	// The fallback must be copied, not aliased: mutating the result must not
	// corrupt the package-level default slice for the next caller.
	got[0] = "mutated"
	again := envCSVOrDefault("SAMO_TEST_CSV", []string{"audible", "itunes"})
	if again[0] != "audible" {
		t.Fatal("the default slice was aliased and got mutated by a caller")
	}
}

func TestValidateRejectsEmptyRequiredFields(t *testing.T) {
	base := Config{Addr: ":6969", DataDir: "data", DBDSN: "postgres://x", RadioConfigPath: "r.json"}
	for name, mutate := range map[string]func(*Config){
		"addr":       func(c *Config) { c.Addr = "" },
		"data dir":   func(c *Config) { c.DataDir = "" },
		"dsn":        func(c *Config) { c.DBDSN = "" },
		"radio path": func(c *Config) { c.RadioConfigPath = "" },
	} {
		cfg := base
		mutate(&cfg)
		if _, err := cfg.Validate(); err == nil {
			t.Errorf("empty %s should be rejected", name)
		}
	}
}

// A library with a path but no name gets named after its folder rather than
// being rejected or ending up blank in the UI.
func TestValidateDefaultsLibraryNameToFolder(t *testing.T) {
	cfg := Config{
		Addr: ":6969", DataDir: "data", DBDSN: "postgres://x", RadioConfigPath: "r.json",
		Libraries: []Library{{Path: "/mnt/media/Music", Kind: "music"}},
	}
	out, err := cfg.Validate()
	if err != nil {
		t.Fatal(err)
	}
	if out.Libraries[0].Name != "Music" {
		t.Fatalf("expected the folder name, got %q", out.Libraries[0].Name)
	}
}

func TestValidateRejectsLibraryWithoutPath(t *testing.T) {
	cfg := Config{
		Addr: ":6969", DataDir: "data", DBDSN: "postgres://x", RadioConfigPath: "r.json",
		Libraries: []Library{{Name: "Music", Kind: "music"}},
	}
	if _, err := cfg.Validate(); err == nil {
		t.Fatal("a library with no path must be rejected")
	}
}

// The radio config path defaults inside the data dir, so moving SAMO_DATA_DIR
// moves the config with it rather than silently reading a stale file.
func TestRadioConfigDefaultsUnderDataDir(t *testing.T) {
	t.Setenv("SAMO_DB_DSN", "postgres://x/y")
	t.Setenv("SAMO_DATA_DIR", "/var/lib/samo")
	t.Setenv("SAMO_RADIO_CONFIG", "")

	cfg, err := LoadEnv()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("/var/lib/samo", "radio.json"); cfg.RadioConfigPath != want {
		t.Fatalf("radio config path = %q, want %q", cfg.RadioConfigPath, want)
	}
}

func TestLogLevelDefaultsToInfo(t *testing.T) {
	t.Setenv("SAMO_DB_DSN", "postgres://x/y")
	t.Setenv("SAMO_LOG_LEVEL", "")
	cfg, err := LoadEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LogLevel != "info" {
		t.Fatalf("LogLevel = %q, want info", cfg.LogLevel)
	}
}
