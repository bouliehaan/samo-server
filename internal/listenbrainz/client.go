package listenbrainz

// The ListenBrainz wire protocol.
//
// Two endpoints carry everything: GET /1/validate-token to check a pasted
// token, and POST /1/submit-listens for both real listens and "playing now".
// Authentication is a single header, so unlike Last.fm there is nothing to
// sign and no session to expire out from under us — a token works until the
// user revokes it.
//
// The only subtlety worth naming is error CLASS. A 429 or a 5xx means try
// again later and the listen must be kept; a 400 means this payload will never
// be accepted and retrying forever would wedge the queue behind it. Everything
// downstream keys off classify().

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// userAgent identifies Samo to ListenBrainz, which asks clients to say who
// they are.
const userAgent = "SamoServer/0.1 ( https://github.com/bouliehaan/samo-server )"

const (
	// submissionClient is reported in additional_info so a listener can see
	// where a listen came from in their ListenBrainz history.
	submissionClient        = "samo-server"
	submissionClientVersion = "0.1"

	// MaxListensPerRequest is ListenBrainz's documented ceiling on one
	// submit-listens call. The queue never sends batches near this size, but
	// it is the hard bound everything below stays under.
	MaxListensPerRequest = 1000
)

// listenType values understood by submit-listens.
const (
	listenTypeSingle     = "single"
	listenTypeImport     = "import"
	listenTypePlayingNow = "playing_now"
)

// Client talks to one ListenBrainz instance as one user.
type Client struct {
	apiRoot    string
	token      string
	httpClient *http.Client
}

func NewClient(apiRoot, token string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &Client{
		apiRoot:    NormalizeAPIRoot(apiRoot),
		token:      strings.TrimSpace(token),
		httpClient: httpClient,
	}
}

// NormalizeAPIRoot trims a configured root to the form request URLs are built
// from. A blank root means the hosted instance.
func NormalizeAPIRoot(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return DefaultAPIRoot
	}
	return strings.TrimRight(root, "/")
}

// ValidateAPIRoot rejects anything that is not an absolute http(s) URL, so a
// typo becomes an error at connect time rather than a silent stream of
// undeliverable listens.
func ValidateAPIRoot(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", nil
	}
	parsed, err := url.Parse(root)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", ErrInvalidRoot
	}
	return strings.TrimRight(root, "/"), nil
}

func (c *Client) Enabled() bool {
	return c != nil && strings.TrimSpace(c.token) != ""
}

func (c *Client) APIRoot() string {
	if c == nil || c.apiRoot == "" {
		return DefaultAPIRoot
	}
	return c.apiRoot
}

// ---------------------------------------------------------------------------
// wire format
// ---------------------------------------------------------------------------

type submitRequest struct {
	ListenType string       `json:"listen_type"`
	Payload    []listenItem `json:"payload"`
}

type listenItem struct {
	// ListenedAt is omitted for playing_now, which is the one case where
	// ListenBrainz rejects the field rather than requiring it.
	ListenedAt    int64         `json:"listened_at,omitempty"`
	TrackMetadata trackMetadata `json:"track_metadata"`
}

type trackMetadata struct {
	ArtistName     string          `json:"artist_name"`
	TrackName      string          `json:"track_name"`
	ReleaseName    string          `json:"release_name,omitempty"`
	AdditionalInfo *additionalInfo `json:"additional_info,omitempty"`
}

// additionalInfo is optional enrichment. ListenBrainz asks that unknown fields
// be omitted entirely rather than sent as null, which is what every omitempty
// here is for.
type additionalInfo struct {
	MediaPlayer             string   `json:"media_player,omitempty"`
	SubmissionClient        string   `json:"submission_client,omitempty"`
	SubmissionClientVersion string   `json:"submission_client_version,omitempty"`
	RecordingMBID           string   `json:"recording_mbid,omitempty"`
	ReleaseMBID             string   `json:"release_mbid,omitempty"`
	TrackMBID               string   `json:"track_mbid,omitempty"`
	ArtistMBIDs             []string `json:"artist_mbids,omitempty"`
	TrackNumber             int      `json:"tracknumber,omitempty"`
	DurationMS              int      `json:"duration_ms,omitempty"`
	DurationPlayed          int      `json:"duration_played,omitempty"`
}

