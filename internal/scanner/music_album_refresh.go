package scanner

import (
	"context"
	"database/sql"
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
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT t.album_id
		FROM music_tracks t
		JOIN media_files mf ON mf.track_id = t.id
		WHERE mf.library_id = ? AND t.album_id IS NOT NULL AND TRIM(t.album_id) != ''`,
		libraryID)
	if err != nil {
		return fmt.Errorf("list albums for library refresh: %w", err)
	}
	defer rows.Close()

	ids := map[string]struct{}{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("scan album id for refresh: %w", err)
		}
		if id = strings.TrimSpace(id); id != "" {
			ids[id] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return s.refreshMusicAlbums(ctx, ids)
}

type albumRefreshTrack struct {
	title         string
	albumTitle    string
	displayArtist string
	imagesJSON    string
}

func (s *Scanner) refreshOneMusicAlbum(ctx context.Context, albumID string) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT title, album_title, display_artist, images_json
		FROM music_tracks
		WHERE album_id = ?
		ORDER BY disc_number, track_number, title`, albumID)
	if err != nil {
		return fmt.Errorf("load tracks for album refresh %q: %w", albumID, err)
	}
	defer rows.Close()

	var tracks []albumRefreshTrack
	for rows.Next() {
		var row albumRefreshTrack
		if err := rows.Scan(&row.title, &row.albumTitle, &row.displayArtist, &row.imagesJSON); err != nil {
			return fmt.Errorf("scan track for album refresh: %w", err)
		}
		tracks = append(tracks, row)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(tracks) == 0 {
		return nil
	}

	titleCandidates := make([]string, 0, len(tracks))
	for _, row := range tracks {
		if v := strings.TrimSpace(row.albumTitle); v != "" {
			titleCandidates = append(titleCandidates, v)
		}
	}
	title := majorityString(titleCandidates, strings.TrimSpace(tracks[0].albumTitle))

	displayArtist := strings.TrimSpace(albumDisplayArtistFromDB(ctx, s.db, albumID))
	if displayArtist == "" {
		artistCandidates := make([]string, 0, len(tracks))
		for _, row := range tracks {
			if v := strings.TrimSpace(row.displayArtist); v != "" {
				artistCandidates = append(artistCandidates, v)
			}
		}
		displayArtist = majorityString(artistCandidates, "")
	}

	var coverImages []catalog.Image
	for _, row := range tracks {
		var images []catalog.Image
		if row.imagesJSON != "" && row.imagesJSON != "[]" {
			_ = json.Unmarshal([]byte(row.imagesJSON), &images)
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

	_, err = s.db.ExecContext(ctx, `
		UPDATE music_albums
		SET title = CASE WHEN ? != '' THEN ? ELSE title END,
		    display_artist = CASE WHEN ? != '' THEN ? ELSE display_artist END,
		    images_json = CASE
		      WHEN ? NOT IN ('[]', 'null', '')
		      THEN ?
		      ELSE images_json
		    END,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		title, title,
		displayArtist, displayArtist,
		imagesJSON, imagesJSON,
		albumID)
	if err != nil {
		return fmt.Errorf("refresh music album %q: %w", albumID, err)
	}
	return nil
}

func albumDisplayArtistFromDB(ctx context.Context, db *sql.DB, albumID string) string {
	rows, err := db.QueryContext(ctx, `
		SELECT a.name
		FROM music_album_artists aa
		JOIN music_artists a ON a.id = aa.artist_id
		WHERE aa.album_id = ?
		ORDER BY aa.position`, albumID)
	if err != nil {
		return ""
	}
	defer rows.Close()

	names := make([]string, 0, 4)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return strings.Join(names, ", ")
		}
		name = strings.TrimSpace(name)
		if name != "" {
			names = append(names, name)
		}
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
