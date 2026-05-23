package playlists

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

var (
	ErrDisabled     = errors.New("playlist service is disabled")
	ErrNotFound     = errors.New("playlist not found")
	ErrForbidden    = errors.New("playlist owner required")
	ErrInvalidInput = errors.New("invalid playlist input")
)

type Service struct {
	db *sql.DB
}

func New(db *sql.DB) *Service {
	return &Service{db: db}
}

type CreateInput struct {
	Name          string   `json:"name"`
	Description   string   `json:"description,omitempty"`
	Public        bool     `json:"public"`
	Collaborative bool     `json:"collaborative,omitempty"`
	TrackIDs      []string `json:"trackIds,omitempty"`
}

type UpdateInput struct {
	Name          *string  `json:"name,omitempty"`
	Description   *string  `json:"description,omitempty"`
	Public        *bool    `json:"public,omitempty"`
	Collaborative *bool    `json:"collaborative,omitempty"`
	TrackIDs      []string `json:"trackIds,omitempty"`
}

func (s *Service) Create(ctx context.Context, ownerID string, input CreateInput) (catalog.MusicPlaylist, error) {
	if s == nil || s.db == nil {
		return catalog.MusicPlaylist{}, ErrDisabled
	}
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		return catalog.MusicPlaylist{}, ErrForbidden
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return catalog.MusicPlaylist{}, ErrInvalidInput
	}
	trackIDs, duration, err := s.validateTrackIDs(ctx, input.TrackIDs)
	if err != nil {
		return catalog.MusicPlaylist{}, err
	}
	id := playlistID(ownerID, name)
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO music_playlists (
		  id, name, description, owner_id, public, collaborative, track_ids_json,
		  track_count, duration_seconds, images_json, playback_json, created_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, '[]', '{}', ?, ?)`,
		id, name, strings.TrimSpace(input.Description), ownerID, boolInt(input.Public),
		boolInt(input.Collaborative), jsonText(trackIDs), len(trackIDs), duration, now, now)
	if err != nil {
		return catalog.MusicPlaylist{}, fmt.Errorf("create playlist: %w", err)
	}
	return s.loadByID(ctx, id)
}

func (s *Service) Update(ctx context.Context, ownerID, id string, input UpdateInput) (catalog.MusicPlaylist, error) {
	if s == nil || s.db == nil {
		return catalog.MusicPlaylist{}, ErrDisabled
	}
	current, err := s.loadByID(ctx, id)
	if err != nil {
		return catalog.MusicPlaylist{}, err
	}
	if err := assertOwner(ownerID, current.OwnerID); err != nil {
		return catalog.MusicPlaylist{}, err
	}

	name := current.Name
	if input.Name != nil {
		name = strings.TrimSpace(*input.Name)
		if name == "" {
			return catalog.MusicPlaylist{}, ErrInvalidInput
		}
	}
	description := current.Description
	if input.Description != nil {
		description = strings.TrimSpace(*input.Description)
	}
	public := current.Public
	if input.Public != nil {
		public = *input.Public
	}
	collaborative := current.Collaborative
	if input.Collaborative != nil {
		collaborative = *input.Collaborative
	}
	trackIDs := append([]string(nil), current.TrackIDs...)
	if input.TrackIDs != nil {
		trackIDs, _, err = s.validateTrackIDs(ctx, input.TrackIDs)
		if err != nil {
			return catalog.MusicPlaylist{}, err
		}
	}
	duration, err := s.sumTrackDuration(ctx, trackIDs)
	if err != nil {
		return catalog.MusicPlaylist{}, err
	}

	_, err = s.db.ExecContext(ctx, `
		UPDATE music_playlists
		SET name = ?,
		    description = ?,
		    public = ?,
		    collaborative = ?,
		    track_ids_json = ?,
		    track_count = ?,
		    duration_seconds = ?,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		name, description, boolInt(public), boolInt(collaborative), jsonText(trackIDs),
		len(trackIDs), duration, id)
	if err != nil {
		return catalog.MusicPlaylist{}, fmt.Errorf("update playlist: %w", err)
	}
	return s.loadByID(ctx, id)
}

