package playback

import (
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

type TargetKind string

const (
	TargetMusicArtist    TargetKind = "music-artist"
	TargetMusicAlbum     TargetKind = "music-album"
	TargetMusicTrack     TargetKind = "music-track"
	TargetMusicPlaylist  TargetKind = "music-playlist"
	TargetAudiobook      TargetKind = "audiobook"
	TargetPodcast        TargetKind = "podcast"
	TargetPodcastEpisode TargetKind = "podcast-episode"
)

type State = catalog.PlaybackState

type PatchInput struct {
	UserID              *string    `json:"userId,omitempty"`
	PlayCount           *int       `json:"playCount,omitempty"`
	SkipCount           *int       `json:"skipCount,omitempty"`
	Rating              *int       `json:"rating,omitempty"`
	Starred             *bool      `json:"starred,omitempty"`
	Favorite            *bool      `json:"favorite,omitempty"`
	ProgressSeconds     *int       `json:"progressSeconds,omitempty"`
	Completed           *bool      `json:"completed,omitempty"`
	LastPlayedAt        *time.Time `json:"lastPlayedAt,omitempty"`
	LastPositionAt      *time.Time `json:"lastPositionAt,omitempty"`
	IncrementPlayCount  bool       `json:"incrementPlayCount,omitempty"`
	IncrementSkipCount  bool       `json:"incrementSkipCount,omitempty"`
	TouchLastPlayedAt   bool       `json:"touchLastPlayedAt,omitempty"`
	TouchLastPositionAt bool       `json:"touchLastPositionAt,omitempty"`
}
