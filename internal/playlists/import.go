package playlists

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

const maxImportBytes = 5 << 20

type ImportInput struct {
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	Public        bool   `json:"public"`
	Collaborative bool   `json:"collaborative,omitempty"`
	PlaylistID    string `json:"playlistId,omitempty"`
	Replace       *bool  `json:"replace,omitempty"`
	DryRun        bool   `json:"dryRun,omitempty"`
	SourceType    string `json:"sourceType,omitempty"`
	Content       string `json:"content,omitempty"`
	URL           string `json:"url,omitempty"`
}

type ImportResult struct {
	Playlist       *catalog.MusicPlaylist `json:"playlist,omitempty"`
	SourceType     string                 `json:"sourceType"`
	ParsedCount    int                    `json:"parsedCount"`
	MatchedCount   int                    `json:"matchedCount"`
	UnmatchedCount int                    `json:"unmatchedCount"`
	TrackIDs       []string               `json:"trackIds"`
	Items          []ImportItem           `json:"items"`
}

type ImportItem struct {
	Position        int    `json:"position"`
	Title           string `json:"title,omitempty"`
	Artist          string `json:"artist,omitempty"`
	Album           string `json:"album,omitempty"`
	DurationSeconds int    `json:"durationSeconds,omitempty"`
	Path            string `json:"path,omitempty"`
	Raw             string `json:"raw,omitempty"`
	TrackID         string `json:"trackId,omitempty"`
	MatchScore      int    `json:"matchScore,omitempty"`
	MatchReason     string `json:"matchReason,omitempty"`
	Status          string `json:"status"`
}

type importCandidate struct {
	Position        int
	Title           string
	Artist          string
	Album           string
	DurationSeconds int
	Path            string
	TrackID         string
	Raw             string
}

type importTrack struct {
	ID              string
	Title           string
	DisplayArtist   string
	AlbumTitle      string
	DurationSeconds int
	Paths           []string
	FileNames       []string
}

func (s *Service) Import(ctx context.Context, ownerID string, input ImportInput) (ImportResult, error) {
	if s == nil || s.db == nil {
		return ImportResult{}, ErrDisabled
	}
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		return ImportResult{}, ErrForbidden
	}

	sourceType, content, err := s.importContent(ctx, input)
	if err != nil {
		return ImportResult{}, err
	}
	candidates, sourceType, err := parseImportCandidates(sourceType, content)
	if err != nil {
		return ImportResult{}, err
	}
	items, trackIDs, err := s.matchImportCandidates(ctx, candidates)
	if err != nil {
		return ImportResult{}, err
	}

	result := ImportResult{
		SourceType:  sourceType,
		ParsedCount: len(candidates),
		TrackIDs:    trackIDs,
		Items:       items,
	}
	for _, item := range items {
		if item.TrackID != "" {
			result.MatchedCount++
		} else {
			result.UnmatchedCount++
		}
	}
	if input.DryRun {
		return result, nil
	}

	name := strings.TrimSpace(input.Name)
	if name == "" {
		return ImportResult{}, fmt.Errorf("%w: playlist name is required", ErrInvalidInput)
	}
	replace := true
	if input.Replace != nil {
		replace = *input.Replace
	}

	var playlist catalog.MusicPlaylist
	if playlistID := strings.TrimSpace(input.PlaylistID); playlistID != "" {
		current, err := s.loadByID(ctx, playlistID)
		if err != nil {
			return ImportResult{}, err
		}
		if err := assertOwner(ownerID, current.OwnerID); err != nil {
			return ImportResult{}, err
		}
		allTrackIDs := trackIDs
		if !replace {
			allTrackIDs = append(append([]string(nil), current.TrackIDs...), trackIDs...)
		}
		playlist, err = s.Update(ctx, ownerID, playlistID, UpdateInput{
			Name:          &name,
			Description:   &input.Description,
			Public:        &input.Public,
			Collaborative: &input.Collaborative,
			TrackIDs:      allTrackIDs,
		})
		if err != nil {
			return ImportResult{}, err
		}
		result.Playlist = &playlist
		return result, nil
	}

	targetID := playlistID(ownerID, name)
	if current, err := s.loadByID(ctx, targetID); err == nil {
		allTrackIDs := trackIDs
		if !replace {
			allTrackIDs = append(append([]string(nil), current.TrackIDs...), trackIDs...)
		}
		playlist, err = s.Update(ctx, ownerID, targetID, UpdateInput{
			Name:          &name,
			Description:   &input.Description,
			Public:        &input.Public,
			Collaborative: &input.Collaborative,
			TrackIDs:      allTrackIDs,
		})
		if err != nil {
			return ImportResult{}, err
		}
		result.Playlist = &playlist
		return result, nil
	} else if !errors.Is(err, ErrNotFound) {
		return ImportResult{}, err
	}

	playlist, err = s.Create(ctx, ownerID, CreateInput{
		Name:          name,
		Description:   input.Description,
		Public:        input.Public,
		Collaborative: input.Collaborative,
		TrackIDs:      trackIDs,
	})
	if err != nil {
		return ImportResult{}, err
	}
	result.Playlist = &playlist
	return result, nil
}

