package scanner

import (
	"encoding/xml"
	"strconv"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

type overdriveMarkers struct {
	Markers []overdriveMarker `xml:"Marker"`
}

type overdriveMarker struct {
	Name string `xml:"Name"`
	Time string `xml:"Time"`
}

func overdriveChapters(tags catalog.Tags) []catalog.AudioChapter {
	value := firstTag(tags, "overdrive_mediamarkers", "overdrive media markers", "mediamarkers", "media_markers")
	if value == "" {
		return nil
	}
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "<") {
		value = "<Markers>" + value + "</Markers>"
	}

	var markers overdriveMarkers
	if err := xml.Unmarshal([]byte(value), &markers); err != nil {
		return nil
	}
	if len(markers.Markers) == 0 {
		return nil
	}

	chapters := make([]catalog.AudioChapter, 0, len(markers.Markers))
	for index, marker := range markers.Markers {
		title := strings.TrimSpace(marker.Name)
		if title == "" {
			title = "Chapter " + strconv.Itoa(index+1)
		}
		chapters = append(chapters, catalog.AudioChapter{
			Index:        index + 1,
			Title:        title,
			StartSeconds: parseMarkerSeconds(marker.Time),
		})
	}

	for i := 0; i < len(chapters)-1; i++ {
		chapters[i].EndSeconds = chapters[i+1].StartSeconds
	}
	if len(chapters) > 0 {
		last := &chapters[len(chapters)-1]
		if last.EndSeconds <= last.StartSeconds {
			last.EndSeconds = last.StartSeconds + 1
		}
	}
	return chapters
}

func parseMarkerSeconds(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	parts := strings.Split(value, ":")
	seconds := 0.0
	multiplier := 1.0
	for i := len(parts) - 1; i >= 0; i-- {
		part := strings.TrimSpace(parts[i])
		parsed, _ := strconv.ParseFloat(part, 64)
		seconds += parsed * multiplier
		multiplier *= 60
	}
	return int(mathRound(seconds))
}
