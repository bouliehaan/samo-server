package sources

const podcastFeedSelectSQL = `
	SELECT id, podcast_id, feed_url, title, description, author, site_url, image_url, language,
	       explicit, categories_json, owner_name, owner_email, episode_count, status, last_error,
	       last_fetched_at, poll_enabled, poll_interval_seconds, next_poll_at, last_poll_started_at,
	       last_poll_finished_at, consecutive_errors, created_at, updated_at
	FROM podcast_feeds`
