package metadata

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

const audibleProviderName = "audible"

var asinPattern = regexp.MustCompile(`^B[0-9A-Z]{9}$`)

var audibleRegionTLD = map[string]string{
	"us": ".com",
	"ca": ".ca",
	"uk": ".co.uk",
	"au": ".com.au",
	"fr": ".fr",
	"de": ".de",
	"jp": ".co.jp",
	"it": ".it",
	"in": ".in",
	"es": ".es",
}

type AudibleProvider struct {
	client             *http.Client
	audnexusURL        string
	catalogBaseURL     string
	catalogProductsURL string
	region             string
}

func NewAudibleProvider(client *http.Client) *AudibleProvider {
	// Callers (e.g. the scan subprocess wiring) pass nil to mean "use a
	// default client". Without this guard every Audnexus request did
	// `nil.Do(req)` and panicked the whole scan the moment an audiobook needed
	// the chapter fallback.
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &AudibleProvider{
		client:         client,
		audnexusURL:    "https://api.audnex.us",
		catalogBaseURL: "https://api.audible",
		region:         "us",
	}
}

// withRegion overrides the Audible/Audnexus region used for catalog search and
// chapter lookups (default "us"). An empty value is ignored so callers can pass
// an unset config field straight through.
func (p *AudibleProvider) withRegion(region string) *AudibleProvider {
	if r := strings.ToLower(strings.TrimSpace(region)); r != "" {
		p.region = r
	}
	return p
}

func (p *AudibleProvider) Name() string {
	return audibleProviderName
}

func (p *AudibleProvider) Supports(kind Kind) bool {
	return kind == KindAudiobook
}

func (p *AudibleProvider) Status() ProviderStatus {
	return ProviderStatus{
		Name:    p.Name(),
		Enabled: true,
		Kinds:   []Kind{KindAudiobook},
		Notes: []string{
			"Searches Audible catalog by title/author, then loads square cover art and audiobook metadata from Audnexus.",
			"Direct ASIN lookup when audibleAsin/asin is provided or embedded in tags.",
		},
	}
}

func (p *AudibleProvider) Search(ctx context.Context, request SearchRequest) ([]SearchResult, error) {
	request = prepareSearchRequest(request)
	limit := request.Limit
	if limit <= 0 {
		limit = defaultLimit
	}

	for _, asin := range audibleLookupASINs(request) {
		book, err := p.fetchBook(ctx, asin)
		if err != nil {
			continue
		}
		return []SearchResult{p.mapBook(book, 100)}, nil
	}

	title := strings.TrimSpace(firstNonEmpty(request.Title, request.Query))
	if title == "" {
		return nil, nil
	}

	asins, err := p.searchCatalog(ctx, title, request.Author, limit)
	if err != nil {
		return nil, err
	}

	results := make([]SearchResult, 0, len(asins))
	for index, asin := range asins {
		book, err := p.fetchBook(ctx, asin)
		if err != nil {
			continue
		}
		results = append(results, p.mapBook(book, audibleSearchScore(book, title, request.Author, index)))
	}
	return results, nil
}

// audibleSearchScore ranks a catalog hit by how well it actually matches the
// requested title/author rather than by Audible's raw return order — which is
// all the old `100-index` conveyed, letting whatever the marketplace listed
// first win. It reuses the verified-match similarity helpers; because their
// tokenizer keeps only [a-z0-9] runs, a foreign-script edition (e.g. the Russian
// "Дыхание" surfaced for an English "Breath" search) tokenizes to nothing and
// scores ~0, so the matching English edition is no longer buried beneath a
// localized one the catalog happened to relevance-rank first. Audible's own
// ordering is kept only as a gentle tiebreaker between equally good matches.
func audibleSearchScore(book audnexusBook, title, author string, order int) int {
	titleSim := titleSimilarity(book.Title, title)
	quality := titleSim
	if authorSim, haveAuthor := authorSimilarity(book.Authors, author); haveAuthor {
		quality = 0.75*titleSim + 0.25*authorSim
	}
	return int(math.Round(quality*1000)) - order
}

func audibleLookupASINs(request SearchRequest) []string {
	candidates := []string{
		strings.ToUpper(strings.TrimSpace(request.AudibleASIN)),
		strings.ToUpper(strings.TrimSpace(request.ASIN)),
		strings.ToUpper(strings.TrimSpace(request.Title)),
		strings.ToUpper(strings.TrimSpace(request.Query)),
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(candidates))
	for _, asin := range candidates {
		if !isValidASIN(asin) {
			continue
		}
		if _, ok := seen[asin]; ok {
			continue
		}
		seen[asin] = struct{}{}
		out = append(out, asin)
	}
	return out
}

