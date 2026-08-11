package samoradio

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// TokenMinter issues and revokes the Samo API token a device carries.
//
// A narrow interface rather than a dependency on internal/users: this package
// has no other reason to know Samo has accounts, and the api layer already
// holds the users service it can adapt.
type TokenMinter interface {
	IssueDeviceToken(ctx context.Context, label string) (tokenID, secret, userID string, err error)
	RevokeDeviceToken(ctx context.Context, userID, tokenID string) error
}

// Service owns registered devices and the conversation with them.
type Service struct {
	db     *sql.DB
	tokens TokenMinter
	http   *http.Client
}

// ServiceOptions wires the service up.
type ServiceOptions struct {
	DB     *sql.DB
	Tokens TokenMinter
	HTTP   *http.Client
}

// NewService builds the service. A nil DB yields a service whose calls all
// report ErrDisabled, which keeps every handler's nil-check identical.
func NewService(opts ServiceOptions) *Service {
	client := opts.HTTP
	if client == nil {
		client = defaultHTTPClient()
	}
	return &Service{db: opts.DB, tokens: opts.Tokens, http: client}
}

// Enabled reports whether the service has storage behind it.
func (s *Service) Enabled() bool { return s != nil && s.db != nil }

func (s *Service) ready() error {
	if !s.Enabled() {
		return ErrDisabled
	}
	return nil
}

// ----- device management ------------------------------------------------

// ListDevices returns every device, each with its live state where reachable.
//
// States are fetched concurrently and failures are folded into the row rather
// than failing the call: a device that is powered off is a normal thing for
// this list to contain, and one unreachable device must not blank the others.
func (s *Service) ListDevices(ctx context.Context) ([]Device, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	devices, err := listDevices(ctx, s.db)
	if err != nil {
		return nil, err
	}

	var wg sync.WaitGroup
	for index := range devices {
		if !devices[index].Enabled {
			continue
		}
		wg.Add(1)
		go func(target *Device) {
			defer wg.Done()
			state, err := s.fetchState(ctx, *target)
			if err != nil {
				target.LastError = err.Error()
				return
			}
			target.State = &state
		}(&devices[index])
	}
	wg.Wait()
	return devices, nil
}

// GetDevice returns one device with its live state.
func (s *Service) GetDevice(ctx context.Context, id string) (Device, error) {
	if err := s.ready(); err != nil {
		return Device{}, err
	}
	device, err := getDevice(ctx, s.db, id)
	if err != nil {
		return Device{}, err
	}
	if device.Enabled {
		if state, stateErr := s.fetchState(ctx, device); stateErr == nil {
			device.State = &state
		} else {
			device.LastError = stateErr.Error()
		}
	}
	return device, nil
}

// CreateDevice registers a device without pairing it.
func (s *Service) CreateDevice(ctx context.Context, input CreateDeviceInput) (Device, error) {
	if err := s.ready(); err != nil {
		return Device{}, err
	}
	return insertDevice(ctx, s.db, input)
}

// UpdateDevice edits a device's registration.
func (s *Service) UpdateDevice(ctx context.Context, id string, input UpdateDeviceInput) (Device, error) {
	if err := s.ready(); err != nil {
		return Device{}, err
	}
	return updateDevice(ctx, s.db, id, input)
}

// DeleteDevice removes a device and revokes the token it was given.
//
// Revocation is best-effort and happens first: if the row disappeared but the
// credential lived on, a device somebody threw in a drawer would keep a working
// key to the library.
func (s *Service) DeleteDevice(ctx context.Context, id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	device, err := getDevice(ctx, s.db, id)
	if err != nil {
		return err
	}
	if s.tokens != nil && device.tokenID != "" {
		_ = s.tokens.RevokeDeviceToken(ctx, device.tokenUserID, device.tokenID)
	}
	return deleteDevice(ctx, s.db, device.ID)
}

// PairOptions describe how the device should reach Samo back.
type PairOptions struct {
	// StreamBaseURL overrides the stored one for this pairing. Typically
	// http://127.0.0.1:6969 — the device is on this machine.
	StreamBaseURL string
	ServerName    string
}

