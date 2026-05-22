package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bouliehaan/samo-server/internal/metadata"
	"github.com/bouliehaan/samo-server/internal/radio"
)

func TestMetadataProvidersAreEmptyByDefault(t *testing.T) {
	handler := metadataTestServer(t, metadata.NewService(metadata.ServiceOptions{}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/metadata/providers", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var providers []metadata.ProviderStatus
	if err := json.NewDecoder(rec.Body).Decode(&providers); err != nil {
		t.Fatal(err)
	}
	if len(providers) != 0 {
		t.Fatalf("providers = %#v, want none", providers)
	}
}

func TestMetadataSearchHandler(t *testing.T) {
	handler := metadataTestServer(t, metadata.NewService(metadata.ServiceOptions{Providers: []metadata.Provider{
		apiFakeMetadataProvider{},
	}}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/metadata/search?kind=audiobook&title=Signal+Manual&provider=fake", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var response metadata.SearchResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if len(response.Results) != 1 || response.Results[0].Provider != "fake" {
		t.Fatalf("response = %#v, want fake result", response)
	}
}

func metadataTestServer(t *testing.T, metadataService *metadata.Service) http.Handler {
	t.Helper()
	radioService, err := radio.NewService(radio.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return NewServer(ServerOptions{
		Metadata: metadataService,
		Radio:    radioService,
	})
}

type apiFakeMetadataProvider struct{}

func (p apiFakeMetadataProvider) Name() string {
	return "fake"
}

func (p apiFakeMetadataProvider) Supports(kind metadata.Kind) bool {
	return kind == metadata.KindAudiobook
}

func (p apiFakeMetadataProvider) Status() metadata.ProviderStatus {
	return metadata.ProviderStatus{Name: p.Name(), Kinds: []metadata.Kind{metadata.KindAudiobook}}
}

func (p apiFakeMetadataProvider) Search(ctx context.Context, request metadata.SearchRequest) ([]metadata.SearchResult, error) {
	return []metadata.SearchResult{{
		ID:        "fake-1",
		Provider:  p.Name(),
		MediaType: "audiobook",
		Title:     request.Title,
	}}, nil
}
