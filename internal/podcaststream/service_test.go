package podcaststream

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServeEnclosureResumesWhenRSSSizeMissing(t *testing.T) {
	payload := []byte("0123456789abcdefghij")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodHead:
			w.Header().Set("Content-Length", "20")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Header.Get("Range") != "bytes=10-" {
			t.Fatalf("range = %q", r.Header.Get("Range"))
		}
		w.Header().Set("Content-Range", "bytes 10-19/20")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[10:])
	}))
	defer upstream.Close()

	service := New(ServiceOptions{AllowPrivateHosts: true})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	err := service.ServeEnclosure(context.Background(), Enclosure{
		URL:             upstream.URL,
		ContentType:     "audio/mpeg",
		DurationSeconds: 10,
		OffsetSeconds:   5,
	}, rec, req)
	if err != nil {
		t.Fatal(err)
	}
	if !bytesEqual(rec.Body.Bytes(), payload[10:]) {
		t.Fatalf("body = %q", rec.Body.Bytes())
	}
}

func TestServeEnclosureSkipsWhenUpstreamIgnoresRange(t *testing.T) {
	payload := []byte("0123456789abcdefghij")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", "20")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Header.Get("Range") != "bytes=10-" {
			t.Fatalf("range = %q", r.Header.Get("Range"))
		}
		w.Header().Set("Content-Length", "20")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	defer upstream.Close()

	service := New(ServiceOptions{AllowPrivateHosts: true})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	err := service.ServeEnclosure(context.Background(), Enclosure{
		URL:             upstream.URL,
		ContentType:     "audio/mpeg",
		DurationSeconds: 10,
		OffsetSeconds:   5,
	}, rec, req)
	if err != nil {
		t.Fatal(err)
	}
	if !bytesEqual(rec.Body.Bytes(), payload[10:]) {
		t.Fatalf("body = %q", rec.Body.Bytes())
	}
}

func TestServeEnclosureProxiesBytes(t *testing.T) {
	payload := []byte("0123456789abcdefghij")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			w.Header().Set("Content-Range", "bytes 10-19/20")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(payload[10:])
			return
		}
		w.Header().Set("Content-Length", "20")
		_, _ = w.Write(payload)
	}))
	defer upstream.Close()

	service := New(ServiceOptions{AllowPrivateHosts: true})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	err := service.ServeEnclosure(context.Background(), Enclosure{
		URL:             upstream.URL,
		ContentType:     "audio/mpeg",
		SizeBytes:       int64(len(payload)),
		DurationSeconds: 10,
		OffsetSeconds:   5,
	}, rec, req)
	if err != nil {
		t.Fatal(err)
	}
	if got := rec.Header().Get("X-Samo-Stream-Source"); got != "enclosure" {
		t.Fatalf("source header = %q", got)
	}
	if !bytesEqual(rec.Body.Bytes(), payload[10:]) {
		t.Fatalf("body = %q, want tail bytes", rec.Body.Bytes())
	}
}

func TestValidateEnclosureURLRejectsLoopback(t *testing.T) {
	if _, err := validateEnclosureURL("http://127.0.0.1/podcast.mp3"); err != ErrForbiddenEnclosure {
		t.Fatalf("err = %v, want forbidden", err)
	}
}

func TestValidateEnclosureURLAllowsHTTPS(t *testing.T) {
	if _, err := validateEnclosureURL("https://cdn.example.com/ep.mp3"); err != nil {
		t.Fatal(err)
	}
}

func bytesEqual(a, b []byte) bool {
	return string(a) == string(b)
}

func TestServeEnclosureForwardsClientRange(t *testing.T) {
	payload := []byte("0123456789")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "bytes=2-5" {
			t.Fatalf("range = %q", r.Header.Get("Range"))
		}
		w.Header().Set("Content-Range", "bytes 2-5/10")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[2:6])
	}))
	defer upstream.Close()

	service := New(ServiceOptions{AllowPrivateHosts: true})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	req.Header.Set("Range", "bytes=2-5")
	if err := service.ServeEnclosure(context.Background(), Enclosure{URL: upstream.URL}, rec, req); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if !bytesEqual(body, payload[2:6]) {
		t.Fatalf("body = %q", body)
	}
}

func TestServeEnclosureUsesHeadForClientHead(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Fatalf("method = %s, want HEAD", r.Method)
		}
		w.Header().Set("Content-Length", "20")
		w.Header().Set("Content-Type", "audio/mpeg")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	service := New(ServiceOptions{AllowPrivateHosts: true})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodHead, "/stream", nil)
	if err := service.ServeEnclosure(context.Background(), Enclosure{
		URL:         upstream.URL,
		ContentType: "audio/mpeg",
		SizeBytes:   20,
	}, rec, req); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("body = %q, want empty", rec.Body.Bytes())
	}
}

func TestFetchEnclosureErrorsWhenUnknownLengthExceedsMax(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		_, _ = w.Write([]byte("0123456789"))
	}))
	defer upstream.Close()

	service := New(ServiceOptions{AllowPrivateHosts: true})
	body, _, err := service.FetchEnclosure(context.Background(), upstream.URL, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	_, err = io.Copy(io.Discard, body)
	if err == nil || !strings.Contains(err.Error(), "exceeds max cache file size") {
		t.Fatalf("err = %v, want max size error", err)
	}
}
