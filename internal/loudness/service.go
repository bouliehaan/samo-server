package loudness

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bouliehaan/samo-server/internal/safego"
)

const (
	// failureCooldown is how long a file that could not be measured is left
	// alone. Without it, an unreadable file is re-analysed on every airing —
	// the most expensive possible response to a permanent problem.
	failureCooldown = 6 * time.Hour

	// partialTTL is how long a windowed measurement stays trusted. A live
	// station's level is stable over an evening but not over a year, and a
	// re-measure costs one 45-second listen.
	partialTTL = 7 * 24 * time.Hour

	// liveWindowSeconds is how much of a live stream is sampled. Long enough
	// to span a few sentences or a whole song, short enough that warming a
	// station does not tie up an analysis slot.
	liveWindowSeconds = 45
)

// ServiceOptions configure the loudness service.
type ServiceOptions struct {
	DB         *sql.DB
	FFmpegPath string

	// Target is the policy. Zero fields fall back to DefaultTarget.
	Target Target

	// Concurrency caps simultaneous analysis passes. This competes with live
	// transcoding for CPU on a box whose actual job is playing audio, so the
	// default is deliberately 1: measurement is never urgent.
	Concurrency int

	Logger *log.Logger

	// BaseContext roots background analysis. It must outlive the HTTP request
	// or playback item that triggered it — the whole point of warming is that
	// it continues after whatever noticed the gap has moved on.
	BaseContext context.Context
}

// Service turns "what should this item's gain be?" into an answer that is
// available instantly, by making sure the measurement already happened.
//
// Reads never block on analysis. PlanFor returns whatever is cached and
// schedules the work if nothing is; the caller plays the item at unity this
// once and correctly forever after. Warm is the same thing without a return
// value, used to measure an item during the one before it — which is why a
// channel gets the level right on an item's very first airing.
type Service struct {
	store   store
	ffmpeg  string
	target  Target
	logger  *log.Logger
	baseCtx context.Context

	slots chan struct{}

	probeOnce sync.Once
	limiterOK bool

	meterOnce sync.Once
	meterOK   bool

	mu       sync.Mutex
	inflight map[string]struct{}
}

// limiterReady reports whether this ffmpeg can actually run the limiter, by
// running it once on a fraction of a second of silence.
//
// ffmpeg is a bundled binary that varies by platform and build, and a
// filtergraph naming a filter it does not have is a hard startup failure for
// that item: ffmpeg exits immediately, the streamer sees zero bytes, and the
// channel skips. A handful of quiet-but-peaky items failing to play would look
// exactly like a source problem and would be miserable to trace back to here.
//
// The probe runs the real filter with the real options rather than reading
// `-filters`, so an option this code gets wrong is caught too. If it fails,
// levelling continues without the limiter: MaxLimitDB drops to zero, which
// means no gain is ever allowed past the item's own headroom. Affected items
// come out slightly quieter than target, which is the same conservative
// direction the policy already prefers.
func (s *Service) limiterReady() bool {
	s.probeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(s.baseCtx, 15*time.Second)
		defer cancel()
		probe := (Plan{GainDB: 1, Limit: true, CeilingDBTP: DefaultTarget.CeilingDBTP}).FilterSpec()
		cmd := exec.CommandContext(ctx, s.ffmpeg,
			"-hide_banner", "-loglevel", "error", "-nostdin",
			"-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo",
			"-af", probe, "-t", "0.1", "-f", "null", "-")
		if out, err := cmd.CombinedOutput(); err != nil {
			s.logger.Printf("loudness: this ffmpeg cannot run %q (%v: %s) — "+
				"levelling continues without peak limiting, so high-crest items stay "+
				"a little under target instead of failing to play",
				probe, err, strings.TrimSpace(string(out)))
			return
		}
		s.limiterOK = true
	})
	return s.limiterOK
}

// ErrMeterUnavailable means this ffmpeg cannot produce a usable measurement.
// It is an environment fault, not a fault of any particular file, so it must
// never be cached against one.
var ErrMeterUnavailable = errors.New("loudness: this ffmpeg cannot measure loudness")

