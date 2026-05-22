package sources

import "time"

type PodcastFeed struct {
	ID            string     `json:"id"`
	PodcastID     string     `json:"podcastId"`
	FeedURL       string     `json:"feedUrl"`
	Title         string     `json:"title"`
	Description   string     `json:"description,omitempty"`
	Author        string     `json:"author,omitempty"`
	SiteURL       string     `json:"siteUrl,omitempty"`
	ImageURL      string     `json:"imageUrl,omitempty"`
	Language      string     `json:"language,omitempty"`
	Explicit      bool       `json:"explicit,omitempty"`
	Categories    []string   `json:"categories,omitempty"`
	OwnerName     string     `json:"ownerName,omitempty"`
	OwnerEmail    string     `json:"ownerEmail,omitempty"`
	EpisodeCount  int        `json:"episodeCount"`
	Status        string     `json:"status"`
	LastError     string     `json:"lastError,omitempty"`
	LastFetchedAt *time.Time `json:"lastFetchedAt,omitempty"`
	CreatedAt     *time.Time `json:"createdAt,omitempty"`
	UpdatedAt     *time.Time `json:"updatedAt,omitempty"`
}

type AddPodcastFeedInput struct {
	URL   string `json:"url"`
	Title string `json:"title,omitempty"`
}

type InternetRadioStation struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Description   string     `json:"description,omitempty"`
	StreamURL     string     `json:"streamUrl"`
	HomepageURL   string     `json:"homepageUrl,omitempty"`
	ImageURL      string     `json:"imageUrl,omitempty"`
	ContentType   string     `json:"contentType,omitempty"`
	Codec         string     `json:"codec,omitempty"`
	Bitrate       int        `json:"bitrate,omitempty"`
	Country       string     `json:"country,omitempty"`
	Language      string     `json:"language,omitempty"`
	Tags          []string   `json:"tags,omitempty"`
	Enabled       bool       `json:"enabled"`
	LastCheckedAt *time.Time `json:"lastCheckedAt,omitempty"`
	CreatedAt     *time.Time `json:"createdAt,omitempty"`
	UpdatedAt     *time.Time `json:"updatedAt,omitempty"`
}

type AddInternetRadioStationInput struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	StreamURL   string   `json:"streamUrl"`
	HomepageURL string   `json:"homepageUrl,omitempty"`
	ImageURL    string   `json:"imageUrl,omitempty"`
	ContentType string   `json:"contentType,omitempty"`
	Codec       string   `json:"codec,omitempty"`
	Bitrate     int      `json:"bitrate,omitempty"`
	Country     string   `json:"country,omitempty"`
	Language    string   `json:"language,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Enabled     *bool    `json:"enabled,omitempty"`
}
