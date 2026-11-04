package wrapped

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/scrobble"
)

type play = scrobble.Play

func loadSettings(ctx context.Context, db *sql.DB, userID string) (Settings, error) {
	var (
		settings  Settings
		enabledAt sql.NullTime
	)
	err := db.QueryRowContext(ctx, `
		SELECT local_history, enabled_at FROM wrapped_settings WHERE user_id = ?`, userID).
		Scan(&settings.LocalHistory, &enabledAt)
	if err != nil && err != sql.ErrNoRows {
		return Settings{}, fmt.Errorf("load wrapped settings: %w", err)
	}
	if enabledAt.Valid {
		at := enabledAt.Time.UTC()
		settings.EnabledAt = &at
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM wrapped_listens WHERE user_id = ?`, userID).
		Scan(&settings.Listens); err != nil {
		return Settings{}, fmt.Errorf("count wrapped listens: %w", err)
	}
	return settings, nil
}

func localHistoryOn(ctx context.Context, db *sql.DB, userID string) (bool, error) {
	var on bool
	err := db.QueryRowContext(ctx, `SELECT local_history FROM wrapped_settings WHERE user_id = ?`, userID).Scan(&on)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("load wrapped settings: %w", err)
	}
	return on, nil
}

// saveLocalHistory turns samo's own history on or off. enabled_at records the
// first time it was turned on and is kept when it is switched off, so the log
// it began stays dated; it is cleared only with the history itself.
func saveLocalHistory(ctx context.Context, db *sql.DB, userID string, on bool, now time.Time) error {
	var enabledAt any
	if on {
		enabledAt = now.UTC()
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO wrapped_settings (user_id, local_history, enabled_at, updated_at)
		VALUES (?, ?, ?, NOW())
		ON CONFLICT(user_id) DO UPDATE SET
			local_history = excluded.local_history,
			enabled_at = COALESCE(wrapped_settings.enabled_at, excluded.enabled_at),
			updated_at = NOW()`, userID, on, enabledAt)
	if err != nil {
		return fmt.Errorf("save wrapped settings: %w", err)
	}
	return nil
}

func deleteHistory(ctx context.Context, db *sql.DB, userID string, now time.Time) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, statement := range []string{
		`DELETE FROM wrapped_listens WHERE user_id = ?`,
		`DELETE FROM wrapped_plays WHERE user_id = ?`,
		`DELETE FROM wrapped_reports WHERE user_id = ? AND source = 'samo'`,
	} {
		if _, err := tx.ExecContext(ctx, statement, userID); err != nil {
			return fmt.Errorf("delete wrapped history: %w", err)
		}
	}
	// A history still on starts again from now.
	if _, err := tx.ExecContext(ctx, `
		UPDATE wrapped_settings SET enabled_at = CASE WHEN local_history THEN ?::timestamptz ELSE NULL END,
		       updated_at = NOW()
		WHERE user_id = ?`, now.UTC(), userID); err != nil {
		return fmt.Errorf("delete wrapped history: %w", err)
	}
	return tx.Commit()
}

func loadPlay(ctx context.Context, db *sql.DB, userID, trackID string) (play, error) {
	var (
		playID                                  string
		startedAt, lastPosition, lastObservedAt int64
		lastAdvanceAt, listened, duration       int64
		scrobbled, closed                       int64
	)
	err := db.QueryRowContext(ctx, `
		SELECT play_id, started_at, last_position, last_observed_at, last_advance_at,
		       listened_seconds, duration_seconds, scrobbled, closed
		FROM wrapped_plays
		WHERE user_id = ? AND track_id = ?`, userID, trackID).
		Scan(&playID, &startedAt, &lastPosition, &lastObservedAt, &lastAdvanceAt,
			&listened, &duration, &scrobbled, &closed)
	if err == sql.ErrNoRows {
		return play{UserID: userID, TrackID: trackID}, nil
	}
	if err != nil {
		return play{}, fmt.Errorf("load wrapped play: %w", err)
	}
	return play{
		UserID:          userID,
		TrackID:         trackID,
		PlayID:          playID,
		StartedAt:       unixTime(startedAt),
		LastPosition:    int(lastPosition),
		LastObservedAt:  unixTime(lastObservedAt),
		LastAdvanceAt:   unixTime(lastAdvanceAt),
		ListenedSeconds: int(listened),
		DurationSeconds: int(duration),
		Scrobbled:       scrobbled != 0,
		Closed:          closed != 0,
		Exists:          true,
	}, nil
}

