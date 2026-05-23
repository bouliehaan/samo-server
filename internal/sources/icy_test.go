package sources

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestProbeIcyStreamCapturesHeadersAndStreamTitle(t *testing.T) {
	body := buildIcyBody(t, 8192, "StreamTitle='Aphex Twin - Xtal';StreamUrl='https://example.com';")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Icy-MetaData"); got != "1" {
			t.Errorf("Icy-MetaData = %q, want 1", got)
		}
		w.Header().Set("icy-name", "Night Signals")
		w.Header().Set("icy-genre", "ambient, electronic")
		w.Header().Set("icy-br", "192")
		w.Header().Set("icy-description", "After-hours frequencies")
		w.Header().Set("icy-url", "https://example.com/")
		w.Header().Set("icy-metaint", "8192")
		w.Header().Set("Content-Type", "audio/mpeg")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	defer server.Close()

	result, err := ProbeIcyStream(context.Background(), nil, server.URL)
	if err != nil {
		t.Fatalf("ProbeIcyStream returned error: %v", err)
	}
	if result.StationName != "Night Signals" {
		t.Errorf("StationName = %q, want Night Signals", result.StationName)
	}
	if result.Description != "After-hours frequencies" {
		t.Errorf("Description = %q", result.Description)
	}
	if result.Genre != "ambient, electronic" {
		t.Errorf("Genre = %q", result.Genre)
	}
	if result.Bitrate != 192 {
		t.Errorf("Bitrate = %d, want 192", result.Bitrate)
	}
	if result.Codec != "mp3" {
		t.Errorf("Codec = %q, want mp3", result.Codec)
	}
	if result.ContentType != "audio/mpeg" {
		t.Errorf("ContentType = %q", result.ContentType)
	}
	if result.HomepageURL != "https://example.com/" {
		t.Errorf("HomepageURL = %q", result.HomepageURL)
	}
	if got := result.Tags; len(got) != 2 || got[0] != "ambient" || got[1] != "electronic" {
		t.Errorf("Tags = %v", got)
	}
	if result.NowPlaying != "Aphex Twin - Xtal" {
		t.Errorf("NowPlaying = %q", result.NowPlaying)
	}
	if result.Artist != "Aphex Twin" {
		t.Errorf("Artist = %q", result.Artist)
	}
	if result.Title != "Xtal" {
		t.Errorf("Title = %q", result.Title)
	}
	if result.ProbedAt.IsZero() {
		t.Errorf("ProbedAt is zero")
	}
}

func TestProbeIcyStreamWithoutMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("icy-name", "Plain FM")
		w.Header().Set("Content-Type", "audio/aac")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("audiodata"))
	}))
	defer server.Close()

	result, err := ProbeIcyStream(context.Background(), nil, server.URL)
	if err != nil {
		t.Fatalf("ProbeIcyStream returned error: %v", err)
	}
	if result.StationName != "Plain FM" {
		t.Errorf("StationName = %q", result.StationName)
	}
	if result.Codec != "aac" {
		t.Errorf("Codec = %q, want aac", result.Codec)
	}
	if result.NowPlaying != "" {
		t.Errorf("NowPlaying = %q, want empty", result.NowPlaying)
	}
}

