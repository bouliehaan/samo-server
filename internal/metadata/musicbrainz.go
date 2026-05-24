package metadata

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

const musicBrainzProviderName = "musicbrainz"

type MusicBrainzProvider struct {
	client    *http.Client
	baseURL   string
	userAgent string
}

func NewMusicBrainzProvider(client *http.Client, userAgent string) *MusicBrainzProvider {
	return &MusicBrainzProvider{
		client:    client,
		baseURL:   "https://musicbrainz.org/ws/2",
		userAgent: userAgent,
	}
}

func (p *MusicBrainzProvider) Name() string {
	return musicBrainzProviderName
}

func (p *MusicBrainzProvider) Supports(kind Kind) bool {
	return kind == KindMusic
}

func (p *MusicBrainzProvider) Status() ProviderStatus {
	return ProviderStatus{
		Name:    p.Name(),
		Enabled: true,
		Kinds:   []Kind{KindMusic},
		Notes:   []string{"Uses MusicBrainz search for artist, release-group, and recording metadata candidates."},
	}
}

func (p *MusicBrainzProvider) Search(ctx context.Context, request SearchRequest) ([]SearchResult, error) {
	searchType := musicSearchType(request)
	values := url.Values{}
	values.Set("fmt", "json")
	values.Set("limit", strconv.Itoa(request.Limit))
	values.Set("query", musicBrainzQuery(searchType, request))

	endpoint := "recording"
	switch searchType {
	case MusicSearchArtist:
		endpoint = "artist"
	case MusicSearchAlbum:
		endpoint = "release-group"
	case MusicSearchTrack:
		endpoint = "recording"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, withQuery(p.baseURL+"/"+endpoint, values), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", p.userAgent)

	switch searchType {
	case MusicSearchArtist:
		response, err := getJSON[musicBrainzArtistResponse](p.client, req)
		if err != nil {
			return nil, err
		}
		return p.artistResults(response.Artists), nil
	case MusicSearchAlbum:
		response, err := getJSON[musicBrainzReleaseGroupResponse](p.client, req)
		if err != nil {
			return nil, err
		}
		return p.releaseGroupResults(response.ReleaseGroups), nil
	default:
		response, err := getJSON[musicBrainzRecordingResponse](p.client, req)
		if err != nil {
			return nil, err
		}
		return p.recordingResults(response.Recordings), nil
	}
}

func (p *MusicBrainzProvider) artistResults(items []musicBrainzArtist) []SearchResult {
	results := make([]SearchResult, 0, len(items))
	for _, item := range items {
		result := SearchResult{
			ID:          item.ID,
			Provider:    p.Name(),
			MediaType:   "musicArtist",
			Score:       scoreFromString(item.Score),
			Title:       item.Name,
			SortTitle:   item.SortName,
			Description: item.Disambiguation,
			Tags:        tagNames(item.Tags),
			ExternalIDs: catalog.ExternalIDs{MusicBrainzArtistID: item.ID},
			Links:       []Link{{Label: "MusicBrainz Artist", URL: "https://musicbrainz.org/artist/" + item.ID}},
		}
		if item.Country != "" {
			result.Raw = map[string]any{"country": item.Country, "type": item.Type}
		}
		if result.Title != "" {
			results = append(results, result)
		}
	}
	return results
}

func (p *MusicBrainzProvider) releaseGroupResults(items []musicBrainzReleaseGroup) []SearchResult {
	results := make([]SearchResult, 0, len(items))
	for _, item := range items {
		result := SearchResult{
			ID:            item.ID,
			Provider:      p.Name(),
			MediaType:     "musicAlbum",
			Score:         scoreFromString(item.Score),
			Title:         item.Title,
			Authors:       musicBrainzContributors(item.ArtistCredit),
			PublishedDate: item.FirstReleaseDate,
			PublishedYear: yearFromDate(item.FirstReleaseDate),
			Genres:        tagNames(item.Tags),
			Tags:          unique(append([]string{item.PrimaryType}, item.SecondaryTypes...)),
			ExternalIDs:   catalog.ExternalIDs{MusicBrainzReleaseGroupID: item.ID},
			Links:         []Link{{Label: "MusicBrainz Release Group", URL: "https://musicbrainz.org/release-group/" + item.ID}},
		}
		if result.Title != "" {
			results = append(results, result)
		}
	}
	return results
}

func (p *MusicBrainzProvider) recordingResults(items []musicBrainzRecording) []SearchResult {
	results := make([]SearchResult, 0, len(items))
	for _, item := range items {
		result := SearchResult{
			ID:              item.ID,
			Provider:        p.Name(),
			MediaType:       "musicTrack",
			Score:           scoreFromString(item.Score),
			Title:           item.Title,
			Authors:         musicBrainzContributors(item.ArtistCredit),
			PublishedDate:   item.FirstReleaseDate,
			PublishedYear:   yearFromDate(item.FirstReleaseDate),
			DurationSeconds: msToSeconds(item.Length),
			Genres:          tagNames(item.Tags),
			ExternalIDs:     catalog.ExternalIDs{MusicBrainzRecordingID: item.ID},
			Links:           []Link{{Label: "MusicBrainz Recording", URL: "https://musicbrainz.org/recording/" + item.ID}},
		}
		if len(item.Releases) > 0 {
			release := item.Releases[0]
			result.Raw = map[string]any{
				"releaseId":    release.ID,
				"releaseTitle": release.Title,
				"releaseDate":  release.Date,
			}
			if release.ReleaseGroup.ID != "" {
				result.ExternalIDs.MusicBrainzReleaseGroupID = release.ReleaseGroup.ID
			}
			if release.ID != "" {
				result.ExternalIDs.MusicBrainzReleaseID = release.ID
			}
		}
		if result.Title != "" {
			results = append(results, result)
		}
	}
	return results
}

type musicBrainzArtistResponse struct {
	Artists []musicBrainzArtist `json:"artists"`
}

type musicBrainzReleaseGroupResponse struct {
	ReleaseGroups []musicBrainzReleaseGroup `json:"release-groups"`
}

type musicBrainzRecordingResponse struct {
	Recordings []musicBrainzRecording `json:"recordings"`
}

type musicBrainzArtist struct {
	ID             string           `json:"id"`
	Score          string           `json:"score"`
	Name           string           `json:"name"`
	SortName       string           `json:"sort-name"`
	Disambiguation string           `json:"disambiguation"`
	Country        string           `json:"country"`
	Type           string           `json:"type"`
	Tags           []musicBrainzTag `json:"tags"`
}

type musicBrainzReleaseGroup struct {
	ID               string                    `json:"id"`
	Score            string                    `json:"score"`
	Title            string                    `json:"title"`
	FirstReleaseDate string                    `json:"first-release-date"`
	PrimaryType      string                    `json:"primary-type"`
	SecondaryTypes   []string                  `json:"secondary-types"`
	ArtistCredit     []musicBrainzArtistCredit `json:"artist-credit"`
	Tags             []musicBrainzTag          `json:"tags"`
}

type musicBrainzRecording struct {
	ID               string                    `json:"id"`
	Score            string                    `json:"score"`
	Title            string                    `json:"title"`
	Length           int                       `json:"length"`
	FirstReleaseDate string                    `json:"first-release-date"`
	ArtistCredit     []musicBrainzArtistCredit `json:"artist-credit"`
	Tags             []musicBrainzTag          `json:"tags"`
	Releases         []musicBrainzRelease      `json:"releases"`
}

type musicBrainzArtistCredit struct {
	Name   string            `json:"name"`
	Artist musicBrainzArtist `json:"artist"`
}

type musicBrainzRelease struct {
	ID           string                  `json:"id"`
	Title        string                  `json:"title"`
	Date         string                  `json:"date"`
	ReleaseGroup musicBrainzReleaseGroup `json:"release-group"`
}

type musicBrainzTag struct {
	Name string `json:"name"`
}

func musicSearchType(request SearchRequest) MusicSearchType {
	if request.MusicType != "" {
		return request.MusicType
	}
	switch {
	case request.Track != "":
		return MusicSearchTrack
	case request.Album != "":
		return MusicSearchAlbum
	case request.Artist != "":
		return MusicSearchArtist
	default:
		return MusicSearchTrack
	}
}

func musicBrainzQuery(searchType MusicSearchType, request SearchRequest) string {
	var parts []string
	switch searchType {
	case MusicSearchArtist:
		if request.Artist != "" {
			parts = append(parts, luceneField("artist", request.Artist))
		}
	case MusicSearchAlbum:
		if request.Album != "" {
			parts = append(parts, luceneField("releasegroup", request.Album))
		}
		if request.Artist != "" {
			parts = append(parts, luceneField("artist", request.Artist))
		}
	case MusicSearchTrack:
		if request.Track != "" {
			parts = append(parts, luceneField("recording", request.Track))
		}
		if request.Artist != "" {
			parts = append(parts, luceneField("artist", request.Artist))
		}
		if request.Album != "" {
			parts = append(parts, luceneField("release", request.Album))
		}
	}
	if len(parts) == 0 {
		return request.Query
	}
	return strings.Join(parts, " AND ")
}

func luceneField(field string, value string) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return field + `:"` + value + `"`
}

func musicBrainzContributors(credits []musicBrainzArtistCredit) []catalog.ContributorRef {
	contributors := make([]catalog.ContributorRef, 0, len(credits))
	for _, credit := range credits {
		name := first(credit.Name, credit.Artist.Name)
		if name == "" {
			continue
		}
		contributors = append(contributors, catalog.ContributorRef{
			ID:       credit.Artist.ID,
			Name:     name,
			SortName: credit.Artist.SortName,
			Role:     "artist",
		})
	}
	return contributors
}

func tagNames(tags []musicBrainzTag) []string {
	names := make([]string, 0, len(tags))
	for _, tag := range tags {
		names = append(names, tag.Name)
	}
	return unique(names)
}
