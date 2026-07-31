package scanner

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// refreshMusicAlbums rebuilds album rows from their tracks, mirroring Navidrome's
// phase 3 (refresh albums). Per-file upserts can leave stale titles, artists, or
// cover metadata on the album when later tracks disagree.
func (s *Scanner) refreshMusicAlbums(ctx context.Context, albumIDs map[string]struct{}) error {
	if len(albumIDs) == 0 {
		return nil
	}
	ids := make([]string, 0, len(albumIDs))
	for id := range albumIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	total := len(ids)
	for index, albumID := range ids {
		if err := s.refreshOneMusicAlbum(ctx, albumID); err != nil {
			return err
		}
		if index > 0 && index%200 == 0 {
			log.Printf("scanner: album refresh progress %d/%d", index, total)
			if s.onActivity != nil {
				s.onActivity(fmt.Sprintf("refreshing albums… %d/%d", index, total))
			}
		}
	}
	if total > 0 {
		log.Printf("scanner: album refresh done (%d albums)", total)
	}
	return nil
}

// refreshMusicAlbumsForLibrary refreshes every album that has indexed tracks in
// the library. Used after full scans so grouping or tag fixes propagate.
func (s *Scanner) refreshMusicAlbumsForLibrary(ctx context.Context, libraryID string) error {
	libraryID = strings.TrimSpace(libraryID)
	if libraryID == "" {
		return nil
	}
	albumIDs, err := s.store.AlbumIDsWithTracksInLibrary(ctx, libraryID)
	if err != nil {
		return err
	}
	ids := map[string]struct{}{}
	for _, id := range albumIDs {
		if id = strings.TrimSpace(id); id != "" {
			ids[id] = struct{}{}
		}
	}
	return s.refreshMusicAlbums(ctx, ids)
}

func (s *Scanner) refreshOneMusicAlbum(ctx context.Context, albumID string) error {
	tracks, err := s.store.AlbumRefreshTracks(ctx, albumID)
	if err != nil {
		return err
	}
	if len(tracks) == 0 {
		return nil
	}

	titleCandidates := make([]string, 0, len(tracks))
	for _, row := range tracks {
		if v := strings.TrimSpace(row.AlbumTitle); v != "" {
			titleCandidates = append(titleCandidates, v)
		}
	}
	title := majorityString(titleCandidates, strings.TrimSpace(tracks[0].AlbumTitle))

	displayArtist := strings.TrimSpace(s.albumDisplayArtist(ctx, albumID))
	if displayArtist == "" {
		artistCandidates := make([]string, 0, len(tracks))
		for _, row := range tracks {
			if v := strings.TrimSpace(row.DisplayArtist); v != "" {
				artistCandidates = append(artistCandidates, v)
			}
		}
		displayArtist = majorityString(artistCandidates, "")
	}

	var coverImages []catalog.Image
	for _, row := range tracks {
		var images []catalog.Image
		if row.ImagesJSON != "" && row.ImagesJSON != "[]" {
			_ = json.Unmarshal([]byte(row.ImagesJSON), &images)
		}
		if images = nonEmptyCatalogImages(images); len(images) > 0 {
			coverImages = images
			break
		}
	}

	imagesJSON := "[]"
	if len(coverImages) > 0 {
		imagesJSON = jsonText(coverImages)
	}

	return s.store.RefreshMusicAlbum(ctx, albumID, title, displayArtist, imagesJSON)
}

// albumDisplayArtist renders the album's credited artists as one display
// string. Credits, when present, outrank whatever the individual tracks claim.
func (s *Scanner) albumDisplayArtist(ctx context.Context, albumID string) string {
	names, err := s.store.AlbumArtistNames(ctx, albumID)
	if err != nil {
		return ""
	}
	return strings.Join(names, ", ")
}

func majorityString(values []string, fallback string) string {
	if len(values) == 0 {
		return strings.TrimSpace(fallback)
	}
	counts := map[string]int{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		counts[value]++
	}
	if len(counts) == 0 {
		return strings.TrimSpace(fallback)
	}
	best := ""
	bestCount := 0
	for value, count := range counts {
		if count > bestCount || (count == bestCount && value < best) {
			best = value
			bestCount = count
		}
	}
	if best != "" {
		return best
	}
	return strings.TrimSpace(fallback)
}

func nonEmptyCatalogImages(images []catalog.Image) []catalog.Image {
	if len(images) == 0 {
		return nil
	}
	filtered := make([]catalog.Image, 0, len(images))
	for _, image := range images {
		if strings.TrimSpace(image.Path) != "" ||
			strings.TrimSpace(image.URL) != "" ||
			strings.TrimSpace(image.ID) != "" {
			filtered = append(filtered, image)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}
