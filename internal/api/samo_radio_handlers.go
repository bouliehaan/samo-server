package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/loudness"
	"github.com/bouliehaan/samo-server/internal/samoradio"
	"github.com/bouliehaan/samo-server/internal/users"
)

// ----- errors ----------------------------------------------------------

func writeSamoRadioError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, samoradio.ErrDisabled):
		writeError(w, http.StatusServiceUnavailable, "samo-radio is not available")
	case errors.Is(err, samoradio.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, samoradio.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		// Anything else is the device failing to answer. That is a normal
		// state for a box somebody unplugged, so it reads as an upstream
		// problem rather than a server fault.
		writeError(w, http.StatusBadGateway, err.Error())
	}
}

func (s *Server) samoRadioService() (*samoradio.Service, bool) {
	if s.samoRadio == nil || !s.samoRadio.Enabled() {
		return nil, false
	}
	return s.samoRadio, true
}

// ----- device management -----------------------------------------------

func (s *Server) listSamoRadioDevices(w http.ResponseWriter, r *http.Request) {
	service, ok := s.samoRadioService()
	if !ok {
		// An empty list rather than an error: a client's output picker asks for
		// this on every open and should simply show no devices.
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}, "total": 0})
		return
	}
	devices, err := service.ListDevices(r.Context())
	if err != nil {
		writeSamoRadioError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": devices, "total": len(devices)})
}

func (s *Server) getSamoRadioDevice(w http.ResponseWriter, r *http.Request) {
	service, ok := s.samoRadioService()
	if !ok {
		writeSamoRadioError(w, samoradio.ErrDisabled)
		return
	}
	device, err := service.GetDevice(r.Context(), r.PathValue("id"))
	if err != nil {
		writeSamoRadioError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, device)
}

func (s *Server) createSamoRadioDevice(w http.ResponseWriter, r *http.Request) {
	service, ok := s.samoRadioService()
	if !ok {
		writeSamoRadioError(w, samoradio.ErrDisabled)
		return
	}
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	var input samoradio.CreateDeviceInput
	if !readJSONBody(w, r, &input) {
		return
	}
	device, err := service.CreateDevice(r.Context(), input)
	if err != nil {
		writeSamoRadioError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, device)
}

func (s *Server) updateSamoRadioDevice(w http.ResponseWriter, r *http.Request) {
	service, ok := s.samoRadioService()
	if !ok {
		writeSamoRadioError(w, samoradio.ErrDisabled)
		return
	}
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	var input samoradio.UpdateDeviceInput
	if !readJSONBody(w, r, &input) {
		return
	}
	device, err := service.UpdateDevice(r.Context(), r.PathValue("id"), input)
	if err != nil {
		writeSamoRadioError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, device)
}

