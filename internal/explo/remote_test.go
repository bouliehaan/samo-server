package explo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRemoteContractAndCredentials(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing integration credential")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/prefix/api/samo/status":
			w.Write([]byte(`{"service":"samo-explo","version":1,"configured":true,"providers":["slskd","youtube"]}`))
		case "/prefix/api/samo/search":
			if r.URL.Query().Get("q") != "artist & song" {
				t.Error("query corrupted")
			}
			w.Write([]byte(`{"songs":[{"id":"recording","title":"Song","artist":"Artist"}]}`))
		case "/prefix/api/samo/downloads":
			if r.Method != "POST" {
				t.Error("wrong method")
			}
			var input map[string]string
			json.NewDecoder(r.Body).Decode(&input)
			if input["provider"] != "youtube" {
				t.Error("provider selection lost")
			}
			w.WriteHeader(202)
			w.Write([]byte(`{"id":"recording","state":"queued"}`))
		case "/prefix/api/samo/downloads/recording":
			w.Write([]byte(`{"id":"recording","state":"downloading","provider":"youtube","phase":"converting","progress":42,"attempts":[{"provider":"slskd","phase":"failed","message":"Peer rejected transfer"}]}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	c := NewRemote(upstream.URL+"/prefix/", "secret")
	status, err := c.Status(context.Background())
	if err != nil || !status.Configured {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	songs, err := c.Search(context.Background(), "artist & song")
	if err != nil || len(songs.Songs) != 1 {
		t.Fatalf("songs=%+v err=%v", songs, err)
	}
	job, err := c.Add(context.Background(), "recording", "youtube")
	if err != nil || job.State != "queued" {
		t.Fatalf("job=%+v err=%v", job, err)
	}
	job, err = c.Job(context.Background(), "recording")
	if err != nil || job.State != "downloading" || job.Provider != "youtube" || job.Phase != "converting" || job.Progress != 42 || len(job.Attempts) != 1 {
		t.Fatalf("job=%+v err=%v", job, err)
	}
}
func TestRemoteRejectsMisconfigurationAndWrongService(t *testing.T) {
	for _, base := range []string{"", ":bad", "file:///tmp/explo", "http://user:secret@localhost", "http://localhost?secret=1"} {
		if NewRemote(base, "key") != nil {
			t.Errorf("accepted %q", base)
		}
	}
	if NewRemote("http://localhost", "") != nil {
		t.Fatal("accepted missing key")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"configured":true}`)) }))
	defer server.Close()
	if _, err := NewRemote(server.URL, "secret").Status(context.Background()); err == nil {
		t.Fatal("accepted an unrelated service")
	}
}
func TestRemoteDoesNotFollowRedirectsOrExposeErrors(t *testing.T) {
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("followed credential-bearing redirect") }))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 302) }))
	defer source.Close()
	if _, err := NewRemote(source.URL, "secret").Status(context.Background()); err == nil {
		t.Fatal("accepted redirect")
	}
	failure := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "secret token filesystem path", 500) }))
	defer failure.Close()
	_, err := NewRemote(failure.URL, "secret").Search(context.Background(), "query")
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe error: %v", err)
	}
}
