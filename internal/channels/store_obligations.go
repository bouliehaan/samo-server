package channels

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// sqlObligations is the real station's memory of what it owes.
type sqlObligations struct {
	db        *sql.DB
	channelID string
}

// NewSQLObligations reads and writes a channel's obligations.
func NewSQLObligations(db *sql.DB, channelID string) ObligationStore {
	return &sqlObligations{db: db, channelID: channelID}
}

func (s *sqlObligations) List(ctx context.Context, now time.Time) ([]Obligation, error) {
	channelID := strings.TrimSpace(s.channelID)
	if channelID == "" {
		return nil, ErrInvalidID
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT source_id, item_ref, title, tier, published_at, noticed_at, expires_at, credit, state, airings, target
		FROM channel_obligations
		WHERE channel_id = ? AND state <> ?
		ORDER BY published_at DESC`,
		channelID, string(ObligationExpired),
	)
	if err != nil {
		return nil, fmt.Errorf("list obligations: %w", err)
	}
	defer rows.Close()

	out := []Obligation{}
	expired := []string{}
	for rows.Next() {
		var obligation Obligation
		var tier, state, published, noticed, expires string
		if err := rows.Scan(&obligation.SourceID, &obligation.ItemRef, &obligation.Title, &tier,
			&published, &noticed, &expires, &obligation.Credit, &state, &obligation.Airings,
			&obligation.SettleAt); err != nil {
			return nil, fmt.Errorf("scan obligation: %w", err)
		}
		obligation.ChannelID = channelID
		obligation.Tier = ParseTier(tier)
		obligation.PublishedAt = parseStoredTime(published)
		obligation.NoticedAt = parseStoredTime(noticed)
		obligation.ExpiresAt = parseStoredTime(expires)
		obligation.State = ObligationState(state)

		// Expiry is settled on read rather than by a sweeper: it is a pure
		// function of the clock, and a background job that has not run yet is
		// one more way for the station to behave differently from what the
		// tables say.
		settle(&obligation, now)
		if obligation.State == ObligationExpired {
			expired = append(expired, obligation.ItemRef)
			continue
		}
		out = append(out, obligation)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(expired) > 0 {
		s.markExpired(ctx, expired, now)
	}
	return out, nil
}

// markExpired writes back what List worked out, so the table stops growing a
// tail of things nobody will ever be offered. Best effort — the read already
// treated them as expired.
func (s *sqlObligations) markExpired(ctx context.Context, refs []string, now time.Time) {
	stamp := now.UTC().Format(time.RFC3339)
	for _, ref := range refs {
		_, _ = s.db.ExecContext(ctx, `
			UPDATE channel_obligations SET state = ?, updated_at = ?
			WHERE channel_id = ? AND item_ref = ?`,
			string(ObligationExpired), stamp, s.channelID, ref)
	}
}

func (s *sqlObligations) Notice(ctx context.Context, obligations []Obligation, now time.Time) error {
	if len(obligations) == 0 {
		return nil
	}
	channelID := strings.TrimSpace(s.channelID)
	if channelID == "" {
		return ErrInvalidID
	}
	stamp := now.UTC().Format(time.RFC3339)
	for _, obligation := range obligations {
		if strings.TrimSpace(obligation.ItemRef) == "" {
			continue
		}
		// On conflict, refresh only what describes the SHOW — never the credit,
		// the state, or the expiry. Those belong to the obligation's own life:
		// re-reading a feed must not undo an airing or hand an episode a fresh
		// seventy-two hours.
		//
		// The tier is not one of them. It was frozen on the theory that
		// re-rating a show should not reorder a queue the listener has half
		// heard — which sounded principled and meant that setting a podcast to
		// S tier changed nothing at all for any episode already noticed. Every
		// obligation in the queue stayed at the default while the source said S.
		// A rating you cannot apply to the things you are waiting for is not a
		// rating.
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO channel_obligations
				(channel_id, item_ref, source_id, title, tier, published_at, noticed_at, expires_at, credit, state, airings, target, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, ?, 0, ?, ?)
			ON CONFLICT (channel_id, item_ref) DO UPDATE SET
				tier = EXCLUDED.tier,
				title = EXCLUDED.title,
				source_id = EXCLUDED.source_id,
				-- Re-rating a show has to reach the episodes already waiting,
				-- or promoting a podcast to S changes nothing for anything in
				-- the queue. Never lowered below what has already been earned,
				-- so a demotion cannot un-satisfy something already heard.
				target = GREATEST(EXCLUDED.target, channel_obligations.credit)`,
			channelID, obligation.ItemRef, obligation.SourceID, obligation.Title, string(obligation.Tier),
			formatStoredTime(obligation.PublishedAt), stamp, formatStoredTime(obligation.ExpiresAt),
			string(ObligationPending), obligation.Target(), stamp,
		); err != nil {
			return fmt.Errorf("notice obligation: %w", err)
		}
	}
	return nil
}

func (s *sqlObligations) Credit(ctx context.Context, itemRef string, credit float64, now time.Time) error {
	itemRef = strings.TrimSpace(itemRef)
	if itemRef == "" {
		return nil
	}
	stamp := now.UTC().Format(time.RFC3339)
	// Settled in SQL so a concurrent airing cannot read-modify-write over
	// itself: the streamer and an operator poking the API are different
	// goroutines with different connections.
	if _, err := s.db.ExecContext(ctx, `
		UPDATE channel_obligations
		SET credit = credit + ?,
		    airings = airings + 1,
		    state = CASE WHEN credit + ? >= target THEN ? ELSE state END,
		    updated_at = ?
		WHERE channel_id = ? AND item_ref = ?`,
		credit, credit, string(ObligationSatisfied), stamp, s.channelID, itemRef,
	); err != nil {
		return fmt.Errorf("credit obligation: %w", err)
	}
	return nil
}

func formatStoredTime(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	return at.UTC().Format(time.RFC3339)
}

// ObligationsFor reads a channel's obligations for the API and the UI.
func ObligationsFor(ctx context.Context, db *sql.DB, channelID string, now time.Time) ([]Obligation, error) {
	return NewSQLObligations(db, channelID).List(ctx, clockOr(now))
}
