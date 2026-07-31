package scanner

import (
	"context"
	"encoding/json"
	"log"
	"strings"
)

// noteTrackIDMigration records that a music_tracks row was superseded during this
// scan (e.g. track_pid changed but the file path is the same). Playlists keep
// stable track_ids_json; we remap stale ids before orphan prune.
func (s *Scanner) noteTrackIDMigration(oldID, newID string) {
	oldID = strings.TrimSpace(oldID)
	newID = strings.TrimSpace(newID)
	if oldID == "" || newID == "" || oldID == newID {
		return
	}
	if s.trackIDMigrations == nil {
		s.trackIDMigrations = map[string]string{}
	}
	// Follow migration chains so A->B then B->C becomes A->C.
	for existing, next := range s.trackIDMigrations {
		if next == oldID {
			s.trackIDMigrations[existing] = newID
		}
	}
	s.trackIDMigrations[oldID] = newID
}

func (s *Scanner) resolveMigratedTrackID(trackID string) string {
	trackID = strings.TrimSpace(trackID)
	if trackID == "" || len(s.trackIDMigrations) == 0 {
		return trackID
	}
	seen := map[string]struct{}{trackID: {}}
	for {
		next, ok := s.trackIDMigrations[trackID]
		if !ok || next == "" || next == trackID {
			return trackID
		}
		if _, loop := seen[next]; loop {
			return trackID
		}
		seen[next] = struct{}{}
		trackID = next
	}
}

// reconcilePlaylistTrackReferences rewrites playlist track_ids_json after a
// scan so rows still point at valid music_tracks. Does not delete playlists.
func (s *Scanner) reconcilePlaylistTrackReferences(ctx context.Context) (int, error) {
	playlists, err := s.store.PlaylistTrackReferences(ctx)
	if err != nil {
		return 0, err
	}

	updated := 0
	for _, playlist := range playlists {
		ids := decodeTrackIDList(playlist.TrackIDsJSON)
		if len(ids) == 0 {
			continue
		}
		remapped, changed := s.remapPlaylistTrackIDs(ctx, ids)
		if !changed {
			continue
		}
		if err := s.store.SetPlaylistTrackIDs(ctx, playlist.ID, remapped); err != nil {
			return updated, err
		}
		updated++
	}
	if updated > 0 {
		log.Printf("scanner: remapped track ids on %d playlist(s)", updated)
	}
	return updated, nil
}

func (s *Scanner) remapPlaylistTrackIDs(ctx context.Context, ids []string) ([]string, bool) {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(ids))
	changed := false
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		resolved := s.resolveMigratedTrackID(id)
		if resolved != id {
			changed = true
		}
		if !s.trackIDExists(ctx, resolved) {
			changed = true
			continue
		}
		if _, dup := seen[resolved]; dup {
			if resolved != id {
				changed = true
			}
			continue
		}
		seen[resolved] = struct{}{}
		out = append(out, resolved)
		if resolved != id {
			changed = true
		}
	}
	if len(out) != len(ids) {
		changed = true
	}
	return out, changed
}

func (s *Scanner) trackIDExists(ctx context.Context, trackID string) bool {
	trackID = strings.TrimSpace(trackID)
	if trackID == "" {
		return false
	}
	return s.store.MusicTrackExists(ctx, trackID)
}

func decodeTrackIDList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" || raw == "null" {
		return nil
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return nil
	}
	return ids
}