func savePlay(ctx context.Context, db *sql.DB, p play) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO wrapped_plays (
			user_id, track_id, play_id, started_at, last_position, last_observed_at,
			last_advance_at, listened_seconds, duration_seconds, scrobbled, closed, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NOW())
		ON CONFLICT(user_id, track_id) DO UPDATE SET
			play_id = excluded.play_id,
			started_at = excluded.started_at,
			last_position = excluded.last_position,
			last_observed_at = excluded.last_observed_at,
			last_advance_at = excluded.last_advance_at,
			listened_seconds = excluded.listened_seconds,
			duration_seconds = excluded.duration_seconds,
			scrobbled = excluded.scrobbled,
			closed = excluded.closed,
			updated_at = NOW()`,
		p.UserID, p.TrackID, p.PlayID,
		unixSeconds(p.StartedAt), p.LastPosition, unixSeconds(p.LastObservedAt),
		unixSeconds(p.LastAdvanceAt), p.ListenedSeconds, p.DurationSeconds,
		boolInt(p.Scrobbled), boolInt(p.Closed),
	)
	if err != nil {
		return fmt.Errorf("save wrapped play: %w", err)
	}
	return nil
}

type listenRow struct {
	DedupeKey       string
	TrackID         string
	AlbumID         string
	Artist          string
	Album           string
	Title           string
	StartedAt       time.Time
	ListenedSeconds int
	DurationSeconds int
}

// insertListen logs one earned listen. The dedupe key identifies the listen
// itself, so a racing or replayed observation cannot log it twice.
func insertListen(ctx context.Context, db *sql.DB, userID string, row listenRow) (bool, error) {
	result, err := db.ExecContext(ctx, `
		INSERT INTO wrapped_listens (
			user_id, dedupe_key, track_id, album_id, artist, album, title,
			started_at, listened_seconds, duration_seconds
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, dedupe_key) DO NOTHING`,
		userID, row.DedupeKey, row.TrackID, row.AlbumID, row.Artist, row.Album, row.Title,
		row.StartedAt.UTC(), row.ListenedSeconds, row.DurationSeconds)
	if err != nil {
		return false, fmt.Errorf("log wrapped listen: %w", err)
	}
	inserted, _ := result.RowsAffected()
	return inserted > 0, nil
}

func listensBetween(ctx context.Context, db *sql.DB, userID string, from, to time.Time) ([]Listen, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT track_id, album_id, artist, album, title, started_at, listened_seconds
		FROM wrapped_listens
		WHERE user_id = ? AND started_at >= ? AND started_at < ?
		ORDER BY started_at`, userID, from.UTC(), to.UTC())
	if err != nil {
		return nil, fmt.Errorf("load wrapped listens: %w", err)
	}
	defer rows.Close()
	var out []Listen
	for rows.Next() {
		var listen Listen
		if err := rows.Scan(&listen.TrackID, &listen.AlbumID, &listen.Artist, &listen.Album,
			&listen.Title, &listen.At, &listen.Seconds); err != nil {
			return nil, err
		}
		listen.At = listen.At.UTC()
		out = append(out, listen)
	}
	return out, rows.Err()
}