// Pair mints a Samo token for the device and hands it over.
//
// The device needs a credential that outlives the pairing request: it holds a
// channel open for weeks and has to re-authenticate by itself after a reboot.
// So this is a real API token, stored on the device, revoked when the device is
// deleted — not a short-lived stream token.
func (s *Service) Pair(ctx context.Context, id string, opts PairOptions) (Device, error) {
	if err := s.ready(); err != nil {
		return Device{}, err
	}
	if s.tokens == nil {
		return Device{}, fmt.Errorf("%w: user accounts are required to pair a device", ErrInvalid)
	}
	device, err := getDevice(ctx, s.db, id)
	if err != nil {
		return Device{}, err
	}

	streamBaseURL := strings.TrimRight(strings.TrimSpace(opts.StreamBaseURL), "/")
	if streamBaseURL == "" {
		streamBaseURL = device.StreamBaseURL
	}
	if streamBaseURL == "" {
		return Device{}, fmt.Errorf("%w: the device needs a URL to reach Samo on", ErrInvalid)
	}
	if streamBaseURL, err = normalizeBaseURL(streamBaseURL); err != nil {
		return Device{}, err
	}

	tokenID, secret, tokenUserID, err := s.tokens.IssueDeviceToken(ctx, "samo-radio: "+device.Name)
	if err != nil {
		return Device{}, fmt.Errorf("issue device token: %w", err)
	}

	client := newDeviceClient(device, s.http)
	payload := map[string]string{
		"serverUrl":  streamBaseURL,
		"token":      secret,
		"serverName": strings.TrimSpace(opts.ServerName),
		"deviceName": device.Name,
	}
	if err := client.do(ctx, http.MethodPost, "/v1/pair", payload, nil); err != nil {
		// The device rejected it, so nothing out there is using this token.
		// Leaving it live would litter the account with dead credentials.
		_ = s.tokens.RevokeDeviceToken(ctx, tokenUserID, tokenID)
		markError(ctx, s.db, device.ID, err.Error())
		return Device{}, err
	}

	// The token that was just accepted replaces any earlier one.
	if device.tokenID != "" && device.tokenID != tokenID {
		_ = s.tokens.RevokeDeviceToken(ctx, device.tokenUserID, device.tokenID)
	}
	if _, err := updateDevice(ctx, s.db, device.ID, UpdateDeviceInput{StreamBaseURL: &streamBaseURL}); err != nil {
		return Device{}, err
	}
	if err := setDeviceToken(ctx, s.db, device.ID, tokenID, tokenUserID); err != nil {
		return Device{}, err
	}
	markReachable(ctx, s.db, device.ID)
	return s.GetDevice(ctx, device.ID)
}

// ----- transport --------------------------------------------------------

// PlayRequest is what a client asks a device to play.
type PlayRequest struct {
	Mode        string `json:"mode"`
	ChannelID   string `json:"channelId,omitempty"`
	ChannelName string `json:"channelName,omitempty"`
	StationID   string `json:"stationId,omitempty"`
	StationName string `json:"stationName,omitempty"`
	Items       []Item `json:"items,omitempty"`
	StartIndex  int    `json:"startIndex,omitempty"`
	// Append adds to a running queue instead of replacing it, so sending two
	// albums in a row builds a queue rather than cutting the first one off.
	Append bool `json:"append,omitempty"`
}

// Play sends a channel or a queue to a device.
func (s *Service) Play(ctx context.Context, id string, request PlayRequest) (State, error) {
	device, client, err := s.clientFor(ctx, id)
	if err != nil {
		return State{}, err
	}
	// Append only means anything for a queue: tuning a station replaces
	// whatever was on, which is what tuning means.
	path := "/v1/play"
	if request.Append && !isTuneMode(request.Mode) {
		path = "/v1/enqueue"
	}
	var state State
	if err := client.do(ctx, http.MethodPost, path, request, &state); err != nil {
		markError(ctx, s.db, device.ID, err.Error())
		return State{}, err
	}
	markReachable(ctx, s.db, device.ID)
	return state, nil
}

// Command runs a no-argument transport action.
// isTuneMode reports whether a play request tunes a station rather than
// queueing items.
func isTuneMode(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case StationChannel, StationInternet:
		return true
	default:
		return false
	}
}

