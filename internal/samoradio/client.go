package samoradio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// deviceClient speaks the samo-radio daemon's control API.
//
// Every call is short and JSON — audio never passes through here. The device
// pulls its own audio straight from Samo's stream endpoints, so a slow control
// call can delay a button press but can never interrupt playback.
type deviceClient struct {
	baseURL string
	token   string
	http    *http.Client
}

func newDeviceClient(device Device, httpClient *http.Client) *deviceClient {
	return &deviceClient{
		baseURL: strings.TrimRight(device.BaseURL, "/"),
		token:   strings.TrimSpace(device.controlToken),
		http:    httpClient,
	}
}

// deviceError carries the device's own message so the UI can show "no such
// channel" rather than a bare 502.
type deviceError struct {
	Status  int
	Message string
}

func (e *deviceError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("samo-radio device returned %d", e.Status)
	}
	return e.Message
}

func (c *deviceClient) do(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}
	request.Header.Set("Accept", "application/json")

	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		_ = response.Body.Close()
	}()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		payload := struct {
			Error string `json:"error"`
		}{}
		raw, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		_ = json.Unmarshal(raw, &payload)
		message := strings.TrimSpace(payload.Error)
		if message == "" {
			message = fmt.Sprintf("samo-radio device returned %s", response.Status)
		}
		return &deviceError{Status: response.StatusCode, Message: message}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(response.Body).Decode(out)
}

// defaultHTTPClient is shared by every device conversation.
//
// The timeout is short on purpose: an unreachable device must not hold a
// dashboard request open. A device that has been unplugged is a normal state,
// not an outage.
func defaultHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 6 * time.Second,
		Transport: &http.Transport{
			MaxIdleConnsPerHost:   4,
			IdleConnTimeout:       60 * time.Second,
			ResponseHeaderTimeout: 5 * time.Second,
		},
	}
}
