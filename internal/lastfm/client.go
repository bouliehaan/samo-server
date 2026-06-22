package lastfm

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const apiBaseURL = "https://ws.audioscrobbler.com/2.0/"

type Client struct {
	apiKey       string
	sharedSecret string
	http         *http.Client
	apiBaseURL   string
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
		params["duration"] = fmt.Sprintf("%d", submission.DurationSeconds)
	}
	applyRecordingID(params, submission.MusicBrainzRecording)
	return c.post(ctx, params, sessionKey, nil)
}

func (c *Client) Scrobble(ctx context.Context, sessionKey string, submission TrackSubmission) error {
	params := map[string]string{
		"method":       "track.scrobble",
		"artist[0]":    submission.Artist,
		"track[0]":     submission.Track,
		"timestamp[0]": fmt.Sprintf("%d", submission.Timestamp.Unix()),
	}
	if album := strings.TrimSpace(submission.Album); album != "" {
		params["album[0]"] = album
	}
	if submission.DurationSeconds > 0 {
		params["duration[0]"] = fmt.Sprintf("%d", submission.DurationSeconds)
	}
	applyRecordingIDIndexed(params, submission.MusicBrainzRecording, 0)
	return c.post(ctx, params, sessionKey, nil)
}

func (c *Client) LoveTrack(ctx context.Context, sessionKey string, submission TrackSubmission) error {
	params := map[string]string{
		"method": "track.love",
		"artist": submission.Artist,
		"track":  submission.Track,
	}
	return c.post(ctx, params, sessionKey, nil)
}

func (c *Client) UnloveTrack(ctx context.Context, sessionKey string, submission TrackSubmission) error {
	params := map[string]string{
		"method": "track.unlove",
		"artist": submission.Artist,
		"track":  submission.Track,
	}
	return c.post(ctx, params, sessionKey, nil)
}

func applyRecordingID(params map[string]string, recordingID string) {
	recordingID = strings.TrimSpace(recordingID)
	if recordingID != "" {
		params["trackId"] = recordingID
	}
}

func applyRecordingIDIndexed(params map[string]string, recordingID string, index int) {
	recordingID = strings.TrimSpace(recordingID)
	if recordingID != "" {
		params[fmt.Sprintf("trackId[%d]", index)] = recordingID
	}
}

func (c *Client) AuthURL(token string) string {
	values := url.Values{}
	values.Set("api_key", c.apiKey)
	values.Set("token", strings.TrimSpace(token))
	return "https://www.last.fm/api/auth/?" + values.Encode()
}

func (c *Client) post(ctx context.Context, params map[string]string, sessionKey string, out any) error {
	if !c.Enabled() {
		return ErrDisabled
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

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.requestBaseURL(), strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("last.fm http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var envelope struct {
		Error   int    `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("decode last.fm response: %w", err)
	}
	if envelope.Error != 0 {
		if envelope.Error == 4 || envelope.Error == 9 || envelope.Error == 14 {
			return ErrInvalidToken
		}
		if envelope.Error == 13 {
			return fmt.Errorf("%w: check that the API key and shared secret are a matching pair from your Last.fm application settings", ErrInvalidSignature)
		}
		if envelope.Message == "" {
			envelope.Message = fmt.Sprintf("last.fm error %d", envelope.Error)
		}
		return fmt.Errorf("%s", envelope.Message)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode last.fm payload: %w", err)
	}
	return nil
}

func signParams(secret string, params map[string]string) string {
	keys := make([]string, 0, len(params))
	for key := range params {
		if key == "format" || key == "api_sig" {
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