func (s *Service) Delete(ctx context.Context, ownerID, id string) error {
	if s == nil || s.db == nil {
		return ErrDisabled
	}
	current, err := s.loadByID(ctx, id)
	if err != nil {
		return err
	}
	if err := assertOwner(ownerID, current.OwnerID); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM music_playlists WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete playlist: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return catalog.DeleteMetadataOverridesForTarget(ctx, s.db, catalog.OverrideKindMusicPlaylist, id)
}

func (s *Service) loadByID(ctx context.Context, id string) (catalog.MusicPlaylist, error) {
	var (
		item          catalog.MusicPlaylist
		public        int
		collaborative int
		trackIDsJSON  string
		imagesJSON    string
		playbackJSON  string
		createdAt     sql.NullString
		updatedAt     sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, name, description, owner_id, public, collaborative, track_ids_json,
		       track_count, duration_seconds, images_json, playback_json, created_at, updated_at
		FROM music_playlists
		WHERE id = ?`, id).Scan(
		&item.ID, &item.Name, &item.Description, &item.OwnerID, &public, &collaborative,
		&trackIDsJSON, &item.TrackCount, &item.DurationSeconds, &imagesJSON, &playbackJSON,
		&createdAt, &updatedAt,
	)
	if err == sql.ErrNoRows {
		return catalog.MusicPlaylist{}, ErrNotFound
	}
	if err != nil {
		return catalog.MusicPlaylist{}, fmt.Errorf("load playlist: %w", err)
	}
	item.Public = public != 0
	item.Collaborative = collaborative != 0
	decodeJSON(trackIDsJSON, &item.TrackIDs)
	decodeJSON(imagesJSON, &item.Images)
	decodeJSON(playbackJSON, &item.Playback)
	item.CreatedAt = parseTimePtr(createdAt)
	item.UpdatedAt = parseTimePtr(updatedAt)
	return item, nil
}

func parseTimePtr(value sql.NullString) *time.Time {
	if !value.Valid || strings.TrimSpace(value.String) == "" {
		return nil
	}
	formats := []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"}
	for _, format := range formats {
		parsed, err := time.Parse(format, value.String)
		if err == nil {
			parsed = parsed.UTC()
			return &parsed
		}
	}
	return nil
}

func (s *Service) validateTrackIDs(ctx context.Context, trackIDs []string) ([]string, int, error) {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(trackIDs))
	for _, trackID := range trackIDs {
		trackID = strings.TrimSpace(trackID)
		if trackID == "" {
			continue
		}
		if _, ok := seen[trackID]; ok {
			continue
		}
		var found string
		if err := s.db.QueryRowContext(ctx, `SELECT id FROM music_tracks WHERE id = ?`, trackID).Scan(&found); err == sql.ErrNoRows {
			return nil, 0, fmt.Errorf("%w: unknown track %q", ErrInvalidInput, trackID)
		} else if err != nil {
			return nil, 0, err
		}
		seen[trackID] = struct{}{}
		out = append(out, trackID)
	}
	duration, err := s.sumTrackDuration(ctx, out)
	if err != nil {
		return nil, 0, err
	}
	return out, duration, nil
}

func (s *Service) sumTrackDuration(ctx context.Context, trackIDs []string) (int, error) {
	total := 0
	for _, trackID := range trackIDs {
		var duration int
		if err := s.db.QueryRowContext(ctx, `SELECT duration_seconds FROM music_tracks WHERE id = ?`, trackID).Scan(&duration); err != nil {
			return 0, err
		}
		total += duration
	}
	return total, nil
}

func assertOwner(requesterID, ownerID string) error {
	if strings.TrimSpace(requesterID) == "" {
		return ErrForbidden
	}
	if ownerID != "" && ownerID != requesterID {
		return ErrForbidden
	}
	return nil
}

func playlistID(ownerID, name string) string {
	hash := sha256.New()
	hash.Write([]byte(strings.ToLower(strings.TrimSpace(ownerID))))
	hash.Write([]byte{0})
	hash.Write([]byte(strings.ToLower(strings.TrimSpace(name))))
	sum := hash.Sum(nil)
	return "playlist_" + hex.EncodeToString(sum[:12])
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func jsonText(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "[]"
	}
	return string(data)
}

func decodeJSON(value string, out any) {
	if strings.TrimSpace(value) == "" || value == "null" {
		return
	}
	_ = json.Unmarshal([]byte(value), out)
}