func (s *Service) importContent(ctx context.Context, input ImportInput) (string, string, error) {
	sourceType := normalizeSourceType(input.SourceType)
	content := strings.TrimSpace(input.Content)
	rawURL := strings.TrimSpace(input.URL)
	if content != "" {
		return sourceType, content, nil
	}
	if rawURL == "" {
		return sourceType, "", fmt.Errorf("%w: import content or url is required", ErrInvalidInput)
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", "", fmt.Errorf("%w: invalid import url", ErrInvalidInput)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", "", fmt.Errorf("%w: import url must be http or https", ErrInvalidInput)
	}
	if isYouTubeURL(parsed) {
		sourceType = "youtube"
		content, err := fetchYouTubePlaylistContent(ctx, rawURL)
		if err != nil {
			return "", "", err
		}
		return sourceType, content, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", youtubeBrowserUserAgent)
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("fetch playlist import url: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("%w: import url returned http %d", ErrInvalidInput, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxImportBytes+1))
	if err != nil {
		return "", "", err
	}
	if len(body) > maxImportBytes {
		return "", "", fmt.Errorf("%w: import content is too large", ErrInvalidInput)
	}
	if sourceType == "auto" {
		sourceType = inferSourceTypeFromURL(parsed)
	}
	return sourceType, string(body), nil
}

func parseImportCandidates(sourceType string, content string) ([]importCandidate, string, error) {
	sourceType = inferSourceType(sourceType, content)
	var (
		items []importCandidate
		err   error
	)
	switch sourceType {
	case "csv":
		items, err = parseCSVImport(content)
	case "m3u":
		items, err = parseM3UImport(content)
	case "json":
		items, err = parseJSONImport(content)
	case "youtube":
		items, err = parseYouTubeImport(content)
		if err != nil {
			return nil, sourceType, err
		}
		if len(items) == 0 {
			return nil, sourceType, fmt.Errorf("%w: youtube playlist did not contain any tracks", ErrInvalidInput)
		}
	default:
		sourceType = "plain"
		items, err = parsePlainImport(content)
	}
	if err != nil {
		return nil, sourceType, err
	}
	cleaned := make([]importCandidate, 0, len(items))
	for _, item := range items {
		item.Title = cleanText(item.Title)
		item.Artist = cleanText(item.Artist)
		item.Album = cleanText(item.Album)
		item.Path = strings.TrimSpace(item.Path)
		item.TrackID = strings.TrimSpace(item.TrackID)
		item.Raw = cleanText(item.Raw)
		if item.Title == "" && item.Path != "" {
			fillCandidateFromPath(&item)
		}
		if item.Title == "" && item.Artist == "" && item.Album == "" && item.Path == "" && item.TrackID == "" {
			continue
		}
		item.Position = len(cleaned) + 1
		cleaned = append(cleaned, item)
	}
	return cleaned, sourceType, nil
}

func parseCSVImport(content string) ([]importCandidate, error) {
	reader := csv.NewReader(strings.NewReader(content))
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("%w: parse csv: %v", ErrInvalidInput, err)
	}
	if len(records) == 0 {
		return nil, nil
	}
	header := csvHeader(records[0])
	start := 0
	if len(header) > 0 {
		start = 1
	}
	items := make([]importCandidate, 0, len(records)-start)
	for _, record := range records[start:] {
		if len(record) == 0 {
			continue
		}
		var item importCandidate
		if len(header) > 0 {
			item = candidateFromCSVHeader(record, header)
		} else {
			item = candidateFromCSVRecord(record)
		}
		item.Raw = strings.Join(record, " ")
		items = append(items, item)
	}
	return items, nil
}

func csvHeader(record []string) map[string]int {
	header := map[string]int{}
	for index, value := range record {
		name := normalizeHeader(value)
		switch name {
		case "id", "trackid", "title", "name", "song", "track", "artist", "artists", "album", "duration", "length", "path", "file", "filename", "location", "url":
			header[name] = index
		}
	}
	if len(header) == 0 {
		return nil
	}
	return header
}

func candidateFromCSVHeader(record []string, header map[string]int) importCandidate {
	return importCandidate{
		TrackID:         csvValue(record, header, "id", "trackid"),
		Title:           csvValue(record, header, "title", "name", "song", "track"),
		Artist:          csvValue(record, header, "artist", "artists"),
		Album:           csvValue(record, header, "album"),
		DurationSeconds: parseDuration(csvValue(record, header, "duration", "length")),
		Path:            csvValue(record, header, "path", "file", "filename", "location", "url"),
	}
}

func candidateFromCSVRecord(record []string) importCandidate {
	item := importCandidate{}
	if len(record) > 0 {
		item.Title = record[0]
	}
	if len(record) > 1 {
		item.Artist = record[1]
	}
	if len(record) > 2 {
		item.Album = record[2]
	}
	if len(record) > 3 {
		item.DurationSeconds = parseDuration(record[3])
	}
	return item
}

func csvValue(record []string, header map[string]int, names ...string) string {
	for _, name := range names {
		index, ok := header[name]
		if ok && index >= 0 && index < len(record) {
			return strings.TrimSpace(record[index])
		}
	}
	return ""
}

func parseM3UImport(content string) ([]importCandidate, error) {
	var items []importCandidate
	var pending importCandidate
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#EXTINF:") {
			pending = parseEXTINF(line)
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		item := pending
		pending = importCandidate{}
		item.Path = line
		item.Raw = firstString(item.Raw, line)
		if item.Title == "" {
			fillCandidateFromPath(&item)
		}
		items = append(items, item)
	}
	return items, nil
}

