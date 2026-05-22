package metadata

import (
	"context"
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
