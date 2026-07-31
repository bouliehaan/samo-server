package catalog

import (
	"encoding/json"
	"strings"
	"time"
)

// Patch decoding and merging. Pure functions over already-loaded patches — no
// database — so they belong to the model. internal/catalogstore calls the
// exported ones when writing overrides back.

func MergeOverrideExternalIDs(current, incoming json.RawMessage) json.RawMessage {
	var left, right ExternalIDs
	decodeOverrideJSON(current, &left)
	decodeOverrideJSON(incoming, &right)
	merged := mergeExternalIDsOverride(left, right)
	data, err := json.Marshal(merged)
	if err != nil {
		return incoming
	}
	return data
}
func DecodePatchString(patch MetadataOverridePatch, field string) (string, bool) {
	raw, ok := patch[field]
	if !ok {
		return "", false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	return value, true
}
func DecodePatchStringSlice(patch MetadataOverridePatch, field string) ([]string, bool) {
	raw, ok := patch[field]
	if !ok {
		return nil, false
	}
	var value []string
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, false
	}
	return value, true
}
func decodeOverrideJSON(value json.RawMessage, out any) {
	if len(value) == 0 {
		return
	}
	_ = json.Unmarshal(value, out)
}

func mergeExternalIDsOverride(current, incoming ExternalIDs) ExternalIDs {
	merged := current
	if incoming.MusicBrainzArtistID != "" {
		merged.MusicBrainzArtistID = incoming.MusicBrainzArtistID
	}
	if incoming.MusicBrainzReleaseGroupID != "" {
		merged.MusicBrainzReleaseGroupID = incoming.MusicBrainzReleaseGroupID
	}
	if incoming.MusicBrainzReleaseID != "" {
		merged.MusicBrainzReleaseID = incoming.MusicBrainzReleaseID
	}
	if incoming.MusicBrainzRecordingID != "" {
		merged.MusicBrainzRecordingID = incoming.MusicBrainzRecordingID
	}
	if incoming.MusicBrainzTrackID != "" {
		merged.MusicBrainzTrackID = incoming.MusicBrainzTrackID
	}
	if incoming.MusicBrainzWorkID != "" {
		merged.MusicBrainzWorkID = incoming.MusicBrainzWorkID
	}
	if incoming.DiscogsID != "" {
		merged.DiscogsID = incoming.DiscogsID
	}
	if incoming.SpotifyID != "" {
		merged.SpotifyID = incoming.SpotifyID
	}
	if incoming.AppleMusicID != "" {
		merged.AppleMusicID = incoming.AppleMusicID
	}
	if incoming.ISRC != "" {
		merged.ISRC = incoming.ISRC
	}
	if incoming.ISBN10 != "" {
		merged.ISBN10 = incoming.ISBN10
	}
	if incoming.ISBN13 != "" {
		merged.ISBN13 = incoming.ISBN13
	}
	if incoming.ASIN != "" {
		merged.ASIN = incoming.ASIN
	}
	if incoming.AudibleASIN != "" {
		merged.AudibleASIN = incoming.AudibleASIN
	}
	if incoming.GoogleBooksID != "" {
		merged.GoogleBooksID = incoming.GoogleBooksID
	}
	if incoming.OpenLibraryID != "" {
		merged.OpenLibraryID = incoming.OpenLibraryID
	}
	if incoming.ITunesID != "" {
		merged.ITunesID = incoming.ITunesID
	}
	if incoming.FeedGUID != "" {
		merged.FeedGUID = incoming.FeedGUID
	}
	merged.URLs = mergeStringSlicesOverride(current.URLs, incoming.URLs)
	return merged
}

func mergeStringSlicesOverride(current, incoming []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(current)+len(incoming))
	for _, value := range append(current, incoming...) {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}
func decodePatchExternalIDs(patch MetadataOverridePatch, field string) (ExternalIDs, bool) {
	raw, ok := patch[field]
	if !ok {
		return ExternalIDs{}, false
	}
	var value ExternalIDs
	if err := json.Unmarshal(raw, &value); err != nil {
		return ExternalIDs{}, false
	}
	return value, true
}
func decodePatchInt(patch MetadataOverridePatch, field string) (int, bool) {
	raw, ok := patch[field]
	if !ok {
		return 0, false
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, false
	}
	return value, true
}
func decodePatchImages(patch MetadataOverridePatch, field string) ([]Image, bool) {
	raw, ok := patch[field]
	if !ok {
		return nil, false
	}
	var value []Image
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, false
	}
	return value, true
}
func decodePatchBool(patch MetadataOverridePatch, field string) (bool, bool) {
	raw, ok := patch[field]
	if !ok {
		return false, false
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, false
	}
	return value, true
}
func decodePatchTime(patch MetadataOverridePatch, field string) (*time.Time, bool) {
	raw, ok := patch[field]
	if !ok {
		return nil, false
	}
	var value time.Time
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, false
	}
	value = value.UTC()
	return &value, true
}

func decodePatchContributors(patch MetadataOverridePatch, field string) ([]ContributorRef, bool) {
	raw, ok := patch[field]
	if !ok {
		return nil, false
	}
	var value []ContributorRef
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, false
	}
	return value, true
}
func DecodePatchImage(patch MetadataOverridePatch, field string) (*Image, bool) {
	raw, ok := patch[field]
	if !ok {
		return nil, false
	}
	var value Image
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, false
	}
	return &value, true
}
func decodePatchSeries(patch MetadataOverridePatch, field string) ([]SeriesRef, bool) {
	raw, ok := patch[field]
	if !ok {
		return nil, false
	}
	var value []SeriesRef
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, false
	}
	return value, true
}
