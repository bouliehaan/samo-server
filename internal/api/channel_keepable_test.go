package api

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/channels"
	"github.com/bouliehaan/samo-server/internal/explo"
	"github.com/bouliehaan/samo-server/internal/users"
)

// keepableFixture is a server whose explo folder is /drops/explo and whose
// catalog holds one track at trackPath. db is a bare pointer: nothing on this
// path touches storage, and a real database would test the harness instead of
// the rule.
func keepableFixture(trackPath string) *Server {
	return &Server{
		explo: explo.NewService(explo.ServiceOptions{
			DB:         new(sql.DB),
			Dirs:       []string{"/drops/explo"},
			FFmpegPath: "/usr/bin/ffmpeg",
			TrackByID: func(string) (catalog.MusicTrack, error) {
				return catalog.MusicTrack{
					AudioFiles: []catalog.AudioFile{{Path: trackPath}},
				}, nil
			},
		}),
	}
}

func keepableRequest(server *Server, role string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/channels/jake/now", nil)
	return r.WithContext(server.withPrincipal(r.Context(), users.Principal{
		User: users.User{ID: "u1", Role: role},
	}))
}

func airing(ref string) *channels.PlaybackItem {
	return &channels.PlaybackItem{ItemRef: ref, Title: "Crystalised"}
}

// The case the feature exists for: a station airing an Explore drop names the
// track, so the panel can offer to keep it before rotation deletes the file.
func TestKeepableAiringTrackIDNamesTheDrop(t *testing.T) {
	server := keepableFixture("/drops/explo/The xx - Crystalised.flac")
	got := server.keepableAiringTrackID(keepableRequest(server, users.RoleAdmin), airing("track:t1"))
	if got != "t1" {
		t.Fatalf("keepableAiringTrackID = %q, want %q", got, "t1")
	}
}

// And the case that keeps it from being noise on everyone else's station.
func TestKeepableAiringTrackIDStaysSilent(t *testing.T) {
	drop := "/drops/explo/The xx - Crystalised.flac"
	cases := []struct {
		name    string
		server  *Server
		role    string
		current *channels.PlaybackItem
	}{
		// A channel programmed from the ordinary library — a Christmas
		// rotation swapped in for the season — has nothing to save.
		{"library track", keepableFixture("/mnt/music/Wham!/Last Christmas.flac"), users.RoleAdmin, airing("track:t1")},
		// Keeping is admin-only, and an entry whose only outcome is a 403 is
		// worse than no entry.
		{"not an admin", keepableFixture(drop), users.RoleUser, airing("track:t1")},
		// Only music has a file to copy.
		{"a relayed station", keepableFixture(drop), users.RoleAdmin, airing("station:s1")},
		{"a podcast episode", keepableFixture(drop), users.RoleAdmin, airing("episode:e1")},
		{"between items", keepableFixture(drop), users.RoleAdmin, nil},
		{"explo not configured", &Server{}, users.RoleAdmin, airing("track:t1")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.server.keepableAiringTrackID(keepableRequest(tc.server, tc.role), tc.current)
			if got != "" {
				t.Fatalf("keepableAiringTrackID = %q, want empty", got)
			}
		})
	}
}