// meterReady measures a synthetic tone of known shape and checks the answer,
// once, before this service is trusted with the library.
//
// The whole chain is exercised — argument list, subprocess, stderr capture,
// parser, validity rules — against the ffmpeg that is actually installed here
// rather than the one on a developer's laptop. That distinction is the entire
// reason this exists. A filter option accepted by ffmpeg 6 and rejected by the
// 5.1 in the container produced a summary that parsed cleanly and was
// completely wrong, and roughly 19,000 files were cached from it before anyone
// heard the result. A self-test costs one subprocess at startup and makes that
// class of failure impossible to ship silently: either the meter demonstrably
// works here, or levelling refuses to run and says so.
func (s *Service) meterReady() bool {
	s.meterOnce.Do(func() {
		ctx, cancel := context.WithTimeout(s.baseCtx, 60*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, s.ffmpeg,
			"-nostdin", "-hide_banner",
			"-f", "lavfi", "-i", "sine=frequency=1000:duration=5:sample_rate=48000",
			"-threads", "1", "-vn",
			"-af", meterFilter,
			"-f", "null", "-")
		cmd.Stdin = nil
		stderr := &tailBuffer{limit: 64 << 10}
		cmd.Stderr = stderr

		if err := cmd.Run(); err != nil {
			s.logger.Printf("loudness: DISABLED — %q failed on this ffmpeg (%v: %s)",
				meterFilter, err, lastLine(stderr.String()))
			return
		}
		measurement, err := parseEBUR128Summary(stderr.String())
		if err != nil {
			s.logger.Printf("loudness: DISABLED — could not read a measurement back from this ffmpeg: %v", err)
			return
		}
		if !measurement.Valid() {
			s.logger.Printf("loudness: DISABLED — this ffmpeg measured a test tone as %.1f LUFS / %.1f dBTP, "+
				"which is not a coherent result; levelling would corrupt the library",
				measurement.IntegratedLUFS, measurement.TruePeakDBTP)
			return
		}
		s.meterOK = true
		s.logger.Printf("loudness: meter self-test passed (test tone read as %.1f LUFS, peak %.1f dBTP)",
			measurement.IntegratedLUFS, measurement.TruePeakDBTP)
	})
	return s.meterOK
}

// planFor applies the policy with the limiter allowance this ffmpeg can
// actually honour.
func (s *Service) planFor(m Measurement) Plan {
	target := s.target
	if !s.limiterReady() {
		target.MaxLimitDB = 0
	}
	return target.Plan(m)
}

// NewService builds a loudness service. A nil DB or an empty ffmpeg path
// yields a service that answers "no adjustment" for everything, which is the
// pre-normalisation behaviour and keeps the feature strictly opt-in on a
// deployment that has not run the migration yet.
func NewService(opts ServiceOptions) *Service {
	logger := opts.Logger
	if logger == nil {
		logger = log.Default()
	}
	baseCtx := opts.BaseContext
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = 1
	}
	return &Service{
		store:    store{db: opts.DB},
		ffmpeg:   strings.TrimSpace(opts.FFmpegPath),
		target:   opts.Target.normalized(),
		logger:   logger,
		baseCtx:  baseCtx,
		slots:    make(chan struct{}, concurrency),
		inflight: map[string]struct{}{},
	}
}

// Enabled reports whether the service can actually do anything.
func (s *Service) Enabled() bool {
	return s != nil && s.store.db != nil && s.ffmpeg != ""
}

// Target reports the policy in force.
func (s *Service) Target() Target {
	if s == nil {
		return DefaultTarget
	}
	return s.target
}

// Request describes one item to level.
type Request struct {
	// Input is what ffmpeg will open to PLAY the item. Measuring the same
	// string that plays is the only way to be sure the numbers describe the
	// audio that actually airs.
	Input string

	// Headers ride on HTTP inputs that need authentication.
	Headers map[string]string

	// Live marks an endless source, measured through a window instead of to
	// the end.
	Live bool

	DurationSeconds int
}

// RequestFor builds a request from a playback URL, which is all most callers
// have and all they should need.
func RequestFor(input string, durationSeconds int, live bool) Request {
	return Request{Input: input, DurationSeconds: durationSeconds, Live: live}
}

// key is the cache identity for a request.
//
// Local files key on their absolute path rather than on any catalog id, so the
// channel scheduler (which knows a path), the samo-radio resolver (which knows
// a track id it can turn into a path) and the backfill sweep (which walks the
// media table) all land on the same row for the same file. One measurement,
// three callers, no coordination.
func (r Request) key() string {
	input := strings.TrimSpace(r.Input)
	if input == "" {
		return ""
	}
	if isHTTP(input) {
		return "url:" + input
	}
	if abs, err := filepath.Abs(input); err == nil {
		return "file:" + abs
	}
	return "file:" + input
}

// fingerprint changes when the underlying bytes do. Only local files have one:
// remote content is treated as immutable, which is true of a podcast episode
// and near enough true of everything else served over HTTP.
func (r Request) fingerprint() string {
	if isHTTP(r.Input) {
		return ""
	}
	info, err := os.Stat(r.Input)
	if err != nil {
		return ""
	}
	return strconv.FormatInt(info.Size(), 10) + ":" + strconv.FormatInt(info.ModTime().UnixNano(), 10)
}

// PlanFor returns the gain to apply right now, plus the measurement it came
// from for logging. A cache miss returns a zero plan and starts the analysis
// in the background.
func (s *Service) PlanFor(ctx context.Context, req Request) (Plan, Measurement, bool) {
	if !s.Enabled() {
		return Plan{}, Measurement{}, false
	}
	key := req.key()
	if key == "" {
		return Plan{}, Measurement{}, false
	}

	cached, found := s.store.lookup(ctx, key)
	if found && s.fresh(cached, req) {
		if cached.Failure != "" {
			return Plan{}, Measurement{}, false
		}
		return s.planFor(cached.Measurement), cached.Measurement, true
	}

	s.warm(key, req)
	return Plan{}, Measurement{}, false
}

