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

// recordingReleaseRefs is what one MusicBrainz recording lookup yields for
// cover resolution: the most album-like release group, plus the individual
// release MBIDs (Cover Art Archive frequently has art on a specific release
// when the release group itself has none).
type recordingReleaseRefs struct {
	ReleaseGroupID string
	// ReleaseGroupTitle is the canonical name of the record this recording
	// belongs to. It is the ONLY trustworthy album name for an explo drop:
	// the file's own tag was written by whoever shared it (typically a rip of
	// a hits compilation), and when there is no tag at all the scanner falls
	// back to the drop folder's name. Both routinely reached disk as the
	// album of a kept track.
	ReleaseGroupTitle string
	ReleaseIDs        []string
}

// fetchRecordingReleaseRefs resolves a MusicBrainz recording MBID to its
// release group and release MBIDs, so the caller can build Cover Art Archive
// URLs. Returns empty refs with no error when the recording has no usable
// releases (a definitive "no cover to find here"); a non-nil error signals a
// transient failure the caller should retry later rather than mark resolved.
func fetchRecordingReleaseRefs(ctx context.Context, client *http.Client, recordingMBID string) (recordingReleaseRefs, error) {
	id := strings.TrimSpace(recordingMBID)
	if id == "" {
		return recordingReleaseRefs{}, nil
	}
	url := musicbrainzRecordingURL + id + "?inc=releases+release-groups&fmt=json"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return recordingReleaseRefs{}, err
	}
	req.Header.Set("User-Agent", musicbrainzUserAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return recordingReleaseRefs{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return recordingReleaseRefs{}, fmt.Errorf("musicbrainz recording lookup %s: status %d", id, resp.StatusCode)
	}
	var body struct {
		Releases []struct {
			ID           string `json:"id"`
			ReleaseGroup struct {
				ID             string   `json:"id"`
				Title          string   `json:"title"`
				PrimaryType    string   `json:"primary-type"`
				SecondaryTypes []string `json:"secondary-types"`
			} `json:"release-group"`
		} `json:"releases"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return recordingReleaseRefs{}, err
	}

	// Pick the release group by the same anti-compilation ranking the
	// AcoustID path uses: a hit song's recording sits on dozens of
	// compilations, and "first Album-type" used to pick whichever disco
	// sampler happened to be listed first.
	refs := recordingReleaseRefs{}
	bestRank := len(releaseGroupRankOrder) + 2
	for _, r := range body.Releases {
		rgID := strings.TrimSpace(r.ReleaseGroup.ID)
		if rgID == "" {
			continue
		}
		rank := releaseGroupRank(r.ReleaseGroup.PrimaryType, r.ReleaseGroup.SecondaryTypes)
		if rank < bestRank {
			bestRank = rank
			refs.ReleaseGroupID = rgID
			refs.ReleaseGroupTitle = strings.TrimSpace(r.ReleaseGroup.Title)
		}
	}
	// Order candidate releases so the chosen release group's own releases
	// come first: the per-release CAA rung should try the real record's
	// pressings before any compilation appearance.
	for _, r := range body.Releases {
		if strings.TrimSpace(r.ID) != "" && strings.TrimSpace(r.ReleaseGroup.ID) == refs.ReleaseGroupID {
			refs.ReleaseIDs = append(refs.ReleaseIDs, strings.TrimSpace(r.ID))
		}
	}
	for _, r := range body.Releases {
		if strings.TrimSpace(r.ID) != "" && strings.TrimSpace(r.ReleaseGroup.ID) != refs.ReleaseGroupID {
			refs.ReleaseIDs = append(refs.ReleaseIDs, strings.TrimSpace(r.ID))
		}
	}
	return refs, nil
}

// musicbrainzReleaseGroupURL is the MusicBrainz release-group lookup base. A
// package var so tests can point it at a stub server.
var musicbrainzReleaseGroupURL = "https://musicbrainz.org/ws/2/release-group/"

// fetchReleaseGroupTitle resolves a release-group MBID to its title.
//
// The ledger already stores the release group chosen at identification time,
// so this is the cheap path when only the NAME is missing — which is the
// common case for rows identified before album titles were resolved. Returns
// "" with no error when MusicBrainz has no title for the id; a non-nil error
// is transient and the caller should keep the existing name rather than
// treat the album as unnamed.
func fetchReleaseGroupTitle(ctx context.Context, client *http.Client, releaseGroupMBID string) (string, error) {
	id := strings.TrimSpace(releaseGroupMBID)
	if id == "" {
		return "", nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, musicbrainzReleaseGroupURL+id+"?fmt=json", nil)
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
		return "", fmt.Errorf("musicbrainz release-group lookup %s: status %d", id, resp.StatusCode)
	}
	var body struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	return strings.TrimSpace(body.Title), nil
}
