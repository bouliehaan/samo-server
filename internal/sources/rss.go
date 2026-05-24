package sources

import (
	"bytes"
	"encoding/xml"
	"errors"
	"html"
	"io"
	"net/mail"
	"strconv"
	"strings"
	"time"
)

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
	ExternalURLs    []string
}

type rssDocument struct {
	XMLName xml.Name   `xml:"rss"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title            string         `xml:"title"`
	Link             string         `xml:"link"`
	Description      string         `xml:"description"`
	Summary          string         `xml:"summary"`
	ITunesSummary    string         `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd summary"`
	Language         string         `xml:"language"`
	Author           string         `xml:"author"`
	ITunesAuthor     string         `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd author"`
	Explicit         string         `xml:"explicit"`
	ITunesExplicit   string         `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd explicit"`
	Owner            rssOwner       `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd owner"`
	Image            rssImage       `xml:"image"`
	ITunesImage      itunesImage    `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd image"`
	MediaThumbnail   mediaImage     `xml:"http://search.yahoo.com/mrss/ thumbnail"`
	MediaContent     []mediaContent `xml:"http://search.yahoo.com/mrss/ content"`
	AtomLinks        []rssAtomLink  `xml:"http://www.w3.org/2005/Atom link"`
	Categories       []rssCategory  `xml:"category"`
	ITunesCategories []rssCategory  `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd category"`
	Items            []rssItem      `xml:"item"`
}

type rssOwner struct {
	Name  string `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd name"`
	Email string `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd email"`
}

type rssImage struct {
	URL  string `xml:"url"`
	Href string `xml:"href,attr"`
}

type itunesImage struct {
	Href string `xml:"href,attr"`
}

type mediaImage struct {
	URL string `xml:"url,attr"`
}

type mediaContent struct {
	URL      string `xml:"url,attr"`
	Type     string `xml:"type,attr"`
	Medium   string `xml:"medium,attr"`
	FileSize string `xml:"fileSize,attr"`
	Length   string `xml:"length,attr"`
}

type rssAtomLink struct {
	Href   string `xml:"href,attr"`
	Rel    string `xml:"rel,attr"`
	Type   string `xml:"type,attr"`
	Length string `xml:"length,attr"`
}

type rssCategory struct {
	Value    string        `xml:",chardata"`
	Text     string        `xml:"text,attr"`
	Children []rssCategory `xml:"category"`
}

type rssItem struct {
	Title             string         `xml:"title"`
	Subtitle          string         `xml:"subtitle"`
	ITunesSubtitle    string         `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd subtitle"`
	Description       string         `xml:"description"`
	Summary           string         `xml:"summary"`
	ITunesSummary     string         `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd summary"`
	ContentEncoded    string         `xml:"http://purl.org/rss/1.0/modules/content/ encoded"`
	Link              string         `xml:"link"`
	GUID              rssGUID        `xml:"guid"`
	PubDate           string         `xml:"pubDate"`
	Author            string         `xml:"author"`
	ITunesAuthor      string         `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd author"`
	DCCreator         string         `xml:"http://purl.org/dc/elements/1.1/ creator"`
	Duration          string         `xml:"duration"`
	ITunesDuration    string         `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd duration"`
	Season            string         `xml:"season"`
	ITunesSeason      string         `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd season"`
	Episode           string         `xml:"episode"`
	ITunesEpisode     string         `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd episode"`
	EpisodeType       string         `xml:"episodeType"`
	ITunesEpisodeType string         `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd episodeType"`
	Explicit          string         `xml:"explicit"`
	ITunesExplicit    string         `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd explicit"`
	Enclosures        []rssEnclosure `xml:"enclosure"`
	ITunesImage       itunesImage    `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd image"`
	MediaThumbnail    mediaImage     `xml:"http://search.yahoo.com/mrss/ thumbnail"`
	MediaContent      []mediaContent `xml:"http://search.yahoo.com/mrss/ content"`
	AtomLinks         []rssAtomLink  `xml:"http://www.w3.org/2005/Atom link"`
	Categories        []rssCategory  `xml:"category"`
	ITunesCategories  []rssCategory  `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd category"`
}

