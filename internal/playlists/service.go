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
	"github.com/bouliehaan/samo-server/internal/catalogstore"
	"github.com/bouliehaan/samo-server/internal/users"
)

var (
	ErrDisabled     = errors.New("playlist service is disabled")
	ErrNotFound     = errors.New("playlist not found")
	ErrForbidden    = errors.New("playlist owner required")
	ErrInvalidInput = errors.New("invalid playlist input")
	// ErrSystemPlaylist rejects client mutations of server-managed playlists
	// (e.g. the explo "Explore" queue). Their name and membership are re-derived
	// by the owning service on every pass, so a client edit would not stick -
	// it would be silently reverted, which reads as data loss. Refusing loudly
	// here is the honest behavior; internal callers use the System* methods.
	ErrSystemPlaylist = errors.New("this playlist is managed by the server")
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
	// System marks a server-managed playlist (e.g. explo). Not settable via
	// the public API - only internal callers (explo service) pass true.
	System bool `json:"-"`
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
		  track_count, duration_seconds, images_json, playback_json, created_at, updated_at, system
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, '[]', '{}', ?, ?, ?)`,
		id, name, strings.TrimSpace(input.Description), ownerID, boolInt(input.Public),
		boolInt(input.Collaborative), jsonText(trackIDs), len(trackIDs), duration, now, now, boolInt(input.System))
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
	if current.System {
		return catalog.MusicPlaylist{}, ErrSystemPlaylist
	}
	if err := assertOwner(ownerID, current.OwnerID); err != nil {
		// Admins may edit any NON-system playlist, for the same reason Delete
		// allows it: filesystem imports and migrated rows land under the
		// internal bootstrap account no human can authenticate as. Every
		// surface shows those playlists (PlaylistVisibleToUser treats a
		// bootstrap owner as public) and an admin can delete them, so without
		// this override they were the one thing nobody could write to —
		// adding a song 403'd "playlist owner required" on desktop and mobile.
		admin, adminErr := s.requesterIsAdmin(ctx, ownerID)
		if adminErr != nil || !admin {
			return catalog.MusicPlaylist{}, err
		}
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
	if current.System {
		return ErrSystemPlaylist
	}
	if err := assertOwner(ownerID, current.OwnerID); err != nil {
		// Admins may delete any NON-system playlist. Filesystem imports and
		// migrated rows land under the internal bootstrap account, which no
		// human can authenticate as — without this override those rows are
		// undeletable by everyone (the web UI hides the button, the API 403s).
		admin, adminErr := s.requesterIsAdmin(ctx, ownerID)
		if adminErr != nil || !admin {
			return err
		}
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
	// Record the deletion so the scanner's .m3u auto-import cannot resurrect
	// the playlist on the next pass. Names never present on disk just leave
	// an inert row; a manual API import of the same name clears it.
	if err := s.writeTombstone(ctx, current.Name); err != nil {
		return err
	}
	return catalogstore.DeleteMetadataOverridesForTarget(ctx, s.db, catalog.OverrideKindMusicPlaylist, id)
}

// playlistNameKey is the tombstone identity: scan imports name playlists after
// the .m3u basename, so a case-folded trimmed name is the stable key.
func playlistNameKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func (s *Service) writeTombstone(ctx context.Context, name string) error {
	key := playlistNameKey(name)
	if key == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO playlist_tombstones (name_key, name, deleted_at)
		VALUES (?, ?, NOW())
		ON CONFLICT (name_key) DO UPDATE SET deleted_at = NOW()`,
		key, strings.TrimSpace(name))
	if err != nil {
		return fmt.Errorf("write playlist tombstone: %w", err)
	}
	return nil
}

func (s *Service) clearTombstone(ctx context.Context, name string) error {
	key := playlistNameKey(name)
	if key == "" {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM playlist_tombstones WHERE name_key = ?`, key); err != nil {
		return fmt.Errorf("clear playlist tombstone: %w", err)
	}
	return nil
}

func (s *Service) nameTombstoned(ctx context.Context, name string) (bool, error) {
	key := playlistNameKey(name)
	if key == "" {
		return false, nil
	}
	var one int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM playlist_tombstones WHERE name_key = ?`, key).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read playlist tombstone: %w", err)
	}
	return true, nil
}

func (s *Service) requesterIsAdmin(ctx context.Context, requesterID string) (bool, error) {
	requesterID = strings.TrimSpace(requesterID)
	if requesterID == "" {
		return false, nil
	}
	var role string
	err := s.db.QueryRowContext(ctx, `SELECT role FROM users WHERE id = ?`, requesterID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("resolve requester role: %w", err)
	}
	return strings.TrimSpace(role) == users.RoleAdmin, nil
}

// SetSystemTracks replaces the membership of a SYSTEM playlist. It is the
// internal write path for server-managed playlists (the explo reconciler):
// Update/Delete refuse system rows so no client can edit a server-derived
// queue out from under its reconciler, and this method refuses non-system
// rows so it can never become a backdoor around the ownership checks.
func (s *Service) SetSystemTracks(ctx context.Context, id string, trackIDs []string) (catalog.MusicPlaylist, error) {
	if s == nil || s.db == nil {
		return catalog.MusicPlaylist{}, ErrDisabled
	}
	current, err := s.loadByID(ctx, id)
	if err != nil {
		return catalog.MusicPlaylist{}, err
	}
	if !current.System {
		return catalog.MusicPlaylist{}, ErrForbidden
	}
	validated, duration, err := s.validateTrackIDs(ctx, trackIDs)
	if err != nil {
		return catalog.MusicPlaylist{}, err
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE music_playlists
		SET track_ids_json = ?,
		    track_count = ?,
		    duration_seconds = ?,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		jsonText(validated), len(validated), duration, id)
	if err != nil {
		return catalog.MusicPlaylist{}, fmt.Errorf("update system playlist: %w", err)
	}
	return s.loadByID(ctx, id)
}

func (s *Service) loadByID(ctx context.Context, id string) (catalog.MusicPlaylist, error) {
	var (
		item          catalog.MusicPlaylist
		public        int
		collaborative int
		system        int
		trackIDsJSON  string
		imagesJSON    string
		playbackJSON  string
		createdAt     sql.NullString
		updatedAt     sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, name, description, owner_id, public, collaborative, track_ids_json,
		       track_count, duration_seconds, images_json, playback_json, created_at, updated_at, system
		FROM music_playlists
		WHERE id = ?`, id).Scan(
		&item.ID, &item.Name, &item.Description, &item.OwnerID, &public, &collaborative,
		&trackIDsJSON, &item.TrackCount, &item.DurationSeconds, &imagesJSON, &playbackJSON,
		&createdAt, &updatedAt, &system,
	)
	if err == sql.ErrNoRows {
		return catalog.MusicPlaylist{}, ErrNotFound
	}
	if err != nil {
		return catalog.MusicPlaylist{}, fmt.Errorf("load playlist: %w", err)
	}
	item.Public = public != 0
	item.Collaborative = collaborative != 0
	item.System = system != 0
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
