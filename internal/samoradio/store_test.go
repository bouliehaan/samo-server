package samoradio

import (
	"errors"
	"testing"
)

func TestNormalizeBaseURL(t *testing.T) {
	valid := map[string]string{
		"http://127.0.0.1:7970":  "http://127.0.0.1:7970",
		"http://127.0.0.1:7970/": "http://127.0.0.1:7970",
		// A bare host:port is what somebody types; assume plain HTTP rather
		// than rejecting it, since the device is normally on loopback.
		"127.0.0.1:7970":        "http://127.0.0.1:7970",
		"  samo-radio.local  ":  "http://samo-radio.local",
		"https://radio.example": "https://radio.example",
	}
	for input, want := range valid {
		got, err := normalizeBaseURL(input)
		if err != nil {
			t.Fatalf("normalizeBaseURL(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("normalizeBaseURL(%q) = %q, want %q", input, got, want)
		}
	}

	// A path would silently produce broken command URLs later
	// (http://host/radio + /v1/play), so it is rejected up front.
	for _, input := range []string{"", "   ", "http://host/radio", "ftp://host"} {
		if _, err := normalizeBaseURL(input); !errors.Is(err, ErrInvalid) {
			t.Fatalf("normalizeBaseURL(%q) should be rejected, got %v", input, err)
		}
	}
}

// Every method must fail cleanly when there is no database, because the api
// layer's nil-check depends on it rather than on a nil pointer panic.
func TestDisabledServiceReportsErrDisabled(t *testing.T) {
	service := NewService(ServiceOptions{})
	if service.Enabled() {
		t.Fatal("a service with no DB is not enabled")
	}
	if _, err := service.ListDevices(t.Context()); !errors.Is(err, ErrDisabled) {
		t.Fatalf("expected ErrDisabled, got %v", err)
	}
	if _, err := service.GetDevice(t.Context(), "x"); !errors.Is(err, ErrDisabled) {
		t.Fatalf("expected ErrDisabled, got %v", err)
	}
	if _, err := service.Play(t.Context(), "x", PlayRequest{}); !errors.Is(err, ErrDisabled) {
		t.Fatalf("expected ErrDisabled, got %v", err)
	}
}

func TestCommandRejectsUnknownAction(t *testing.T) {
	service := NewService(ServiceOptions{})
	if _, err := service.Command(t.Context(), "device", "reboot"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid for an unknown action, got %v", err)
	}
}

func TestParseStoredTimeAcceptsBothFormats(t *testing.T) {
	if parseStoredTime("2026-08-06T12:00:00Z").IsZero() {
		t.Fatal("RFC3339 should parse")
	}
	if parseStoredTime("2026-08-06 12:00:00").IsZero() {
		t.Fatal("legacy CURRENT_TIMESTAMP format should parse")
	}
	if !parseStoredTime("").IsZero() {
		t.Fatal("empty should be the zero time")
	}
}