type rssGUID struct {
	Value       string `xml:",chardata"`
	IsPermalink string `xml:"isPermaLink,attr"`
}

type rssEnclosure struct {
	URL    string `xml:"url,attr"`
	Type   string `xml:"type,attr"`
	Length string `xml:"length,attr"`
}

type atomDocument struct {
	XMLName          xml.Name       `xml:"feed"`
	Title            string         `xml:"title"`
	Subtitle         string         `xml:"subtitle"`
	Updated          string         `xml:"updated"`
	Language         string         `xml:"lang,attr"`
	Authors          []atomPerson   `xml:"author"`
	Links            []rssAtomLink  `xml:"link"`
	Icon             string         `xml:"icon"`
	Logo             string         `xml:"logo"`
	ITunesImage      itunesImage    `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd image"`
	ITunesAuthor     string         `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd author"`
	ITunesSummary    string         `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd summary"`
	ITunesExplicit   string         `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd explicit"`
	MediaThumbnail   mediaImage     `xml:"http://search.yahoo.com/mrss/ thumbnail"`
	MediaContent     []mediaContent `xml:"http://search.yahoo.com/mrss/ content"`
	Categories       []atomCategory `xml:"category"`
	ITunesCategories []rssCategory  `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd category"`
	Entries          []atomEntry    `xml:"entry"`
}

type atomPerson struct {
	Name  string `xml:"name"`
	Email string `xml:"email"`
}

type atomCategory struct {
	Term  string `xml:"term,attr"`
	Label string `xml:"label,attr"`
}

type atomEntry struct {
	Title             string         `xml:"title"`
	Subtitle          string         `xml:"subtitle"`
	ITunesSubtitle    string         `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd subtitle"`
	Summary           string         `xml:"summary"`
	Content           string         `xml:"content"`
	ITunesSummary     string         `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd summary"`
	ID                string         `xml:"id"`
	Published         string         `xml:"published"`
	Updated           string         `xml:"updated"`
	Authors           []atomPerson   `xml:"author"`
	Links             []rssAtomLink  `xml:"link"`
	Duration          string         `xml:"duration"`
	ITunesDuration    string         `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd duration"`
	Season            string         `xml:"season"`
	ITunesSeason      string         `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd season"`
	Episode           string         `xml:"episode"`
	ITunesEpisode     string         `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd episode"`
	EpisodeType       string         `xml:"episodeType"`
	ITunesEpisodeType string         `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd episodeType"`
	Explicit          string         `xml:"explicit"`
	ITunesExplicit    string         `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd explicit"`
	ITunesImage       itunesImage    `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd image"`
	MediaThumbnail    mediaImage     `xml:"http://search.yahoo.com/mrss/ thumbnail"`
	MediaContent      []mediaContent `xml:"http://search.yahoo.com/mrss/ content"`
	Categories        []atomCategory `xml:"category"`
	ITunesCategories  []rssCategory  `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd category"`
}

func parsePodcastFeedXML(reader io.Reader) (parsedPodcastFeed, error) {
	body, err := io.ReadAll(reader)
	if err != nil {
		return parsedPodcastFeed{}, err
	}
	feed, rssErr := parseRSSPodcastFeedXML(body)
	if rssErr == nil {
		return feed, nil
	}
	if atomFeed, atomErr := parseAtomPodcastFeedXML(body); atomErr == nil {
		return atomFeed, nil
	}
	return parsedPodcastFeed{}, rssErr
}