// catalogIndex matches a Last.fm scrobble to the library by name, so a listen
// of something samo has gets its real length and samo's own cover.
type catalogIndex struct {
	tracks  map[string]catalogTrack // fold(artist) \x00 fold(title)
	artists map[string]string       // fold(name) -> artist id
}

type catalogTrack struct {
	id, albumID string
	seconds     int
}

func loadCatalogIndex(ctx context.Context, db *sql.DB) (catalogIndex, error) {
	index := catalogIndex{tracks: map[string]catalogTrack{}, artists: map[string]string{}}
	rows, err := db.QueryContext(ctx, `
		SELECT id, COALESCE(album_id, ''), display_artist, title, duration_seconds FROM music_tracks`)
	if err != nil {
		return index, fmt.Errorf("load track index: %w", err)
	}
	for rows.Next() {
		var (
			track         catalogTrack
			artist, title string
			seconds       int64
		)
		if err := rows.Scan(&track.id, &track.albumID, &artist, &title, &seconds); err != nil {
			rows.Close()
			return index, err
		}
		track.seconds = int(seconds)
		key := fold(artist) + "\x00" + fold(title)
		if _, taken := index.tracks[key]; !taken {
			index.tracks[key] = track
		}
	}
	rows.Close()
	rows, err = db.QueryContext(ctx, `SELECT id, name FROM music_artists`)
	if err != nil {
		return index, fmt.Errorf("load artist index: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return index, err
		}
		if _, taken := index.artists[fold(name)]; !taken {
			index.artists[fold(name)] = id
		}
	}
	return index, rows.Err()
}

func (c catalogIndex) enrich(listen *Listen) {
	if track, ok := c.tracks[fold(listen.Artist)+"\x00"+fold(listen.Title)]; ok {
		listen.TrackID = track.id
		listen.AlbumID = track.albumID
		if listen.Seconds <= 0 {
			listen.Seconds = track.seconds
		}
	}
	if id, ok := c.artists[fold(listen.Artist)]; ok {
		listen.ArtistID = id
	}
}

