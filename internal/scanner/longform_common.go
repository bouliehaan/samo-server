package scanner

import (
	"context"
	"log"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// groupedAudio is one logical media unit (one audiobook, one podcast show)
// expressed as the root folder plus the list of audio files under it.
type groupedAudio struct {
	Root  string
	Files []string
}

type probedFile struct {
	AudioFile        catalog.AudioFile
	Tags             catalog.Tags
	Chapters         []catalog.AudioChapter
	HasEmbeddedCover bool
}

func (s *Scanner) probeGroup(ctx context.Context, libraryID, root string, files []string, ownerColumn string) ([]probedFile, error) {
	if !s.groupNeedsProbe(files) {
		s.markIndexedGroupSeen(files)
		return nil, nil
	}

	probes := make([]probedFile, 0, len(files))
	for _, path := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if s.onFileActive != nil {
			s.onFileActive(path)
		}
		if s.activeScan != nil {
			s.activeScan.seeFile(path)
		}
		var probe probeInfo
		var err error
		if ownerColumn == "audiobook_id" {
			probe, err = s.probeAudiobook(ctx, path)
		} else {
			probe, err = s.probe(ctx, path)
		}
		if err != nil && ownerColumn != "" {
			if cached, cacheErr := s.loadCachedMediaProbe(ctx, libraryID, path, ownerColumn); cacheErr == nil {
				probe = cached
				err = nil
			}
		}
		if err != nil {
			// Skip the bad file, log it, keep going. A whole audiobook
			// shouldn't be dropped because chapter 7 is corrupt.
			log.Printf("scanner: skipping %q (probe failed: %v)", path, err)
			continue
		}
		relPath, _ := filepath.Rel(root, path)
		probe.AudioFile.RelativePath = relPath
		probes = append(probes, probedFile{
			AudioFile:        probe.AudioFile,
			Tags:             probe.Tags,
			Chapters:         probe.Chapters,
			HasEmbeddedCover: probe.HasEmbeddedCover,
		})
	}
	sort.Slice(probes, func(i, j int) bool {
		discI, trackI := mediaOrder(probes[i].Tags, probes[i].AudioFile.Path)
		discJ, trackJ := mediaOrder(probes[j].Tags, probes[j].AudioFile.Path)
		if discI != discJ {
			return discI < discJ
		}
		if trackI != trackJ {
			return trackI < trackJ
		}
		return probes[i].AudioFile.RelativePath < probes[j].AudioFile.RelativePath
	})
	return probes, nil
}

func groupByRoot(root string, files []string, keyFunc func(parts []string) string) []groupedAudio {
	groups := map[string][]string{}
	for _, path := range files {
		rel, _ := filepath.Rel(root, path)
		parts := strings.Split(rel, string(filepath.Separator))
		groupRoot := keyFunc(parts)
		groups[groupRoot] = append(groups[groupRoot], path)
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make([]groupedAudio, 0, len(keys))
	for _, key := range keys {
		sort.Strings(groups[key])
		out = append(out, groupedAudio{Root: key, Files: groups[key]})
	}
	return out
}

func groupDurationAndSize(probes []probedFile) (int, int64) {
	var duration int
	var size int64
	for _, probe := range probes {
		duration += probe.AudioFile.DurationSeconds
		size += probe.AudioFile.SizeBytes
	}
	return duration, size
}

func titleOrFile(tags catalog.Tags, path string) string {
	title := firstTag(tags, "title")
	if title != "" {
		return title
	}
	return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
}

func trimName(name string) string {
	return strings.TrimSpace(name)
}