func parseRSSPodcastFeedXML(body []byte) (parsedPodcastFeed, error) {
	var doc rssDocument
	decoder := xml.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&doc); err != nil {
		return parsedPodcastFeed{}, err
	}
	if doc.Channel.Title == "" && len(doc.Channel.Items) == 0 {
		return parsedPodcastFeed{}, errors.New("rss channel not found")
	}

	channel := doc.Channel
	feed := parsedPodcastFeed{
		Title:       cleanText(channel.Title),
		Description: cleanText(firstNonEmpty(channel.ITunesSummary, channel.Summary, channel.Description)),
		Author:      cleanText(firstNonEmpty(channel.ITunesAuthor, channel.Author)),
		SiteURL:     cleanText(channel.Link),
		ImageURL:    cleanText(firstNonEmpty(channel.ITunesImage.Href, channel.Image.Href, channel.Image.URL, channel.MediaThumbnail.URL, firstMediaImageURL(channel.MediaContent))),
		Language:    cleanText(channel.Language),
		Explicit:    parseExplicit(firstNonEmpty(channel.ITunesExplicit, channel.Explicit)),
		Categories:  uniqueStrings(appendRSSCategories(channel.Categories, channel.ITunesCategories)),
		OwnerName:   cleanText(channel.Owner.Name),
		OwnerEmail:  cleanText(channel.Owner.Email),
		Episodes:    make([]parsedPodcastEpisode, 0, len(channel.Items)),
	}

	for _, item := range channel.Items {
		enclosure := primaryPodcastEnclosure(item)
		link := cleanText(firstNonEmpty(item.Link, atomLinkHref(item.AtomLinks, "alternate"), permalinkGUID(item.GUID)))
		imageURL := cleanText(firstNonEmpty(item.ITunesImage.Href, item.MediaThumbnail.URL, firstMediaImageURL(item.MediaContent)))
		episode := parsedPodcastEpisode{
			Title:           cleanText(item.Title),
			Subtitle:        cleanText(firstNonEmpty(item.ITunesSubtitle, item.Subtitle)),
			Description:     cleanText(firstNonEmpty(item.ITunesSummary, item.Summary, item.ContentEncoded, item.Description)),
			Link:            link,
			GUID:            cleanText(item.GUID.Value),
			PublishedAt:     parsePodcastDate(item.PubDate),
			Season:          parseInt(firstNonEmpty(item.ITunesSeason, item.Season)),
			Episode:         parseInt(firstNonEmpty(item.ITunesEpisode, item.Episode)),
			EpisodeType:     cleanText(firstNonEmpty(item.ITunesEpisodeType, item.EpisodeType)),
			DurationSeconds: parseDurationSeconds(firstNonEmpty(item.ITunesDuration, item.Duration)),
			Explicit:        parseExplicit(firstNonEmpty(item.ITunesExplicit, item.Explicit)),
			EnclosureURL:    cleanText(enclosure.URL),
			EnclosureType:   cleanText(enclosure.Type),
			EnclosureBytes:  parseInt64(enclosure.Length),
			ImageURL:        imageURL,
			ExternalURLs: cleanStringSlice([]string{
				link,
				permalinkGUID(item.GUID),
				enclosure.URL,
				imageURL,
			}),
		}
		if episode.Title == "" {
			episode.Title = firstNonEmpty(episode.GUID, episode.EnclosureURL, "Untitled Episode")
		}
		feed.Episodes = append(feed.Episodes, episode)
	}

	return feed, nil
}

