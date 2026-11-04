package wrapped

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/bouliehaan/samo-server/internal/lastfm"
	"github.com/bouliehaan/samo-server/internal/scrobble"
)

// LastFM is the part of the Last.fm integration a Wrapped reads from.
type LastFM interface {
	// LinkedUsername is the Last.fm account a samo user has linked, if any.
	LinkedUsername(ctx context.Context, userID string) (string, bool)
	// UserListens reads that account's scrobbles in [from, to), oldest first,
	// reporting progress as each page of up to 200 arrives.
	UserListens(ctx context.Context, userID string, from, to time.Time, progress func(page, pages int)) ([]lastfm.RecentListen, error)
}

type ServiceOptions struct {
	DB     *sql.DB
	LastFM LastFM
	// Zone is the one days and hours are read in; the server's own.
	Zone      *time.Location
	Now       func() time.Time
	NewPlayID func() string
	Logger    func(format string, args ...any)
}

type Service struct {
	db     *sql.DB
	lastfm LastFM
	zone   *time.Location
	now    func() time.Time
	playID func() string
	logger func(format string, args ...any)

	locks sync.Map // user id -> *sync.Mutex, one observation at a time

	mu       sync.Mutex
	building map[buildKey]*Building
	failed   map[buildKey]error
}

type buildKey struct {
	userID string
	year   int
	source string
}

// Building is a Wrapped being read from Last.fm, which for a year of heavy
// listening is a few hundred requests.
type Building struct {
	Pages      int `json:"pages"`
	TotalPages int `json:"totalPages"`
}

// A Last.fm year is reread at most this often; samo's own is rebuilt each time.
const lastFMReportLifetime = 6 * time.Hour

func NewService(options ServiceOptions) *Service {
	zone := options.Zone
	if zone == nil {
		zone = time.Local
	}
	now := options.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	playID := options.NewPlayID
	if playID == nil {
		playID = newPlayID
	}
	logger := options.Logger
	if logger == nil {
		logger = log.Printf
	}
	return &Service{
		db:       options.DB,
		lastfm:   options.LastFM,
		zone:     zone,
		now:      now,
		playID:   playID,
		logger:   logger,
		building: map[buildKey]*Building{},
		failed:   map[buildKey]error{},
	}
}

func (s *Service) Settings(ctx context.Context, userID string) (Settings, error) {
	return loadSettings(ctx, s.db, userID)
}

// SetLocalHistory turns samo's own listening history on or off for a user.
// Turning it off stops logging; what was logged stays until it is deleted.
func (s *Service) SetLocalHistory(ctx context.Context, userID string, on bool) (Settings, error) {
	if err := saveLocalHistory(ctx, s.db, userID, on, s.now()); err != nil {
		return Settings{}, err
	}
	return loadSettings(ctx, s.db, userID)
}

func (s *Service) DeleteHistory(ctx context.Context, userID string) (Settings, error) {
	if err := deleteHistory(ctx, s.db, userID, s.now()); err != nil {
		return Settings{}, err
	}
	return loadSettings(ctx, s.db, userID)
}

