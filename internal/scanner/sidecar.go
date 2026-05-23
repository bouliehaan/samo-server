package scanner

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

type bookSidecar struct {
	Title         string
	Authors       []string
	Narrators     []string
	Publisher     string
	PublishedDate string
	Description   string
	Genres        []string
	Language      string
	ISBNs         []string
	Series        []catalog.SeriesRef
}

func readBookSidecar(dir string) bookSidecar {
	var out bookSidecar
	out.mergeJSON(readJSONBookMetadata(dir))

	if description := readFirstTextFile(dir, "desc.txt", "description.txt", "summary.txt"); description != "" {
		out.Description = description
	}
	if narrator := readFirstTextFile(dir, "reader.txt", "narrator.txt", "narrators.txt"); narrator != "" {
		out.Narrators = splitPeopleString(narrator)
	}

	opfPath := firstMatchingFile(dir, ".opf")
	if opfPath != "" {
		out.mergeOPF(readOPF(opfPath))
	}

	return out
}

func (b *bookSidecar) mergeJSON(jsonMeta bookSidecar) {
	if b.Title == "" {
		b.Title = jsonMeta.Title
	}
	if len(b.Authors) == 0 {
		b.Authors = jsonMeta.Authors
	}
	if len(b.Narrators) == 0 {
		b.Narrators = jsonMeta.Narrators
	}
	if b.Publisher == "" {
		b.Publisher = jsonMeta.Publisher
	}
	if b.PublishedDate == "" {
		b.PublishedDate = jsonMeta.PublishedDate
	}
	if b.Description == "" {
		b.Description = jsonMeta.Description
	}
	if len(b.Genres) == 0 {
		b.Genres = jsonMeta.Genres
	}
	if b.Language == "" {
		b.Language = jsonMeta.Language
	}
	if len(b.ISBNs) == 0 {
		b.ISBNs = jsonMeta.ISBNs
	}
	if len(b.Series) == 0 {
		b.Series = jsonMeta.Series
	}
}

func (b *bookSidecar) mergeOPF(opf bookSidecar) {
	if b.Title == "" {
		b.Title = opf.Title
	}
	if len(b.Authors) == 0 {
		b.Authors = opf.Authors
	}
	if len(b.Narrators) == 0 {
		b.Narrators = opf.Narrators
	}
	if b.Publisher == "" {
		b.Publisher = opf.Publisher
	}
	if b.PublishedDate == "" {
		b.PublishedDate = opf.PublishedDate
	}
	if b.Description == "" {
		b.Description = opf.Description
	}
	if len(b.Genres) == 0 {
		b.Genres = opf.Genres
	}
	if b.Language == "" {
		b.Language = opf.Language
	}
	if len(b.ISBNs) == 0 {
		b.ISBNs = opf.ISBNs
	}
	if len(b.Series) == 0 {
		b.Series = opf.Series
	}
}

type opfPackage struct {
	Metadata opfMetadata `xml:"metadata"`
}

type opfMetadata struct {
	Title       []string        `xml:"title"`
	Creator     []opfCreator    `xml:"creator"`
	Contributor []opfCreator    `xml:"contributor"`
	Publisher   []string        `xml:"publisher"`
	Date        []string        `xml:"date"`
	Description []string        `xml:"description"`
	Subject     []string        `xml:"subject"`
	Language    []string        `xml:"language"`
	Identifier  []opfIdentifier `xml:"identifier"`
	Meta        []opfMeta       `xml:"meta"`
}

type opfCreator struct {
	Role string `xml:"role,attr"`
	Text string `xml:",chardata"`
}

type opfIdentifier struct {
	Scheme string `xml:"scheme,attr"`
	Text   string `xml:",chardata"`
}

type opfMeta struct {
	Name     string `xml:"name,attr"`
	Property string `xml:"property,attr"`
	Content  string `xml:"content,attr"`
	Text     string `xml:",chardata"`
}

