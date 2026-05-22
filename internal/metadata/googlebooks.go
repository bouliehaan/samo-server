package metadata

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

const googleBooksProviderName = "googlebooks"

type GoogleBooksProvider struct {
	client  *http.Client
	baseURL string
}

func NewGoogleBooksProvider(client *http.Client) *GoogleBooksProvider {
	return &GoogleBooksProvider{
		client:  client,
		baseURL: "https://www.googleapis.com/books/v1/volumes",
	}
}

func (p *GoogleBooksProvider) Name() string {
	return googleBooksProviderName
}

func (p *GoogleBooksProvider) Supports(kind Kind) bool {
	return kind == KindAudiobook
}

func (p *GoogleBooksProvider) Status() ProviderStatus {
	return ProviderStatus{
		Name:    p.Name(),
		Enabled: true,
		Kinds:   []Kind{KindAudiobook},
		Notes:   []string{"Uses Google Books volume search for audiobook book metadata candidates."},
	}
}

func (p *GoogleBooksProvider) Search(ctx context.Context, request SearchRequest) ([]SearchResult, error) {
	values := url.Values{}
	values.Set("q", googleBooksQuery(request))
	values.Set("maxResults", strconv.Itoa(request.Limit))
	values.Set("printType", "books")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, withQuery(p.baseURL, values), nil)
	if err != nil {
		return nil, err
	}
	response, err := getJSON[googleBooksSearchResponse](p.client, req)
	if err != nil {
		return nil, err
	}

	results := make([]SearchResult, 0, len(response.Items))
	for index, item := range response.Items {
		info := item.VolumeInfo
		result := SearchResult{
			ID:            item.ID,
			Provider:      p.Name(),
			MediaType:     "audiobook",
			Score:         100 - index,
			Title:         info.Title,
			Subtitle:      info.Subtitle,
			Description:   info.Description,
			Authors:       contributors(info.Authors, "author"),
			Publisher:     info.Publisher,
			PublishedDate: info.PublishedDate,
			PublishedYear: yearFromDate(info.PublishedDate),
			Language:      info.Language,
			Genres:        unique(info.Categories),
			ExternalIDs: catalog.ExternalIDs{
				GoogleBooksID: item.ID,
				ISBN10:        googleISBN(info.IndustryIdentifiers, "ISBN_10"),
				ISBN13:        googleISBN(info.IndustryIdentifiers, "ISBN_13"),
			},
			Links: googleBooksLinks(info),
		}
		if coverURL := first(info.ImageLinks.ExtraLarge, info.ImageLinks.Large, info.ImageLinks.Medium, info.ImageLinks.Small, info.ImageLinks.Thumbnail, info.ImageLinks.SmallThumbnail); coverURL != "" {
			result.Cover = &catalog.Image{URL: coverURL}
		}
		if result.Title != "" {
			results = append(results, result)
		}
	}
	return results, nil
}

type googleBooksSearchResponse struct {
	Items []googleVolume `json:"items"`
}

type googleVolume struct {
	ID         string           `json:"id"`
	VolumeInfo googleVolumeInfo `json:"volumeInfo"`
}

type googleVolumeInfo struct {
	Title               string                     `json:"title"`
	Subtitle            string                     `json:"subtitle"`
	Authors             []string                   `json:"authors"`
	Publisher           string                     `json:"publisher"`
	PublishedDate       string                     `json:"publishedDate"`
	Description         string                     `json:"description"`
	IndustryIdentifiers []googleIndustryIdentifier `json:"industryIdentifiers"`
	Categories          []string                   `json:"categories"`
	Language            string                     `json:"language"`
	ImageLinks          googleImageLinks           `json:"imageLinks"`
	InfoLink            string                     `json:"infoLink"`
	CanonicalVolumeLink string                     `json:"canonicalVolumeLink"`
}

type googleIndustryIdentifier struct {
	Type       string `json:"type"`
	Identifier string `json:"identifier"`
}

type googleImageLinks struct {
	SmallThumbnail string `json:"smallThumbnail"`
	Thumbnail      string `json:"thumbnail"`
	Small          string `json:"small"`
	Medium         string `json:"medium"`
	Large          string `json:"large"`
	ExtraLarge     string `json:"extraLarge"`
}

func googleBooksQuery(request SearchRequest) string {
	if request.ISBN != "" {
		return "isbn:" + request.ISBN
	}
	var parts []string
	if request.Title != "" {
		parts = append(parts, "intitle:"+request.Title)
	}
	if request.Author != "" {
		parts = append(parts, "inauthor:"+request.Author)
	}
	if len(parts) == 0 {
		return request.Query
	}
	return strings.Join(parts, " ")
}

func googleISBN(ids []googleIndustryIdentifier, idType string) string {
	for _, id := range ids {
		if id.Type == idType {
			return cleanISBN(id.Identifier)
		}
	}
	return ""
}

func googleBooksLinks(info googleVolumeInfo) []Link {
	var links []Link
	if info.InfoLink != "" {
		links = append(links, Link{Label: "Google Books", URL: info.InfoLink})
	}
	if info.CanonicalVolumeLink != "" && info.CanonicalVolumeLink != info.InfoLink {
		links = append(links, Link{Label: "Canonical Volume", URL: info.CanonicalVolumeLink})
	}
	return links
}
