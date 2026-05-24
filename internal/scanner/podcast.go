package scanner

import (
	"context"
	"log"
	"path/filepath"
	"strconv"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/media"
)

// scanPodcastLibrary walks a podcast library root and writes one podcast
// row per detected show folder. Each top-level subfolder is treated as one
// show; files inside become episodes.
func (s *Scanner) scanPodcastLibrary(ctx context.Context, library Library, root string, files []string) error {
	groups := groupPodcasts(root, files)
	if len(files) > 0 && len(groups) == 0 {
		log.Printf("scanner: podcast library %q has %d audio files but produced 0 groups (likely a grouping bug)", library.Path, len(files))
	} else if len(files) == 0 {
		log.Printf("scanner: podcast library %q has no audio files under %q; check the path and supported extensions (.mp3, .m4a, .m4b, .ogg, .opus, .flac, .wav, .aac, .wma, .aif, .aiff, .alac)", library.Path, root)
	}
	for _, group := range groups {
		if err := s.scanPodcast(ctx, library, root, group); err != nil {
			return err
		}
	}
	_, err := s.db.ExecContext(ctx, `UPDATE libraries SET last_scan_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, library.ID)
	return err
}

func (s *Scanner) scanPodcast(ctx context.Context, library Library, root string, group groupedAudio) error {
	probes, err := s.probeGroup(ctx, root, group.Files)
	if err != nil {
		return err
	}
	if len(probes) == 0 {
		log.Printf("scanner: podcast group %q had %d files but ffprobe rejected all of them; skipping", group.Root, len(group.Files))
		return nil
	}

	commonTags := mergeProbeTags(probes)
	title := firstTag(commonTags, "album", "show", "podcast", "album_artist")
	if title == "" {
		title = filepath.Base(group.Root)
	}
	podcastID := stableID("podcast", library.ID, group.Root)
	duration, sizeBytes := groupDurationAndSize(probes)
	categories := splitGenreTag(commonTags, "genre", "category", "categories")
	for _, category := range categories {
		if err := s.upsertGenre(ctx, string(media.KindPodcast), category); err != nil {
			return err
		}
	}
	audioPaths := make([]string, 0, len(probes))
	checksums := make([]string, 0, len(probes))
	for _, probed := range probes {
		audioPaths = append(audioPaths, probed.AudioFile.Path)
		checksums = append(checksums, probed.AudioFile.Checksum)
	}
	cover := s.resolveCover(ctx, group.Root, audioPaths, checksums)

	item := catalog.PodcastItem{
		ID:              podcastID,
		LibraryID:       library.ID,
		Path:            group.Root,
		FolderID:        stableID("folder", group.Root),
		Inode:           fileInode(group.Root),
		SizeBytes:       sizeBytes,
		Genres:          categories,
		DurationSeconds: duration,
		Cover:           cover,
		Podcast: &catalog.PodcastMetadata{
			Title:        title,
			Author:       firstTag(commonTags, "artist", "author", "album_artist"),
			Description:  firstTag(commonTags, "description", "comment", "summary"),
			FeedURL:      firstTag(commonTags, "podcasturl", "feed", "feed_url", "rss"),
			SiteURL:      firstTag(commonTags, "url", "website"),
			Language:     firstTag(commonTags, "language"),
			Explicit:     explicitTag(commonTags),
			Categories:   categories,
			OwnerName:    firstTag(commonTags, "owner", "owner_name"),
			OwnerEmail:   firstTag(commonTags, "owner_email"),
			EpisodeCount: len(probes),
			ExternalIDs: catalog.ExternalIDs{
				FeedGUID: firstTag(commonTags, "podcast_guid", "guid"),
				ITunesID: firstTag(commonTags, "itunes_id"),
			},
		},
	}
	if err := s.upsertPodcast(ctx, item); err != nil {
		return err
	}
	if s.activeScan != nil {
		s.activeScan.seePodcast(item.ID)
	}

	for _, probed := range probes {
		tags := probed.Tags
		file := probed.AudioFile
		file.ID = stableID("file", file.Path)
		episodeID := stableID("episode", item.ID, file.RelativePath)
		episodeNumber, _ := parseNumberPair(firstTag(tags, "episode", "track", "tracknumber"))
		season, _ := strconv.Atoi(firstTag(tags, "season"))
		episode := catalog.PodcastEpisode{
			ID:              episodeID,
			LibraryID:       library.ID,
			PodcastID:       item.ID,
			Title:           titleOrFile(tags, file.Path),
			Subtitle:        firstTag(tags, "subtitle"),
			Description:     firstTag(tags, "description", "comment", "summary"),
			PublishedAt:     parseDatePtr(firstTag(tags, "date", "releasedate", "pubdate")),
			Season:          season,
			Episode:         episodeNumber,
			EpisodeType:     firstTag(tags, "episode_type", "episodetype"),
			DurationSeconds: file.DurationSeconds,
			Explicit:        explicitTag(tags),
			EnclosureURL:    firstTag(tags, "enclosure_url", "enclosureurl", "url"),
			EnclosureType:   file.MimeType,
			EnclosureBytes:  file.SizeBytes,
			ExternalIDs: catalog.ExternalIDs{
				FeedGUID: firstTag(tags, "guid", "episode_guid"),
				ITunesID: firstTag(tags, "itunes_id"),
			},
		}
		if err := s.upsertPodcastEpisode(ctx, episode); err != nil {
			return err
		}
		if s.activeScan != nil {
			s.activeScan.seeEpisode(episode.ID)
		}
		if err := s.upsertAudioFile(ctx, library.ID, audioFileOwner{PodcastID: item.ID, EpisodeID: episode.ID}, file); err != nil {
			return err
		}
		if err := s.replaceEpisodeChapters(ctx, episode.ID, probed.Chapters); err != nil {
			return err
		}
	}

	return nil
}

func groupPodcasts(root string, files []string) []groupedAudio {
	return groupByRoot(root, files, func(parts []string) string {
		if len(parts) >= 2 {
			return filepath.Join(root, parts[0])
		}
		return root
	})
}