func (s *Server) deleteSamoRadioDevice(w http.ResponseWriter, r *http.Request) {
	service, ok := s.samoRadioService()
	if !ok {
		writeSamoRadioError(w, samoradio.ErrDisabled)
		return
	}
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if err := service.DeleteDevice(r.Context(), r.PathValue("id")); err != nil {
		writeSamoRadioError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// pairSamoRadioDevice hands the device a Samo token so it can pull audio.
//
// The URL the device is told to use is deliberately not derived from this
// request. A phone pairing over the public tunnel would otherwise send the
// device — which is sitting next to the server — off to fetch its audio through
// Cloudflare and back. Default to loopback on this machine's own port.
func (s *Server) pairSamoRadioDevice(w http.ResponseWriter, r *http.Request) {
	service, ok := s.samoRadioService()
	if !ok {
		writeSamoRadioError(w, samoradio.ErrDisabled)
		return
	}
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	var body struct {
		StreamBaseURL string `json:"streamBaseUrl,omitempty"`
	}
	if r.ContentLength > 0 && !readJSONBody(w, r, &body) {
		return
	}
	streamBaseURL := strings.TrimSpace(body.StreamBaseURL)
	if streamBaseURL == "" {
		if existing, err := service.StreamBaseURL(r.Context(), r.PathValue("id")); err == nil {
			streamBaseURL = existing
		}
	}
	if streamBaseURL == "" {
		streamBaseURL = s.loopbackBaseURL()
	}

	device, err := service.Pair(r.Context(), r.PathValue("id"), samoradio.PairOptions{
		StreamBaseURL: streamBaseURL,
		ServerName:    "samo",
	})
	if err != nil {
		writeSamoRadioError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, device)
}

// loopbackBaseURL is how a device on this machine reaches Samo. The listen
// address is the source of truth; a bare ":6969" means every interface, of
// which loopback is the one guaranteed to work from here.
func (s *Server) loopbackBaseURL() string {
	addr := strings.TrimSpace(s.listenAddr)
	if addr == "" {
		addr = ":6969"
	}
	if strings.HasPrefix(addr, ":") {
		return "http://127.0.0.1" + addr
	}
	if host, port, found := strings.Cut(addr, ":"); found {
		if host == "" || host == "0.0.0.0" || host == "[::]" {
			return "http://127.0.0.1:" + port
		}
		return "http://" + addr
	}
	return "http://" + addr
}

// ----- device status ---------------------------------------------------

func (s *Server) samoRadioDeviceState(w http.ResponseWriter, r *http.Request) {
	service, ok := s.samoRadioService()
	if !ok {
		writeSamoRadioError(w, samoradio.ErrDisabled)
		return
	}
	state, err := service.State(r.Context(), r.PathValue("id"))
	if err != nil {
		writeSamoRadioError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) samoRadioDeviceOutputs(w http.ResponseWriter, r *http.Request) {
	service, ok := s.samoRadioService()
	if !ok {
		writeSamoRadioError(w, samoradio.ErrDisabled)
		return
	}
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	outputs, err := service.Outputs(r.Context(), r.PathValue("id"), r.URL.Query().Get("backend"))
	if err != nil {
		writeSamoRadioError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, outputs)
}

func (s *Server) updateSamoRadioDeviceSettings(w http.ResponseWriter, r *http.Request) {
	service, ok := s.samoRadioService()
	if !ok {
		writeSamoRadioError(w, samoradio.ErrDisabled)
		return
	}
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	var input samoradio.SettingsInput
	if !readJSONBody(w, r, &input) {
		return
	}
	// The daemon caches the station's name so it can label what it is playing
	// at boot without waiting for the server; fill it in here, where the
	// catalog is, rather than trusting whatever the client sent.
	if input.DefaultStation != nil && strings.TrimSpace(input.DefaultStation.ID) != "" {
		if name := s.stationName(r.Context(), *input.DefaultStation); name != "" {
			input.DefaultStation.Name = name
		}
	}
	state, err := service.UpdateSettings(r.Context(), r.PathValue("id"), input)
	if err != nil {
		writeSamoRadioError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

// ----- transport -------------------------------------------------------

// playItemRef is a client's reference to something in the catalog.
//
// Clients send ids, never URLs: the mapping from an id to a stream URL exists
// once, here, and a client cannot ask the device to fetch an arbitrary address.
type playItemRef struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type playToDeviceRequest struct {
	Mode        string        `json:"mode,omitempty"`
	ChannelID   string        `json:"channelId,omitempty"`
	ChannelName string        `json:"channelName,omitempty"`
	StationID   string        `json:"stationId,omitempty"`
	StationName string        `json:"stationName,omitempty"`
	Items       []playItemRef `json:"items,omitempty"`
	StartIndex  int           `json:"startIndex,omitempty"`
	Append      bool          `json:"append,omitempty"`
}

func (s *Server) playToSamoRadioDevice(w http.ResponseWriter, r *http.Request) {
	service, ok := s.samoRadioService()
	if !ok {
		writeSamoRadioError(w, samoradio.ErrDisabled)
		return
	}
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var body playToDeviceRequest
	if !readJSONBody(w, r, &body) {
		return
	}
	deviceID := r.PathValue("id")

	request := samoradio.PlayRequest{
		Mode:        strings.ToLower(strings.TrimSpace(body.Mode)),
		ChannelID:   strings.TrimSpace(body.ChannelID),
		ChannelName: strings.TrimSpace(body.ChannelName),
		StationID:   strings.TrimSpace(body.StationID),
		StationName: strings.TrimSpace(body.StationName),
		StartIndex:  body.StartIndex,
		Append:      body.Append,
	}
	if request.Mode == "" {
		switch {
		case request.ChannelID != "":
			request.Mode = "channel"
		case request.StationID != "":
			request.Mode = "station"
		default:
			request.Mode = "queue"
		}
	}

	switch request.Mode {
	case "channel":
		if request.ChannelID == "" {
			writeError(w, http.StatusBadRequest, "channelId required")
			return
		}
		request.ChannelName = s.stationName(r.Context(), samoradio.StationRef{
			Kind: samoradio.StationChannel, ID: request.ChannelID, Name: request.ChannelName,
		})
	case "station":
		if request.StationID == "" {
			writeError(w, http.StatusBadRequest, "stationId required")
			return
		}
		request.StationName = s.stationName(r.Context(), samoradio.StationRef{
			Kind: samoradio.StationInternet, ID: request.StationID, Name: request.StationName,
		})
	default:
		streamBase, err := service.StreamBaseURL(r.Context(), deviceID)
		if err != nil {
			writeSamoRadioError(w, err)
			return
		}
		if streamBase == "" {
			writeError(w, http.StatusPreconditionRequired, "pair the device before sending it a queue")
			return
		}
		items, err := s.resolveSamoRadioItems(r, principal, streamBase, body.Items)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if len(items) == 0 {
			writeError(w, http.StatusBadRequest, "nothing playable in that request")
			return
		}
		request.Items = items
	}

	state, err := service.Play(r.Context(), deviceID, request)
	if err != nil {
		writeSamoRadioError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) samoRadioDeviceCommand(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		service, ok := s.samoRadioService()
		if !ok {
			writeSamoRadioError(w, samoradio.ErrDisabled)
			return
		}
		state, err := service.Command(r.Context(), r.PathValue("id"), action)
		if err != nil {
			writeSamoRadioError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, state)
	}
}

func (s *Server) seekSamoRadioDevice(w http.ResponseWriter, r *http.Request) {
	service, ok := s.samoRadioService()
	if !ok {
		writeSamoRadioError(w, samoradio.ErrDisabled)
		return
	}
	var body struct {
		PositionSeconds float64 `json:"positionSeconds"`
	}
	if !readJSONBody(w, r, &body) {
		return
	}
	state, err := service.Seek(r.Context(), r.PathValue("id"), body.PositionSeconds)
	if err != nil {
		writeSamoRadioError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) setSamoRadioDeviceVolume(w http.ResponseWriter, r *http.Request) {
	service, ok := s.samoRadioService()
	if !ok {
		writeSamoRadioError(w, samoradio.ErrDisabled)
		return
	}
	var body struct {
		Volume float64 `json:"volume"`
	}
	if !readJSONBody(w, r, &body) {
		return
	}
	state, err := service.SetVolume(r.Context(), r.PathValue("id"), body.Volume)
	if err != nil {
		writeSamoRadioError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

// ----- catalog resolution ----------------------------------------------

// resolveSamoRadioItems turns catalog references into playable items.
//
// Two different base URLs are in play and mixing them up is the bug worth
// avoiding: stream URLs are built on streamBase, because the *device* fetches
// them and it lives next to the server; artwork URLs are built on the request's
// own host, because the *client* fetches those and may be anywhere.
func (s *Server) resolveSamoRadioItems(
	r *http.Request,
	principal users.Principal,
	streamBase string,
	refs []playItemRef,
) ([]samoradio.Item, error) {
	ctx := r.Context()
	streamBase = strings.TrimRight(streamBase, "/")
	items := make([]samoradio.Item, 0, len(refs))

	for _, ref := range refs {
		id := strings.TrimSpace(ref.ID)
		if id == "" {
			continue
		}
		item, err := s.resolveSamoRadioItem(ctx, principal, streamBase, r, strings.ToLower(strings.TrimSpace(ref.Type)), id)
		if err != nil {
			return nil, err
		}
		if item.StreamURL == "" {
			continue
		}
		s.attachLoudness(ctx, &item, strings.ToLower(strings.TrimSpace(ref.Type)), id)
		items = append(items, item)
	}
	return items, nil
}

// attachLoudness fills in the item's level correction so six things sent from
// a phone come out of the amplifier at the same volume.
//
// The gain travels with the item rather than being looked up by the device,
// because the device is handed a fully-resolved queue once and then left alone
// — there is no second round trip to correct it later. A cache miss therefore
// costs this queue its levelling and nothing more: the measurement it kicks
// off lands before the next time that item is sent anywhere.
//
// Measurement reads the file directly rather than the stream URL it hands the
// device. Same bytes, no HTTP, no token, and no request served by this process
// to itself.
func (s *Server) attachLoudness(ctx context.Context, item *samoradio.Item, kind, id string) {
	if s.loudness == nil || !s.loudness.Enabled() || item.Live {
		return
	}
	path := s.mediaPathFor(kind, id)
	if path == "" {
		return
	}
	plan, _, ok := s.loudness.PlanFor(ctx, loudness.RequestFor(path, int(item.DurationSeconds), false))
	if !ok || plan.Zero() {
		return
	}
	item.GainDB = plan.GainDB
	item.LimitPeaks = plan.Limit
	item.CeilingDBTP = plan.CeilingDBTP
}

// mediaPathFor resolves an item to a file on disk, or "" when there is not one
// (a live station, a remote episode that has never been cached).
//
// A multi-file audiobook is represented by its first file. A book is recorded
// and mastered in one session, so one file's measurement describes the rest of
// it closely enough — and the alternative, measuring every file of a
// twenty-hour book to normalise one queue, is not a trade worth making.
func (s *Server) mediaPathFor(kind, id string) string {
	if s.catalog == nil {
		return ""
	}
	switch kind {
	case "track", "music-track", "song":
		track, err := s.catalog.MusicTrack(id)
		if err != nil {
			return ""
		}
		return firstAudioFilePath(track.AudioFiles)
	case "audiobook", "book":
		book, err := s.catalog.Audiobook(id)
		if err != nil {
			return ""
		}
		return firstAudioFilePath(book.AudioFiles)
	case "episode", "podcast-episode":
		episode, err := s.catalog.PodcastEpisode(id)
		if err != nil {
			return ""
		}
		return firstAudioFilePath(episode.AudioFiles)
	default:
		return ""
	}
}

func firstAudioFilePath(files []catalog.AudioFile) string {
	for _, file := range files {
		if strings.TrimSpace(file.Path) != "" {
			return file.Path
		}
	}
	return ""
}

func (s *Server) resolveSamoRadioItem(
	ctx context.Context,
	principal users.Principal,
	streamBase string,
	r *http.Request,
	kind, id string,
) (samoradio.Item, error) {
	escaped := url.PathEscape(id)

	switch kind {
	case "track", "music-track", "song":
		track, err := s.catalog.MusicTrack(id)
		if err != nil {
			return samoradio.Item{}, fmt.Errorf("track %s: %w", id, err)
		}
		item := samoradio.Item{
			Ref:             "track:" + id,
			Title:           track.Title,
			Subtitle:        musicTrackArtist(track),
			StreamURL:       streamBase + "/api/v1/music/tracks/" + escaped + "/stream",
			DurationSeconds: float64(track.DurationSeconds),
			Kind:            "track",
		}
		if track.AlbumID != "" {
			item.ArtworkURL = publicURL(r, "/api/v1/music/albums/"+url.PathEscape(track.AlbumID)+"/cover")
		}
		return item, nil

	case "episode", "podcast-episode":
		episode, err := s.podcastEpisodeWithUserPlayback(ctx, principal.User.ID, id)
		if err != nil {
			return samoradio.Item{}, fmt.Errorf("episode %s: %w", id, err)
		}
		item := samoradio.Item{
			Ref:             "episode:" + id,
			Title:           episode.Title,
			Subtitle:        episode.PodcastTitle,
			StreamURL:       streamBase + "/api/v1/podcasts/episodes/" + escaped + "/stream",
			DurationSeconds: float64(episode.DurationSeconds),
			Kind:            "episode",
		}
		if episode.PodcastID != "" {
			item.ArtworkURL = publicURL(r, "/api/v1/podcasts/shows/"+url.PathEscape(episode.PodcastID)+"/cover")
		}
		return item, nil

	case "audiobook", "book":
		book, err := s.catalog.Audiobook(id)
		if err != nil {
			return samoradio.Item{}, fmt.Errorf("audiobook %s: %w", id, err)
		}
		item := samoradio.Item{
			Ref:             "audiobook:" + id,
			Title:           audiobookTitle(book),
			Subtitle:        audiobookAuthor(book),
			ArtworkURL:      publicURL(r, "/api/v1/audiobooks/"+escaped+"/cover"),
			StreamURL:       streamBase + "/api/v1/audiobooks/" + escaped + "/stream",
			DurationSeconds: float64(book.DurationSeconds),
			Kind:            "audiobook",
		}
		return item, nil

	case "file", "media-file":
		return samoradio.Item{
			Ref:       "file:" + id,
			Title:     id,
			StreamURL: streamBase + "/api/v1/media/files/" + escaped + "/stream",
			Kind:      "file",
		}, nil

	case "station", "internet-radio":
		station, err := s.sourcesService().GetInternetRadioStation(ctx, id)
		if err != nil {
			return samoradio.Item{}, fmt.Errorf("station %s: %w", id, err)
		}
		return samoradio.Item{
			Ref:       "station:" + id,
			Title:     station.Name,
			Subtitle:  station.Description,
			StreamURL: streamBase + "/internet-radio/" + escaped + "/stream",
			Kind:      "station",
			// Live: no duration, no seeking, and a dropped connection is
			// retried rather than treated as the end of the item.
			Live: true,
		}, nil

	case "radio", "radio-station":
		item := samoradio.Item{
			Ref:       "radio:" + id,
			Title:     id,
			StreamURL: streamBase + "/radio/" + escaped + "/stream",
			Kind:      "radio",
			Live:      true,
		}
		if s.radio != nil {
			if station, ok := s.radio.Station(id); ok {
				item.Title = station.Name
			}
		}
		return item, nil

	default:
		return samoradio.Item{}, fmt.Errorf("unsupported item type %q", kind)
	}
}

// stationName resolves a station's display name from the catalog, falling back
// to whatever the client supplied and then to the id.
//
// The device caches this name so it can label what it is playing at boot,
// before the server has necessarily answered anything — so it is worth getting
// from the source of truth rather than trusting the client.
func (s *Server) stationName(ctx context.Context, ref samoradio.StationRef) string {
	switch ref.Kind {
	case samoradio.StationInternet:
		if s.sourcesService() != nil {
			if station, err := s.sourcesService().GetInternetRadioStation(ctx, ref.ID); err == nil {
				if strings.TrimSpace(station.Name) != "" {
					return station.Name
				}
			}
		}
	default:
		if s.channels != nil {
			if channel, err := s.channels.GetChannel(ctx, ref.ID); err == nil {
				if strings.TrimSpace(channel.Name) != "" {
					return channel.Name
				}
			}
		}
	}
	return strings.TrimSpace(ref.Name)
}

func musicTrackArtist(track catalog.MusicTrack) string {
	if strings.TrimSpace(track.DisplayArtist) != "" {
		return track.DisplayArtist
	}
	if len(track.ArtistNames) > 0 {
		return strings.Join(track.ArtistNames, ", ")
	}
	return strings.Join(track.AlbumArtistNames, ", ")
}

func audiobookTitle(book catalog.AudiobookItem) string {
	if book.Book != nil && strings.TrimSpace(book.Book.Title) != "" {
		return book.Book.Title
	}
	return book.ID
}

func audiobookAuthor(book catalog.AudiobookItem) string {
	if book.Book == nil {
		return ""
	}
	names := make([]string, 0, len(book.Book.Authors))
	for _, author := range book.Book.Authors {
		if strings.TrimSpace(author.Name) != "" {
			names = append(names, author.Name)
		}
	}
	return strings.Join(names, ", ")
}