func parseEXTINF(line string) importCandidate {
	payload := strings.TrimPrefix(line, "#EXTINF:")
	durationText, label, _ := strings.Cut(payload, ",")
	if semi := strings.Index(durationText, " "); semi >= 0 {
		durationText = durationText[:semi]
	}
	item := importCandidate{DurationSeconds: parseDuration(durationText), Raw: label}
	item.Artist, item.Title = splitArtistTitle(label)
	if item.Title == "" {
		item.Title = label
	}
	return item
}

func parsePlainImport(content string) ([]importCandidate, error) {
	var items []importCandidate
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		item := importCandidate{Raw: line}
		if looksLikePathOrURL(line) {
			item.Path = line
			fillCandidateFromPath(&item)
		} else {
			item.Artist, item.Title = splitArtistTitle(line)
			if item.Title == "" {
				item.Title = line
			}
		}
		items = append(items, item)
	}
	return items, nil
}

func parseJSONImport(content string) ([]importCandidate, error) {
	var value any
	if err := json.Unmarshal([]byte(content), &value); err != nil {
		return nil, fmt.Errorf("%w: parse json: %v", ErrInvalidInput, err)
	}
	if root, ok := value.(map[string]any); ok {
		for _, key := range []string{"tracks", "items", "playlist"} {
			if nested, ok := root[key]; ok {
				value = nested
				break
			}
		}
	}
	list, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%w: json import expects an array or an object with tracks/items", ErrInvalidInput)
	}
	items := make([]importCandidate, 0, len(list))
	for _, entry := range list {
		object, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		items = append(items, importCandidate{
			TrackID:         jsonString(object, "id", "trackId"),
			Title:           jsonString(object, "title", "name", "track", "song"),
			Artist:          jsonString(object, "artist", "artists"),
			Album:           jsonString(object, "album"),
			DurationSeconds: parseDuration(jsonString(object, "duration", "length", "durationSeconds")),
			Path:            jsonString(object, "path", "file", "filename", "location", "url"),
		})
	}
	return items, nil
}

