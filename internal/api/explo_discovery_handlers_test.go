package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/bouliehaan/samo-server/internal/explo"
	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
)

func TestExploDiscoveryAvailability(t *testing.T) {
	for _, test := range []struct {
		name                  string
		folder, remote, ready bool
		upstreamStatus        int
		available             bool
	}{
		{name: "unconfigured"},
		{name: "folder only", folder: true},
		{name: "remote only", remote: true, ready: true, upstreamStatus: 200},
		{name: "unauthorized", folder: true, remote: true, ready: true, upstreamStatus: 401},
		{name: "remote not configured", folder: true, remote: true, upstreamStatus: 200},
		{name: "connected", folder: true, remote: true, ready: true, upstreamStatus: 200, available: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				code := test.upstreamStatus
				if code == 0 {
					code = 503
				}
				w.WriteHeader(code)
				json.NewEncoder(w).Encode(map[string]any{"service": "samo-explo", "version": 1, "configured": test.ready, "providers": []string{"slskd", "youtube"}})
			}))
			defer upstream.Close()
			var remote *explo.Remote
			if test.remote {
				remote = explo.NewRemote(upstream.URL, "secret")
			}
			s := discoveryTestServer(t, test.folder, remote, "")
			response := httptest.NewRecorder()
			s.ServeHTTP(response, httptest.NewRequest("GET", "/api/v1/explo/discovery/status", nil))
			var status struct {
				Available bool `json:"available"`
			}
			json.Unmarshal(response.Body.Bytes(), &status)
			if response.Code != 200 || status.Available != test.available {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if !test.available {
				response = httptest.NewRecorder()
				s.ServeHTTP(response, httptest.NewRequest("POST", "/api/v1/explo/downloads", strings.NewReader(`{"id":"12345678-1234-1234-1234-123456789012"}`)))
				if response.Code != 503 {
					t.Fatalf("download should be gated: %d", response.Code)
				}
			}
		})
	}
}
func TestExploDiscoveryRequiresBearer(t *testing.T) {
	s := discoveryTestServer(t, true, nil, "account-secret")
	for _, route := range []struct{ method, path string }{{"GET", "/api/v1/explo/discovery/status"}, {"POST", "/api/v1/explo/connection"}, {"GET", "/api/v1/explo/search?q=song"}, {"POST", "/api/v1/explo/downloads"}, {"GET", "/api/v1/explo/downloads/12345678-1234-1234-1234-123456789012"}} {
		response := httptest.NewRecorder()
		s.ServeHTTP(response, httptest.NewRequest(route.method, route.path, nil))
		if response.Code != 401 {
			t.Errorf("%s: %d", route.path, response.Code)
		}
	}
}

func discoveryTestServer(t *testing.T, folder bool, remote *explo.Remote, token string) http.Handler {
	t.Helper()
	var dirs []string
	if folder {
		dirs = []string{"/music/explo"}
	}
	db := storagetest.Open(t)
	service := explo.NewService(explo.ServiceOptions{DB: db, Dirs: dirs, AcoustIDAPIKey: "key", FpcalcPath: "/fake/fpcalc"})
	return NewServer(ServerOptions{DB: db, Explo: service, ExploRemote: remote, APIToken: token})
}

func TestExploRegistrationConnectsExistingSetupAndSurvivesRestart(t *testing.T) {
	token := strings.Repeat("s", 64)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("wrong callback credential")
		}
		writeJSON(w, 200, map[string]any{"service": "samo-explo", "version": 1, "configured": true, "providers": []string{"slskd"}})
	}))
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	host, portString, _ := net.SplitHostPort(u.Host)
	port, _ := strconv.Atoi(portString)
	db := storagetest.Open(t)
	// Automatic registration preserves the existing identification prerequisites.
	service := explo.NewService(explo.ServiceOptions{DB: db, Dirs: []string{"/music/explo"}, AcoustIDAPIKey: "key", FpcalcPath: "/fake/fpcalc"})
	newHandler := func() http.Handler {
		return NewServer(ServerOptions{DB: db, Explo: service, APIToken: "existing-admin-login"})
	}
	handler := newHandler()
	req := httptest.NewRequest("POST", "/api/v1/explo/connection", strings.NewReader(fmt.Sprintf(`{"port":%d,"token":%q}`, port, token)))
	req.RemoteAddr = net.JoinHostPort(host, "12345")
	req.Header.Set("X-Forwarded-For", "198.51.100.20")
	req.Header.Set("Authorization", "Bearer existing-admin-login")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != 200 {
		t.Fatalf("registration %d: %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), token) {
		t.Fatal("credential leaked")
	}
	saved, err := explo.RegisteredRemote(context.Background(), db)
	if err != nil || saved == nil {
		t.Fatalf("connection not persisted: %v", err)
	}
	handler = newHandler()
	req = httptest.NewRequest("GET", "/api/v1/explo/discovery/status", nil)
	req.Header.Set("Authorization", "Bearer existing-admin-login")
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	var status exploDiscoveryStatus
	json.Unmarshal(res.Body.Bytes(), &status)
	if !status.Available || !status.Connected || !status.Configured {
		t.Fatalf("registered setup hidden: %s", res.Body.String())
	}
}
func TestExploRegistrationRejectsUnreachableCallback(t *testing.T) {
	handler := discoveryTestServer(t, true, nil, "")
	req := httptest.NewRequest("POST", "/api/v1/explo/connection", strings.NewReader(`{"port":1,"token":"0123456789012345678901234567890123456789"}`))
	req.RemoteAddr = "127.0.0.1:12345"
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != 502 {
		t.Fatalf("status %d: %s", res.Code, res.Body.String())
	}
}

func TestExploDiscoveryExplainsMissingIdentificationPrerequisite(t *testing.T) {
	db := storagetest.Open(t)
	service := explo.NewService(explo.ServiceOptions{DB: db, Dirs: []string{"/music/explo"}, FpcalcPath: "/fake/fpcalc"})
	handler := NewServer(ServerOptions{DB: db, Explo: service})
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest("GET", "/api/v1/explo/discovery/status", nil))
	var state exploDiscoveryStatus
	if err := json.Unmarshal(res.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if !state.Configured || state.Available || !strings.Contains(state.Reason, "AcoustID") {
		t.Fatalf("missing prerequisite silently hidden: %s", res.Body.String())
	}
}
