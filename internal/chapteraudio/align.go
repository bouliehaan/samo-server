package chapteraudio

import (
	"strconv"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// nameMatch records, per audio chapter, which metadata chapter (if any) lent it
// its title.
type nameMatch struct {
	MetaIndex int // index into the metadata chapter list, or -1 if none matched
}

// alignNames maps metadata chapter NAMES onto the audio-derived chapter starts.
// The audio decides HOW MANY chapters there are and WHERE they begin; metadata
// is consulted only to label them. We find the lowest-cost MONOTONIC matching
// between the two ordered timelines: each metadata title attaches to the audio
// boundary nearest it in time without crossing another match. Extra audio
// chapters (more boundaries than names) simply go unnamed; surplus metadata
// titles (fewer boundaries than names) are dropped — because the file, not the
// metadata, is the source of truth for the count.
//
// audioStarts and meta must both be sorted ascending by start time.
func alignNames(audioStarts []float64, meta []catalog.AudioChapter, skipPenalty float64) []nameMatch {
	m := len(audioStarts)
	matches := make([]nameMatch, m)
	for i := range matches {
		matches[i].MetaIndex = -1
	}
	k := len(meta)
	if m == 0 || k == 0 {
		return matches
	}

	metaStarts := make([]float64, k)
	for j, c := range meta {
		metaStarts[j] = c.StartSeconds
	}

	const (
		moveSkipAudio = 1 // audio i-1 left unnamed (free)
		moveSkipMeta  = 2 // metadata j-1 left unused (penalized)
		moveMatch     = 3 // audio i-1 named by metadata j-1
	)

	dp := make([][]float64, m+1)
	from := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]float64, k+1)
		from[i] = make([]int, k+1)
	}
	// Skipping audio chapters is free (surplus boundaries are expected).
	for i := 1; i <= m; i++ {
		dp[i][0] = 0
		from[i][0] = moveSkipAudio
	}
	// Skipping metadata titles costs, so the DP prefers to place them.
	for j := 1; j <= k; j++ {
		dp[0][j] = dp[0][j-1] + skipPenalty
		from[0][j] = moveSkipMeta
	}

	for i := 1; i <= m; i++ {
		for j := 1; j <= k; j++ {
			best := dp[i-1][j] // skip audio
			move := moveSkipAudio

			if c := dp[i][j-1] + skipPenalty; c < best { // skip meta
				best = c
				move = moveSkipMeta
			}
			if c := dp[i-1][j-1] + absF(audioStarts[i-1]-metaStarts[j-1]); c < best { // match
				best = c
				move = moveMatch
			}
			dp[i][j] = best
			from[i][j] = move
		}
	}

	// Backtrack from (m, k) to recover the matched pairs.
	i, j := m, k
	for i > 0 || j > 0 {
		switch from[i][j] {
		case moveMatch:
			matches[i-1].MetaIndex = j - 1
			i--
			j--
		case moveSkipMeta:
			j--
		default: // moveSkipAudio
			i--
		}
	}
	return matches
}

// titleFor returns the chapter title for audio chapter i (0-based): the matched
// metadata title when there is one and it's non-empty, otherwise a synthesized
// "Chapter N".
func titleFor(i int, match nameMatch, meta []catalog.AudioChapter) (title string, named bool) {
	if match.MetaIndex >= 0 && match.MetaIndex < len(meta) {
		if t := strings.TrimSpace(meta[match.MetaIndex].Title); t != "" {
			return t, true
		}
	}
	return genericChapterTitle(i + 1), false
}

func genericChapterTitle(n int) string {
	return "Chapter " + strconv.Itoa(n)
}

func absF(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