func parseYouTubeImport(content string) ([]importCandidate, error) {
	content = strings.TrimSpace(content)
	data := content
	if !strings.HasPrefix(content, "{") {
		data = extractYouTubeInitialData(content)
		if data == "" {
			return nil, fmt.Errorf("%w: could not find playlist metadata in youtube page", ErrInvalidInput)
		}
	}
	var root any
	if err := json.Unmarshal([]byte(data), &root); err != nil {
		return nil, fmt.Errorf("%w: parse youtube metadata: %v", ErrInvalidInput, err)
	}
	var items []importCandidate
	walkYouTube(root, &items)
	return items, nil
}

func walkYouTube(value any, items *[]importCandidate) {
	switch node := value.(type) {
	case map[string]any:
		if renderer, ok := node["musicResponsiveListItemRenderer"]; ok {
			if item := candidateFromYouTubeRenderer(renderer); item.Title != "" {
				*items = append(*items, item)
			}
		}
		if renderer, ok := node["playlistVideoRenderer"]; ok {
			if item := candidateFromYouTubeRenderer(renderer); item.Title != "" {
				*items = append(*items, item)
			}
		}
		if renderer, ok := node["playlistPanelVideoRenderer"]; ok {
			if item := candidateFromYouTubeRenderer(renderer); item.Title != "" {
				*items = append(*items, item)
			}
		}
		if renderer, ok := node["musicTwoRowItemRenderer"]; ok {
			if item := candidateFromYouTubeRenderer(renderer); item.Title != "" {
				*items = append(*items, item)
			}
		}
		for _, child := range node {
			walkYouTube(child, items)
		}
	case []any:
		for _, child := range node {
			walkYouTube(child, items)
		}
	}
}

func candidateFromYouTubeRenderer(value any) importCandidate {
	texts := collectYouTubeTexts(value)
	if len(texts) == 0 {
		return importCandidate{}
	}
	item := importCandidate{Title: texts[0], Raw: strings.Join(texts, " ")}
	for _, text := range texts[1:] {
		parts := splitMetadataLine(text)
		for _, part := range parts {
			if item.Artist == "" && !looksLikeDuration(part) && !strings.EqualFold(part, "song") {
				item.Artist = part
				continue
			}
			if item.Album == "" && !looksLikeDuration(part) && !strings.EqualFold(part, item.Artist) {
				item.Album = part
			}
			if item.DurationSeconds == 0 && looksLikeDuration(part) {
				item.DurationSeconds = parseDuration(part)
			}
		}
	}
	return item
}

