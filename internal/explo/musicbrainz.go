package explo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// musicbrainzRecordingURL is the MusicBrainz recording-lookup base. A package
// var so tests can point it at a stub server.
var musicbrainzRecordingURL = "https://musicbrainz.org/ws/2/recording/"

// musicbrainzUserAgent identifies Samo to MusicBrainz, which requires a
// descriptive User-Agent for unauthenticated clients.
const musicbrainzUserAgent = "SamoServer/0.1 ( https://github.com/bouliehaan/samo-server )"

// fetchReleaseGroupID resolves a MusicBrainz recording MBID to the MBID of its
// most album-like release group, so the caller can build a Cover Art Archive
// URL. Returns "" with no error when the recording has no usable release group
// (a definitive "no cover to find here"); a non-nil error signals a transient
// failure the caller should retry later rather than mark the album resolved.
func fetchReleaseGroupID(ctx context.Context, client *http.Client, recordingMBID string) (string, error) {
	id := strings.TrimSpace(recordingMBID)
	if id == "" {
		return "", nil
	}
	url := musicbrainzRecordingURL + id + "?inc=releases+release-groups&fmt=json"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", musicbrainzUserAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("musicbrainz recording lookup %s: status %d", id, resp.StatusCode)
	}
	var body struct {
		Releases []struct {
			ReleaseGroup struct {
				ID          string `json:"id"`
				PrimaryType string `json:"primary-type"`
			} `json:"release-group"`
		} `json:"releases"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	// Prefer an Album-type release group; otherwise take the first with an id.
	for _, r := range body.Releases {
		if strings.EqualFold(r.ReleaseGroup.PrimaryType, "Album") && strings.TrimSpace(r.ReleaseGroup.ID) != "" {
			return strings.TrimSpace(r.ReleaseGroup.ID), nil
		}
	}
	for _, r := range body.Releases {
		if strings.TrimSpace(r.ReleaseGroup.ID) != "" {
			return strings.TrimSpace(r.ReleaseGroup.ID), nil
		}
	}
	return "", nil
}
