package sources

import (
	"encoding/xml"
	"errors"
	"html"
	"io"
	"strconv"
	"strings"
	"time"
)

const itunesNamespace = "http://www.itunes.com/dtds/podcast-1.0.dtd"

type parsedPodcastFeed struct {
	Title       string
	Description string
	Author      string
	SiteURL     string
	ImageURL    string
	Language    string
	Explicit    bool
	Categories  []string
	OwnerName   string
	OwnerEmail  string
	Episodes    []parsedPodcastEpisode
}

type parsedPodcastEpisode struct {
	Title           string
	Subtitle        string
	Description     string
	Link            string
	GUID            string
	PublishedAt     *time.Time
	Season          int
	Episode         int
	EpisodeType     string
	DurationSeconds int
	Explicit        bool
	EnclosureURL    string
	EnclosureType   string
	EnclosureBytes  int64
	ImageURL        string
}

type rssDocument struct {
	XMLName xml.Name   `xml:"rss"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title            string        `xml:"title"`
	Link             string        `xml:"link"`
	Description      string        `xml:"description"`
	Language         string        `xml:"language"`
	Author           string        `xml:"author"`
	ITunesAuthor     string        `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd author"`
	Explicit         string        `xml:"explicit"`
	ITunesExplicit   string        `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd explicit"`
	Owner            rssOwner      `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd owner"`
	Image            rssImage      `xml:"image"`
	ITunesImage      itunesImage   `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd image"`
	Categories       []rssCategory `xml:"category"`
	ITunesCategories []rssCategory `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd category"`
	Items            []rssItem     `xml:"item"`
}

type rssOwner struct {
	Name  string `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd name"`
	Email string `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd email"`
}

type rssImage struct {
	URL string `xml:"url"`
}

type itunesImage struct {
	Href string `xml:"href,attr"`
}

type rssCategory struct {
	Value    string        `xml:",chardata"`
	Text     string        `xml:"text,attr"`
	Children []rssCategory `xml:"category"`
}

type rssItem struct {
	Title             string        `xml:"title"`
	Subtitle          string        `xml:"subtitle"`
	ITunesSubtitle    string        `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd subtitle"`
	Description       string        `xml:"description"`
	Summary           string        `xml:"summary"`
	ITunesSummary     string        `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd summary"`
	Link              string        `xml:"link"`
	GUID              rssGUID       `xml:"guid"`
	PubDate           string        `xml:"pubDate"`
	Author            string        `xml:"author"`
	ITunesAuthor      string        `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd author"`
	Duration          string        `xml:"duration"`
	ITunesDuration    string        `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd duration"`
	Season            string        `xml:"season"`
	ITunesSeason      string        `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd season"`
	Episode           string        `xml:"episode"`
	ITunesEpisode     string        `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd episode"`
	EpisodeType       string        `xml:"episodeType"`
	ITunesEpisodeType string        `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd episodeType"`
	Explicit          string        `xml:"explicit"`
	ITunesExplicit    string        `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd explicit"`
	Enclosure         rssEnclosure  `xml:"enclosure"`
	ITunesImage       itunesImage   `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd image"`
	Categories        []rssCategory `xml:"category"`
	ITunesCategories  []rssCategory `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd category"`
}

type rssGUID struct {
	Value string `xml:",chardata"`
}

type rssEnclosure struct {
	URL    string `xml:"url,attr"`
	Type   string `xml:"type,attr"`
	Length string `xml:"length,attr"`
}

