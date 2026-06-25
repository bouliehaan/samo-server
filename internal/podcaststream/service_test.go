package podcaststream

import (
	"context"
	"errors"
	"fmt"
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

func TestServeEnclosureRejectsHTMLErrorPage(t *testing.T) {
	// Premium/expired private feeds often return HTTP 200 with an HTML paywall
	// or error page. Streaming that as audio surfaced the misleading ExoPlayer
	// "no supported source was found". The proxy must refuse it with a real
	// error instead, even when the stored enclosure type claims audio.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><body>Members only</body></html>"))
	}))
	defer upstream.Close()

	service := New(ServiceOptions{AllowPrivateHosts: true})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	err := service.ServeEnclosure(context.Background(), Enclosure{
		URL:         upstream.URL,
		ContentType: "audio/mpeg",
	}, rec, req)
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("err = %v, want ErrUpstream", err)
	}
	if !strings.Contains(err.Error(), "non-audio") {
		t.Fatalf("err = %v, want a non-audio message", err)
	}
}

func TestServeEnclosurePrefersUpstreamAudioContentType(t *testing.T) {
	// RSS feeds frequently declare the wrong enclosure type. When the CDN
	// reports a real audio type, that wins over the stored one so the client
	// (which sniffs/derives from Content-Type) sees the correct container.
	payload := []byte("0123456789")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mp4")
		_, _ = w.Write(payload)
	}))
	defer upstream.Close()

	service := New(ServiceOptions{AllowPrivateHosts: true})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	if err := service.ServeEnclosure(context.Background(), Enclosure{
		URL:         upstream.URL,
		ContentType: "video/mp4", // wrong, stored from the feed
	}, rec, req); err != nil {
		t.Fatal(err)
	}
	if got := rec.Header().Get("Content-Type"); got != "audio/mp4" {
		t.Fatalf("Content-Type = %q, want audio/mp4 (CDN type should win)", got)
	}
}

func TestServeEnclosureFollowsAdChainRedirects(t *testing.T) {
	// Real podcast enclosures route through stacked ad/analytics redirectors
	// before the audio host — Audioboom and Megaphone shows measure 6-7 hops
	// today. The old cap of 5 failed every episode of those shows with "too
	// many redirects"; this locks the cap comfortably above real-world chains.
	const hops = 9
	payload := []byte("ID3fakeaudiopayload")
	mux := http.NewServeMux()
	upstream := httptest.NewServer(mux)
	defer upstream.Close()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		var hop int
		_, _ = fmt.Sscanf(r.URL.Path, "/hop/%d", &hop)
		if r.URL.Path == "/audio.mp3" {
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = w.Write(payload)
			return
		}
		next := fmt.Sprintf("%s/hop/%d", upstream.URL, hop+1)
		if hop == hops {
			next = upstream.URL + "/audio.mp3"
		}
		http.Redirect(w, r, next, http.StatusFound)
	})

	service := New(ServiceOptions{AllowPrivateHosts: true})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	if err := service.ServeEnclosure(context.Background(), Enclosure{
		URL:         upstream.URL + "/hop/1",
		ContentType: "audio/mpeg",
	}, rec, req); err != nil {
		t.Fatalf("redirect chain of %d hops should be followed: %v", hops, err)
	}
	if !bytesEqual(rec.Body.Bytes(), payload) {
		t.Fatalf("body = %q, want proxied audio payload", rec.Body.Bytes())
	}
}