func (s *Service) Command(ctx context.Context, id, action string) (State, error) {
	switch action {
	// "next" and "next-kind" mean "move the programming on" when the device is
	// tuned to a channel, and the device is the right place to send them even
	// though the decision is the server's: it forwards the skip to the channel
	// AND drops the seconds of audio it has already pulled. Calling the channel
	// endpoint directly does only the first, which is why a skip used to be
	// followed by several more seconds of the thing you skipped.
	case "pause", "resume", "next", "next-kind", "previous", "stop", "standby":
	default:
		return State{}, fmt.Errorf("%w: unknown action %q", ErrInvalid, action)
	}
	return s.post(ctx, id, "/v1/"+action, nil)
}

// Seek jumps within the current item.
func (s *Service) Seek(ctx context.Context, id string, positionSeconds float64) (State, error) {
	return s.post(ctx, id, "/v1/seek", map[string]float64{"positionSeconds": positionSeconds})
}

// SetVolume sets the device's output level.
func (s *Service) SetVolume(ctx context.Context, id string, volume float64) (State, error) {
	return s.post(ctx, id, "/v1/volume", map[string]float64{"volume": volume})
}

// UpdateSettings changes device-side settings (output, default channel).
func (s *Service) UpdateSettings(ctx context.Context, id string, input SettingsInput) (State, error) {
	device, client, err := s.clientFor(ctx, id)
	if err != nil {
		return State{}, err
	}
	var state State
	if err := client.do(ctx, http.MethodPatch, "/v1/settings", input, &state); err != nil {
		markError(ctx, s.db, device.ID, err.Error())
		return State{}, err
	}
	markReachable(ctx, s.db, device.ID)
	return state, nil
}

// State fetches a device's current status.
func (s *Service) State(ctx context.Context, id string) (State, error) {
	device, err := s.deviceFor(ctx, id)
	if err != nil {
		return State{}, err
	}
	state, err := s.fetchState(ctx, device)
	if err != nil {
		markError(ctx, s.db, device.ID, err.Error())
		return State{}, err
	}
	markReachable(ctx, s.db, device.ID)
	return state, nil
}

// Outputs lists the audio devices the machine can play to.
func (s *Service) Outputs(ctx context.Context, id, backend string) (Outputs, error) {
	device, client, err := s.clientFor(ctx, id)
	if err != nil {
		return Outputs{}, err
	}
	path := "/v1/outputs"
	if strings.TrimSpace(backend) != "" {
		path += "?backend=" + strings.TrimSpace(backend)
	}
	var outputs Outputs
	if err := client.do(ctx, http.MethodGet, path, nil, &outputs); err != nil {
		markError(ctx, s.db, device.ID, err.Error())
		return Outputs{}, err
	}
	return outputs, nil
}

// StreamBaseURL is where the given device pulls audio from, used by the API
// layer when it builds stream URLs to send.
func (s *Service) StreamBaseURL(ctx context.Context, id string) (string, error) {
	device, err := s.deviceFor(ctx, id)
	if err != nil {
		return "", err
	}
	return device.StreamBaseURL, nil
}

func (s *Service) post(ctx context.Context, id, path string, body any) (State, error) {
	device, client, err := s.clientFor(ctx, id)
	if err != nil {
		return State{}, err
	}
	var state State
	if err := client.do(ctx, http.MethodPost, path, body, &state); err != nil {
		markError(ctx, s.db, device.ID, err.Error())
		return State{}, err
	}
	markReachable(ctx, s.db, device.ID)
	return state, nil
}

func (s *Service) deviceFor(ctx context.Context, id string) (Device, error) {
	if err := s.ready(); err != nil {
		return Device{}, err
	}
	device, err := getDevice(ctx, s.db, id)
	if err != nil {
		return Device{}, err
	}
	if !device.Enabled {
		return Device{}, fmt.Errorf("%w: device is disabled", ErrInvalid)
	}
	return device, nil
}

func (s *Service) clientFor(ctx context.Context, id string) (Device, *deviceClient, error) {
	device, err := s.deviceFor(ctx, id)
	if err != nil {
		return Device{}, nil, err
	}
	return device, newDeviceClient(device, s.http), nil
}

// fetchState is the one call made on every dashboard render, so it gets a
// tighter deadline than the shared client's: a device that is slow to answer
// should degrade to "unreachable" quickly rather than stall the page.
func (s *Service) fetchState(ctx context.Context, device Device) (State, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var state State
	if err := newDeviceClient(device, s.http).do(ctx, http.MethodGet, "/v1/state", nil, &state); err != nil {
		return State{}, err
	}
	return state, nil
}
