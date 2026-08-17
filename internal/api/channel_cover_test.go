package api

import (
	"context"
	"testing"

	"github.com/bouliehaan/samo-server/internal/channels"
)

// An uploaded cover is answered verbatim and nothing is generated over it —
// the generated tile is the fallback, not a competitor.
func TestChannelCoverPrefersTheUpload(t *testing.T) {
	server := &Server{}
	got := server.channelCoverID(context.Background(), channels.Channel{
		ID:      "jake",
		CoverID: "cover_abc123",
	})
	if got != "cover_abc123" {
		t.Fatalf("channelCoverID = %q, want the uploaded cover", got)
	}
}

// A server with no cover store still lists its channels. coversService()
// panics when covers are unconfigured, which is the right answer on an upload
// route and the wrong one on a list — this is the guard for that.
func TestChannelCoverSurvivesNoCoverStore(t *testing.T) {
	server := &Server{}
	if got := server.channelCoverID(context.Background(), channels.Channel{ID: "jake"}); got != "" {
		t.Fatalf("channelCoverID = %q, want empty with no cover store", got)
	}
}
