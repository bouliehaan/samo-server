package catalog

// Book-global timeline maths over a set of audio files. Pure — no database —
// so it lives with the model. internal/catalogstore calls it while assembling
// the seed, and the stream selector calls it when answering a request; both
// must agree, which is why there is one copy.

// assignStreamOffsets stamps each file's StartOffsetSeconds with the running
// book-global start position — the exact, millisecond-derived sum of every
// earlier file's duration. This per-file manifest lets clients map book-time
// <-> (file, file-time) without re-accumulating durations and drifting.
// Single-file items keep offset 0; the slice must already be sorted.
func AssignStreamOffsets(files []AudioFile) []AudioFile {
	if len(files) <= 1 {
		return files
	}
	offset := 0.0
	for i := range files {
		files[i].StartOffsetSeconds = offset
		offset += AudioFileDurationSeconds(files[i])
	}
	return files
}

// audioFileDurationSeconds returns the file duration in fractional seconds,
// preferring the exact millisecond field.
func AudioFileDurationSeconds(file AudioFile) float64 {
	if file.DurationMs > 0 {
		return float64(file.DurationMs) / 1000
	}
	return float64(file.DurationSeconds)
}
