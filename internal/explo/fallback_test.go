package explo

import (
	"context"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/metadata"
)

func TestParseFilenameSearchQuery(t *testing.T) {
	tests := []struct {
		path       string
		wantTitle  string
		wantArtist string
	}{
		{
			path:       "/music/explo/Daft Punk - One More Time.mp3",
			wantArtist: "Daft Punk",
			wantTitle:  "One More Time",
		},
		{
			path:       "/music/explo/02 - Daft Punk - One More Time.mp3",
			wantArtist: "Daft Punk",
			wantTitle:  "One More Time",
		},
		{
			path:       "/music/explo/02. Daft Punk - One More Time.flac",
			wantArtist: "Daft Punk",
			wantTitle:  "One More Time",
		},
		{
			path:       "/music/explo/Daft_Punk_-_One_More_Time.mp3",
			wantArtist: "Daft Punk",
			wantTitle:  "One More Time",
		},
		{
			// No " - " delimiter: whole cleaned name becomes a title-only query.
			path:       "/music/explo/SomeTrackWithNoDelimiter.mp3",
			wantArtist: "",
			wantTitle:  "SomeTrackWithNoDelimiter",
		},
		{
			// A 4-digit year must NOT be treated as a track-number prefix.
			path:       "/music/explo/1999 - Prince - Little Red Corvette.mp3",
			wantArtist: "1999",
			wantTitle:  "Prince - Little Red Corvette",
		},
		{
			// A bare number with nothing after it isn't a "prefix" (the regex
			// requires a trailing separator), so it's kept as a weak title
			// hint - harmless, since identifyByTextSearch's duration gate is
			// the actual safety net regardless of hint quality.
			path:       "/music/explo/005.mp3",
			wantArtist: "",
			wantTitle:  "005",
		},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			title, artist := parseFilenameSearchQuery(tc.path)
			if title != tc.wantTitle || artist != tc.wantArtist {
				t.Fatalf("parseFilenameSearchQuery(%q) = (title=%q, artist=%q), want (title=%q, artist=%q)",
					tc.path, title, artist, tc.wantTitle, tc.wantArtist)
			}
		})
	}
}

func TestWithinDurationTolerance(t *testing.T) {
	tests := []struct {
		name      string
		candidate int
		known     int
		want      bool
	}{
		{name: "exact match", candidate: 200, known: 200, want: true},
		{name: "within 5s floor on a short track", candidate: 34, known: 30, want: true},
		{name: "just outside 5s floor on a short track", candidate: 36, known: 30, want: false},
		{name: "within 5 percent on a long track", candidate: 580, known: 600, want: true},
		{name: "outside 5 percent on a long track", candidate: 500, known: 600, want: false},
		{name: "same length but clearly a different song", candidate: 200, known: 90, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := withinDurationTolerance(tc.candidate, tc.known); got != tc.want {
				t.Fatalf("withinDurationTolerance(%d, %d) = %v, want %v", tc.candidate, tc.known, got, tc.want)
			}
		})
	}
}

// fakeMusicProvider is a minimal metadata.Provider stub so identifyByTextSearch
// can be tested without depending on the real MusicBrainz HTTP API.
type fakeMusicProvider struct {
	results []metadata.SearchResult
	err     error
	calls   int
}

func (p *fakeMusicProvider) Name() string                     { return "fake" }
func (p *fakeMusicProvider) Supports(kind metadata.Kind) bool { return kind == metadata.KindMusic }
func (p *fakeMusicProvider) Status() metadata.ProviderStatus {
	return metadata.ProviderStatus{Enabled: true, Kinds: []metadata.Kind{metadata.KindMusic}, Name: "fake"}
}
func (p *fakeMusicProvider) Search(_ context.Context, _ metadata.SearchRequest) ([]metadata.SearchResult, error) {
	p.calls++
	return p.results, p.err
}

func newExploServiceWithFakeProvider(provider *fakeMusicProvider) *Service {
	return &Service{
		metadata: metadata.NewService(metadata.ServiceOptions{Providers: []metadata.Provider{provider}}),
	}
}

func TestIdentifyByTextSearchAcceptsDurationMatchedCandidate(t *testing.T) {
	provider := &fakeMusicProvider{
		results: []metadata.SearchResult{
			{
				Authors:         []catalog.ContributorRef{{Name: "Daft Punk"}},
				DurationSeconds: 320,
				ExternalIDs:     catalog.ExternalIDs{MusicBrainzRecordingID: "mb-1"},
				Raw:             map[string]any{"releaseTitle": "Discovery"},
				Score:           90,
				Title:           "One More Time",
			},
		},
	}
	service := newExploServiceWithFakeProvider(provider)

	match, ok, err := service.identifyByTextSearch(context.Background(), "/music/explo/Daft Punk - One More Time.mp3", 320)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected a match")
	}
	if match.Source != "musicbrainz-search" {
		t.Fatalf("source = %q", match.Source)
	}
	if match.Title != "One More Time" || match.Artist != "Daft Punk" || match.Album != "Discovery" {
		t.Fatalf("match = %#v", match)
	}
	if match.MusicBrainzRecordingID != "mb-1" {
		t.Fatalf("recording id = %q", match.MusicBrainzRecordingID)
	}
}

func TestIdentifyByTextSearchRejectsDurationMismatch(t *testing.T) {
	// Same title/artist text, but the actual file is 90s and the search
	// result is a 320s song - a plausible-sounding but WRONG match. This is
	// exactly the failure mode the duration gate exists to prevent.
	provider := &fakeMusicProvider{
		results: []metadata.SearchResult{
			{
				Authors:         []catalog.ContributorRef{{Name: "Daft Punk"}},
				DurationSeconds: 320,
				Score:           90,
				Title:           "One More Time",
			},
		},
	}
	service := newExploServiceWithFakeProvider(provider)

	_, ok, err := service.identifyByTextSearch(context.Background(), "/music/explo/Daft Punk - One More Time.mp3", 90)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected the duration-mismatched candidate to be rejected")
	}
}

func TestIdentifyByTextSearchSkipsWithoutKnownDuration(t *testing.T) {
	provider := &fakeMusicProvider{
		results: []metadata.SearchResult{
			{
				Authors:         []catalog.ContributorRef{{Name: "Daft Punk"}},
				DurationSeconds: 320,
				Score:           90,
				Title:           "One More Time",
			},
		},
	}
	service := newExploServiceWithFakeProvider(provider)

	_, ok, err := service.identifyByTextSearch(context.Background(), "/music/explo/Daft Punk - One More Time.mp3", 0)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected no attempt without a known reference duration")
	}
	if provider.calls != 0 {
		t.Fatalf("provider should never be called without a known duration, got %d calls", provider.calls)
	}
}

func TestIdentifyByTextSearchNoMetadataServiceConfigured(t *testing.T) {
	service := &Service{} // metadata is nil - fallback disabled, not a hard error
	_, ok, err := service.identifyByTextSearch(context.Background(), "/music/explo/Daft Punk - One More Time.mp3", 320)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected no match without a configured metadata service")
	}
}

func TestIdentifyByTextSearchSkipsResultsMissingArtist(t *testing.T) {
	provider := &fakeMusicProvider{
		results: []metadata.SearchResult{
			{DurationSeconds: 320, Score: 90, Title: "One More Time"}, // no authors
		},
	}
	service := newExploServiceWithFakeProvider(provider)

	_, ok, err := service.identifyByTextSearch(context.Background(), "/music/explo/One More Time.mp3", 320)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected no match for a result with no attributable artist")
	}
}
