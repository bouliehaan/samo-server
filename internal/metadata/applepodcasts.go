package metadata

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

const applePodcastProviderName = "itunes"

type ApplePodcastProvider struct {
	client  *http.Client
	baseURL string
}

func NewApplePodcastProvider(client *http.Client) *ApplePodcastProvider {
	return &ApplePodcastProvider{
		client:  client,
		baseURL: "https://itunes.apple.com/search",
	}
}

func (p *ApplePodcastProvider) Name() string {
	return applePodcastProviderName
}

func (p *ApplePodcastProvider) Supports(kind Kind) bool {
	return kind == KindPodcast
}

func (p *ApplePodcastProvider) Status() ProviderStatus {
	return ProviderStatus{
		Name:    p.Name(),
		Enabled: true,
		Kinds:   []Kind{KindPodcast},
		Notes:   []string{"Uses Apple's iTunes Search API for podcast directory metadata candidates."},
	}
}

func (p *ApplePodcastProvider) Search(ctx context.Context, request SearchRequest) ([]SearchResult, error) {
	values := url.Values{}
	values.Set("media", "podcast")
	values.Set("entity", "podcast")
	values.Set("term", first(request.Title, request.Query))
	values.Set("limit", strconv.Itoa(request.Limit))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, withQuery(p.baseURL, values), nil)
	if err != nil {
		return nil, err
	}
	response, err := getJSON[applePodcastSearchResponse](p.client, req)
	if err != nil {
		return nil, err
	}

	results := make([]SearchResult, 0, len(response.Results))
	for index, item := range response.Results {
		title := first(item.CollectionName, item.TrackName)
		result := SearchResult{
			ID:          strconv.FormatInt(item.CollectionID, 10),
			Provider:    p.Name(),
			MediaType:   "podcast",
			Score:       100 - index,
			Title:       title,
			Authors:     contributors([]string{item.ArtistName}, "author"),
			Genres:      unique(append(item.Genres, item.PrimaryGenreName)),
			Description: item.Description,
			ExternalIDs: catalog.ExternalIDs{
				ITunesID: strconv.FormatInt(item.CollectionID, 10),
				URLs:     unique([]string{item.FeedURL, item.TrackViewURL, item.CollectionViewURL}),
			},
			Links: applePodcastLinks(item),
		}
		if item.ArtworkURL600 != "" {
			result.Cover = &catalog.Image{URL: item.ArtworkURL600}
		} else if item.ArtworkURL100 != "" {
			result.Cover = &catalog.Image{URL: item.ArtworkURL100}
		}
		if item.Country != "" {
			result.Raw = map[string]any{"country": item.Country}
		}
		if publishedDate := parseAppleDate(item.ReleaseDate); publishedDate != "" {
			result.PublishedDate = publishedDate
			result.PublishedYear = yearFromDate(publishedDate)
		}
		if result.Title != "" {
			results = append(results, result)
		}
	}
	return results, nil
}

type applePodcastSearchResponse struct {
	Results []applePodcastResult `json:"results"`
}

type applePodcastResult struct {
	CollectionID      int64    `json:"collectionId"`
	CollectionName    string   `json:"collectionName"`
	CollectionViewURL string   `json:"collectionViewUrl"`
	TrackName         string   `json:"trackName"`
	TrackViewURL      string   `json:"trackViewUrl"`
	ArtistName        string   `json:"artistName"`
	FeedURL           string   `json:"feedUrl"`
	ArtworkURL100     string   `json:"artworkUrl100"`
	ArtworkURL600     string   `json:"artworkUrl600"`
	PrimaryGenreName  string   `json:"primaryGenreName"`
	Genres            []string `json:"genres"`
	Country           string   `json:"country"`
	ReleaseDate       string   `json:"releaseDate"`
	Description       string   `json:"description"`
}

func applePodcastLinks(item applePodcastResult) []Link {
	var links []Link
	if item.TrackViewURL != "" {
		links = append(links, Link{Label: "Apple Podcasts", URL: item.TrackViewURL})
	}
	if item.CollectionViewURL != "" && item.CollectionViewURL != item.TrackViewURL {
		links = append(links, Link{Label: "iTunes Collection", URL: item.CollectionViewURL})
	}
	if item.FeedURL != "" {
		links = append(links, Link{Label: "RSS Feed", URL: item.FeedURL})
	}
	return links
}

func parseAppleDate(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err == nil {
		return parsed.UTC().Format("2006-01-02")
	}
	return value
}
