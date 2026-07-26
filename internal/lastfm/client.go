package lastfm

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const apiBaseURL = "https://ws.audioscrobbler.com/2.0/"

// httpAttempts is how many times one call is tried inside a single request
// before the durable queue takes over. It only covers blips — a dropped
// connection, a momentary 502 — because anything longer is the retry loop's
// job, and holding an HTTP handler open is not.
const httpAttempts = 3

type Client struct {
	apiKey       string
	sharedSecret string
	http         *http.Client
	apiBaseURL   string
	// sleep is indirected for tests so retry backoff costs no wall time.
	sleep func(time.Duration)
}

func NewClient(apiKey, sharedSecret string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{
		apiKey:       strings.TrimSpace(apiKey),
		sharedSecret: strings.TrimSpace(sharedSecret),
		http:         httpClient,
		apiBaseURL:   apiBaseURL,
		sleep:        func(d time.Duration) { time.Sleep(d) },
	}
}

// SetAPIBaseURL overrides the Last.fm API endpoint. Intended for tests.
func (c *Client) SetAPIBaseURL(baseURL string) {
	if c == nil {
		return
	}
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		c.apiBaseURL = apiBaseURL
		return
	}
	c.apiBaseURL = baseURL
}

func (c *Client) requestBaseURL() string {
	if c == nil || strings.TrimSpace(c.apiBaseURL) == "" {
		return apiBaseURL
	}
	return c.apiBaseURL
}

func (c *Client) Enabled() bool {
	return c != nil && c.apiKey != "" && c.sharedSecret != ""
}

func (c *Client) GetToken(ctx context.Context) (string, error) {
	var payload struct {
		Token string `json:"token"`
	}
	if err := c.post(ctx, map[string]string{
		"method": "auth.getToken",
	}, "", &payload); err != nil {
		return "", err
	}
	if strings.TrimSpace(payload.Token) == "" {
		return "", ErrInvalidToken
	}
	return payload.Token, nil
}

func (c *Client) GetSession(ctx context.Context, token string) (username, sessionKey string, err error) {
	var payload struct {
		Session struct {
			Name       string `json:"name"`
			Key        string `json:"key"`
			Subscriber int    `json:"subscriber"`
		} `json:"session"`
	}
	if err := c.post(ctx, map[string]string{
		"method": "auth.getSession",
		"token":  strings.TrimSpace(token),
	}, "", &payload); err != nil {
		return "", "", err
	}
	if payload.Session.Key == "" || payload.Session.Name == "" {
		return "", "", ErrInvalidToken
	}
	return payload.Session.Name, payload.Session.Key, nil
}

func (c *Client) UpdateNowPlaying(ctx context.Context, sessionKey string, submission TrackSubmission) error {
	params := map[string]string{
		"method": "track.updateNowPlaying",
		"artist": submission.Artist,
		"track":  submission.Track,
	}
	if album := strings.TrimSpace(submission.Album); album != "" {
		params["album"] = album
	}
	if submission.DurationSeconds > 0 {
		params["duration"] = strconv.Itoa(submission.DurationSeconds)
	}
	if mbid := strings.TrimSpace(submission.MusicBrainzRecording); mbid != "" {
		params["mbid"] = mbid
	}
	// One attempt only. "Now playing" describes this instant, so a reply that
	// arrives after a round of backoff is worth nothing — and during an outage
	// the retries would pile up behind every position report a client sends.
	// The next report simply sends a fresh one.
	return c.postWithAttempts(ctx, params, sessionKey, nil, 1)
}

// Scrobble submits one play. A 200 response does NOT mean Last.fm kept it: the
// body reports per-track acceptance, and a rejected scrobble comes back as
// `ignored` with a reason. The old code never looked, so silently-dropped plays
// were indistinguishable from delivered ones.
func (c *Client) Scrobble(ctx context.Context, sessionKey string, submission TrackSubmission) error {
	params := map[string]string{
		"method":       "track.scrobble",
		"artist[0]":    submission.Artist,
		"track[0]":     submission.Track,
		"timestamp[0]": strconv.FormatInt(submission.Timestamp.Unix(), 10),
	}
	if album := strings.TrimSpace(submission.Album); album != "" {
		params["album[0]"] = album
	}
	if submission.DurationSeconds > 0 {
		params["duration[0]"] = strconv.Itoa(submission.DurationSeconds)
	}
	if mbid := strings.TrimSpace(submission.MusicBrainzRecording); mbid != "" {
		params["mbid[0]"] = mbid
	}

	var payload scrobbleResponse
	if err := c.post(ctx, params, sessionKey, &payload); err != nil {
		return err
	}
	if code, message, ignored := payload.ignoredReason(); ignored {
		return &IgnoredError{Code: code, Message: message}
	}
	return nil
}

