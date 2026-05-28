package metadata

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"
)

const defaultLimit = 10

var (
	ErrInvalidRequest   = errors.New("invalid metadata search request")
	ErrProviderNotFound = errors.New("metadata provider not found")
	ErrUnsupportedKind  = errors.New("metadata provider does not support requested kind")
)

type Service struct {
	providers map[string]Provider
}

type ServiceOptions struct {
	Providers []Provider
}

func NewService(options ServiceOptions) *Service {
	service := &Service{providers: map[string]Provider{}}
	for _, provider := range options.Providers {
		if provider == nil {
			continue
		}
		service.providers[strings.ToLower(provider.Name())] = provider
	}
	return service
}

func NewDefaultService(providerNames []string, userAgent string) *Service {
	return NewService(ServiceOptions{
		Providers: DefaultProviders(providerNames, userAgent, nil),
	})
}

func (s *Service) Providers() []ProviderStatus {
	if s == nil {
		return []ProviderStatus{}
	}
	statuses := make([]ProviderStatus, 0, len(s.providers))
	for _, provider := range s.providers {
		status := provider.Status()
		status.Enabled = true
		statuses = append(statuses, status)
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].Name < statuses[j].Name })
	return statuses
}

func (s *Service) Search(ctx context.Context, request SearchRequest) (SearchResponse, error) {
	request = normalizeRequest(request)
	if err := validateRequest(request); err != nil {
		return SearchResponse{}, err
	}

	response := SearchResponse{
		Query:     prepareSearchRequest(request),
		Providers: s.Providers(),
	}
	if s == nil || len(s.providers) == 0 {
		response.Results = []SearchResult{}
		return response, nil
	}

	providers, err := s.providersForRequest(request)
	if err != nil {
		return SearchResponse{}, err
	}

	attempts := searchAttempts(request)
	var providerErrors []ProviderError
	seen := make(map[string]struct{})
	for _, attempt := range attempts {
		results, attemptErrors, err := s.searchProviders(ctx, providers, attempt)
		if err != nil {
			return SearchResponse{}, err
		}
		for _, result := range results {
			key := strings.ToLower(result.Provider) + ":" + result.ID
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			response.Results = append(response.Results, result)
		}
		if len(attemptErrors) > 0 {
			providerErrors = attemptErrors
		}
		if len(response.Results) > 0 {
			break
		}
	}
	response.ProviderErrors = providerErrors

	sort.SliceStable(response.Results, func(i, j int) bool {
		if response.Results[i].Score == response.Results[j].Score {
			return response.Results[i].Title < response.Results[j].Title
		}
		return response.Results[i].Score > response.Results[j].Score
	})
	if len(response.Results) > request.Limit {
		response.Results = slices.Clone(response.Results[:request.Limit])
	}
	return response, nil
}

func (s *Service) searchProviders(ctx context.Context, providers []Provider, request SearchRequest) ([]SearchResult, []ProviderError, error) {
	request = prepareSearchRequest(request)
	var results []SearchResult
	var providerErrors []ProviderError
	for _, provider := range providers {
		providerResults, err := provider.Search(ctx, request)
		if err != nil {
			if request.Provider != "" {
				return nil, nil, fmt.Errorf("%s metadata search: %w", provider.Name(), err)
			}
			providerErrors = append(providerErrors, ProviderError{
				Provider: provider.Name(),
				Error:    err.Error(),
			})
			continue
		}
		results = append(results, providerResults...)
	}
	return results, providerErrors, nil
}

func (s *Service) providersForRequest(request SearchRequest) ([]Provider, error) {
	if request.Provider != "" {
		provider, ok := s.providers[strings.ToLower(request.Provider)]
		if !ok {
			return nil, ErrProviderNotFound
		}
		if !provider.Supports(request.Kind) {
			return nil, ErrUnsupportedKind
		}
		return []Provider{provider}, nil
	}

	var providers []Provider
	for _, provider := range s.providers {
		if provider.Supports(request.Kind) {
			providers = append(providers, provider)
		}
	}
	sort.Slice(providers, func(i, j int) bool { return providers[i].Name() < providers[j].Name() })
	return providers, nil
}

func DefaultProviders(names []string, userAgent string, client *http.Client) []Provider {
	if len(names) == 0 {
		return nil
	}
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	userAgent = strings.TrimSpace(userAgent)
	if userAgent == "" {
		userAgent = "SamoServer/0.1 (https://github.com/bouliehaan/samo-server)"
	}

	var providers []Provider
	for _, name := range names {
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "audible", "audnexus":
			providers = append(providers, NewAudibleProvider(client))
		case "openlibrary", "open-library":
			providers = append(providers, NewOpenLibraryProvider(client))
		case "googlebooks", "google-books", "google_books":
			providers = append(providers, NewGoogleBooksProvider(client))
		case "itunes", "applepodcasts", "apple-podcasts":
			providers = append(providers, NewApplePodcastProvider(client))
		case "musicbrainz", "music-brainz":
			providers = append(providers, NewMusicBrainzProvider(client, userAgent))
		}
	}
	return providers
}

func normalizeRequest(request SearchRequest) SearchRequest {
	request.Kind = Kind(strings.ToLower(strings.TrimSpace(string(request.Kind))))
	request.Provider = strings.ToLower(strings.TrimSpace(request.Provider))
	request.Query = strings.TrimSpace(request.Query)
	request.Title = strings.TrimSpace(request.Title)
	request.Author = strings.TrimSpace(request.Author)
	request.ISBN = strings.TrimSpace(request.ISBN)
	request.ASIN = strings.ToUpper(strings.TrimSpace(request.ASIN))
	request.AudibleASIN = strings.ToUpper(strings.TrimSpace(request.AudibleASIN))
	request.Artist = strings.TrimSpace(request.Artist)
	request.Album = strings.TrimSpace(request.Album)
	request.Track = strings.TrimSpace(request.Track)
	request.MusicType = MusicSearchType(strings.ToLower(strings.TrimSpace(string(request.MusicType))))
	if request.Limit <= 0 {
		request.Limit = defaultLimit
	}
	if request.Limit > 40 {
		request.Limit = 40
	}
	return request
}

func validateRequest(request SearchRequest) error {
	switch request.Kind {
	case KindAudiobook:
		if request.Query == "" && request.Title == "" && request.Author == "" && request.ISBN == "" && request.ASIN == "" && request.AudibleASIN == "" {
			return ErrInvalidRequest
		}
	case KindPodcast:
		if request.Query == "" && request.Title == "" {
			return ErrInvalidRequest
		}
	case KindMusic:
		if request.Query == "" && request.Artist == "" && request.Album == "" && request.Track == "" {
			return ErrInvalidRequest
		}
		switch request.MusicType {
		case "", MusicSearchArtist, MusicSearchAlbum, MusicSearchTrack:
		default:
			return ErrInvalidRequest
		}
	default:
		return ErrInvalidRequest
	}
	return nil
}
