package explo

import (
	"context"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/metadata"
)

func TestNormalizeSearchTitle(t *testing.T) {
	// The decorated titles that were stuck "AWAITING RETRY" — feat. credits,
	// bracketed qualifiers — must reduce to their canonical MusicBrainz form.
	cases := map[string]string{
		"I'll Take Care of You feat. Yebba":                   "I'll Take Care of You",
		"STAY HERE 4 LIFE feat. Tim Burton, Brent Faiyaz":     "STAY HERE 4 LIFE",
		"Best Part feat. H.E.R.":                              "Best Part",
		"Don't You Worry Baby feat. Madison McFerrin":         "Don't You Worry Baby",
		"Ordinary World (single version)":                     "Ordinary World",
		"Sunflower (Post Malone, Swae Lee Cover Mix)":         "Sunflower",
		"How Old Are You (Rmx)":                               "How Old Are You",
		`Tainted Love / Where Did Our Love Go (original 12")`: "Tainted Love / Where Did Our Love Go",
		"WHERE IS MY HUSBAND!":                                "WHERE IS MY HUSBAND!", // no feat/bracket → untouched
		"Folded":                                              "Folded",
		"feat. Someone":                                       "feat. Someone", // all-credit → kept, never emptied
	}
	for in, want := range cases {
		if got := normalizeSearchTitle(in); got != want {
			t.Errorf("normalizeSearchTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTextSearchSeedPrefersCleanTags(t *testing.T) {
	// The real trap: the filename is "Artist - Album - Title.mp3", so a crude
	// filename re-parse folds the album into the title. The scanner's already-
	// split tags must win (and get normalized).
	title, artist := textSearchSeed(
		"/music/explo/ASAP Rocky - Don't Be Dumb - STAY HERE 4 LIFE feat. Tim Burton.mp3",
		"STAY HERE 4 LIFE feat. Tim Burton, Brent Faiyaz",
		"ASAP Rocky",
	)
	if title != "STAY HERE 4 LIFE" || artist != "ASAP Rocky" {
		t.Fatalf("tag seed = (%q, %q), want (STAY HERE 4 LIFE, ASAP Rocky)", title, artist)
	}

	// A genuinely tag-less drop still falls back to the filename (normalized).
	title, artist = textSearchSeed("/music/explo/Duran Duran - Ordinary World (single version).mp3", "", "")
	if title != "Ordinary World" || artist != "Duran Duran" {
		t.Fatalf("filename seed = (%q, %q), want (Ordinary World, Duran Duran)", title, artist)
	}
}

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

	match, ok, err := service.identifyByTextSearch(context.Background(), "/music/explo/Daft Punk - One More Time.mp3", "", "", 320)
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

	_, ok, err := service.identifyByTextSearch(context.Background(), "/music/explo/Daft Punk - One More Time.mp3", "", "", 90)
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

	_, ok, err := service.identifyByTextSearch(context.Background(), "/music/explo/Daft Punk - One More Time.mp3", "", "", 0)
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
	_, ok, err := service.identifyByTextSearch(context.Background(), "/music/explo/Daft Punk - One More Time.mp3", "", "", 320)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected no match without a configured metadata service")
	}
}

// TestIdentifyByTextSearchPrefersUnderivedRelease locks in the classics fix:
// a hit song has duplicate MusicBrainz recordings, and the top-scored one is
// often a duplicate that exists only on compilations ("Ultimate 90" was the
// real-world case for Ordinary World). Among duration-passing candidates the
// fallback must prefer one whose release is underived, and must never adopt
// a sampler's title as the track's album.
func TestIdentifyByTextSearchPrefersUnderivedRelease(t *testing.T) {
	provider := &fakeMusicProvider{
		results: []metadata.SearchResult{
			{
				// Top-scored duplicate: only exists on a compilation.
				ID:              "mb-comp",
				Authors:         []catalog.ContributorRef{{Name: "Duran Duran"}},
				DurationSeconds: 294,
				ExternalIDs:     catalog.ExternalIDs{MusicBrainzRecordingID: "mb-comp", MusicBrainzReleaseGroupID: "rg-comp"},
				Raw:             map[string]any{"releaseTitle": "Ultimate 90", "releaseIsDerived": true},
				Score:           100,
				Title:           "Ordinary World",
			},
			{
				// Canonical recording on the real album.
				ID:              "mb-real",
				Authors:         []catalog.ContributorRef{{Name: "Duran Duran"}},
				DurationSeconds: 295,
				ExternalIDs:     catalog.ExternalIDs{MusicBrainzRecordingID: "mb-real", MusicBrainzReleaseGroupID: "rg-real"},
				Raw:             map[string]any{"releaseTitle": "Duran Duran (The Wedding Album)", "releaseIsDerived": false},
				Score:           98,
				Title:           "Ordinary World",
			},
		},
	}
	service := newExploServiceWithFakeProvider(provider)

	match, ok, err := service.identifyByTextSearch(context.Background(), "/music/explo/Duran Duran - Ordinary World.mp3", "", "", 294)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v, want match", ok, err)
	}
	if match.MusicBrainzRecordingID != "mb-real" || match.MusicBrainzReleaseGroupID != "rg-real" {
		t.Fatalf("picked %q/%q, want the underived-release candidate", match.MusicBrainzRecordingID, match.MusicBrainzReleaseGroupID)
	}
	if match.Album != "Duran Duran (The Wedding Album)" {
		t.Fatalf("album = %q", match.Album)
	}
}

func TestIdentifyByTextSearchDerivedOnlyStillMatchesWithoutAlbum(t *testing.T) {
	provider := &fakeMusicProvider{
		results: []metadata.SearchResult{
			{
				Authors:         []catalog.ContributorRef{{Name: "Alicia Bridges"}},
				DurationSeconds: 187,
				ExternalIDs:     catalog.ExternalIDs{MusicBrainzRecordingID: "mb-comp-only"},
				Raw:             map[string]any{"releaseTitle": "Ultimate Disco: 30th Anniversary Collection", "releaseIsDerived": true},
				Score:           100,
				Title:           "I Love the Nightlife (Disco Round)",
			},
		},
	}
	service := newExploServiceWithFakeProvider(provider)

	match, ok, err := service.identifyByTextSearch(context.Background(), "/music/explo/Alicia Bridges - I Love the Nightlife.mp3", "", "", 187)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v, want match (identity is still correct, only the album title is unusable)", ok, err)
	}
	if match.Album != "" {
		t.Fatalf("album = %q, want empty — a sampler title must never become the track's album", match.Album)
	}
	if match.Title == "" || match.Artist == "" {
		t.Fatalf("match lost identity fields: %#v", match)
	}
}

func TestIdentifyByTextSearchSkipsResultsMissingArtist(t *testing.T) {
	provider := &fakeMusicProvider{
		results: []metadata.SearchResult{
			{DurationSeconds: 320, Score: 90, Title: "One More Time"}, // no authors
		},
	}
	service := newExploServiceWithFakeProvider(provider)

	_, ok, err := service.identifyByTextSearch(context.Background(), "/music/explo/One More Time.mp3", "", "", 320)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected no match for a result with no attributable artist")
	}
}
