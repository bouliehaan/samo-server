package artistmeta

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const wikipediaSummaryURL = "https://en.wikipedia.org/api/rest_v1/page/summary/"

// musicBioKeywords gate a name-based Wikipedia hit to an actually music-related
// page. Name-only lookup is collision-prone ("Bush" the band vs. the president);
// requiring one of these terms in the page's description/extract keeps the wrong
// topic's biography from being shown. Imperfect but far better than unfiltered.
var musicBioKeywords = []string{
	"singer", "songwriter", "musician", "rapper", "band", "music",
	"composer", "producer", "guitarist", "drummer", "bassist", "pianist",
	"vocalist", "dj", "duo", "trio", "orchestra", "rock", "pop", "hip hop",
	"jazz", "metal", "punk", "folk", "country", "electronic", "discography",
}

type wikipediaSummary struct {
	Type        string `json:"type"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Extract     string `json:"extract"`
}

// wikipediaArtistBio fetches an artist biography from Wikipedia with no API key.
// It tries music-disambiguated titles first ("Name (band)", "Name (musician)")
// then the bare name, accepting a result only when it looks music-related.
func wikipediaArtistBio(ctx context.Context, client *http.Client, name string) string {
	if client == nil {
		client = http.DefaultClient
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	candidates := []string{
		name + " (band)",
		name + " (musician)",
		name + " (singer)",
		name,
	}
	for _, candidate := range candidates {
		summary, ok := fetchWikipediaSummary(ctx, client, candidate)
		if !ok {
			continue
		}
		extract := strings.TrimSpace(summary.Extract)
		if extract == "" || summary.Type == "disambiguation" {
			continue
		}
		// A disambiguated title ("… (band)") is already music-scoped; a bare
		// name must prove it's music-related to avoid wrong-topic collisions.
		if strings.Contains(candidate, "(") || looksMusicRelated(summary) {
			return extract
		}
	}
	return ""
}

func fetchWikipediaSummary(ctx context.Context, client *http.Client, title string) (wikipediaSummary, bool) {
	endpoint := wikipediaSummaryURL + url.PathEscape(strings.ReplaceAll(strings.TrimSpace(title), " ", "_"))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return wikipediaSummary{}, false
	}
	// Wikimedia asks for a descriptive UA with contact; a clear app identity is
	// the expected etiquette for the public REST summary endpoint.
	req.Header.Set("User-Agent", "Samo-Server/1.0 (self-hosted media; artist bios)")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return wikipediaSummary{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return wikipediaSummary{}, false
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return wikipediaSummary{}, false
	}
	var summary wikipediaSummary
	if err := json.Unmarshal(body, &summary); err != nil {
		return wikipediaSummary{}, false
	}
	return summary, true
}

func looksMusicRelated(summary wikipediaSummary) bool {
	haystack := strings.ToLower(summary.Description + " " + summary.Extract)
	for _, keyword := range musicBioKeywords {
		if strings.Contains(haystack, keyword) {
			return true
		}
	}
	return false
}
