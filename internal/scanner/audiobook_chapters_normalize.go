package scanner

import "github.com/bouliehaan/samo-server/internal/catalog"

func normalizeAudiobookChapters(probes []probedFile, chapters []catalog.AudioChapter) []catalog.AudioChapter {
	if len(chapters) == 0 {
		return chapters
	}
	total := totalProbeDurationSeconds(probes)
	if len(probes) == 1 && total > 0 && shouldCollapseToSingleBook(chapters, total) {
		return []catalog.AudioChapter{singleProbeChapter(probes[0], total)}
	}
	return fixChapterEndTimes(chapters, total)
}

func totalProbeDurationSeconds(probes []probedFile) int {
	total := 0
	for _, probe := range probes {
		total += probe.AudioFile.DurationSeconds
	}
	return total
}

func fixChapterEndTimes(chapters []catalog.AudioChapter, totalDuration int) []catalog.AudioChapter {
	if len(chapters) == 0 {
		return chapters
	}
	out := make([]catalog.AudioChapter, len(chapters))
	copy(out, chapters)
	for i := range out {
		if out[i].EndSeconds > out[i].StartSeconds {
			continue
		}
		if i+1 < len(out) && out[i+1].StartSeconds > out[i].StartSeconds {
			out[i].EndSeconds = out[i+1].StartSeconds
			continue
		}
		if totalDuration > out[i].StartSeconds {
			out[i].EndSeconds = totalDuration
		} else {
			out[i].EndSeconds = out[i].StartSeconds + 1
		}
	}
	if totalDuration > 0 {
		last := &out[len(out)-1]
		if last.EndSeconds < totalDuration {
			last.EndSeconds = totalDuration
		}
	}
	return out
}

func shouldCollapseToSingleBook(chapters []catalog.AudioChapter, total int) bool {
	if total <= 0 || len(chapters) == 0 {
		return false
	}
	coverageEnd := chapterCoverageEnd(chapters)
	minCoverage := minInt(600, int(float64(total)*0.05))
	if minCoverage < 120 {
		minCoverage = 120
	}
	if coverageEnd < minCoverage {
		return true
	}
	if len(chapters) <= 3 && coverageEnd < int(float64(total)*0.5) && coverageEnd/len(chapters) < 120 {
		return true
	}
	return false
}

func chapterCoverageEnd(chapters []catalog.AudioChapter) int {
	coverageEnd := 0
	for _, chapter := range chapters {
		if chapter.EndSeconds <= chapter.StartSeconds {
			continue
		}
		if chapter.EndSeconds > coverageEnd {
			coverageEnd = chapter.EndSeconds
		}
	}
	return coverageEnd
}

func singleProbeChapter(probe probedFile, total int) catalog.AudioChapter {
	if total <= 0 {
		total = 1
	}
	return catalog.AudioChapter{
		Index:        1,
		Title:        titleOrFile(probe.Tags, probe.AudioFile.Path),
		StartSeconds: 0,
		EndSeconds:   total,
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