func collectYouTubeTexts(value any) []string {
	seen := map[string]struct{}{}
	var out []string
	var walk func(any)
	walk = func(value any) {
		switch node := value.(type) {
		case map[string]any:
			if text, ok := node["simpleText"].(string); ok {
				addUniqueText(&out, seen, text)
			}
			if runs, ok := node["runs"].([]any); ok {
				var joined []string
				for _, run := range runs {
					if object, ok := run.(map[string]any); ok {
						if text, ok := object["text"].(string); ok {
							joined = append(joined, text)
						}
					}
				}
				addUniqueText(&out, seen, strings.Join(joined, ""))
			}
			for _, child := range node {
				walk(child)
			}
		case []any:
			for _, child := range node {
				walk(child)
			}
		}
	}
	walk(value)
	return out
}

func addUniqueText(out *[]string, seen map[string]struct{}, value string) {
	value = cleanText(value)
	if value == "" || value == "•" {
		return
	}
	key := normalizeForMatch(value)
	if _, ok := seen[key]; ok {
		return
	}
	seen[key] = struct{}{}
	*out = append(*out, value)
}

func (s *Service) matchImportCandidates(ctx context.Context, candidates []importCandidate) ([]ImportItem, []string, error) {
	tracks, err := s.loadImportTracks(ctx)
	if err != nil {
		return nil, nil, err
	}
	items := make([]ImportItem, 0, len(candidates))
	trackIDs := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		item := ImportItem{
			Position:        candidate.Position,
			Title:           candidate.Title,
			Artist:          candidate.Artist,
			Album:           candidate.Album,
			DurationSeconds: candidate.DurationSeconds,
			Path:            candidate.Path,
			Raw:             candidate.Raw,
			Status:          "unmatched",
		}
		trackID, score, reason, ambiguous := bestTrackMatch(candidate, tracks)
		item.MatchScore = score
		item.MatchReason = reason
		if ambiguous {
			item.Status = "ambiguous"
		} else if trackID != "" {
			item.TrackID = trackID
			item.Status = "matched"
			trackIDs = append(trackIDs, trackID)
		}
		items = append(items, item)
	}
	return items, trackIDs, nil
}

