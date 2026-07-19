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
	ReleaseIDs     []string
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
