package sources

import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

type scanner interface {
	Scan(dest ...any) error
}

func scanPodcastFeed(row scanner) (PodcastFeed, error) {
	var feed PodcastFeed
	var explicit int
	var pollEnabled int
	var categoriesJSON string
	var lastFetchedAt, nextPollAt, lastPollStartedAt, lastPollFinishedAt, createdAt, updatedAt sql.NullString
	if err := row.Scan(
		&feed.ID,
		&feed.PodcastID,
		&feed.FeedURL,
		&feed.Title,
		&feed.Description,
		&feed.Author,
		&feed.SiteURL,
		&feed.ImageURL,
		&feed.Language,
		&explicit,
		&categoriesJSON,
		&feed.OwnerName,
		&feed.OwnerEmail,
		&feed.EpisodeCount,
		&feed.Status,
		&feed.LastError,
		&lastFetchedAt,
		&pollEnabled,
		&feed.Poll.IntervalSeconds,
		&nextPollAt,
		&lastPollStartedAt,
		&lastPollFinishedAt,
		&feed.Poll.ConsecutiveErrors,
		&createdAt,
		&updatedAt,
	); err != nil {
		return PodcastFeed{}, err
	}
	feed.Explicit = explicit != 0
	decodeJSON(categoriesJSON, &feed.Categories)
	feed.LastFetchedAt = parseTimePtr(lastFetchedAt)
	feed.Poll.Enabled = pollEnabled != 0
	feed.Poll.NextPollAt = parseTimePtr(nextPollAt)
	feed.Poll.LastPollStartedAt = parseTimePtr(lastPollStartedAt)
	feed.Poll.LastPollFinishedAt = parseTimePtr(lastPollFinishedAt)
	feed.CreatedAt = parseTimePtr(createdAt)
	feed.UpdatedAt = parseTimePtr(updatedAt)
	return feed, nil
}

func scanInternetRadioStation(row scanner) (InternetRadioStation, error) {
	var station InternetRadioStation
	var enabled int
	var probeEnabled int
	var tagsJSON string
	var lastCheckedAt, createdAt, updatedAt sql.NullString
	var nowPlayingRaw, nowPlayingArtist, nowPlayingTitle sql.NullString
	var nowPlayingUpdatedAt, nextProbeAt, lastProbeStartedAt, lastProbeFinishedAt sql.NullString
	var lastProbeError, probeStatus sql.NullString
	var probeInterval, consecutiveProbeErrors int
	if err := row.Scan(
		&station.ID,
		&station.Name,
		&station.Description,
		&station.StreamURL,
		&station.HomepageURL,
		&station.ImageURL,
		&station.ContentType,
		&station.Codec,
		&station.Bitrate,
		&station.Country,
		&station.Language,
		&tagsJSON,
		&enabled,
		&lastCheckedAt,
		&createdAt,
		&updatedAt,
		&nowPlayingRaw,
		&nowPlayingArtist,
		&nowPlayingTitle,
		&nowPlayingUpdatedAt,
		&probeEnabled,
		&probeInterval,
		&nextProbeAt,
		&lastProbeStartedAt,
		&lastProbeFinishedAt,
		&lastProbeError,
		&consecutiveProbeErrors,
		&probeStatus,
	); err != nil {
		return InternetRadioStation{}, err
	}
	station.Enabled = enabled != 0
	decodeJSON(tagsJSON, &station.Tags)
	station.LastCheckedAt = parseTimePtr(lastCheckedAt)
	station.CreatedAt = parseTimePtr(createdAt)
	station.UpdatedAt = parseTimePtr(updatedAt)

	raw := strings.TrimSpace(nullableString(nowPlayingRaw))
	title := strings.TrimSpace(nullableString(nowPlayingTitle))
	artist := strings.TrimSpace(nullableString(nowPlayingArtist))
	updatedAtPtr := parseTimePtr(nowPlayingUpdatedAt)
	if raw != "" || title != "" || artist != "" {
		station.NowPlaying = &InternetRadioNowPlaying{
			Raw:       raw,
			Title:     title,
			Artist:    artist,
			UpdatedAt: updatedAtPtr,
		}
	}

	station.Probe = ProbeSchedule{
		Enabled:             probeEnabled != 0,
		IntervalSeconds:     probeInterval,
		NextProbeAt:         parseTimePtr(nextProbeAt),
		LastProbeStartedAt:  parseTimePtr(lastProbeStartedAt),
		LastProbeFinishedAt: parseTimePtr(lastProbeFinishedAt),
		LastError:           strings.TrimSpace(nullableString(lastProbeError)),
		Status:              strings.TrimSpace(nullableString(probeStatus)),
		ConsecutiveErrors:   consecutiveProbeErrors,
	}
	return station, nil
}

func nullableString(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func paginate[T any](items []T, page catalog.PageRequest) catalog.Page[T] {
	page = normalizePage(page)
	total := len(items)
	if page.Offset > total {
		return catalog.Page[T]{Items: []T{}, Total: total, Limit: page.Limit, Offset: page.Offset}
	}

	end := page.Offset + page.Limit
	if end > total {
		end = total
	}
	return catalog.Page[T]{
		Items:  append([]T(nil), items[page.Offset:end]...),
		Total:  total,
		Limit:  page.Limit,
		Offset: page.Offset,
	}
}

func normalizePage(page catalog.PageRequest) catalog.PageRequest {
	if page.Limit <= 0 {
		page.Limit = 50
	}
	if page.Limit > 500 {
		page.Limit = 500
	}
	if page.Offset < 0 {
		page.Offset = 0
	}
	return page
}

func decodeJSON(value string, out any) {
	if value == "" || value == "null" {
		return
	}
	_ = json.Unmarshal([]byte(value), out)
}

func parseTimePtr(value sql.NullString) *time.Time {
	if !value.Valid || value.String == "" {
		return nil
	}
	formats := []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"}
	for _, format := range formats {
		parsed, err := time.Parse(format, value.String)
		if err == nil {
			return &parsed
		}
	}
	return nil
}