// HandlePlayback is one observation of a music track, the same one Last.fm
// and ListenBrainz are given. It logs a listen once the shared engine says
// one was earned, and does nothing for a user who has not turned history on.
func (s *Service) HandlePlayback(ctx context.Context, input scrobble.PlaybackInput) {
	if s == nil || strings.TrimSpace(input.UserID) == "" || strings.TrimSpace(input.Track.ID) == "" {
		return
	}
	on, err := localHistoryOn(ctx, s.db, input.UserID)
	if err != nil {
		s.logger("wrapped: settings load failed: %v", err)
		return
	}
	if !on {
		return
	}
	submission, err := scrobble.TrackSubmissionFrom(input.Track, 0)
	if err != nil {
		return
	}
	if input.ObservedAt.IsZero() {
		input.ObservedAt = s.now()
	}

	mu := s.userLock(input.UserID)
	mu.Lock()
	defer mu.Unlock()

	current, err := loadPlay(ctx, s.db, input.UserID, input.Track.ID)
	if err != nil {
		s.logger("wrapped: play load failed for %s: %v", input.Track.ID, err)
		return
	}
	update, earned := scrobble.Settle(current, scrobble.ObservationFrom(input, submission.DurationSeconds), s.playID())
	if update.Started && input.StartedAt != nil && !input.StartedAt.IsZero() {
		update.Play.StartedAt = input.StartedAt.UTC()
	}
	if earned {
		startedAt := scrobble.ScrobbleTimestamp(update.Play.StartedAt, input.ObservedAt, 0)
		_, err := insertListen(ctx, s.db, input.UserID, listenRow{
			DedupeKey:       scrobble.DedupeKey(submission.TrackID, submission.Artist, submission.Track, startedAt),
			TrackID:         input.Track.ID,
			AlbumID:         input.Track.AlbumID,
			Artist:          submission.Artist,
			Album:           submission.Album,
			Title:           submission.Track,
			StartedAt:       startedAt,
			ListenedSeconds: update.Play.ListenedSeconds,
			DurationSeconds: submission.DurationSeconds,
		})
		if err != nil {
			// Leave the play unlogged so the next observation tries again.
			s.logger("wrapped: %v", err)
		} else {
			update.Play.Scrobbled = true
		}
	}
	if err := savePlay(ctx, s.db, update.Play); err != nil {
		s.logger("wrapped: %v", err)
	}
}

// Report is a user's Wrapped for a year. Music comes from their Last.fm
// account when one is linked, otherwise from samo's own history if they have
// turned it on. Audiobooks and podcasts are included only with samo's history
// on. A Last.fm year not yet read returns (nil, building) and is read in the
// background; ask again.
func (s *Service) Report(ctx context.Context, userID string, year int, refresh bool) (*Report, *Building, error) {
	now := s.now()
	thisYear := now.In(s.zone).Year()
	if year == 0 {
		year = thisYear
	}
	if year < 2000 || year > thisYear {
		return nil, nil, ErrBadYear
	}
	settings, err := loadSettings(ctx, s.db, userID)
	if err != nil {
		return nil, nil, err
	}
	lastfmUser := ""
	linked := false
	if s.lastfm != nil {
		lastfmUser, linked = s.lastfm.LinkedUsername(ctx, userID)
	}
	switch {
	case linked:
		return s.lastFMReport(ctx, userID, year, lastfmUser, settings, refresh)
	case settings.LocalHistory || settings.Listens > 0:
		report, err := s.build(ctx, userID, year, SourceSamo, "", settings, nil)
		return report, nil, err
	default:
		return nil, nil, ErrNoSource
	}
}

func (s *Service) lastFMReport(ctx context.Context, userID string, year int, username string, settings Settings, refresh bool) (*Report, *Building, error) {
	key := buildKey{userID: userID, year: year, source: SourceLastFM}
	s.mu.Lock()
	if progress, ok := s.building[key]; ok {
		snapshot := *progress
		s.mu.Unlock()
		return nil, &snapshot, nil
	}
	s.mu.Unlock()

	cached, ok, err := loadReport(ctx, s.db, userID, year, SourceLastFM)
	if err != nil {
		return nil, nil, err
	}
	if ok && cached.LastFMUser == username {
		age := s.now().Sub(cached.BuiltAt)
		// A finished year never changes. Otherwise it is reread after six
		// hours, or on request once it is ten minutes old.
		if cached.Complete || (!refresh && age < lastFMReportLifetime) || (refresh && age < 10*time.Minute) {
			// Audiobooks and podcasts follow the switch as it is now.
			s.addSamoKinds(ctx, userID, &cached, settings)
			return &cached, nil, nil
		}
	}
	s.mu.Lock()
	failure, failed := s.failed[key]
	delete(s.failed, key)
	s.mu.Unlock()
	if failed && !refresh {
		if ok {
			// An older year beats none while Last.fm is unreachable.
			s.addSamoKinds(ctx, userID, &cached, settings)
			return &cached, nil, nil
		}
		return nil, nil, failure
	}

	progress := &Building{}
	s.mu.Lock()
	if existing, ok := s.building[key]; ok {
		snapshot := *existing
		s.mu.Unlock()
		return nil, &snapshot, nil
	}
	s.building[key] = progress
	s.mu.Unlock()

	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.building, key)
			s.mu.Unlock()
		}()
		buildCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		from, to := s.yearBounds(year)
		listens, err := s.lastfm.UserListens(buildCtx, userID, from, to, func(page, pages int) {
			s.mu.Lock()
			progress.Pages, progress.TotalPages = page, pages
			s.mu.Unlock()
		})
		if err != nil {
			s.logger("wrapped: reading %d from Last.fm for %s: %v", year, userID, err)
			s.mu.Lock()
			s.failed[key] = fmt.Errorf("reading your year from Last.fm failed: %w", err)
			s.mu.Unlock()
			return
		}
		converted := make([]Listen, 0, len(listens))
		for _, listen := range listens {
			converted = append(converted, Listen{Artist: listen.Artist, Album: listen.Album, Title: listen.Track, At: listen.At, Image: listen.Image})
		}
		report, err := s.build(buildCtx, userID, year, SourceLastFM, username, Settings{}, converted)
		if err != nil {
			s.logger("wrapped: building %d for %s: %v", year, userID, err)
			return
		}
		if err := saveReport(buildCtx, s.db, userID, *report); err != nil {
			s.logger("wrapped: %v", err)
		}
	}()
	snapshot := *progress
	return nil, &snapshot, nil
}

