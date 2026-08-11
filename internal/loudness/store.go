package loudness

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// record is one cached analysis, hit or failure.
type record struct {
	Measurement Measurement
	Fingerprint string
	Failure     string
	MeasuredAt  time.Time
}

// store is the persistence half of the service, split out so the policy and
// the scheduling can be tested without a database.
type store struct{ db *sql.DB }

func (s store) lookup(ctx context.Context, key string) (record, bool) {
	if s.db == nil {
		return record{}, false
	}
	var (
		fingerprint string
		integrated  float64
		truePeak    float64
		lra         float64
		partial     bool
		failure     string
		measuredAt  string
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT fingerprint, integrated_lufs, true_peak_dbtp, loudness_range,
		       partial, failure, measured_at
		FROM loudness_measurements
		WHERE cache_key = ?`, key).
		Scan(&fingerprint, &integrated, &truePeak, &lra, &partial, &failure, &measuredAt)
	if err != nil {
		return record{}, false
	}
	return record{
		Measurement: Measurement{
			IntegratedLUFS: integrated,
			TruePeakDBTP:   truePeak,
			LoudnessRange:  lra,
			Partial:        partial,
			MeasuredAt:     parseStoredTime(measuredAt),
		},
		Fingerprint: fingerprint,
		Failure:     failure,
		MeasuredAt:  parseStoredTime(measuredAt),
	}, true
}

func (s store) save(ctx context.Context, key, fingerprint string, m Measurement, failure error) error {
	if s.db == nil {
		return errors.New("loudness: no database")
	}
	message := ""
	if failure != nil {
		message = truncate(failure.Error(), 500)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO loudness_measurements
			(cache_key, fingerprint, integrated_lufs, true_peak_dbtp, loudness_range,
			 partial, failure, measured_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (cache_key) DO UPDATE SET
			fingerprint     = EXCLUDED.fingerprint,
			integrated_lufs = EXCLUDED.integrated_lufs,
			true_peak_dbtp  = EXCLUDED.true_peak_dbtp,
			loudness_range  = EXCLUDED.loudness_range,
			partial         = EXCLUDED.partial,
			failure         = EXCLUDED.failure,
			measured_at     = EXCLUDED.measured_at`,
		key, fingerprint, m.IntegratedLUFS, m.TruePeakDBTP, m.LoudnessRange,
		m.Partial, message, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func parseStoredTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	for _, format := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(format, raw); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit]
}