func parseAtomPodcastFeedXML(body []byte) (parsedPodcastFeed, error) {
	var doc atomDocument
	decoder := xml.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&doc); err != nil {
		return parsedPodcastFeed{}, err
	}
	if doc.XMLName.Local != "feed" || (doc.Title == "" && len(doc.Entries) == 0) {
		return parsedPodcastFeed{}, errors.New("atom feed not found")
	}

	authorName, authorEmail := firstAtomPerson(doc.Authors)
	feed := parsedPodcastFeed{
		Title:       cleanText(doc.Title),
		Description: cleanText(firstNonEmpty(doc.ITunesSummary, doc.Subtitle)),
		Author:      cleanText(firstNonEmpty(doc.ITunesAuthor, authorName)),
		SiteURL:     cleanText(atomLinkHref(doc.Links, "alternate")),
		ImageURL:    cleanText(firstNonEmpty(doc.ITunesImage.Href, doc.Logo, doc.Icon, doc.MediaThumbnail.URL, firstMediaImageURL(doc.MediaContent))),
		Language:    cleanText(doc.Language),
		Explicit:    parseExplicit(doc.ITunesExplicit),
		Categories:  uniqueStrings(appendAtomCategories(doc.Categories, doc.ITunesCategories)),
		OwnerName:   cleanText(authorName),
		OwnerEmail:  cleanText(authorEmail),
		Episodes:    make([]parsedPodcastEpisode, 0, len(doc.Entries)),
	}

	for _, entry := range doc.Entries {
		enclosure := atomEntryEnclosure(entry)
		link := cleanText(firstNonEmpty(atomLinkHref(entry.Links, "alternate"), entry.ID))
		imageURL := cleanText(firstNonEmpty(entry.ITunesImage.Href, entry.MediaThumbnail.URL, firstMediaImageURL(entry.MediaContent)))
		episode := parsedPodcastEpisode{
			Title:           cleanText(entry.Title),
			Subtitle:        cleanText(firstNonEmpty(entry.ITunesSubtitle, entry.Subtitle)),
			Description:     cleanText(firstNonEmpty(entry.ITunesSummary, entry.Summary, entry.Content)),
			Link:            link,
			GUID:            cleanText(entry.ID),
			PublishedAt:     parsePodcastDate(firstNonEmpty(entry.Published, entry.Updated)),
			Season:          parseInt(firstNonEmpty(entry.ITunesSeason, entry.Season)),
			Episode:         parseInt(firstNonEmpty(entry.ITunesEpisode, entry.Episode)),
			EpisodeType:     cleanText(firstNonEmpty(entry.ITunesEpisodeType, entry.EpisodeType)),
			DurationSeconds: parseDurationSeconds(firstNonEmpty(entry.ITunesDuration, entry.Duration)),
			Explicit:        parseExplicit(firstNonEmpty(entry.ITunesExplicit, entry.Explicit)),
			EnclosureURL:    cleanText(enclosure.URL),
			EnclosureType:   cleanText(enclosure.Type),
			EnclosureBytes:  parseInt64(enclosure.Length),
			ImageURL:        imageURL,
			ExternalURLs: cleanStringSlice([]string{
				link,
				entry.ID,
				enclosure.URL,
				imageURL,
			}),
		}
		if episode.Title == "" {
			episode.Title = firstNonEmpty(episode.GUID, episode.EnclosureURL, "Untitled Episode")
		}
		feed.Episodes = append(feed.Episodes, episode)
	}
	return feed, nil
}

func firstAtomPerson(people []atomPerson) (string, string) {
	for _, person := range people {
		name := cleanText(person.Name)
		email := cleanText(person.Email)
		if name != "" || email != "" {
			return name, email
		}
	}
	return "", ""
}

func appendAtomCategories(categories []atomCategory, itunesCategories []rssCategory) []string {
	values := make([]string, 0, len(categories)+len(itunesCategories))
	for _, category := range categories {
		value := cleanText(firstNonEmpty(category.Label, category.Term))
		if value != "" {
			values = append(values, value)
		}
	}
	return append(values, appendRSSCategories(nil, itunesCategories)...)
}

func atomEntryEnclosure(entry atomEntry) rssEnclosure {
	if atom := atomEnclosure(entry.Links); atom.Href != "" {
		return rssEnclosure{URL: atom.Href, Type: atom.Type, Length: atom.Length}
	}
	if media := firstMediaAudioContent(entry.MediaContent); media.URL != "" {
		return rssEnclosure{URL: media.URL, Type: media.Type, Length: firstNonEmpty(media.FileSize, media.Length)}
	}
	return rssEnclosure{}
}

