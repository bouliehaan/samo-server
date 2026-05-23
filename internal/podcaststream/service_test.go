package podcaststream

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
