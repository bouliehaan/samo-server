package catalog

import (
	"fmt"
)

// isHiRes reports whether technical metadata exceeds CD quality (16-bit / 44.1 kHz).
func isHiRes(bitDepth, sampleRate int) bool {
	return bitDepth > 16 || sampleRate > 44100
}

// formatAudioQuality renders a hi-res badge label such as "24/192".
func formatAudioQuality(bitDepth, sampleRate int) string {
	if !isHiRes(bitDepth, sampleRate) {
		return ""
	}
	if bitDepth > 0 && sampleRate > 0 {
		return fmt.Sprintf("%d/%d", bitDepth, sampleRate/1000)
	}
	if bitDepth > 16 {
		return fmt.Sprintf("%d-bit", bitDepth)
	}
	if sampleRate > 44100 {
		return fmt.Sprintf("%d kHz", sampleRate/1000)
	}
	return ""
}

func summarizeTrackAudioQuality(track MusicTrack) (bitDepth, sampleRate int) {
	for _, file := range track.AudioFiles {
		file = NormalizeAudioFile(file)
		if file.BitDepth > bitDepth {
			bitDepth = file.BitDepth
		}
		if file.SampleRate > sampleRate {
			sampleRate = file.SampleRate
		}
	}
	return bitDepth, sampleRate
}

func summarizeAlbumAudioQuality(tracks []MusicTrack) (maxBitDepth, maxSampleRate int, audioQuality string, hiRes bool) {
	for _, track := range tracks {
		depth, rate := summarizeTrackAudioQuality(track)
		if depth > maxBitDepth {
			maxBitDepth = depth
		}
		if rate > maxSampleRate {
			maxSampleRate = rate
		}
	}
	hiRes = isHiRes(maxBitDepth, maxSampleRate)
	audioQuality = formatAudioQuality(maxBitDepth, maxSampleRate)
	return maxBitDepth, maxSampleRate, audioQuality, hiRes
}
