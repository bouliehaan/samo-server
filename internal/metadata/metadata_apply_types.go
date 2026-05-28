package metadata

import (
	"errors"
)

var (
	ErrMetadataApplyDisabled = errors.New("metadata apply service is disabled")
	ErrInvalidApplyTarget    = errors.New("invalid metadata apply target kind")
	ErrInvalidApplyField     = errors.New("metadata apply field is not allowed for this target")
	ErrEmptyApplyFields      = errors.New("at least one metadata field must be selected")
	ErrApplyCandidateKind    = errors.New("metadata candidate does not match apply target")
	ErrApplyNotFound         = errors.New("metadata apply target not found")
)

type ApplyTargetKind string

// ApplyTargetKind constants use the Samo-native domain names. Audiobooks
// and podcasts are independent — there is intentionally no shared "shelf"
// apply target — and podcast episodes are distinct from podcast shows.
const (
	ApplyTargetAudiobook      ApplyTargetKind = "audiobook"
	ApplyTargetPodcast        ApplyTargetKind = "podcast"
	ApplyTargetPodcastEpisode ApplyTargetKind = "podcast-episode"
	ApplyTargetMusicArtist    ApplyTargetKind = "music-artist"
	ApplyTargetMusicAlbum     ApplyTargetKind = "music-album"
	ApplyTargetMusicTrack     ApplyTargetKind = "music-track"
	ApplyTargetPodcastFeed    ApplyTargetKind = "podcast-feed"
)

func ParseApplyTargetKind(raw string) (ApplyTargetKind, error) {
	switch ApplyTargetKind(raw) {
	case ApplyTargetAudiobook, ApplyTargetPodcast, ApplyTargetPodcastEpisode,
		ApplyTargetMusicArtist, ApplyTargetMusicAlbum, ApplyTargetMusicTrack,
		ApplyTargetPodcastFeed:
		return ApplyTargetKind(raw), nil
	default:
		return "", ErrInvalidApplyTarget
	}
}

// MetadataApplyRequest is the user-confirmed apply payload from a metadata search candidate.
type MetadataApplyRequest struct {
	TargetKind         string       `json:"targetKind"`
	TargetID           string       `json:"targetId"`
	Candidate          SearchResult `json:"candidate"`
	Fields             []string     `json:"fields"`
	DeferCatalogReload bool         `json:"deferCatalogReload,omitempty"`
}

type MetadataApplyPreview struct {
	TargetKind      string   `json:"targetKind"`
	TargetID        string   `json:"targetId"`
	AllowedFields   []string `json:"allowedFields"`
	RequestedFields []string `json:"requestedFields"`
	AppliedFields   []string `json:"appliedFields"`
	SkippedFields   []string `json:"skippedFields"`
	Before          any      `json:"before"`
	After           any      `json:"after"`
}

type MetadataApplyResult struct {
	TargetKind    string   `json:"targetKind"`
	TargetID      string   `json:"targetId"`
	AppliedFields []string `json:"appliedFields"`
	SkippedFields []string `json:"skippedFields"`
}