// listenFor maps one measured play onto the ListenBrainz payload.
func listenFor(submission TrackSubmission, playingNow bool) listenItem {
	info := &additionalInfo{
		MediaPlayer:             submissionClient,
		SubmissionClient:        submissionClient,
		SubmissionClientVersion: submissionClientVersion,
		RecordingMBID:           strings.TrimSpace(submission.MusicBrainzRecording),
		ReleaseMBID:             strings.TrimSpace(submission.MusicBrainzRelease),
		TrackMBID:               strings.TrimSpace(submission.MusicBrainzTrack),
		TrackNumber:             submission.TrackNumber,
	}
	if artist := strings.TrimSpace(submission.MusicBrainzArtist); artist != "" {
		info.ArtistMBIDs = []string{artist}
	}
	if submission.DurationSeconds > 0 {
		info.DurationMS = submission.DurationSeconds * 1000
	}
	// duration_played is only meaningful for a listen that actually happened,
	// and only when it does not exceed the track itself.
	if !playingNow && submission.PlayedSeconds > 0 {
		played := submission.PlayedSeconds
		if submission.DurationSeconds > 0 && played > submission.DurationSeconds {
			played = submission.DurationSeconds
		}
		info.DurationPlayed = played
	}

	item := listenItem{
		TrackMetadata: trackMetadata{
			ArtistName:     strings.TrimSpace(submission.Artist),
			TrackName:      strings.TrimSpace(submission.Track),
			ReleaseName:    strings.TrimSpace(submission.Album),
			AdditionalInfo: info,
		},
	}
	if !playingNow && !submission.Timestamp.IsZero() {
		item.ListenedAt = submission.Timestamp.Unix()
	}
	return item
}

// ---------------------------------------------------------------------------
// calls
// ---------------------------------------------------------------------------

type validateTokenResponse struct {
	Code     int    `json:"code"`
	Message  string `json:"message"`
	Valid    bool   `json:"valid"`
	UserName string `json:"user_name"`
}

// ValidateToken confirms a pasted token and returns the ListenBrainz account
// it belongs to.
//
// Note that an INVALID token still answers 200 with valid:false — the HTTP
// status alone is not the answer, which is why the body is inspected.
func (c *Client) ValidateToken(ctx context.Context) (string, error) {
	if !c.Enabled() {
		return "", ErrMissingToken
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.APIRoot()+"/1/validate-token", nil)
	if err != nil {
		return "", err
	}
	c.decorate(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("listenbrainz validate-token: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode == http.StatusUnauthorized {
		return "", ErrInvalidToken
	}
	if resp.StatusCode != http.StatusOK {
		return "", statusError(resp, body)
	}
	var parsed validateTokenResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("listenbrainz validate-token: malformed response: %w", err)
	}
	if !parsed.Valid {
		return "", ErrInvalidToken
	}
	return strings.TrimSpace(parsed.UserName), nil
}

// SubmitListens sends one or more measured listens.
func (c *Client) SubmitListens(ctx context.Context, submissions []TrackSubmission) error {
	if len(submissions) == 0 {
		return nil
	}
	listenType := listenTypeSingle
	if len(submissions) > 1 {
		listenType = listenTypeImport
	}
	payload := make([]listenItem, 0, len(submissions))
	for _, submission := range submissions {
		payload = append(payload, listenFor(submission, false))
	}
	return c.submit(ctx, submitRequest{ListenType: listenType, Payload: payload})
}

