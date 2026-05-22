package metadata

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

const openLibraryProviderName = "openlibrary"

type OpenLibraryProvider struct {
	client  *http.Client
	baseURL string
}

func NewOpenLibraryProvider(client *http.Client) *OpenLibraryProvider {
	return &OpenLibraryProvider{
		client:  client,
		baseURL: "https://openlibrary.org/search.json",
	}
}

func (p *OpenLibraryProvider) Name() string {
	return openLibraryProviderName
}

func (p *OpenLibraryProvider) Supports(kind Kind) bool {
	return kind == KindAudiobook
}

func (p *OpenLibraryProvider) Status() ProviderStatus {
	return ProviderStatus{
		Name:    p.Name(),
		Enabled: true,
		Kinds:   []Kind{KindAudiobook},
		Notes:   []string{"Uses Open Library book search for audiobook book metadata candidates."},
	}
}

func (p *OpenLibraryProvider) Search(ctx context.Context, request SearchRequest) ([]SearchResult, error) {
	values := url.Values{}
	values.Set("limit", strconv.Itoa(request.Limit))
	if request.ISBN != "" {
		values.Set("isbn", request.ISBN)
	} else {
		if request.Title != "" {
			values.Set("title", request.Title)
		}
		if request.Author != "" {
			values.Set("author", request.Author)
		}
		if request.Query != "" && request.Title == "" && request.Author == "" {
			values.Set("q", request.Query)
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, withQuery(p.baseURL, values), nil)
	if err != nil {
		return nil, err
	}
	var response openLibrarySearchResponse
	response, err = getJSON[openLibrarySearchResponse](p.client, req)
	if err != nil {
		return nil, err
	}

	results := make([]SearchResult, 0, len(response.Docs))
	for _, doc := range response.Docs {
		id := strings.TrimPrefix(doc.Key, "/works/")
		if id == "" {
			id = first(doc.EditionKey...)
		}
		result := SearchResult{
			ID:            id,
			Provider:      p.Name(),
			MediaType:     "audiobook",
			Score:         scoreOpenLibraryBook(request, doc),
			Title:         doc.Title,
			Subtitle:      doc.Subtitle,
			Authors:       contributors(doc.AuthorName, "author"),
			Publisher:     first(doc.Publisher...),
			PublishedDate: first(doc.PublishDate...),
			PublishedYear: first(openLibraryYear(doc.FirstPublishYear), yearFromDate(first(doc.PublishDate...))),
			Language:      first(doc.Language...),
			Genres:        unique(doc.Subject),
			ExternalIDs: catalog.ExternalIDs{
				ISBN10:        isbnByLength(doc.ISBN, 10),
				ISBN13:        isbnByLength(doc.ISBN, 13),
				OpenLibraryID: id,
			},
			Links: openLibraryLinks(doc.Key),
		}
		if doc.CoverID != 0 {
			result.Cover = &catalog.Image{
				ID:  fmt.Sprint(doc.CoverID),
				URL: fmt.Sprintf("https://covers.openlibrary.org/b/id/%d-L.jpg", doc.CoverID),
			}
		}
		if result.Title != "" {
			results = append(results, result)
		}
	}
	return results, nil
}

func openLibraryLinks(key string) []Link {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	return []Link{{Label: "Open Library", URL: "https://openlibrary.org" + key}}
}

func openLibraryYear(year int) string {
	if year <= 0 {
		return ""
	}
	return strconv.Itoa(year)
}

type openLibrarySearchResponse struct {
	Docs []openLibraryDoc `json:"docs"`
}

type openLibraryDoc struct {
	Key              string   `json:"key"`
	Title            string   `json:"title"`
	Subtitle         string   `json:"subtitle"`
	AuthorName       []string `json:"author_name"`
	FirstPublishYear int      `json:"first_publish_year"`
	PublishDate      []string `json:"publish_date"`
	Publisher        []string `json:"publisher"`
	Language         []string `json:"language"`
	Subject          []string `json:"subject"`
	ISBN             []string `json:"isbn"`
	EditionKey       []string `json:"edition_key"`
	CoverID          int      `json:"cover_i"`
}

func scoreOpenLibraryBook(request SearchRequest, doc openLibraryDoc) int {
	score := 70
	title := strings.ToLower(doc.Title)
	if request.Title != "" && strings.Contains(title, strings.ToLower(request.Title)) {
		score += 15
	}
	if request.Author != "" {
		needle := strings.ToLower(request.Author)
		for _, author := range doc.AuthorName {
			if strings.Contains(strings.ToLower(author), needle) {
				score += 10
				break
			}
		}
	}
	if request.ISBN != "" {
		for _, isbn := range doc.ISBN {
			if strings.EqualFold(cleanISBN(isbn), cleanISBN(request.ISBN)) {
				score = 100
				break
			}
		}
	}
	if score > 100 {
		return 100
	}
	return score
}

func isbnByLength(values []string, length int) string {
	for _, value := range values {
		cleaned := cleanISBN(value)
		if len(cleaned) == length {
			return cleaned
		}
	}
	return ""
}

func cleanISBN(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	var builder strings.Builder
	for _, r := range value {
		if r >= '0' && r <= '9' || r == 'X' {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}
