package metadata

import (
	"context"
	"errors"
	"testing"
)

func TestServiceHasNoProvidersByDefault(t *testing.T) {
	service := NewDefaultService(nil, "")

	if providers := service.Providers(); len(providers) != 0 {
		t.Fatalf("providers = %#v, want none", providers)
	}

	response, err := service.Search(context.Background(), SearchRequest{
		Kind:  KindAudiobook,
		Title: "Signal Manual",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Results) != 0 {
		t.Fatalf("results = %#v, want none", response.Results)
	}
}

func TestServiceFiltersProvidersByKind(t *testing.T) {
	service := NewService(ServiceOptions{Providers: []Provider{
		fakeProvider{name: "books", kinds: []Kind{KindAudiobook}},
		fakeProvider{name: "podcasts", kinds: []Kind{KindPodcast}},
	}})

	response, err := service.Search(context.Background(), SearchRequest{
		Kind:  KindPodcast,
		Query: "Night Signals",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(response.Results))
	}
	if response.Results[0].Provider != "podcasts" {
		t.Fatalf("provider = %q, want podcasts", response.Results[0].Provider)
	}
}

func TestServiceReturnsErrorForMissingRequestedProvider(t *testing.T) {
	service := NewService(ServiceOptions{Providers: []Provider{fakeProvider{name: "books", kinds: []Kind{KindAudiobook}}}})

	_, err := service.Search(context.Background(), SearchRequest{
		Kind:     KindAudiobook,
		Provider: "musicbrainz",
		Title:    "Signal Manual",
	})
	if err != ErrProviderNotFound {
		t.Fatalf("err = %v, want ErrProviderNotFound", err)
	}
}

func TestServiceKeepsSearchingWhenOneProviderFails(t *testing.T) {
	service := NewService(ServiceOptions{Providers: []Provider{
		failingProvider{name: "broken", kinds: []Kind{KindPodcast}, err: errors.New("offline")},
		fakeProvider{name: "podcasts", kinds: []Kind{KindPodcast}},
	}})

	response, err := service.Search(context.Background(), SearchRequest{
		Kind:  KindPodcast,
		Query: "Night Signals",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Results) != 1 || response.Results[0].Provider != "podcasts" {
		t.Fatalf("results = %#v, want successful provider result", response.Results)
	}
	if len(response.ProviderErrors) != 1 || response.ProviderErrors[0].Provider != "broken" {
		t.Fatalf("provider errors = %#v, want broken provider error", response.ProviderErrors)
	}
}

func TestServiceReturnsErrorWhenRequestedProviderFails(t *testing.T) {
	service := NewService(ServiceOptions{Providers: []Provider{
		failingProvider{name: "broken", kinds: []Kind{KindPodcast}, err: errors.New("offline")},
	}})

	_, err := service.Search(context.Background(), SearchRequest{
		Kind:     KindPodcast,
		Provider: "broken",
		Query:    "Night Signals",
	})
	if err == nil {
		t.Fatal("expected requested provider failure")
	}
}

func TestValidateApplyFieldsDefaultsEmptyListToAllowedFields(t *testing.T) {
	fields, err := validateApplyFields(ApplyTargetPodcast, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) == 0 || fields[0] != "title" {
		t.Fatalf("fields = %#v, want podcast defaults", fields)
	}
}

type fakeProvider struct {
	name  string
	kinds []Kind
}

func (p fakeProvider) Name() string {
	return p.name
}

func (p fakeProvider) Supports(kind Kind) bool {
	for _, supported := range p.kinds {
		if supported == kind {
			return true
		}
	}
	return false
}

func (p fakeProvider) Status() ProviderStatus {
	return ProviderStatus{Name: p.name, Kinds: p.kinds}
}

func (p fakeProvider) Search(ctx context.Context, request SearchRequest) ([]SearchResult, error) {
	return []SearchResult{{
		ID:        p.name + "-1",
		Provider:  p.name,
		MediaType: string(request.Kind),
		Title:     "Result",
	}}, nil
}

type failingProvider struct {
	name  string
	kinds []Kind
	err   error
}

func (p failingProvider) Name() string {
	return p.name
}

func (p failingProvider) Supports(kind Kind) bool {
	for _, supported := range p.kinds {
		if supported == kind {
			return true
		}
	}
	return false
}

func (p failingProvider) Status() ProviderStatus {
	return ProviderStatus{Name: p.name, Kinds: p.kinds}
}

func (p failingProvider) Search(ctx context.Context, request SearchRequest) ([]SearchResult, error) {
	return nil, p.err
}
