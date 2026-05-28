package catalog

import "time"

func latestAudioFileModified(files []AudioFile) (time.Time, bool) {
	var latest time.Time
	var ok bool
	for _, file := range files {
		if file.ModifiedAt == nil {
			continue
		}
		if !ok || file.ModifiedAt.After(latest) {
			latest = *file.ModifiedAt
			ok = true
		}
	}
	return latest, ok
}

// EnrichAudiobookAddedAtFromFiles sets AddedAt from the newest on-disk file mtime.
func EnrichAudiobookAddedAtFromFiles(items []AudiobookItem) {
	for i := range items {
		if latest, ok := latestAudioFileModified(items[i].AudioFiles); ok {
			items[i].AddedAt = timePtr(latest)
		}
	}
}

// EnrichPodcastAddedAtFromFiles sets AddedAt from show or episode media file mtimes
// so a newly downloaded episode surfaces the show on recently-added views.
func EnrichPodcastAddedAtFromFiles(items []PodcastItem) {
	for i := range items {
		var latest time.Time
		var ok bool
		if t, found := latestAudioFileModified(items[i].AudioFiles); found {
			latest, ok = t, true
		}
		for _, episode := range items[i].Episodes {
			if t, found := latestAudioFileModified(episode.AudioFiles); found && (!ok || t.After(latest)) {
				latest, ok = t, true
			}
			if episode.AddedAt != nil && (!ok || episode.AddedAt.After(latest)) {
				latest, ok = *episode.AddedAt, true
			}
		}
		if ok {
			items[i].AddedAt = timePtr(latest)
		}
	}
}