func (c *Client) LoveTrack(ctx context.Context, sessionKey string, submission TrackSubmission) error {
	return c.post(ctx, map[string]string{
		"method": "track.love",
		"artist": submission.Artist,
		"track":  submission.Track,
	}, sessionKey, nil)
}

func (c *Client) UnloveTrack(ctx context.Context, sessionKey string, submission TrackSubmission) error {
	return c.post(ctx, map[string]string{
		"method": "track.unlove",
		"artist": submission.Artist,
		"track":  submission.Track,
	}, sessionKey, nil)
}

func (c *Client) AuthURL(token string) string {
	values := url.Values{}
	values.Set("api_key", c.apiKey)
	values.Set("token", strings.TrimSpace(token))
	return "https://www.last.fm/api/auth/?" + values.Encode()
}

// ---------------------------------------------------------------------------
// scrobble response
// ---------------------------------------------------------------------------

// scrobbleResponse decodes track.scrobble. Last.fm renders `scrobble` as an
// object for one track and an array for several, and stringifies numbers
// inconsistently, so every field is read leniently.
type scrobbleResponse struct {
	Scrobbles struct {
		Attr struct {
			Accepted json.Number `json:"accepted"`
			Ignored  json.Number `json:"ignored"`
		} `json:"@attr"`
		Scrobble json.RawMessage `json:"scrobble"`
	} `json:"scrobbles"`
}

type scrobbleEntry struct {
	IgnoredMessage struct {
		Code string `json:"code"`
		Text string `json:"#text"`
	} `json:"ignoredMessage"`
}

// ignoredReason returns the rejection Last.fm reported, if any.
func (r scrobbleResponse) ignoredReason() (code int, message string, ignored bool) {
	entries := make([]scrobbleEntry, 0, 1)
	if raw := r.Scrobbles.Scrobble; len(raw) > 0 {
		var one scrobbleEntry
		if err := json.Unmarshal(raw, &one); err == nil {
			entries = append(entries, one)
		} else {
			var many []scrobbleEntry
			if err := json.Unmarshal(raw, &many); err == nil {
				entries = append(entries, many...)
			}
		}
	}
	for _, entry := range entries {
		parsed, _ := strconv.Atoi(strings.TrimSpace(entry.IgnoredMessage.Code))
		if parsed != 0 {
			return parsed, strings.TrimSpace(entry.IgnoredMessage.Text), true
		}
	}
	// No per-track detail: fall back to the summary counts. `accepted` is
	// absent on responses that predate it, so only a positive `ignored` with a
	// zero `accepted` counts as a rejection.
	accepted, _ := strconv.Atoi(r.Scrobbles.Attr.Accepted.String())
	ignoredCount, _ := strconv.Atoi(r.Scrobbles.Attr.Ignored.String())
	if ignoredCount > 0 && accepted == 0 {
		return 0, "last.fm ignored the scrobble", true
	}
	return 0, "", false
}

// IgnoredError is a scrobble Last.fm accepted the request for but declined to
// record. Codes: 1 artist ignored, 2 track ignored, 3 timestamp too old,
// 4 timestamp too new, 5 daily scrobble limit exceeded.
type IgnoredError struct {
	Code    int
	Message string
}

func (e *IgnoredError) Error() string {
	message := e.Message
	if message == "" {
		message = "last.fm ignored the scrobble"
	}
	return fmt.Sprintf("last.fm ignored scrobble (code %d): %s", e.Code, message)
}

// APIError is a Last.fm application-level error, carrying the numeric code the
// retry policy needs. See https://www.last.fm/api/errorcodes.
type APIError struct {
	Code    int
	Message string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("last.fm error %d", e.Code)
	}
	return fmt.Sprintf("last.fm error %d: %s", e.Code, e.Message)
}

// httpError is a non-2xx HTTP response.
type httpError struct {
	Status int
	Body   string
}

func (e *httpError) Error() string {
	return fmt.Sprintf("last.fm http %d: %s", e.Status, e.Body)
}

// ---------------------------------------------------------------------------
// failure classification
// ---------------------------------------------------------------------------

// failureClass decides what the retry loop does with an error.
type failureClass int

const (
	// classTransient will succeed later: network trouble, 5xx, rate limits,
	// Last.fm maintenance.
	classTransient failureClass = iota
	// classAuth means the stored session key is no longer usable. The work
	// stays queued and goes out once the user reconnects.
	classAuth
	// classConfig means the API key/secret pair is wrong or suspended. An
	// operator can fix it, so retry — slowly.
	classConfig
	// classPermanent will never succeed. Drop it rather than retry forever.
	classPermanent
)

// isSessionRejection reports whether Last.fm refused the stored session key, as
// opposed to the server merely being unconfigured or the account unlinked.
// Only a genuine rejection may unlink the user's account.
func isSessionRejection(err error) bool {
	return errors.Is(err, ErrInvalidToken) || errors.Is(err, ErrSessionExpired)
}

