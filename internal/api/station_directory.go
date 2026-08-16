package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// A station directory is any local service that can hand us a list of playable
// stream URLs. Samo does not know or care what is behind one — it asks for a
// list, gets names and URLs back, and offers them when adding an internet
// station. Anything that speaks the contract below works.
//
// The contract is deliberately tiny: GET /channels returning a JSON array of
// {id, name, number, description}. A stream URL is baseURL + "/icecast/" + id.
//
// Discovery is by probe rather than configuration, because configuration means
// somebody has to edit a file on the server to make a UI appear, and then the
// feature is only discoverable by the person who already knew about it. A
// directory running on this host is found on its own; if none is running, none
// of this surfaces anywhere.

// stationDirectoryCandidates are probed in order, first responder wins.
// Loopback only: samo runs with host networking, so a directory publishing a
// port on this machine is reachable here. We deliberately do not scan the LAN —
// probing other people's hosts to find services is not something a media server
// should do uninvited.
var stationDirectoryCandidates = []string{
	"http://127.0.0.1:7717",
	"http://localhost:7717",
}

const (
	// Short: this runs while an operator waits for the settings page to paint,
	// and a directory that cannot answer in a second is not one worth offering.
	stationDirectoryProbeTimeout = 1500 * time.Millisecond
	// A found directory is stable; re-probing on every settings render is waste.
	stationDirectoryHitTTL = 5 * time.Minute
	// A miss is re-checked sooner so a newly started directory shows up without
	// requiring a samo restart.
	stationDirectoryMissTTL = 30 * time.Second
	// A directory returning an implausible number of stations is either not the
	// service we think it is or is broken; refuse rather than render 50k rows.
	stationDirectoryMaxStations = 5000
)

type stationDirectoryEntry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Number      *int   `json:"number,omitempty"`
	Description string `json:"description,omitempty"`
	// Genre is optional in the contract — a directory that doesn't supply it
	// still works, the picker just falls back to one ungrouped list.
	Genre string `json:"genre,omitempty"`
	// StreamURL is resolved server-side so the browser never has to know how a
	// directory composes its URLs. It is what gets SAVED on the station, because
	// it is samo's own ffmpeg that has to open it — reaching a colocated
	// directory over loopback is correct there.
	StreamURL string `json:"streamUrl"`
	// PreviewURL is the same audio, same-origin and proxied by samo, for the
	// browser to play. A browser cannot use StreamURL when the directory is
	// colocated with samo: loopback would resolve to the listener's own machine.
	PreviewURL string `json:"previewUrl,omitempty"`
}

type stationDirectoryResponse struct {
	Available bool                    `json:"available"`
	BaseURL   string                  `json:"baseUrl,omitempty"`
	Stations  []stationDirectoryEntry `json:"stations,omitempty"`
}

var (
	stationDirMu      sync.Mutex
	stationDirBase    string
	stationDirChecked time.Time
	stationDirFound   bool
)

// stationDirectoryOverride lets someone point at a directory on another host.
// Never required — the probe covers the normal case — but without it a
// non-default setup would have no way in at all.
func stationDirectoryOverride() string {
	return strings.TrimRight(strings.TrimSpace(os.Getenv("SAMO_STATION_DIRECTORY_URL")), "/")
}

// discoverStationDirectory returns the base URL of a reachable directory, or ""
// if none answered. Results are cached so repeated settings renders are free.
func (s *Server) discoverStationDirectory(ctx context.Context) string {
	if override := stationDirectoryOverride(); override != "" {
		return override
	}

	stationDirMu.Lock()
	if !stationDirChecked.IsZero() {
		ttl := stationDirectoryMissTTL
		if stationDirFound {
			ttl = stationDirectoryHitTTL
		}
		if time.Since(stationDirChecked) < ttl {
			base := stationDirBase
			stationDirMu.Unlock()
			return base
		}
	}
	stationDirMu.Unlock()

	found := ""
	for _, candidate := range stationDirectoryCandidates {
		if _, err := fetchStationDirectory(ctx, candidate); err == nil {
			found = candidate
			break
		}
	}

	stationDirMu.Lock()
	stationDirBase = found
	stationDirFound = found != ""
	stationDirChecked = time.Now()
	stationDirMu.Unlock()
	return found
}

