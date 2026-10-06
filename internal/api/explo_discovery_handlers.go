package api

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/bouliehaan/samo-server/internal/explo"
	"github.com/bouliehaan/samo-server/internal/log"
)

// Acquisition requires the existing import/identification pipeline to be ready.
// Registering a callback does not bypass that pipeline.
func (s *Server) exploDiscoveryConfigured(r *http.Request) bool {
	if s.explo == nil {
		return false
	}
	cfg, err := s.explo.Config(r.Context())
	return err == nil && cfg.Configured && cfg.Enabled
}
func (s *Server) exploRemoteClient(r *http.Request) (*explo.Remote, error) {
	if s.exploRemote != nil {
		return s.exploRemote, nil
	}
	return explo.RegisteredRemote(r.Context(), s.db)
}

type exploDiscoveryStatus struct {
	Configured bool     `json:"configured"`
	Connected  bool     `json:"connected"`
	Available  bool     `json:"available"`
	Providers  []string `json:"providers"`
	// AlbumProviders is the order whole albums try the providers in, for
	// the album search to show.
	AlbumProviders []string `json:"albumProviders"`
	// Albums is true when the connected Explo can also find whole albums;
	// Artists when it can find an artist and list what they released.
	Albums  bool `json:"albums"`
	Artists bool `json:"artists"`
	// Playlists is true when it can read YouTube Music playlists, for
	// importing one with the songs the library lacks downloaded.
	Playlists bool   `json:"playlists"`
	Reason    string `json:"reason,omitempty"`
}

func (s *Server) exploDiscoveryState(r *http.Request) (exploDiscoveryStatus, *explo.Remote) {
	state := exploDiscoveryStatus{Configured: s.exploDiscoveryConfigured(r), Providers: []string{}, AlbumProviders: []string{}}
	if !state.Configured {
		if s.explo != nil {
			if cfg, err := s.explo.Config(r.Context()); err == nil && cfg.Configured {
				state.Configured = true
				state.Reason = "Explo's import pipeline is not ready: " + cfg.DisabledReason
			}
		}
		return state, nil
	}
	remote, err := s.exploRemoteClient(r)
	if err != nil || remote == nil {
		state.Reason = "Explo is set up, but its song-search connection has not registered yet. Update and restart samo-explo to connect automatically."
		return state, nil
	}
	status, err := remote.Status(r.Context())
	if err != nil {
		state.Reason = "Explo's song-search connection is unavailable. Check that samo-explo is running and reachable."
		return state, remote
	}
	state.Connected = true
	state.Available = status.Configured && len(status.Providers) > 0
	if state.Available {
		state.Providers = status.Providers
		state.AlbumProviders = status.AlbumProviders
		if len(state.AlbumProviders) == 0 {
			state.AlbumProviders = status.Providers
		}
		state.Albums = status.Albums
		state.Artists = status.Artists
		state.Playlists = status.Playlists
	} else {
		state.Reason = "Explo is connected, but its download providers are not ready."
	}
	return state, remote
}
func (s *Server) getExploDiscoveryStatus(w http.ResponseWriter, r *http.Request) {
	state, _ := s.exploDiscoveryState(r)
	writeJSON(w, http.StatusOK, state)
}
func (s *Server) requireExploDiscovery(w http.ResponseWriter, r *http.Request) *explo.Remote {
	state, remote := s.exploDiscoveryState(r)
	if !state.Available {
		message := state.Reason
		if message == "" {
			message = "Explo song search is not configured"
		}
		writeError(w, http.StatusServiceUnavailable, message)
		return nil
	}
	return remote
}

