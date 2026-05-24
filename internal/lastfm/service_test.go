package lastfm

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/playback"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/internal/users"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestSignParamsMatchesLastFMRules(t *testing.T) {
	params := map[string]string{
		"method":  "auth.getToken",
		"api_key": "test-key",
		"format":  "json",
	}
	sig := signParams("test-secret", params)
	if len(sig) != 32 {
		t.Fatalf("signature length = %d, want 32", len(sig))
	}
}

func TestCompleteAuthStoresSession(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "method=auth.getSession") {
			t.Fatalf("unexpected request body: %s", body)
		}
		_, _ = w.Write([]byte(`{"session":{"name":"jake","key":"session-key","subscriber":0}}`))
	}))
	defer server.Close()

	db := openTestDB(t)
	service := newTestService(t, db, server)
	response, err := service.CompleteAuth(ctx, users.BootstrapUserID, "token-123")
	if err != nil {
		t.Fatal(err)
	}
	if response.Username != "jake" || !response.Connected {
		t.Fatalf("response = %+v", response)
	}
}

func TestHandlePlaybackUpdateQueuesScrobbleWhenOffline(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "track.updateNowPlaying") {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		if strings.Contains(string(body), "track.scrobble") {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	db := openTestDB(t)
	seedLastFMSession(t, db)
	service := newTestService(t, db, server)

	track := testTrack()
	patch := playback.PatchInput{
		ProgressSeconds:     intPtr(60),
		TouchLastPlayedAt:   true,
		TouchLastPositionAt: true,
	}
	service.HandlePlaybackUpdate(ctx, users.BootstrapUserID, track, catalog.PlaybackState{}, catalog.PlaybackState{ProgressSeconds: 60}, patch)

	status, err := service.Status(ctx, users.BootstrapUserID)
	if err != nil {
		t.Fatal(err)
	}
	if status.QueueSize == 0 {
		t.Fatal("expected queued submissions after upstream failure")
	}
}

func TestHandleScrobbleEventComplete(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	db := openTestDB(t)
	seedLastFMSession(t, db)
	service := newTestService(t, db, server)

	response, err := service.HandleScrobbleEvent(ctx, users.BootstrapUserID, testTrack(), ScrobbleEventInput{
		TrackID:         "track-1",
		Event:           "complete",
		ProgressSeconds: 120,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !response.Scrobbled {
		t.Fatalf("response = %+v, want scrobbled", response)
	}

	history, err := service.ListHistory(ctx, users.BootstrapUserID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if history.Total == 0 {
		t.Fatal("expected submission history")
	}
}

func TestScrobblesUseSeparateUserSessions(t *testing.T) {
	ctx := context.Background()
	var sessionKeys []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if r.PostForm.Get("method") == "track.scrobble" {
			sessionKeys = append(sessionKeys, r.PostForm.Get("sk"))
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	db := openTestDB(t)
	userService := users.New(users.ServiceOptions{DB: db})
	if err := userService.Bootstrap(ctx, users.BootstrapInput{AdminUsername: "owner", AdminPassword: "owner-pass-123"}); err != nil {
		t.Fatal(err)
	}
	owner, err := userService.AuthenticateCredentials(ctx, "owner", "owner-pass-123")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := userService.Create(ctx, owner, users.CreateUserInput{
		Username: "listener",
		Password: "listener-pass-123",
		Role:     users.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	seedLastFMSessionForUser(t, db, owner.User.ID, "one", "session-one")
	seedLastFMSessionForUser(t, db, listener.ID, "two", "session-two")
	service := newTestService(t, db, server)

	if err := service.SubmitScrobble(ctx, owner.User.ID, testTrack(), time.Unix(1000, 0), 0, "native-test"); err != nil {
		t.Fatal(err)
	}
	if err := service.SubmitScrobble(ctx, listener.ID, testTrack(), time.Unix(2000, 0), 0, "native-test"); err != nil {
		t.Fatal(err)
	}
	if len(sessionKeys) != 2 || sessionKeys[0] != "session-one" || sessionKeys[1] != "session-two" {
		t.Fatalf("session keys = %#v", sessionKeys)
	}
	one, err := service.ListHistory(ctx, owner.User.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	two, err := service.ListHistory(ctx, listener.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if one.Total != 1 || two.Total != 1 {
		t.Fatalf("history totals: one=%d two=%d", one.Total, two.Total)
	}
}

func TestLoveTrackOnFavoriteChange(t *testing.T) {
	ctx := context.Background()
	var loved bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "method=track.love") {
			loved = true
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	db := openTestDB(t)
	seedLastFMSession(t, db)
	service := newTestService(t, db, server)

	favorite := true
	service.HandlePlaybackUpdate(ctx, users.BootstrapUserID, testTrack(), catalog.PlaybackState{}, catalog.PlaybackState{Favorite: true}, playback.PatchInput{
		Favorite:            &favorite,
		ProgressSeconds:     intPtr(10),
		TouchLastPositionAt: true,
	})
	if !loved {
		t.Fatal("expected track.love call")
	}
}

func TestLastFMStatusJSON(t *testing.T) {
	now := time.Now().UTC()
	status := Status{Enabled: true, Connected: true, Username: "jake", QueueSize: 2, ConnectedAt: &now}
	payload, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), `"queueSize":2`) {
		t.Fatalf("payload = %s", payload)
	}
}

func newTestService(t *testing.T, db *sql.DB, server *httptest.Server) *Service {
	t.Helper()
	service := NewService(ServiceOptions{
		DB:           db,
		APIKey:       "key",
		SharedSecret: "secret",
	})
	service.client.http = &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			req.URL.Scheme = "http"
			req.URL.Host = strings.TrimPrefix(server.URL, "http://")
			return http.DefaultTransport.RoundTrip(req)
		}),
	}
	return service
}

func seedLastFMSession(t *testing.T, db *sql.DB) {
	t.Helper()
	seedLastFMSessionForUser(t, db, users.BootstrapUserID, "jake", "session-key")
}

func seedLastFMSessionForUser(t *testing.T, db *sql.DB, userID, username, sessionKey string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), `
		INSERT INTO lastfm_user_settings (user_id, lastfm_username, session_key, connected_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)`, userID, username, sessionKey); err != nil {
		t.Fatal(err)
	}
}

func testTrack() catalog.MusicTrack {
	return catalog.MusicTrack{
		ID:              "track-1",
		Title:           "Signal One",
		ArtistNames:     []string{"The Static"},
		AlbumTitle:      "Night Broadcasts",
		DurationSeconds: 120,
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}
	return db
}

func intPtr(value int) *int {
	return &value
}