func isValidASIN(value string) bool {
	return asinPattern.MatchString(strings.ToUpper(strings.TrimSpace(value)))
}

func (p *AudibleProvider) searchCatalog(ctx context.Context, title, author string, limit int) ([]string, error) {
	values := url.Values{}
	values.Set("num_results", strconv.Itoa(limit))
	values.Set("products_sort_by", "Relevance")
	values.Set("title", title)
	if strings.TrimSpace(author) != "" {
		values.Set("author", strings.TrimSpace(author))
	}

	catalogURL := p.catalogURL()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, withQuery(catalogURL, values), nil)
	if err != nil {
		return nil, err
	}
	response, err := getJSON[audibleCatalogResponse](p.client, req)
	if err != nil {
		return nil, err
	}

	asins := make([]string, 0, len(response.Products))
	for _, product := range response.Products {
		asin := strings.ToUpper(strings.TrimSpace(product.ASIN))
		if isValidASIN(asin) {
			asins = append(asins, asin)
		}
	}
	return asins, nil
}

func (p *AudibleProvider) fetchBook(ctx context.Context, asin string) (audnexusBook, error) {
	values := url.Values{}
	values.Set("region", p.region)
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		withQuery(strings.TrimRight(p.audnexusURL, "/")+"/books/"+url.PathEscape(asin), values),
		nil,
	)
	if err != nil {
		return audnexusBook{}, err
	}
	book, status, err := getJSONOptional[audnexusBook](p.client, req)
	if err != nil {
		return audnexusBook{}, err
	}
	if status == http.StatusNotFound || strings.TrimSpace(book.ASIN) == "" {
		return audnexusBook{}, fmt.Errorf("book not found")
	}
	if status < 200 || status >= 300 {
		return audnexusBook{}, fmt.Errorf("status %d", status)
	}
	return book, nil
}

func (p *AudibleProvider) catalogURL() string {
	if strings.TrimSpace(p.catalogProductsURL) != "" {
		return strings.TrimSpace(p.catalogProductsURL)
	}
	tld := audibleRegionTLD[strings.ToLower(strings.TrimSpace(p.region))]
	if tld == "" {
		tld = ".com"
	}
	return strings.TrimRight(p.catalogBaseURL, "/") + tld + "/1.0/catalog/products"
}

func (p *AudibleProvider) mapBook(book audnexusBook, score int) SearchResult {
	description := strings.TrimSpace(book.Description)
	if description == "" {
		description = strings.TrimSpace(stripHTML(book.Summary))
	}

	genres, tags := splitAudibleGenres(book.Genres)
	authors := audnexusContributors(book.Authors, "author")
	narrators := audnexusContributors(book.Narrators, "narrator")
	series := audnexusSeries(book.SeriesPrimary, book.SeriesSecondary)

	result := SearchResult{
		ID:              strings.ToUpper(strings.TrimSpace(book.ASIN)),
		Provider:        p.Name(),
		MediaType:       "audiobook",
		Score:           score,
		Title:           strings.TrimSpace(book.Title),
		Subtitle:        strings.TrimSpace(book.Subtitle),
		Description:     description,
		Authors:         authors,
		Narrators:       narrators,
		Series:          series,
		Publisher:       strings.TrimSpace(book.PublisherName),
		PublishedDate:   audiblePublishedDate(book.ReleaseDate),
		PublishedYear:   yearFromDate(audiblePublishedDate(book.ReleaseDate)),
		Language:        titleCaseLanguage(book.Language),
		Genres:          genres,
		Tags:            tags,
		DurationSeconds: book.RuntimeLengthMin * 60,
		Explicit:        book.IsAdult,
		ExternalIDs: catalog.ExternalIDs{
			ASIN:        strings.ToUpper(strings.TrimSpace(book.ASIN)),
			AudibleASIN: strings.ToUpper(strings.TrimSpace(book.ASIN)),
			ISBN13:      strings.TrimSpace(book.ISBN),
		},
		Links: []Link{{
			Label: "Audible",
			URL:   audibleProductURL(strings.ToUpper(strings.TrimSpace(book.ASIN)), p.region),
		}},
	}
	if strings.TrimSpace(book.Image) != "" {
		result.Cover = &catalog.Image{URL: strings.TrimSpace(book.Image)}
	}
	if strings.EqualFold(strings.TrimSpace(book.FormatType), "abridged") {
		result.Tags = unique(append(result.Tags, "abridged"))
	}
	return result
}