func parsePodcastFeedXML(reader io.Reader) (parsedPodcastFeed, error) {
	var doc rssDocument
	decoder := xml.NewDecoder(reader)
	if err := decoder.Decode(&doc); err != nil {
		return parsedPodcastFeed{}, err
	}
	if doc.Channel.Title == "" && len(doc.Channel.Items) == 0 {
		return parsedPodcastFeed{}, errors.New("rss channel not found")
	}

	channel := doc.Channel
	feed := parsedPodcastFeed{
		Title:       cleanText(channel.Title),
		Description: cleanText(channel.Description),
		Author:      cleanText(firstNonEmpty(channel.ITunesAuthor, channel.Author)),
		SiteURL:     cleanText(channel.Link),
		ImageURL:    cleanText(firstNonEmpty(channel.ITunesImage.Href, channel.Image.URL)),
		Language:    cleanText(channel.Language),
		Explicit:    parseExplicit(firstNonEmpty(channel.ITunesExplicit, channel.Explicit)),
		Categories:  uniqueStrings(appendRSSCategories(channel.Categories, channel.ITunesCategories)),
		OwnerName:   cleanText(channel.Owner.Name),
		OwnerEmail:  cleanText(channel.Owner.Email),
		Episodes:    make([]parsedPodcastEpisode, 0, len(channel.Items)),
	}

	for _, item := range channel.Items {
		episode := parsedPodcastEpisode{
			Title:           cleanText(item.Title),
			Subtitle:        cleanText(firstNonEmpty(item.ITunesSubtitle, item.Subtitle)),
			Description:     cleanText(firstNonEmpty(item.ITunesSummary, item.Summary, item.Description)),
			Link:            cleanText(item.Link),
			GUID:            cleanText(item.GUID.Value),
			PublishedAt:     parsePodcastDate(item.PubDate),
			Season:          parseInt(firstNonEmpty(item.ITunesSeason, item.Season)),
			Episode:         parseInt(firstNonEmpty(item.ITunesEpisode, item.Episode)),
			EpisodeType:     cleanText(firstNonEmpty(item.ITunesEpisodeType, item.EpisodeType)),
			DurationSeconds: parseDurationSeconds(firstNonEmpty(item.ITunesDuration, item.Duration)),
			Explicit:        parseExplicit(firstNonEmpty(item.ITunesExplicit, item.Explicit)),
			EnclosureURL:    cleanText(item.Enclosure.URL),
			EnclosureType:   cleanText(item.Enclosure.Type),
			EnclosureBytes:  parseInt64(item.Enclosure.Length),
			ImageURL:        cleanText(item.ITunesImage.Href),
		}
		if episode.Title == "" {
			episode.Title = firstNonEmpty(episode.GUID, episode.EnclosureURL, "Untitled Episode")
		}
		feed.Episodes = append(feed.Episodes, episode)
	}

	return feed, nil
}

func appendRSSCategories(categories []rssCategory, itunesCategories []rssCategory) []string {
	values := make([]string, 0, len(categories)+len(itunesCategories))
	for _, category := range categories {
		values = append(values, flattenRSSCategory(category)...)
	}
	for _, category := range itunesCategories {
		values = append(values, flattenRSSCategory(category)...)
	}
	return values
}

func flattenRSSCategory(category rssCategory) []string {
	var values []string
	value := cleanText(firstNonEmpty(category.Text, category.Value))
	if value != "" {
		values = append(values, value)
	}
	for _, child := range category.Children {
		values = append(values, flattenRSSCategory(child)...)
	}
	return values
}

func parseExplicit(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return value == "yes" || value == "true" || value == "explicit" || value == "1"
}

func parseDurationSeconds(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if !strings.Contains(value, ":") {
		return parseInt(value)
	}

	parts := strings.Split(value, ":")
	total := 0
	for _, part := range parts {
		total *= 60
		total += parseInt(part)
	}
	return total
}

func parsePodcastDate(value string) *time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	formats := []string{
		time.RFC1123Z,
		time.RFC1123,
		time.RFC822Z,
		time.RFC822,
		time.RFC3339,
		time.RFC3339Nano,
		"Mon, 02 Jan 2006 15:04:05 -0700",
		"Mon, 2 Jan 2006 15:04:05 -0700",
		"2006-01-02",
	}
	for _, format := range formats {
		parsed, err := time.Parse(format, value)
		if err == nil {
			parsed = parsed.UTC()
			return &parsed
		}
	}
	return nil
}

func cleanText(value string) string {
	return strings.TrimSpace(html.UnescapeString(value))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func parseInt(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	parsed, _ := strconv.Atoi(value)
	return parsed
}

func parseInt64(value string) int64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	parsed, _ := strconv.ParseInt(value, 10, 64)
	return parsed
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	unique := make([]string, 0, len(values))
	for _, value := range values {
		value = cleanText(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, value)
	}
	return unique
}