func primaryPodcastEnclosure(item rssItem) rssEnclosure {
	for _, enclosure := range item.Enclosures {
		if cleanText(enclosure.URL) != "" {
			return enclosure
		}
	}
	if media := firstMediaAudioContent(item.MediaContent); media.URL != "" {
		return rssEnclosure{URL: media.URL, Type: media.Type, Length: firstNonEmpty(media.FileSize, media.Length)}
	}
	if atom := atomEnclosure(item.AtomLinks); atom.Href != "" {
		return rssEnclosure{URL: atom.Href, Type: atom.Type, Length: atom.Length}
	}
	return rssEnclosure{}
}

func firstMediaAudioContent(contents []mediaContent) mediaContent {
	for _, content := range contents {
		if cleanText(content.URL) == "" {
			continue
		}
		contentType := strings.ToLower(strings.TrimSpace(content.Type))
		medium := strings.ToLower(strings.TrimSpace(content.Medium))
		if medium == "audio" || strings.HasPrefix(contentType, "audio/") || urlHasSuffix(content.URL, ".mp3", ".m4a", ".aac", ".ogg", ".oga", ".opus", ".wav", ".flac") {
			return content
		}
	}
	for _, content := range contents {
		if cleanText(content.URL) != "" && !isMediaImage(content) {
			return content
		}
	}
	return mediaContent{}
}

func firstMediaImageURL(contents []mediaContent) string {
	for _, content := range contents {
		if isMediaImage(content) {
			return content.URL
		}
	}
	return ""
}

func isMediaImage(content mediaContent) bool {
	if cleanText(content.URL) == "" {
		return false
	}
	contentType := strings.ToLower(strings.TrimSpace(content.Type))
	medium := strings.ToLower(strings.TrimSpace(content.Medium))
	return medium == "image" || strings.HasPrefix(contentType, "image/") || urlHasSuffix(content.URL, ".jpg", ".jpeg", ".png", ".webp", ".gif")
}

func atomLinkHref(links []rssAtomLink, rel string) string {
	for _, link := range links {
		if cleanText(link.Href) == "" {
			continue
		}
		if atomRelMatches(link.Rel, rel) {
			return link.Href
		}
	}
	return ""
}

func atomEnclosure(links []rssAtomLink) rssAtomLink {
	for _, link := range links {
		if cleanText(link.Href) == "" {
			continue
		}
		linkType := strings.ToLower(strings.TrimSpace(link.Type))
		if atomRelMatches(link.Rel, "enclosure") || strings.HasPrefix(linkType, "audio/") {
			return link
		}
	}
	return rssAtomLink{}
}

func atomRelMatches(value, want string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	want = strings.ToLower(strings.TrimSpace(want))
	if value == "" {
		return want == "alternate"
	}
	for _, token := range strings.Fields(value) {
		if token == want {
			return true
		}
	}
	return value == want
}

func permalinkGUID(guid rssGUID) string {
	value := cleanText(guid.Value)
	if value == "" || !strings.EqualFold(strings.TrimSpace(guid.IsPermalink), "true") || !looksLikeHTTPURL(value) {
		return ""
	}
	return value
}

func looksLikeHTTPURL(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://")
}

func urlHasSuffix(raw string, suffixes ...string) bool {
	value := strings.ToLower(strings.TrimSpace(raw))
	if index := strings.IndexAny(value, "?#"); index >= 0 {
		value = value[:index]
	}
	for _, suffix := range suffixes {
		if strings.HasSuffix(value, suffix) {
			return true
		}
	}
	return false
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
	if parsed, err := mail.ParseDate(value); err == nil {
		parsed = parsed.UTC()
		return &parsed
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