func splitAudibleGenres(items []audnexusGenre) (genres, tags []string) {
	for _, item := range items {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(item.Type)) {
		case "tag":
			tags = append(tags, name)
		default:
			genres = append(genres, name)
		}
	}
	return unique(genres), unique(tags)
}

func audnexusContributors(items []audnexusPerson, role string) []catalog.ContributorRef {
	out := make([]catalog.ContributorRef, 0, len(items))
	for _, item := range items {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		ref := catalog.ContributorRef{Name: name, Role: role}
		if asin := strings.ToUpper(strings.TrimSpace(item.ASIN)); isValidASIN(asin) {
			ref.ID = asin
		}
		out = append(out, ref)
	}
	return out
}

func audnexusSeries(primary, secondary *audnexusSeriesRef) []catalog.SeriesRef {
	out := make([]catalog.SeriesRef, 0, 2)
	if primary != nil {
		if ref := mapAudnexusSeriesRef(*primary); ref.Name != "" {
			out = append(out, ref)
		}
	}
	if secondary != nil {
		if ref := mapAudnexusSeriesRef(*secondary); ref.Name != "" {
			out = append(out, ref)
		}
	}
	return out
}

func mapAudnexusSeriesRef(item audnexusSeriesRef) catalog.SeriesRef {
	sequenceText := cleanSeriesSequence(item.Name, item.Position)
	ref := catalog.SeriesRef{
		ID:           strings.TrimSpace(item.ASIN),
		Name:         strings.TrimSpace(item.Name),
		SequenceText: sequenceText,
	}
	if sequenceText != "" {
		if parsed, err := strconv.ParseFloat(sequenceText, 64); err == nil {
			ref.Sequence = parsed
		}
	}
	return ref
}

func cleanSeriesSequence(seriesName, sequence string) string {
	sequence = strings.TrimSpace(sequence)
	if sequence == "" {
		return ""
	}
	match := regexp.MustCompile(`\.\d+|\d+(?:\.\d+)?`).FindString(sequence)
	if match != "" {
		return match
	}
	return sequence
}

func audiblePublishedDate(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if idx := strings.Index(value, "T"); idx > 0 {
		return value[:idx]
	}
	return value
}

func titleCaseLanguage(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + strings.ToLower(value[1:])
}

func audibleProductURL(asin, region string) string {
	tld := audibleRegionTLD[strings.ToLower(strings.TrimSpace(region))]
	if tld == "" {
		tld = ".com"
	}
	return "https://www.audible" + tld + "/pd/" + url.PathEscape(asin)
}

func stripHTML(value string) string {
	value = regexp.MustCompile(`(?is)<[^>]+>`).ReplaceAllString(value, " ")
	return strings.Join(strings.Fields(value), " ")
}

type audibleCatalogResponse struct {
	Products []struct {
		ASIN string `json:"asin"`
	} `json:"products"`
}

type audnexusBook struct {
	ASIN             string             `json:"asin"`
	Title            string             `json:"title"`
	Subtitle         string             `json:"subtitle"`
	Description      string             `json:"description"`
	Summary          string             `json:"summary"`
	Image            string             `json:"image"`
	ISBN             string             `json:"isbn"`
	Language         string             `json:"language"`
	PublisherName    string             `json:"publisherName"`
	ReleaseDate      string             `json:"releaseDate"`
	RuntimeLengthMin int                `json:"runtimeLengthMin"`
	FormatType       string             `json:"formatType"`
	IsAdult          bool               `json:"isAdult"`
	Authors          []audnexusPerson   `json:"authors"`
	Narrators        []audnexusPerson   `json:"narrators"`
	Genres           []audnexusGenre    `json:"genres"`
	SeriesPrimary    *audnexusSeriesRef `json:"seriesPrimary"`
	SeriesSecondary  *audnexusSeriesRef `json:"seriesSecondary"`
}

type audnexusPerson struct {
	ASIN string `json:"asin"`
	Name string `json:"name"`
}

type audnexusGenre struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type audnexusSeriesRef struct {
	ASIN     string `json:"asin"`
	Name     string `json:"name"`
	Position string `json:"position"`
}