// build assembles a year. For SourceSamo the music is read from samo's own
// log; for SourceLastFM it is given. Audiobooks and podcasts are added from
// samo's records when the user has history on (settings.LocalHistory).
func (s *Service) build(ctx context.Context, userID string, year int, source, lastfmUser string, settings Settings, listens []Listen) (*Report, error) {
	from, to := s.yearBounds(year)
	now := s.now()
	through := to
	if now.Before(to) {
		through = now
	}
	report := &Report{
		Year:       year,
		Source:     source,
		LastFMUser: lastfmUser,
		From:       from,
		Through:    through,
		BuiltAt:    now,
		Complete:   !now.Before(to),
	}

	if source == SourceSamo {
		if settings.EnabledAt != nil && settings.EnabledAt.After(from) {
			report.From = *settings.EnabledAt
		}
		var err error
		listens, err = listensBetween(ctx, s.db, userID, from, to)
		if err != nil {
			return nil, err
		}
	}
	index, err := loadCatalogIndex(ctx, s.db)
	if err != nil {
		return nil, err
	}
	for i := range listens {
		index.enrich(&listens[i])
	}
	report.Music = BuildMusic(listens, s.zone)
	minutes := 0
	if report.Music != nil {
		minutes = report.Music.Minutes
	}
	covered := int(report.Through.Sub(report.From).Hours()/24) + 1
	report.Tier = TierFor(minutes, covered)
	if source == SourceSamo {
		s.addSamoKinds(ctx, userID, report, settings)
	}
	return report, nil
}

// addSamoKinds adds the audiobooks and podcasts samo already records, for a
// user who has history on.
func (s *Service) addSamoKinds(ctx context.Context, userID string, report *Report, settings Settings) {
	report.Audiobooks, report.Podcasts = nil, nil
	if !settings.LocalHistory {
		return
	}
	from, to := s.yearBounds(report.Year)
	if books, err := audiobookStats(ctx, s.db, userID, from, to); err == nil {
		report.Audiobooks = books
	} else {
		s.logger("wrapped: %v", err)
	}
	if podcasts, err := podcastStats(ctx, s.db, userID, from, to); err == nil {
		report.Podcasts = podcasts
	} else {
		s.logger("wrapped: %v", err)
	}
}

func (s *Service) yearBounds(year int) (time.Time, time.Time) {
	from := time.Date(year, time.January, 1, 0, 0, 0, 0, s.zone)
	return from.UTC(), from.AddDate(1, 0, 0).UTC()
}

func (s *Service) userLock(userID string) *sync.Mutex {
	value, _ := s.locks.LoadOrStore(userID, &sync.Mutex{})
	return value.(*sync.Mutex)
}

func newPlayID() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return time.Now().UTC().Format("20060102150405.000000000")
	}
	return hex.EncodeToString(buf)
}

// IsNoSource reports whether err means there is nothing to build from.
func IsNoSource(err error) bool { return errors.Is(err, ErrNoSource) }