func (s *Service) loadImportTracks(ctx context.Context) ([]importTrack, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.id, t.title, t.display_artist, t.album_title, t.duration_seconds,
		       COALESCE(m.path, ''), COALESCE(m.relative_path, ''), COALESCE(m.file_name, '')
		FROM music_tracks t
		LEFT JOIN media_files m ON m.track_id = t.id`)
	if err != nil {
		return nil, fmt.Errorf("load import match catalog: %w", err)
	}
	defer rows.Close()
	byID := map[string]*importTrack{}
	var order []string
	for rows.Next() {
		var id, title, artist, album, filePath, relativePath, fileName string
		var duration int
		if err := rows.Scan(&id, &title, &artist, &album, &duration, &filePath, &relativePath, &fileName); err != nil {
			return nil, err
		}
		track := byID[id]
		if track == nil {
			track = &importTrack{ID: id, Title: title, DisplayArtist: artist, AlbumTitle: album, DurationSeconds: duration}
			byID[id] = track
			order = append(order, id)
		}
		for _, value := range []string{filePath, relativePath} {
			value = strings.TrimSpace(value)
			if value != "" && !slices.Contains(track.Paths, value) {
				track.Paths = append(track.Paths, value)
			}
		}
		fileName = strings.TrimSpace(fileName)
		if fileName != "" && !slices.Contains(track.FileNames, fileName) {
			track.FileNames = append(track.FileNames, fileName)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]importTrack, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out, nil
}

func bestTrackMatch(candidate importCandidate, tracks []importTrack) (string, int, string, bool) {
	bestScore := 0
	bestID := ""
	bestReason := ""
	ambiguous := false
	for _, track := range tracks {
		score, reason := scoreTrackMatch(candidate, track)
		if score > bestScore {
			bestScore = score
			bestID = track.ID
			bestReason = reason
			ambiguous = false
			continue
		}
		if score == bestScore && score >= 85 && track.ID != bestID {
			ambiguous = true
		}
	}
	if bestScore < 85 {
		return "", bestScore, bestReason, false
	}
	return bestID, bestScore, bestReason, ambiguous
}

func scoreTrackMatch(candidate importCandidate, track importTrack) (int, string) {
	if candidate.TrackID != "" && candidate.TrackID == track.ID {
		return 240, "track id"
	}
	if score := pathMatchScore(candidate.Path, track); score > 0 {
		return score, "path"
	}

	score := 0
	reasons := []string{}
	titleScore := textScore(candidate.Title, track.Title)
	score += titleScore
	if titleScore > 0 {
		reasons = append(reasons, "title")
	}
	artistScore := textScore(candidate.Artist, track.DisplayArtist)
	if candidate.Artist != "" && track.DisplayArtist != "" && artistScore == 0 {
		score -= 30
	} else if artistScore > 0 {
		score += artistScore / 2
		reasons = append(reasons, "artist")
	}
	albumScore := textScore(candidate.Album, track.AlbumTitle)
	if albumScore > 0 {
		score += albumScore / 4
		reasons = append(reasons, "album")
	}
	if candidate.DurationSeconds > 0 && track.DurationSeconds > 0 {
		diff := candidate.DurationSeconds - track.DurationSeconds
		if diff < 0 {
			diff = -diff
		}
		switch {
		case diff <= 2:
			score += 15
			reasons = append(reasons, "duration")
		case diff <= 5:
			score += 8
			reasons = append(reasons, "duration")
		}
	}
	return score, strings.Join(reasons, "+")
}

func pathMatchScore(candidatePath string, track importTrack) int {
	candidatePath = strings.TrimSpace(candidatePath)
	if candidatePath == "" {
		return 0
	}
	normalizedPath := normalizePath(candidatePath)
	fileName := normalizePath(path.Base(candidatePath))
	for _, trackPath := range track.Paths {
		normalizedTrackPath := normalizePath(trackPath)
		switch {
		case normalizedPath == normalizedTrackPath:
			return 220
		case strings.HasSuffix(normalizedTrackPath, normalizedPath), strings.HasSuffix(normalizedPath, normalizedTrackPath):
			return 180
		}
	}
	for _, trackFile := range track.FileNames {
		if fileName != "" && fileName == normalizePath(trackFile) {
			return 160
		}
	}
	return 0
}

func textScore(left, right string) int {
	left = normalizeForMatch(left)
	right = normalizeForMatch(right)
	if left == "" || right == "" {
		return 0
	}
	switch {
	case left == right:
		return 100
	case strings.Contains(left, right), strings.Contains(right, left):
		return 50
	default:
		return 0
	}
}

func normalizeSourceType(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "csv", "m3u", "m3u8", "plain", "text", "json", "youtube", "youtube-music", "ytmusic":
		if strings.EqualFold(raw, "m3u8") {
			return "m3u"
		}
		if strings.EqualFold(raw, "text") {
			return "plain"
		}
		if strings.EqualFold(raw, "youtube-music") || strings.EqualFold(raw, "ytmusic") {
			return "youtube"
		}
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return "auto"
	}
}

func inferSourceType(sourceType, content string) string {
	sourceType = normalizeSourceType(sourceType)
	if sourceType != "auto" {
		return sourceType
	}
	trimmed := strings.TrimSpace(strings.TrimPrefix(content, "\ufeff"))
	switch {
	case strings.HasPrefix(trimmed, "#EXTM3U"):
		return "m3u"
	case strings.HasPrefix(trimmed, "{"), strings.HasPrefix(trimmed, "["):
		return "json"
	case strings.Contains(strings.ToLower(firstLine(trimmed)), "youtube.com"):
		return "youtube"
	case strings.Contains(firstLine(trimmed), ","):
		return "csv"
	default:
		return "plain"
	}
}

func inferSourceTypeFromURL(parsed *url.URL) string {
	ext := strings.ToLower(path.Ext(parsed.Path))
	switch ext {
	case ".m3u", ".m3u8":
		return "m3u"
	case ".csv":
		return "csv"
	case ".json":
		return "json"
	default:
		return "plain"
	}
}

func isYouTubeURL(parsed *url.URL) bool {
	host := strings.ToLower(parsed.Hostname())
	return host == "music.youtube.com" || host == "www.youtube.com" || host == "youtube.com" || host == "youtu.be"
}

func matchingJSONEnd(content string, start int) int {
	depth := 0
	inString := false
	escaped := false
	for index := start; index < len(content); index++ {
		ch := content[index]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return index + 1
			}
		}
	}
	return -1
}

func splitArtistTitle(value string) (string, string) {
	value = cleanText(value)
	for _, separator := range []string{" - ", " – ", " — ", " by "} {
		left, right, ok := strings.Cut(value, separator)
		if ok {
			return cleanText(left), cleanText(right)
		}
	}
	return "", value
}

func fillCandidateFromPath(item *importCandidate) {
	cleanedPath := strings.TrimSpace(item.Path)
	if parsed, err := url.Parse(cleanedPath); err == nil && parsed.Path != "" {
		cleanedPath = parsed.Path
	}
	base := path.Base(strings.ReplaceAll(cleanedPath, "\\", "/"))
	ext := path.Ext(base)
	base = strings.TrimSuffix(base, ext)
	artist, title := splitArtistTitle(strings.ReplaceAll(base, "_", " "))
	if item.Artist == "" {
		item.Artist = artist
	}
	if item.Title == "" {
		item.Title = title
	}
}

func parseDuration(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		return seconds
	}
	parts := strings.Split(value, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0
	}
	total := 0
	for _, part := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			return 0
		}
		total = total*60 + n
	}
	return total
}

func jsonString(object map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := object[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			return typed
		case float64:
			return strconv.Itoa(int(typed))
		case []any:
			var parts []string
			for _, entry := range typed {
				if text, ok := entry.(string); ok {
					parts = append(parts, text)
				}
			}
			return strings.Join(parts, ", ")
		}
	}
	return ""
}

func normalizeHeader(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, value)
}

var bracketedText = regexp.MustCompile(`(?i)\s*[\[(](official\s+)?(music\s+)?(video|audio|lyrics?|visualizer|remaster(ed)?|live)[\])]`)

func normalizeForMatch(value string) string {
	value = bracketedText.ReplaceAllString(value, "")
	value = strings.ToLower(value)
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		if unicode.IsSpace(r) {
			return ' '
		}
		return -1
	}, value)
}

func normalizePath(value string) string {
	value = strings.TrimSpace(strings.ToLower(strings.ReplaceAll(value, "\\", "/")))
	if parsed, err := url.Parse(value); err == nil && parsed.Path != "" {
		value = parsed.Path
	}
	return strings.Trim(value, "/")
}

func cleanText(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func firstLine(value string) string {
	line, _, _ := strings.Cut(value, "\n")
	return strings.TrimSpace(line)
}

func firstString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func looksLikePathOrURL(value string) bool {
	value = strings.TrimSpace(value)
	return strings.Contains(value, "/") || strings.Contains(value, "\\") || strings.HasPrefix(strings.ToLower(value), "http://") || strings.HasPrefix(strings.ToLower(value), "https://")
}

func splitMetadataLine(value string) []string {
	var parts []string
	for _, part := range strings.FieldsFunc(value, func(r rune) bool { return r == '•' || r == '|' }) {
		part = cleanText(part)
		if part != "" {
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 && strings.TrimSpace(value) != "" {
		parts = append(parts, cleanText(value))
	}
	return parts
}

func looksLikeDuration(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if _, err := strconv.Atoi(value); err == nil {
		return true
	}
	parts := strings.Split(value, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return false
	}
	for _, part := range parts {
		if _, err := strconv.Atoi(strings.TrimSpace(part)); err != nil {
			return false
		}
	}
	return true
}
