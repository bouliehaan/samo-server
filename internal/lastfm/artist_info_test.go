package lastfm

import "testing"

func TestPickLastFMArtistImageURLSkipsPlaceholder(t *testing.T) {
	images := []struct {
		Text string `json:"#text"`
		Size string `json:"size"`
	}{
		{Text: "https://lastfm.freetls.fastly.net/i/u/300x300/2a96cbd8b46e442fc41c2b86b821562f.png", Size: "large"},
		{Text: "https://lastfm.freetls.fastly.net/i/u/300x300/action-bronson.jpg", Size: "large"},
	}
	got := pickLastFMArtistImageURL(images)
	want := "https://lastfm.freetls.fastly.net/i/u/300x300/action-bronson.jpg"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestPickLastFMArtistImageURLPrefersLargerSize(t *testing.T) {
	images := []struct {
		Text string `json:"#text"`
		Size string `json:"size"`
	}{
		{Text: "https://example.com/small.jpg", Size: "small"},
		{Text: "https://example.com/large.jpg", Size: "large"},
	}
	got := pickLastFMArtistImageURL(images)
	if got != "https://example.com/large.jpg" {
		t.Fatalf("got %q", got)
	}
}