// SubmitPlayingNow announces what the user is listening to right now. It
// carries no timestamp and is never retried: by the time a retry succeeded the
// user would be on a different track.
func (c *Client) SubmitPlayingNow(ctx context.Context, submission TrackSubmission) error {
	return c.submit(ctx, submitRequest{
		ListenType: listenTypePlayingNow,
		Payload:    []listenItem{listenFor(submission, true)},
	})
}

func (c *Client) submit(ctx context.Context, request submitRequest) error {
	if !c.Enabled() {
		return ErrMissingToken
	}
	if len(request.Payload) > MaxListensPerRequest {
		return fmt.Errorf("listenbrainz: %d listens exceeds the %d per-request limit", len(request.Payload), MaxListensPerRequest)
	}
	body, err := json.Marshal(request)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.APIRoot()+"/1/submit-listens", bytes.NewReader(body))
	if err != nil {
		return err
	}
	c.decorate(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("listenbrainz submit-listens: %w", err)
	}
	defer resp.Body.Close()
	responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode == http.StatusOK {
		return nil
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return ErrInvalidToken
	}
	return statusError(resp, responseBody)
}

func (c *Client) decorate(req *http.Request) {
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
}

// ---------------------------------------------------------------------------
// errors
// ---------------------------------------------------------------------------

// APIError is a non-success reply from ListenBrainz.
type APIError struct {
	Status int
	Code   int
	Body   string
	// RetryAfter is what the server asked us to wait, from X-RateLimit-Reset-In
	// or Retry-After. Zero when it did not say.
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	message := strings.TrimSpace(e.Body)
	if message == "" {
		message = http.StatusText(e.Status)
	}
	if len(message) > 300 {
		message = message[:300]
	}
	return fmt.Sprintf("listenbrainz api error %d: %s", e.Status, message)
}

func statusError(resp *http.Response, body []byte) error {
	err := &APIError{Status: resp.StatusCode, Body: string(body)}
	// The message body is JSON when ListenBrainz produced it, and plain HTML
	// when a proxy in front of it did; only the former has a code to keep.
	var parsed struct {
		Code  int    `json:"code"`
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &parsed) == nil {
		err.Code = parsed.Code
		if strings.TrimSpace(parsed.Error) != "" {
			err.Body = parsed.Error
		}
	}
	err.RetryAfter = retryAfterFrom(resp.Header)
	return err
}

func retryAfterFrom(header http.Header) time.Duration {
	for _, name := range []string{"X-RateLimit-Reset-In", "Retry-After"} {
		raw := strings.TrimSpace(header.Get(name))
		if raw == "" {
			continue
		}
		if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
			// A server having a very bad day can ask for an implausible wait;
			// cap it so the queue is not parked for hours by one header.
			if seconds > 3600 {
				seconds = 3600
			}
			return time.Duration(seconds) * time.Second
		}
	}
	return 0
}

// failureClass says what to DO about an error, which is the only thing the
// delivery loop needs from it.
type failureClass int

const (
	// classRetry: a later attempt can succeed and the listen must be kept.
	classRetry failureClass = iota
	// classAuth: the token is bad. Keep the listen, but stop hammering.
	classAuth
	// classPermanent: this payload will never be accepted. Drop it, or the
	// queue stalls behind it forever.
	classPermanent
)

func classify(err error) failureClass {
	if err == nil {
		return classRetry
	}
	if errors.Is(err, ErrInvalidToken) || errors.Is(err, ErrMissingToken) || errors.Is(err, ErrNotConnected) {
		return classAuth
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.Status == http.StatusUnauthorized, apiErr.Status == http.StatusForbidden:
			return classAuth
		case apiErr.Status == http.StatusTooManyRequests:
			return classRetry
		case apiErr.Status >= 500:
			return classRetry
		case apiErr.Status >= 400:
			// 400 and 413 both mean "this body, as sent, is unacceptable".
			return classPermanent
		}
	}
	// Anything left is a transport failure: DNS, connection refused, timeout.
	return classRetry
}

// retryAfter extracts a server-requested delay, if the error carried one.
func retryAfter(err error) time.Duration {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.RetryAfter
	}
	return 0
}
