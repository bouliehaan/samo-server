ALTER TABLE podcast_feeds ADD COLUMN poll_enabled INTEGER NOT NULL DEFAULT 1;
ALTER TABLE podcast_feeds ADD COLUMN poll_interval_seconds INTEGER NOT NULL DEFAULT 3600;
ALTER TABLE podcast_feeds ADD COLUMN next_poll_at TEXT;
ALTER TABLE podcast_feeds ADD COLUMN last_poll_started_at TEXT;
ALTER TABLE podcast_feeds ADD COLUMN last_poll_finished_at TEXT;
ALTER TABLE podcast_feeds ADD COLUMN consecutive_errors INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_podcast_feeds_next_poll ON podcast_feeds(poll_enabled, next_poll_at);