// audiobookStats reads the listening sessions samo already keeps for resume
// and bookmarks. Each session is one progress report, so one longer than two
// minutes is a skip ahead rather than listening, and counts as two.
func audiobookStats(ctx context.Context, db *sql.DB, userID string, from, to time.Time) (*AudiobookStats, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT s.audiobook_id, COALESCE(a.book_json, '{}'),
		       SUM(LEAST(s.duration_seconds, 120)), MAX(s.completed)
		FROM listening_sessions s
		JOIN audiobooks a ON a.id = s.audiobook_id
		WHERE s.user_id = ? AND s.started_at >= ? AND s.started_at < ?
		GROUP BY s.audiobook_id, a.book_json`,
		userID, from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, fmt.Errorf("load audiobook sessions: %w", err)
	}
	defer rows.Close()
	stats := &AudiobookStats{}
	for rows.Next() {
		var (
			id, bookJSON string
			seconds      int64
			completed    int64
		)
		if err := rows.Scan(&id, &bookJSON, &seconds, &completed); err != nil {
			return nil, err
		}
		var book struct {
			Title   string `json:"title"`
			Authors []struct {
				Name string `json:"name"`
			} `json:"authors"`
		}
		_ = json.Unmarshal([]byte(bookJSON), &book)
		author := ""
		if len(book.Authors) > 0 {
			author = book.Authors[0].Name
		}
		stats.Books++
		stats.Minutes += int(seconds) / 60
		if completed != 0 {
			stats.Finished++
		}
		stats.Top = append(stats.Top, Ranked{Name: book.Title, Artist: author, Minutes: int(seconds) / 60, TrackID: id})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if stats.Books == 0 || stats.Minutes == 0 {
		return nil, nil
	}
	sort.SliceStable(stats.Top, func(i, j int) bool { return stats.Top[i].Minutes > stats.Top[j].Minutes })
	if len(stats.Top) > topN {
		stats.Top = stats.Top[:topN]
	}
	return stats, nil
}

// podcastStats reads the per-episode playback samo keeps for resume: an
// episode counts for the year it was last played in.
func podcastStats(ctx context.Context, db *sql.DB, userID string, from, to time.Time) (*PodcastStats, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT e.podcast_id, COALESCE(p.podcast_json, '{}'), up.state_json
		FROM user_playback up
		JOIN podcast_episodes e ON e.id = up.target_id
		JOIN podcasts p ON p.id = e.podcast_id
		WHERE up.user_id = ? AND up.target_kind = 'podcast-episode'`, userID)
	if err != nil {
		return nil, fmt.Errorf("load podcast playback: %w", err)
	}
	defer rows.Close()
	shows := map[string]*Ranked{}
	stats := &PodcastStats{}
	for rows.Next() {
		var podcastID, podcastJSON, stateJSON string
		if err := rows.Scan(&podcastID, &podcastJSON, &stateJSON); err != nil {
			return nil, err
		}
		var state struct {
			LastPlayedAt    time.Time `json:"lastPlayedAt"`
			Completed       bool      `json:"completed"`
			ProgressSeconds int       `json:"progressSeconds"`
		}
		if json.Unmarshal([]byte(stateJSON), &state) != nil || state.LastPlayedAt.IsZero() {
			continue
		}
		if state.LastPlayedAt.Before(from) || !state.LastPlayedAt.Before(to) {
			continue
		}
		if !state.Completed && state.ProgressSeconds < 60 {
			// Opened, not listened to.
			continue
		}
		stats.Episodes++
		if state.Completed {
			stats.Finished++
		}
		show, ok := shows[podcastID]
		if !ok {
			var podcast struct {
				Title  string `json:"title"`
				Author string `json:"author"`
			}
			_ = json.Unmarshal([]byte(podcastJSON), &podcast)
			show = &Ranked{Name: podcast.Title, Artist: podcast.Author, AlbumID: podcastID}
			shows[podcastID] = show
		}
		show.Plays++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if stats.Episodes == 0 {
		return nil, nil
	}
	stats.Shows = len(shows)
	for _, show := range shows {
		stats.Top = append(stats.Top, *show)
	}
	sort.SliceStable(stats.Top, func(i, j int) bool {
		if stats.Top[i].Plays != stats.Top[j].Plays {
			return stats.Top[i].Plays > stats.Top[j].Plays
		}
		return strings.ToLower(stats.Top[i].Name) < strings.ToLower(stats.Top[j].Name)
	})
	if len(stats.Top) > topN {
		stats.Top = stats.Top[:topN]
	}
	return stats, nil
}

func loadReport(ctx context.Context, db *sql.DB, userID string, year int, source string) (Report, bool, error) {
	var raw string
	err := db.QueryRowContext(ctx, `
		SELECT report_json FROM wrapped_reports WHERE user_id = ? AND year = ? AND source = ?`,
		userID, year, source).Scan(&raw)
	if err == sql.ErrNoRows {
		return Report{}, false, nil
	}
	if err != nil {
		return Report{}, false, fmt.Errorf("load wrapped report: %w", err)
	}
	var report Report
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		return Report{}, false, nil
	}
	return report, true, nil
}

func saveReport(ctx context.Context, db *sql.DB, userID string, report Report) error {
	raw, err := json.Marshal(report)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO wrapped_reports (user_id, year, source, report_json, built_at)
		VALUES (?, ?, ?, ?, NOW())
		ON CONFLICT(user_id, year, source) DO UPDATE SET
			report_json = excluded.report_json,
			built_at = NOW()`, userID, report.Year, report.Source, string(raw))
	if err != nil {
		return fmt.Errorf("save wrapped report: %w", err)
	}
	return nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func unixSeconds(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func unixTime(seconds int64) time.Time {
	if seconds <= 0 {
		return time.Time{}
	}
	return time.Unix(seconds, 0).UTC()
}