func classify(err error) failureClass {
	if err == nil {
		return classTransient
	}
	if isSessionRejection(err) || errors.Is(err, ErrNotConnected) {
		return classAuth
	}
	if errors.Is(err, ErrDisabled) {
		// The server has no credentials right now. An operator can add them, so
		// hold the work — and never treat this as the user's session going bad.
		return classConfig
	}
	if errors.Is(err, ErrInvalidSignature) || errors.Is(err, ErrMissingMetadata) {
		if errors.Is(err, ErrMissingMetadata) {
			return classPermanent
		}
		return classConfig
	}

	var ignored *IgnoredError
	if errors.As(err, &ignored) {
		if ignored.Code == 5 {
			// Daily scrobble limit: try again once the day rolls over.
			return classTransient
		}
		return classPermanent
	}

	var apiErr *APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case 4, 9, 14, 15: // auth failed / invalid session key / bad token
			return classAuth
		case 10, 13, 26: // invalid, mis-signed, or suspended API key
			return classConfig
		case 2, 3, 5, 6, 7: // malformed request; retrying changes nothing
			return classPermanent
		default: // 8 operation failed, 11 offline, 16 unavailable, 29 rate limit
			return classTransient
		}
	}

	var httpErr *httpError
	if errors.As(err, &httpErr) {
		if httpErr.Status >= 400 && httpErr.Status < 500 && httpErr.Status != http.StatusTooManyRequests {
			return classPermanent
		}
		return classTransient
	}
	return classTransient
}

// retryableNow reports whether an error is worth an immediate in-request retry.
func retryableNow(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		// Application-level errors are not fixed by trying again at once; let
		// the backoff schedule handle them.
		return false
	}
	var ignored *IgnoredError
	if errors.As(err, &ignored) {
		return false
	}
	return classify(err) == classTransient
}

// ---------------------------------------------------------------------------
// transport
// ---------------------------------------------------------------------------

func (c *Client) post(ctx context.Context, params map[string]string, sessionKey string, out any) error {
	return c.postWithAttempts(ctx, params, sessionKey, out, httpAttempts)
}

func (c *Client) postWithAttempts(ctx context.Context, params map[string]string, sessionKey string, out any, attempts int) error {
	if !c.Enabled() {
		return ErrDisabled
	}
	if attempts < 1 {
		attempts = 1
	}
	if params == nil {
		params = map[string]string{}
	}
	params["api_key"] = c.apiKey
	params["format"] = "json"
	if sessionKey != "" {
		params["sk"] = sessionKey
	}
	params["api_sig"] = signParams(c.sharedSecret, params)

	form := url.Values{}
	for key, value := range params {
		form.Set(key, value)
	}
	body := form.Encode()

	var err error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			c.sleepFor(time.Duration(attempt) * 300 * time.Millisecond)
		}
		err = c.postOnce(ctx, body, out)
		if err == nil || !retryableNow(err) || ctx.Err() != nil {
			return err
		}
	}
	return err
}

func (c *Client) sleepFor(d time.Duration) {
	if c.sleep != nil {
		c.sleep(d)
		return
	}
	time.Sleep(d)
}

func (c *Client) postOnce(ctx context.Context, body string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.requestBaseURL(), strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &httpError{Status: resp.StatusCode, Body: strings.TrimSpace(string(payload))}
	}

	var envelope struct {
		Error   int    `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return fmt.Errorf("decode last.fm response: %w", err)
	}
	if envelope.Error != 0 {
		return wrapAPIError(envelope.Error, strings.TrimSpace(envelope.Message))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("decode last.fm payload: %w", err)
	}
	return nil
}

// wrapAPIError keeps the sentinel errors callers already match on while adding
// the numeric code the retry policy needs.
func wrapAPIError(code int, message string) error {
	apiErr := &APIError{Code: code, Message: message}
	switch code {
	case 4, 9, 14, 15:
		return fmt.Errorf("%w: %s", ErrInvalidToken, apiErr)
	case 13:
		return fmt.Errorf("%w: check that the API key and shared secret are a matching pair from your Last.fm application settings (%s)", ErrInvalidSignature, apiErr)
	}
	return apiErr
}

func signParams(secret string, params map[string]string) string {
	keys := make([]string, 0, len(params))
	for key := range params {
		if key == "format" || key == "api_sig" || key == "callback" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	// Last.fm signature: concatenate name+value for each param in ascending key
	// order (excluding `format`/`callback`/`api_sig`), then append the shared
	// secret ONCE at the END, then md5. A leading secret too (the old bug) makes
	// every signed call fail with error 13 "Invalid method signature supplied".
	var builder strings.Builder
	for _, key := range keys {
		builder.WriteString(key)
		builder.WriteString(params[key])
	}
	builder.WriteString(secret)

	sum := md5.Sum([]byte(builder.String()))
	return hex.EncodeToString(sum[:])
}