// Explo already has an admin account token from its setup wizard. It uses that
// credential to register a callback, and proves the callback works before we
// persist it. The callback host is the actual peer, never a forwarded header or
// client-supplied URL. Manual URL/token overrides still win for proxy topologies.
func (s *Server) registerExploConnection(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if !s.exploDiscoveryConfigured(r) {
		writeError(w, 503, "configure the Explo import folder first")
		return
	}
	var input struct {
		Port  int    `json:"port"`
		Token string `json:"token"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	if !readJSONBody(w, r, &input) {
		return
	}
	if input.Port < 1 || input.Port > 65535 || len(input.Token) < 32 || len(input.Token) > 512 || strings.ContainsAny(input.Token, "\r\n") {
		writeError(w, 400, "invalid Explo connection")
		return
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || net.ParseIP(host) == nil {
		writeError(w, 400, "unable to determine Explo address")
		return
	}
	baseURL := "http://" + net.JoinHostPort(host, strconv.Itoa(input.Port))
	remote := explo.NewRemote(baseURL, input.Token)
	if _, err := remote.Status(r.Context()); err != nil {
		writeError(w, 502, "cannot reach Explo's authenticated song-search API")
		return
	}
	if err := explo.SaveConnection(r.Context(), s.db, baseURL, input.Token); err != nil {
		writeError(w, 500, "unable to save Explo connection")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"registered": true})
}
func (s *Server) searchExploSongs(w http.ResponseWriter, r *http.Request) {
	remote := s.requireExploDiscovery(w, r)
	if remote == nil {
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(query) < 2 || len(query) > 200 {
		writeError(w, 400, "enter between 2 and 200 characters")
		return
	}
	result, err := remote.Search(r.Context(), query)
	if err != nil {
		writeExploRemoteError(w, err)
		return
	}
	writeJSON(w, 200, result)
}

// A requested song ends up in the library (see explo/requests.go), so asking
// for one is admin-only, like Keep.
func (s *Server) addExploSong(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	remote := s.requireExploDiscovery(w, r)
	if remote == nil {
		return
	}
	var input struct {
		ID       string `json:"id"`
		Provider string `json:"provider"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, 400, "recording ID required")
		return
	}
	if !explo.ValidCatalogID(input.ID) {
		writeError(w, 400, "invalid recording ID")
		return
	}
	if input.Provider != "" && input.Provider != "auto" && input.Provider != "youtube" && input.Provider != "slskd" {
		writeError(w, 400, "invalid download provider")
		return
	}
	job, err := remote.Add(r.Context(), input.ID, input.Provider)
	if err != nil {
		writeExploRemoteError(w, err)
		return
	}
	if err := s.explo.RecordRequest(r.Context(), job, principal.User.ID); err != nil {
		// The download is already under way; the request is recorded on the
		// next status poll's sync instead, so this is not the caller's error.
		log.Warnf("explo: record song request %s: %v", job.ID, err)
	}
	writeJSON(w, http.StatusAccepted, s.exploJobView(r, job))
}
func (s *Server) getExploDownload(w http.ResponseWriter, r *http.Request) {
	if !s.exploDiscoveryConfigured(r) {
		writeError(w, 503, "Explo song search is not configured")
		return
	}
	id := r.PathValue("id")
	if !explo.ValidSongID(id) {
		writeError(w, 400, "invalid recording ID")
		return
	}
	remote, err := s.exploRemoteClient(r)
	if err != nil {
		remote = nil
	}
	var job explo.DownloadJob
	if remote != nil {
		job, err = remote.Job(r.Context(), id)
	} else {
		err = &explo.RemoteError{Status: 503, Message: "Explo song search is not connected"}
	}
	if err != nil {
		// Explo keeps jobs in memory, so a staged download is forgotten by a
		// restart while samo is still identifying or has already kept it.
		// Samo's record of the request is the answer then.
		if request, ok, _ := s.explo.Request(r.Context(), id); ok && request.State != explo.RequestDownloading {
			writeJSON(w, 200, explo.DownloadJob{ID: id, State: exploJobState(request.State), Message: request.Message, Library: &request})
			return
		}
		writeExploRemoteError(w, err)
		return
	}
	if err := s.explo.SyncJob(r.Context(), job); err != nil {
		log.Warnf("explo: sync song request %s: %v", id, err)
	}
	writeJSON(w, 200, s.exploJobView(r, job))
}

// exploJobView is a download job as the browser sees it: Explo's report, with
// samo's progress on the request once the file is staged, and without the
// staging path.
func (s *Server) exploJobView(r *http.Request, job explo.DownloadJob) explo.DownloadJob {
	job.File = ""
	if request, ok, err := s.explo.Request(r.Context(), job.ID); err == nil && ok && request.State != explo.RequestDownloading {
		job.Library = &request
		if request.Message != "" && job.State == "staged" {
			job.Message = request.Message
		}
	}
	return job
}

// exploJobState maps a request samo is still tracking onto the job states the
// browser understands, for when Explo no longer has the job.
func exploJobState(requestState string) string {
	if requestState == explo.RequestFailed {
		return "failed"
	}
	return "staged"
}
func writeExploRemoteError(w http.ResponseWriter, err error) {
	var remote *explo.RemoteError
	if errors.As(err, &remote) {
		writeError(w, remote.Status, remote.Message)
		return
	}
	writeError(w, 503, "Explo is unavailable")
}