// Warm measures an item ahead of time without waiting for the answer.
//
// This is what makes a channel correct on an item's FIRST airing rather than
// its second: the streamer peeks at what the scheduler will pick next and
// warms it while the current item is still playing, so by the time the
// measurement is needed it is already a row in the table.
func (s *Service) Warm(ctx context.Context, req Request) {
	if !s.Enabled() {
		return
	}
	key := req.key()
	if key == "" {
		return
	}
	if cached, found := s.store.lookup(ctx, key); found && s.fresh(cached, req) {
		return
	}
	s.warm(key, req)
}

// fresh decides whether a cached row can still be believed.
func (s *Service) fresh(rec record, req Request) bool {
	if rec.Fingerprint != req.fingerprint() {
		return false
	}
	if rec.Failure != "" {
		return time.Since(rec.MeasuredAt) < failureCooldown
	}
	// Only a LIVE measurement goes stale. A station's level drifts over
	// months; a file's does not, and its fingerprint already catches the case
	// where the bytes changed. Expiring on Partial alone would re-scan every
	// long item in the library every week, forever.
	if req.Live && time.Since(rec.MeasuredAt) > partialTTL {
		return false
	}
	return rec.Measurement.Valid()
}

// warm schedules one analysis, at most one per key at a time.
func (s *Service) warm(key string, req Request) {
	s.mu.Lock()
	if _, running := s.inflight[key]; running {
		s.mu.Unlock()
		return
	}
	s.inflight[key] = struct{}{}
	s.mu.Unlock()

	safego.Go("loudness measure "+key, func() {
		defer func() {
			s.mu.Lock()
			delete(s.inflight, key)
			s.mu.Unlock()
		}()

		// Wait for a slot on the base context, not the caller's: the request
		// that noticed the gap has usually returned by now.
		select {
		case s.slots <- struct{}{}:
		case <-s.baseCtx.Done():
			return
		}
		defer func() { <-s.slots }()

		measurement, err := s.measure(s.baseCtx, req)
		if errors.Is(err, ErrMeterUnavailable) {
			return
		}
		if saveErr := s.store.save(s.baseCtx, key, req.fingerprint(), measurement, err); saveErr != nil {
			s.logger.Printf("loudness: could not cache %s: %v", key, saveErr)
		}
		if err != nil {
			if !errors.Is(err, ErrTooShort) {
				s.logger.Printf("loudness: %s: %v", key, err)
			}
			return
		}
		s.logger.Printf("loudness: %s measured %s", key, s.planFor(measurement).Describe(measurement))
	})
}

// Measure runs an analysis pass now and returns the result, bypassing the
// cache-and-schedule dance. Used by the backfill sweep, which is already
// running in the background and wants the outcome so it can pace itself.
func (s *Service) Measure(ctx context.Context, req Request) (Measurement, error) {
	if !s.Enabled() {
		return Measurement{}, errors.New("loudness: service not configured")
	}
	select {
	case s.slots <- struct{}{}:
	case <-ctx.Done():
		return Measurement{}, ctx.Err()
	}
	defer func() { <-s.slots }()

	measurement, err := s.measure(ctx, req)
	if errors.Is(err, ErrMeterUnavailable) {
		return Measurement{}, err
	}
	key := req.key()
	if saveErr := s.store.save(ctx, key, req.fingerprint(), measurement, err); saveErr != nil {
		s.logger.Printf("loudness: could not cache %s: %v", key, saveErr)
	}
	return measurement, err
}

func (s *Service) measure(ctx context.Context, req Request) (Measurement, error) {
	if !s.meterReady() {
		return Measurement{}, ErrMeterUnavailable
	}
	opts := MeasureOptions{
		Input:           req.Input,
		Headers:         req.Headers,
		DurationSeconds: req.DurationSeconds,
		FFmpegPath:      s.ffmpeg,
	}
	switch {
	case req.Live:
		opts.MaxSeconds = liveWindowSeconds
		opts.Partial = true
		// A live source delivers at real time, so the window plus connection
		// overhead is the whole budget. Without a bound tighter than the
		// default, a station that connects and dribbles would hold an analysis
		// slot for ten minutes.
		opts.Timeout = time.Duration(liveWindowSeconds+45) * time.Second
	default:
		// Long items are sampled rather than read end to end. Not marked
		// partial: ten minutes from the body of an audiobook is a solid
		// characterisation, unlike a snatch of a live stream.
		if start, length, windowed := analysisWindow(req.DurationSeconds); windowed {
			opts.StartSeconds = start
			opts.MaxSeconds = length
		}
	}
	return Measure(ctx, opts)
}
