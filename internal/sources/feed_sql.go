package sources

const podcastFeedSelectSQL = `
	SELECT id, podcast_id, feed_url, title, description, author, site_url, image_url, language,
	       explicit, categories_json, owner_name, owner_email, episode_count, status, last_error,
	       last_fetched_at, auto_download_enabled, poll_enabled, poll_interval_seconds, next_poll_at,
	       last_poll_started_at, last_poll_finished_at, consecutive_errors, created_at, updated_at
	FROM podcast_feeds`

const internetRadioStationSelectSQL = `
	SELECT id, name, description, stream_url, homepage_url, image_url, cover_id, content_type, codec, bitrate,
	       country, language, tags_json, enabled, last_checked_at, created_at, updated_at,
	       now_playing, now_playing_artist, now_playing_title, now_playing_updated_at,
	       probe_enabled, probe_interval_seconds, next_probe_at, last_probe_started_at,
	       last_probe_finished_at, last_probe_error, consecutive_probe_errors, probe_status
	FROM internet_radio_stations`