func readOPF(path string) bookSidecar {
	data, err := os.ReadFile(path)
	if err != nil {
		return bookSidecar{}
	}
	var pkg opfPackage
	if err := xml.Unmarshal(data, &pkg); err != nil {
		return bookSidecar{}
	}

	meta := pkg.Metadata
	out := bookSidecar{
		Title:         firstString(meta.Title),
		Publisher:     firstString(meta.Publisher),
		PublishedDate: firstString(meta.Date),
		Description:   strings.TrimSpace(firstString(meta.Description)),
		Genres:        cleanParts(meta.Subject),
		Language:      firstString(meta.Language),
	}

	for _, creator := range meta.Creator {
		name := strings.TrimSpace(creator.Text)
		if name == "" {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(creator.Role))
		if role == "narrator" || role == "nrt" {
			out.Narrators = append(out.Narrators, name)
		} else {
			out.Authors = append(out.Authors, name)
		}
	}
	for _, contributor := range meta.Contributor {
		name := strings.TrimSpace(contributor.Text)
		if name == "" {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(contributor.Role))
		if role == "narrator" || role == "nrt" || strings.Contains(role, "narr") {
			out.Narrators = append(out.Narrators, name)
		}
	}
	for _, identifier := range meta.Identifier {
		scheme := strings.ToLower(identifier.Scheme)
		if strings.Contains(scheme, "isbn") || looksLikeISBN(identifier.Text) {
			out.ISBNs = append(out.ISBNs, strings.TrimSpace(identifier.Text))
		}
	}

	seriesName := ""
	seriesIndex := ""
	for _, entry := range meta.Meta {
		key := strings.ToLower(firstNonEmpty(entry.Name, entry.Property))
		value := strings.TrimSpace(firstNonEmpty(entry.Content, entry.Text))
		switch key {
		case "calibre:series", "belongs-to-collection", "series":
			seriesName = value
		case "calibre:series_index", "group-position", "series_index", "series-part":
			seriesIndex = value
		}
	}
	if seriesName != "" {
		ref := catalog.SeriesRef{ID: stableID("series", seriesName), Name: seriesName, SequenceText: seriesIndex}
		if seriesIndex != "" {
			ref.Sequence = parseFloat(seriesIndex)
		}
		out.Series = []catalog.SeriesRef{ref}
	}

	return out
}

func readFirstTextFile(dir string, names ...string) string {
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	return ""
}

func firstMatchingFile(dir string, ext string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ext) {
			return filepath.Join(dir, entry.Name())
		}
	}
	return ""
}

func findCoverImage(dir string) *catalog.Image {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	preferred := []string{"cover", "folder", "front", "artwork", "album"}
	for _, stem := range preferred {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			ext := strings.ToLower(filepath.Ext(entry.Name()))
			if !isImageExt(ext) {
				continue
			}
			name := strings.ToLower(strings.TrimSuffix(entry.Name(), ext))
			if name == stem {
				path := filepath.Join(dir, entry.Name())
				return &catalog.Image{ID: stableID("image", path), Path: path, MimeType: imageMimeType(ext)}
			}
		}
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if isImageExt(ext) {
			path := filepath.Join(dir, entry.Name())
			return &catalog.Image{ID: stableID("image", path), Path: path, MimeType: imageMimeType(ext)}
		}
	}
	return nil
}

func isImageExt(ext string) bool {
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp":
		return true
	default:
		return false
	}
}

func imageMimeType(ext string) string {
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}

func splitPeopleString(value string) []string {
	value = strings.ReplaceAll(value, " and ", ";")
	value = strings.ReplaceAll(value, " & ", ";")
	return cleanParts(strings.FieldsFunc(value, func(r rune) bool {
		return r == ';' || r == '|'
	}))
}

func authorNamesFromFolder(value string) []string {
	parts := splitPeopleString(value)
	if len(parts) > 1 {
		return parts
	}
	if strings.Count(value, ",") == 1 {
		pair := strings.SplitN(value, ",", 2)
		return []string{strings.TrimSpace(pair[1]) + " " + strings.TrimSpace(pair[0])}
	}
	return cleanParts([]string{value})
}

func firstString(values []string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstNonEmptySlice(values ...[]string) []string {
	for _, value := range values {
		if len(value) > 0 {
			return value
		}
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func looksLikeISBN(value string) bool {
	value = strings.ReplaceAll(value, "-", "")
	value = strings.ReplaceAll(value, " ", "")
	return len(value) == 10 || len(value) == 13
}