func TestProbeInternetRadioStationPersistsMetadata(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	body := buildIcyBody(t, 4096, "StreamTitle='Boards of Canada - Roygbiv';")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("icy-name", "Loop Radio")
		w.Header().Set("icy-genre", "electronic")
		w.Header().Set("icy-br", "128")
		w.Header().Set("icy-metaint", "4096")
		w.Header().Set("Content-Type", "audio/mpeg")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	defer server.Close()

	service := New(db)
	station, err := service.AddInternetRadioStation(ctx, AddInternetRadioStationInput{
		Name:      "Loop Radio",
		StreamURL: server.URL + "/stream",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !station.Probe.Enabled {
		t.Fatalf("probe should be enabled by default")
	}
	if station.Probe.IntervalSeconds != DefaultProbeIntervalSeconds {
		t.Fatalf("default interval = %d, want %d", station.Probe.IntervalSeconds, DefaultProbeIntervalSeconds)
	}

	updated, err := service.ProbeInternetRadioStation(ctx, station.ID)
	if err != nil {
		t.Fatalf("ProbeInternetRadioStation returned error: %v", err)
	}
	if updated.NowPlaying == nil {
		t.Fatalf("NowPlaying not persisted")
	}
	if updated.NowPlaying.Raw != "Boards of Canada - Roygbiv" {
		t.Errorf("NowPlaying.Raw = %q", updated.NowPlaying.Raw)
	}
	if updated.NowPlaying.Artist != "Boards of Canada" {
		t.Errorf("NowPlaying.Artist = %q", updated.NowPlaying.Artist)
	}
	if updated.NowPlaying.Title != "Roygbiv" {
		t.Errorf("NowPlaying.Title = %q", updated.NowPlaying.Title)
	}
	if updated.NowPlaying.UpdatedAt == nil {
		t.Errorf("NowPlaying.UpdatedAt is nil")
	}
	if updated.Bitrate != 128 {
		t.Errorf("Bitrate = %d, want 128 (filled from probe)", updated.Bitrate)
	}
	if updated.Codec != "mp3" {
		t.Errorf("Codec = %q, want mp3", updated.Codec)
	}
	if updated.Probe.Status != ProbeStatusOK {
		t.Errorf("Probe.Status = %q", updated.Probe.Status)
	}
	if updated.Probe.NextProbeAt == nil {
		t.Errorf("NextProbeAt was not scheduled")
	}
}

func TestProbeInternetRadioStationRecordsFailure(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	service := New(db)
	station, err := service.AddInternetRadioStation(ctx, AddInternetRadioStationInput{
		Name:      "Down Stream",
		StreamURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ProbeInternetRadioStation(ctx, station.ID); err == nil {
		t.Fatalf("expected probe to fail")
	}
	after, err := service.GetInternetRadioStation(ctx, station.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Probe.ConsecutiveErrors != 1 {
		t.Fatalf("ConsecutiveErrors = %d, want 1", after.Probe.ConsecutiveErrors)
	}
	if after.Probe.LastError == "" {
		t.Fatalf("LastError empty")
	}
	if after.Probe.Status != ProbeStatusError {
		t.Fatalf("Status = %q, want %q", after.Probe.Status, ProbeStatusError)
	}
}

func TestUpdateInternetRadioStationRejectsBadInterval(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	service := New(db)
	station, err := service.AddInternetRadioStation(ctx, AddInternetRadioStationInput{
		Name:      "Bad Interval Station",
		StreamURL: "https://example.com/stream",
	})
	if err != nil {
		t.Fatal(err)
	}
	tooLow := 1
	if _, err := service.UpdateInternetRadioStation(ctx, station.ID, UpdateInternetRadioStationInput{
		ProbeIntervalSeconds: &tooLow,
	}); err == nil {
		t.Fatal("expected interval validation error")
	}
	disabled := false
	updated, err := service.UpdateInternetRadioStation(ctx, station.ID, UpdateInternetRadioStationInput{
		ProbeEnabled: &disabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Probe.Enabled {
		t.Fatalf("ProbeEnabled = true, want false")
	}
	if updated.Probe.NextProbeAt != nil {
		t.Fatalf("NextProbeAt = %v, want nil when probe disabled", updated.Probe.NextProbeAt)
	}
}

// buildIcyBody assembles a synthetic ICY response body: `interval` audio
// bytes followed by a metadata frame containing the supplied StreamTitle.
func buildIcyBody(t *testing.T, interval int, streamTitle string) []byte {
	t.Helper()
	if interval <= 0 {
		t.Fatalf("buildIcyBody: interval must be positive")
	}
	payload := make([]byte, 0, interval+1+len(streamTitle)+16)
	audio := bytes.Repeat([]byte{0xaa}, interval)
	payload = append(payload, audio...)

	frame := []byte(streamTitle)
	// Pad to a multiple of 16.
	padded := frame
	if remainder := len(padded) % 16; remainder != 0 {
		padded = append(padded, bytes.Repeat([]byte{0}, 16-remainder)...)
	}
	if len(padded) > icyMetadataMaxFrameBytes {
		t.Fatalf("buildIcyBody: frame too large (%d bytes)", len(padded))
	}
	lengthByte := byte(len(padded) / 16)
	payload = append(payload, lengthByte)
	payload = append(payload, padded...)
	return payload
}

// Sanity check on parseStreamTitle directly.
func TestParseStreamTitle(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		raw    string
		title  string
		artist string
	}{
		{name: "artist-title", input: "StreamTitle='Burial - Archangel';", raw: "Burial - Archangel", title: "Archangel", artist: "Burial"},
		{name: "title-only", input: "StreamTitle='Cosmic Journey';", raw: "Cosmic Journey", title: "Cosmic Journey"},
		{name: "with-extras", input: "StreamTitle='Aphex Twin - Xtal';StreamUrl='https://example.com';", raw: "Aphex Twin - Xtal", title: "Xtal", artist: "Aphex Twin"},
		{name: "empty", input: "StreamTitle='';", raw: "", title: "", artist: ""},
		{name: "double-quotes", input: `StreamTitle="Solo Title";`, raw: "Solo Title", title: "Solo Title"},
		{name: "leading-spaces", input: fmt.Sprintf("StreamTitle='  %s  ';", "Padded Title"), raw: "Padded Title", title: "Padded Title"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, title, artist := parseStreamTitle([]byte(tc.input))
			if raw != tc.raw || title != tc.title || artist != tc.artist {
				t.Fatalf("got (%q, %q, %q) want (%q, %q, %q)", raw, title, artist, tc.raw, tc.title, tc.artist)
			}
		})
	}
}
