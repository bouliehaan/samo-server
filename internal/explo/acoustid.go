package explo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// acoustidLookupURL is a var (not const) so tests can point it at an
// httptest server instead of the real AcoustID API.
var acoustidLookupURL = "https://api.acoustid.org/v2/lookup"

// identifiedTrack is a candidate identification for one explo file, from
// whichever method produced it (AcoustID fingerprint match, or the
// filename+duration-gated MusicBrainz text-search fallback) - enough to
// drive a metadata apply, nothing more.
type identifiedTrack struct {
	Source                 string // "acoustid" | "musicbrainz-search"
	AcoustID               string // empty for the text-search fallback
	Score                  float64
	MusicBrainzRecordingID string
	// MusicBrainzReleaseGroupID, when set, lets us fetch album art from the
	// Cover Art Archive so identified explo albums aren't blank tiles.
	MusicBrainzReleaseGroupID string
	Title                     string
	Artist                    string
	Album                     string
}

type acoustidResponse struct {
	Status string `json:"status"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
	Results []acoustidResult `json:"results"`
}

type acoustidResult struct {
	ID         string              `json:"id"`
	Score      float64             `json:"score"`
	Recordings []acoustidRecording `json:"recordings"`
}

type acoustidRecording struct {
	ID            string               `json:"id"`
	Title         string               `json:"title"`
	Artists       []acoustidArtist     `json:"artists"`
	ReleaseGroups []acoustidReleaseGrp `json:"releasegroups"`
}

type acoustidArtist struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type acoustidReleaseGrp struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Type  string `json:"type"`
}

// lookupAcoustID identifies a fingerprint against the AcoustID/MusicBrainz
// database. Returns ok=false (no error) when the lookup succeeded but found
// nothing usable - callers should record that as "unmatched", not retry.
func lookupAcoustID(ctx context.Context, httpClient *http.Client, apiKey string, fp Fingerprint) (identifiedTrack, bool, error) {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	values := url.Values{
		"client":      {apiKey},
		"duration":    {strconv.Itoa(fp.DurationSeconds)},
		"fingerprint": {fp.Value},
		"meta":        {"recordings+releasegroups"},
		"format":      {"json"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, acoustidLookupURL+"?"+values.Encode(), nil)
	if err != nil {
		return identifiedTrack{}, false, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return identifiedTrack{}, false, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return identifiedTrack{}, false, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return identifiedTrack{}, false, fmt.Errorf("acoustid http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload acoustidResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return identifiedTrack{}, false, fmt.Errorf("decode acoustid response: %w", err)
	}
	if payload.Status != "ok" {
		message := "unknown error"
		if payload.Error != nil && strings.TrimSpace(payload.Error.Message) != "" {
			message = payload.Error.Message
		}
		return identifiedTrack{}, false, fmt.Errorf("acoustid: %s", message)
	}

	best, bestRecording, ok := bestAcoustIDResult(payload.Results)
	if !ok {
		return identifiedTrack{}, false, nil
	}

	match := identifiedTrack{
		Source:                 "acoustid",
		AcoustID:               best.ID,
		Score:                  best.Score,
		MusicBrainzRecordingID: bestRecording.ID,
		Title:                  strings.TrimSpace(bestRecording.Title),
	}
	names := make([]string, 0, len(bestRecording.Artists))
	for _, artist := range bestRecording.Artists {
		if name := strings.TrimSpace(artist.Name); name != "" {
			names = append(names, name)
		}
	}
	match.Artist = strings.Join(names, ", ")
	match.MusicBrainzReleaseGroupID, match.Album = bestReleaseGroup(bestRecording.ReleaseGroups)

	if match.Title == "" || match.Artist == "" {
		return identifiedTrack{}, false, nil
	}
	return match, true, nil
}

// bestAcoustIDResult picks the highest-score result that has at least one
// recording with a usable title+artist, and returns that recording too.
func bestAcoustIDResult(results []acoustidResult) (acoustidResult, acoustidRecording, bool) {
	var bestResult acoustidResult
	var bestRecording acoustidRecording
	found := false
	for _, result := range results {
		recording, ok := bestRecordingIn(result.Recordings)
		if !ok {
			continue
		}
		if !found || result.Score > bestResult.Score {
			bestResult = result
			bestRecording = recording
			found = true
		}
	}
	return bestResult, bestRecording, found
}

func bestRecordingIn(recordings []acoustidRecording) (acoustidRecording, bool) {
	for _, recording := range recordings {
		if strings.TrimSpace(recording.Title) != "" && len(recording.Artists) > 0 {
			return recording, true
		}
	}
	return acoustidRecording{}, false
}

// bestReleaseGroup returns the MBID and title of the most album-like release
// group (preferring an "Album" type), so the caller can both name the album and
// fetch its cover art. Either value may be empty.
func bestReleaseGroup(groups []acoustidReleaseGrp) (id, title string) {
	for _, group := range groups {
		if strings.EqualFold(group.Type, "Album") && strings.TrimSpace(group.Title) != "" {
			return strings.TrimSpace(group.ID), strings.TrimSpace(group.Title)
		}
	}
	for _, group := range groups {
		if strings.TrimSpace(group.Title) != "" {
			return strings.TrimSpace(group.ID), strings.TrimSpace(group.Title)
		}
	}
	return "", ""
}
