package scanner

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

type jsonBookMetadata struct {
	Title         string          `json:"title"`
	Subtitle      string          `json:"subtitle"`
	Authors       []string        `json:"authors"`
	Narrators     []string        `json:"narrators"`
	Publisher     string          `json:"publisher"`
	PublishedDate string          `json:"publishedDate"`
	Description   string          `json:"description"`
	Genres        []string        `json:"genres"`
	Language      string          `json:"language"`
	ISBNs         []string        `json:"isbns"`
	Series        []jsonSeriesRef `json:"series"`
	Tags          []string        `json:"tags"`
	Explicit      *bool           `json:"explicit"`
	Abridged      *bool           `json:"abridged"`
}

type jsonSeriesRef struct {
	Name     string  `json:"name"`
	Sequence float64 `json:"sequence"`
	Index    string  `json:"index"`
}

func readJSONBookMetadata(dir string) bookSidecar {
	path := filepath.Join(dir, "metadata.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return bookSidecar{}
	}
	var payload jsonBookMetadata
	if err := json.Unmarshal(data, &payload); err != nil {
		return bookSidecar{}
	}

	out := bookSidecar{
		Title:         strings.TrimSpace(payload.Title),
		Authors:       cleanParts(payload.Authors),
		Narrators:     cleanParts(payload.Narrators),
		Publisher:     strings.TrimSpace(payload.Publisher),
		PublishedDate: strings.TrimSpace(payload.PublishedDate),
		Description:   strings.TrimSpace(payload.Description),
		Genres:        cleanParts(payload.Genres),
		Language:      strings.TrimSpace(payload.Language),
		ISBNs:         cleanParts(payload.ISBNs),
	}
	for _, series := range payload.Series {
		name := strings.TrimSpace(series.Name)
		if name == "" {
			continue
		}
		ref := catalog.SeriesRef{
			ID:   stableID("series", name),
			Name: name,
		}
		if series.Sequence > 0 {
			ref.Sequence = series.Sequence
			ref.SequenceText = strconv.FormatFloat(series.Sequence, 'f', -1, 64)
		} else if strings.TrimSpace(series.Index) != "" {
			ref.SequenceText = strings.TrimSpace(series.Index)
			ref.Sequence = parseFloat(series.Index)
		}
		out.Series = append(out.Series, ref)
	}
	return out
}

type musicAlbumSidecar struct {
	Title       string
	SortTitle   string
	AlbumArtist string
	Artist      string
	ReleaseDate string
	RecordLabel string
	Genres      []string
	Barcode     string
	Description string
}

type nfoAlbum struct {
	Title       string        `xml:"title"`
	Artist      string        `xml:"artist"`
	AlbumArtist string        `xml:"albumartist"`
	Year        string        `xml:"year"`
	Label       string        `xml:"label"`
	Genre       string        `xml:"genre"`
	Plot        string        `xml:"plot"`
	UniqueID    []nfoUniqueID `xml:"uniqueid"`
}

type nfoUniqueID struct {
	Type  string `xml:"type,attr"`
	Value string `xml:",chardata"`
}

func readMusicAlbumSidecar(dir string) musicAlbumSidecar {
	path := firstMatchingFile(dir, ".nfo")
	if path == "" {
		return musicAlbumSidecar{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return musicAlbumSidecar{}
	}
	var album nfoAlbum
	if err := xml.Unmarshal(data, &album); err != nil {
		return musicAlbumSidecar{}
	}
	out := musicAlbumSidecar{
		Title:       strings.TrimSpace(album.Title),
		AlbumArtist: strings.TrimSpace(album.AlbumArtist),
		Artist:      strings.TrimSpace(album.Artist),
		ReleaseDate: strings.TrimSpace(album.Year),
		RecordLabel: strings.TrimSpace(album.Label),
		Description: strings.TrimSpace(album.Plot),
	}
	if album.Genre != "" {
		out.Genres = splitGenreTag(normalizeTags(map[string]string{"genre": album.Genre}), "genre")
	}
	for _, id := range album.UniqueID {
		scheme := strings.ToLower(strings.TrimSpace(id.Type))
		value := strings.TrimSpace(id.Value)
		if value == "" {
			continue
		}
		if scheme == "upc" || scheme == "ean" || scheme == "barcode" || looksLikeISBN(value) {
			out.Barcode = value
		}
	}
	return out
}

func (sidecar *musicAlbumSidecar) mergeIntoAlbum(album *catalog.MusicAlbum) {
	if sidecar.Title != "" && album.Title == "" {
		album.Title = sidecar.Title
	}
	if sidecar.SortTitle != "" && album.SortTitle == "" {
		album.SortTitle = sidecar.SortTitle
	}
	if sidecar.ReleaseDate != "" && album.ReleaseDate == "" {
		album.ReleaseDate = sidecar.ReleaseDate
		album.ReleaseYear = yearFromDate(sidecar.ReleaseDate)
	}
	if sidecar.RecordLabel != "" && album.RecordLabel == "" {
		album.RecordLabel = sidecar.RecordLabel
	}
	if len(sidecar.Genres) > 0 && len(album.Genres) == 0 {
		album.Genres = sidecar.Genres
	}
	if sidecar.Barcode != "" && album.Barcode == "" {
		album.Barcode = sidecar.Barcode
	}
	if sidecar.AlbumArtist != "" && album.DisplayArtist == "" {
		album.DisplayArtist = sidecar.AlbumArtist
	}
}

func readCueChapters(groupRoot string, probes []probedFile) []catalog.AudioChapter {
	cuePath := findCueFile(groupRoot, probes)
	if cuePath == "" {
		return nil
	}
	return parseCueFile(cuePath)
}

func findCueFile(groupRoot string, probes []probedFile) string {
	if path := firstMatchingFile(groupRoot, ".cue"); path != "" {
		return path
	}
	for _, probe := range probes {
		base := strings.TrimSuffix(probe.AudioFile.Path, filepath.Ext(probe.AudioFile.Path))
		if path := base + ".cue"; fileExists(path) {
			return path
		}
	}
	return ""
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func parseCueFile(path string) []catalog.AudioChapter {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	chapters := make([]catalog.AudioChapter, 0, 8)
	var currentTitle string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "TITLE "):
			currentTitle = strings.TrimSpace(line[len("TITLE "):])
			currentTitle = strings.Trim(currentTitle, `"`)
		case strings.HasPrefix(upper, "INDEX 01 "):
			start := parseCueTimestamp(strings.TrimSpace(line[len("INDEX 01 "):]))
			chapters = append(chapters, catalog.AudioChapter{
				Index:        len(chapters) + 1,
				Title:        firstNonEmpty(currentTitle, "Chapter "+strconv.Itoa(len(chapters)+1)),
				StartSeconds: start,
			})
		}
	}
	for i := 0; i < len(chapters)-1; i++ {
		chapters[i].EndSeconds = chapters[i+1].StartSeconds
	}
	return chapters
}

func parseCueTimestamp(value string) float64 {
	value = strings.TrimSpace(value)
	parts := strings.Split(value, ":")
	seconds := 0.0
	multiplier := 1.0
	for i := len(parts) - 1; i >= 0; i-- {
		part := strings.TrimSpace(parts[i])
		parsed, _ := strconv.ParseFloat(part, 64)
		seconds += parsed * multiplier
		multiplier *= 60
	}
	return seconds
}
