//go:build explolive

package explo

// Live end-to-end diagnostics against the REAL MusicBrainz / Cover Art
// Archive / iTunes / Deezer APIs, using the exact songs reported as broken
// (2026-07-16: retired tracks off DONT TAP THE GLASS; artless classics
// "I Love the Nightlife" and "Ordinary World").
//
// Deliberately excluded from normal test runs (network, external rate
// limits): run manually with
//
//	go test -tags explolive -run TestLive -v ./internal/explo/
//
// NOTE: repeated back-to-back runs trip MusicBrainz's per-IP rate limit and
// individual cases fail with "musicbrainz: status 503" — that is the limiter,
// not a defect (each fresh process restarts the in-process pacer). Wait ~30s
// between runs. In production the shared pacer keeps requests compliant, and
// a 503 surfaces as a retriable error on the identify ladder.
//
// Each case exercises the real pipeline shape: text-search identification
// through the real MusicBrainz provider (what a retried, formerly-retired
// track goes through), then the full cover chain with verified downloads
// into a real on-disk cover store.

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/covers"
	"github.com/bouliehaan/samo-server/internal/metadata"
	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
)

func TestLiveNamedSongsIdentifyAndResolveCovers(t *testing.T) {
	ctx := context.Background()
	db := storagetest.Open(t)
	coverStore, err := covers.New(db, covers.Options{CoverDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	coverStore.SetRemoteOptions(covers.RemoteOptions{})

	service := NewService(ServiceOptions{
		DB:            db,
		Dirs:          []string{"/music/explo"},
		FpcalcPath:    "/unused",
		HTTPClient:    &http.Client{Timeout: 30 * time.Second},
		MetadataApply: metadata.NewMetadataApplyServiceWithOptions(db, metadata.MetadataApplyOptions{CoverDownloader: coverStore}),
		Metadata:      metadata.NewDefaultService([]string{"musicbrainz"}, ""),
		Covers:        coverStore,
		Logger:        func(format string, args ...any) { t.Logf(format, args...) },
	})

	cases := []struct {
		file            string
		durationSeconds int
	}{
		{"/music/explo/Tyler, The Creator - Sugar On My Tongue.mp3", 164},
		{"/music/explo/Alicia Bridges - I Love the Nightlife (Disco 'Round).mp3", 187},
		{"/music/explo/Duran Duran - Ordinary World.mp3", 295},
	}

	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			match, ok, err := service.identifyByTextSearch(ctx, tc.file, "", "", tc.durationSeconds)
			if err != nil {
				t.Fatalf("identify: %v", err)
			}
			if !ok {
				t.Fatal("text-search identification found nothing — the un-retired track would stay unmatched")
			}
			t.Logf("identified: %q by %q | album=%q rg=%s recording=%s",
				match.Title, match.Artist, match.Album, match.MusicBrainzReleaseGroupID, match.MusicBrainzRecordingID)

			target := coverTarget{
				trackID:        "live-track",
				albumID:        "live-album",
				releaseGroupID: match.MusicBrainzReleaseGroupID,
				recordingMBID:  match.MusicBrainzRecordingID,
				artist:         match.Artist,
				album:          match.Album,
				title:          match.Title,
			}
			url := service.resolveCoverURL(ctx, target)
			if url == "" {
				t.Fatal("cover chain resolved NOTHING — this song would fall to the placeholder")
			}
			image, err := coverStore.DownloadFromURL(ctx, url)
			if err != nil || image == nil || image.Path == "" {
				t.Fatalf("resolved URL did not verify: %v", err)
			}
			info, err := os.Stat(image.Path)
			if err != nil || info.Size() == 0 {
				t.Fatalf("verified cover has no bytes on disk: %v", err)
			}
			t.Logf("COVER OK via %s (%d bytes on disk)", url, info.Size())
		})
	}
}