// fetchStationDirectory pulls and validates the channel list from one base URL.
func fetchStationDirectory(ctx context.Context, baseURL string) ([]stationDirectoryEntry, error) {
	reqCtx, cancel := context.WithTimeout(ctx, stationDirectoryProbeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, baseURL+"/channels", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errStationDirectoryUnavailable
	}

	var raw []struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Number      *int   `json:"number"`
		Description string `json:"description"`
		Genre       string `json:"genre"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	if len(raw) == 0 || len(raw) > stationDirectoryMaxStations {
		return nil, errStationDirectoryUnavailable
	}

	out := make([]stationDirectoryEntry, 0, len(raw))
	for _, entry := range raw {
		id := strings.TrimSpace(entry.ID)
		name := strings.TrimSpace(entry.Name)
		if id == "" || name == "" {
			continue
		}
		out = append(out, stationDirectoryEntry{
			ID:          id,
			Name:        name,
			Number:      entry.Number,
			Description: strings.TrimSpace(entry.Description),
			Genre:       strings.TrimSpace(entry.Genre),
			StreamURL:   baseURL + "/icecast/" + id,
		})
	}
	if len(out) == 0 {
		return nil, errStationDirectoryUnavailable
	}
	return out, nil
}

var errStationDirectoryUnavailable = &stationDirectoryError{}

type stationDirectoryError struct{}

func (*stationDirectoryError) Error() string { return "no station directory available" }

// proxyAudioStream relays an audio stream through samo instead of redirecting to
// it, so the client only ever talks to samo's own origin.
//
// Deliberately not a general-purpose fetcher: every caller composes the target
// from server-side state, never from anything the client sent, so this cannot be
// aimed at an arbitrary host.
func (s *Server) proxyAudioStream(w http.ResponseWriter, r *http.Request, target string) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target, nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, "bad upstream stream url")
		return
	}
	// Pass the client's ICY negotiation upstream so metadata-aware players still
	// get inline titles rather than a bare byte stream.
	if v := strings.TrimSpace(r.Header.Get("Icy-MetaData")); v != "" {
		req.Header.Set("Icy-MetaData", v)
	}
	req.Header.Set("User-Agent", "Samo Server/0.1")

	// No client timeout — this is an endless stream, and any deadline here would
	// cut playback off mid-song. The request context ends it when the listener
	// goes away.
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream stream unavailable")
		return
	}
	defer resp.Body.Close()

	for _, header := range []string{"Content-Type", "icy-metaint", "icy-name", "icy-genre", "icy-br"} {
		if v := resp.Header.Get(header); v != "" {
			w.Header().Set(header, v)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Accept-Ranges", "none")
	w.WriteHeader(resp.StatusCode)

	// Flush as bytes arrive. Without this the response sits in Go's write buffer
	// waiting to fill, which on a live stream shows up as playback that takes
	// seconds to start or stutters at the head.
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				return // client hung up
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if readErr != nil {
			return
		}
	}
}

// streamStationDirectoryChannel previews a directory channel that has not been
// added as a station yet. Auditioning before saving is the whole point of a
// 400-channel picker — otherwise finding one station you like means adding
// twenty you don't.
//
// The caller supplies only a channel id; the host comes from the discovered
// directory, so this cannot be pointed anywhere else.
func (s *Server) streamStationDirectoryChannel(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "channel id is required")
		return
	}
	base := s.discoverStationDirectory(r.Context())
	if base == "" {
		writeError(w, http.StatusNotFound, "no station directory available")
		return
	}
	s.proxyAudioStream(w, r, base+"/icecast/"+url.PathEscape(id))
}

// getStationDirectory reports whether a directory is reachable and, if so, what
// it offers. It always answers 200 with available=false rather than 404, so the
// client has one shape to handle and "nothing here" is a normal answer instead
// of an error the console complains about.
func (s *Server) getStationDirectory(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	base := s.discoverStationDirectory(r.Context())
	if base == "" {
		writeJSON(w, http.StatusOK, stationDirectoryResponse{Available: false})
		return
	}
	stations, err := fetchStationDirectory(r.Context(), base)
	if err != nil {
		// It answered the probe and then failed: report absent rather than
		// erroring, and clear the cache so the next render re-probes.
		stationDirMu.Lock()
		stationDirChecked = time.Time{}
		stationDirMu.Unlock()
		writeJSON(w, http.StatusOK, stationDirectoryResponse{Available: false})
		return
	}
	// Filled here rather than in fetchStationDirectory because it depends on the
	// request's own scheme/host, so a page served over HTTPS or through a tunnel
	// gets a matching preview URL instead of a mixed-content one.
	for i := range stations {
		stations[i].PreviewURL = publicURL(r, "/internet-radio/directory/"+url.PathEscape(stations[i].ID)+"/stream")
	}
	writeJSON(w, http.StatusOK, stationDirectoryResponse{
		Available: true,
		BaseURL:   base,
		Stations:  stations,
	})
}
