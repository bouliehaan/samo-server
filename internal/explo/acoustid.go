package explo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// fallbackHTTPClient backs the nil-client path. http.DefaultClient must never
// be used here: it has no timeout, so an upstream that accepts the connection
// and then goes silent parks the calling goroutine forever.
var fallbackHTTPClient = &http.Client{Timeout: 30 * time.Second}

// acoustidLookupURL is a var (not const) so tests can point it at an
// httptest server instead of the real AcoustID API.
var acoustidLookupURL = "https://api.acoustid.org/v2/lookup"

// identifiedTrack is a candidate identification for one explo file, from
// whichever method produced it (AcoustID fingerprint match, or the
// filename+duration-gated MusicBrainz text-search fallback) - enough to
// drive a metadata apply, nothing more.
type identifiedTrack struct {
	Source                 string // "acoustid" | "musicbrainz-search"
	AcoustID               string // empty for the text-search fallback
	Score                  float64
	MusicBrainzRecordingID string
	// MusicBrainzReleaseGroupID, when set, lets us fetch album art from the
	// Cover Art Archive so identified explo albums aren't blank tiles.
	MusicBrainzReleaseGroupID string
	Title                     string
	Artist                    string
	Album                     string
}

type acoustidResponse struct {
	Status string `json:"status"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
	Results []acoustidResult `json:"results"`
}

type acoustidResult struct {
	ID         string              `json:"id"`
	Score      float64             `json:"score"`
	Recordings []acoustidRecording `json:"recordings"`
}

type acoustidRecording struct {
	ID            string               `json:"id"`
	Title         string               `json:"title"`
	Artists       []acoustidArtist     `json:"artists"`
	ReleaseGroups []acoustidReleaseGrp `json:"releasegroups"`
	// Sources is how many fingerprint submissions link this recording to the
	// fingerprint (meta=sources) — the crowd's vote for what this audio IS.
	// A hit's fingerprint lists dozens of recordings: its own, with thousands
	// of sources, and every mis-tagged upload anyone ever submitted against
	// the same audio, with one each. Nothing else in the response tells them
	// apart; the list is not ordered by anything meaningful (it came back
	// sorted by MBID, which is how "Klangsberg / Radiohead - Creep", one
	// source, was taken over the real Creep with 9,972).
	Sources int `json:"sources"`
	// Duration is the recording's length in seconds as MusicBrainz has it;
	// zero when unknown. Only a tie-break: a version of the song with the
	// same crowd support but the wrong length is the wrong version.
	Duration float64 `json:"duration"`
}

type acoustidArtist struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type acoustidReleaseGrp struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Type  string `json:"type"`
	// SecondaryTypes flags derived release groups ("Compilation", "Live",
	// "Remix", ...; see releaseGroupDerived). A classic hit appears on
	// hundreds of compilations, and picking one of those as "the album" gives
	// the track a random disco sampler's title and artwork instead of its
	// real record — the exact failure that made well-known songs render with
	// wrong or missing art.
	SecondaryTypes []string `json:"secondarytypes"`
}

// identityEvidence is what is already known about a drop before AcoustID is
// asked: its own tags, its filename, the MusicBrainz recording id it carries
// and its measured length. None of it identifies the file — the fingerprint
// does, and a match is never manufactured from it — but it decides BETWEEN
// the recordings AcoustID lists for one fingerprint, all of which are "this
// audio" as far as AcoustID is concerned. Every field may be empty.
type identityEvidence struct {
	Title  string // scanner tag
	Artist string // scanner tag (display artist)
	// Album is the scanner's album tag. Blank when it is only the drop
	// folder's name (the scanner's stand-in for a file with no album tag),
	// which names nothing.
	Album string
	Path  string // the file; its name is parsed exactly like the text-search seed
	// MusicBrainzRecordingID is the recording id embedded in the file's own
	// tags. When AcoustID lists that very recording for the fingerprint, the
	// file's tags and the audio agree and there is nothing left to decide.
	MusicBrainzRecordingID string
	DurationSeconds        int
}

// lookupAcoustID identifies a fingerprint against the AcoustID/MusicBrainz
// database. Returns ok=false (no error) when the lookup succeeded but found
// nothing usable - callers should record that as "unmatched", not retry.
func lookupAcoustID(ctx context.Context, httpClient *http.Client, apiKey string, fp Fingerprint, evidence identityEvidence) (identifiedTrack, bool, error) {
	if httpClient == nil {
		httpClient = fallbackHTTPClient
	}
	values := url.Values{
		"client":      {apiKey},
		"duration":    {strconv.Itoa(fp.DurationSeconds)},
		"fingerprint": {fp.Value},
		// AcoustID separates multiple meta values with "+", which on the wire
		// means a form-encoded space. url.Values.Encode() runs QueryEscape on
		// each value: a literal "+" becomes "%2B" (a literal plus AcoustID does
		// NOT split on -> it returns results with NO recordings, so every track
		// looks "unmatched" even on a 0.97 fingerprint hit), whereas a SPACE
		// becomes "+" -> the two meta values AcoustID expects. Must stay a space.
		// "sources" adds per-recording submission counts, which is the only
		// thing in the response that separates a song's real recording from
		// the mis-tagged uploads sharing its fingerprint (see chooseRecording).
		"meta":   {"recordings releasegroups sources"},
		"format": {"json"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, acoustidLookupURL+"?"+values.Encode(), nil)
	if err != nil {
		return identifiedTrack{}, false, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return identifiedTrack{}, false, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return identifiedTrack{}, false, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return identifiedTrack{}, false, fmt.Errorf("acoustid http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload acoustidResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return identifiedTrack{}, false, fmt.Errorf("decode acoustid response: %w", err)
	}
	if payload.Status != "ok" {
		message := "unknown error"
		if payload.Error != nil && strings.TrimSpace(payload.Error.Message) != "" {
			message = payload.Error.Message
		}
		return identifiedTrack{}, false, fmt.Errorf("acoustid: %s", message)
	}

	best, bestRecording, ok := bestAcoustIDResult(payload.Results, evidence)
	if !ok {
		return identifiedTrack{}, false, nil
	}

	match := identifiedTrack{
		Source:                 "acoustid",
		AcoustID:               best.ID,
		Score:                  best.Score,
		MusicBrainzRecordingID: bestRecording.ID,
		Title:                  strings.TrimSpace(bestRecording.Title),
	}
	match.Artist = recordingArtist(bestRecording)
	match.MusicBrainzReleaseGroupID, match.Album = bestReleaseGroup(bestRecording.ReleaseGroups)

	if match.Title == "" || match.Artist == "" {
		return identifiedTrack{}, false, nil
	}
	return match, true, nil
}

// bestAcoustIDResult picks the highest-score result that has at least one
// recording with a usable title+artist, and the best-supported recording in
// it (chooseRecording). The result is the fingerprint match — which audio
// this is — and is chosen on score alone; the recording is what that audio
// is CALLED, and one fingerprint carries many names.
func bestAcoustIDResult(results []acoustidResult, evidence identityEvidence) (acoustidResult, acoustidRecording, bool) {
	var bestResult acoustidResult
	var bestRecording acoustidRecording
	found := false
	for _, result := range results {
		recording, ok := chooseRecording(result.Recordings, evidence)
		if !ok {
			continue
		}
		if !found || result.Score > bestResult.Score {
			bestResult = result
			bestRecording = recording
			found = true
		}
	}
	return bestResult, bestRecording, found
}

// chooseRecording picks, among the recordings AcoustID lists for one
// fingerprint, the one to call the file. Ranking, strongest first:
//
//  1. Agreement with what is already known about the file (recordingAgreement):
//     the recording id its tags carry, then artist and title against its
//     tags or, failing those, its filename.
//  2. Sources — how many submissions say this audio is this recording. With
//     no evidence at all this is the whole decision, and it is the right
//     default: the crowd's answer for a fingerprint is the song, and a
//     one-source entry is one person's tagging.
//  3. Closeness of the recording's length to the file's, then the id, so the
//     choice is stable.
//
// It used to take the first recording with a title and an artist, in whatever
// order AcoustID sent them. Recordings without both are skipped as before.
func chooseRecording(recordings []acoustidRecording, evidence identityEvidence) (acoustidRecording, bool) {
	usable := make([]acoustidRecording, 0, len(recordings))
	for _, recording := range recordings {
		if strings.TrimSpace(recording.Title) != "" && len(recording.Artists) > 0 {
			usable = append(usable, recording)
		}
	}
	if len(usable) == 0 {
		return acoustidRecording{}, false
	}
	agreement := make(map[string]int, len(usable))
	for _, recording := range usable {
		agreement[recording.ID] = recordingAgreement(recording, evidence)
	}
	sort.SliceStable(usable, func(i, j int) bool {
		a, b := usable[i], usable[j]
		if agreement[a.ID] != agreement[b.ID] {
			return agreement[a.ID] > agreement[b.ID]
		}
		if a.Sources != b.Sources {
			return a.Sources > b.Sources
		}
		if ga, gb := durationGap(a, evidence), durationGap(b, evidence); ga != gb {
			return ga < gb
		}
		return a.ID < b.ID
	})
	return usable[0], true
}

// recordingArtist is the recording's artist credit as the match reports it:
// every credited artist, comma-joined.
func recordingArtist(recording acoustidRecording) string {
	names := make([]string, 0, len(recording.Artists))
	for _, artist := range recording.Artists {
		if name := strings.TrimSpace(artist.Name); name != "" {
			names = append(names, name)
		}
	}
	return strings.Join(names, ", ")
}

// recordingAgreement scores how well a recording fits what is already known
// about the file. A recording that IS the one the file's tags name outranks
// everything (the file and the audio agree; nothing is left to decide); after
// that, title, artist and album each score 2 when they equal the known value
// and 1 when one contains the other, so "Runaway" fits a file tagged "Runaway
// (feat. Pusha T)", a file tagged "Creep (live at Town & Country club)" fits
// that live recording better than the studio "Creep" — while "Klangsberg"
// against "Radiohead" scores nothing. No evidence, no score: an untagged drop
// leaves the decision to the crowd.
//
// The album counts only against a recording's CLEAN release groups (no
// Compilation/Live/Remix type). MusicBrainz holds a duplicate recording of
// most hits for every sampler they appeared on, and a sharer's rip is very
// often tagged with that sampler's name; letting "Top 2000" pull the track
// onto the sampler's duplicate would file it under the sampler. A real album
// tag ("Sweet Boy", "More Life") still keeps the track on its own record
// when a duplicate with more sources lives only on a compilation.
func recordingAgreement(recording acoustidRecording, evidence identityEvidence) int {
	score := 0
	if known := strings.TrimSpace(evidence.MusicBrainzRecordingID); known != "" && strings.EqualFold(known, strings.TrimSpace(recording.ID)) {
		score += 100
	}
	title, artist := evidence.names()
	score += tokenAgreement(title, recording.Title)
	score += tokenAgreement(artist, recordingArtist(recording))
	if album := strings.TrimSpace(evidence.Album); album != "" {
		best := 0
		for _, group := range recording.ReleaseGroups {
			if releaseGroupDerived(group.SecondaryTypes) {
				continue
			}
			if got := tokenAgreement(album, group.Title); got > best {
				best = got
			}
		}
		score += best
	}
	return score
}

// featuredCredit matches the guest credit a title carries — "(feat. Dwele)",
// "ft. Pusha T", "featuring X" — and captures the names.
var featuredCredit = regexp.MustCompile(`(?i)[(\[]?\s*(?:feat\.?|ft\.?|featuring)\s+([^()\[\]]+?)[)\]]?\s*$`)

// names is the title and artist the file gives for itself: its tags as they
// are, or, when it has no title tag, the "Artist - Title" its filename
// spells. Unlike the text-search seed nothing is stripped — "(live …)" in a
// tag is evidence about which version this is, not noise to search past. A
// guest credited in the title is part of the artist: a file tagged "Kanye
// West" / "Flashing Lights (feat. Dwele)" is by Kanye West and Dwele, which
// is how MusicBrainz credits the recording. A filename that is only a number
// ("01.flac") names nothing.
func (e identityEvidence) names() (title, artist string) {
	if title = strings.TrimSpace(e.Title); title != "" {
		artist = strings.TrimSpace(e.Artist)
	} else {
		title, artist = parseFilenameSearchQuery(e.Path)
		if !strings.ContainsFunc(title, unicode.IsLetter) {
			title = ""
		}
	}
	if m := featuredCredit.FindStringSubmatch(title); m != nil && artist != "" {
		if guest := strings.TrimSpace(m[1]); guest != "" && !subset(identityTokens(guest), identityTokens(artist)) {
			artist += ", " + guest
		}
	}
	return title, artist
}

// tokenAgreement compares two names as bags of words — case, punctuation and
// word order folded away — and answers 2 for the same words, 1 when one
// name's words all appear in the other, 0 otherwise. Bags rather than strings
// because artist credits are written every way round: "xx, The" and "The xx"
// are one band, and "Kanye West" shares no word with "Ye, Pusha T", whereas a
// substring test would find "ye" inside "Kanye". Either side blank is 0,
// never a match — an empty bag is a subset of everything.
func tokenAgreement(known, candidate string) int {
	a, b := identityTokens(known), identityTokens(candidate)
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	if len(a) == len(b) && subset(a, b) {
		return 2
	}
	if subset(a, b) || subset(b, a) {
		return 1
	}
	return 0
}

// identityTokens lowercases a name and splits it into its words, keeping
// letters and digits only. Diacritics are kept as they are: both the tags and
// MusicBrainz spell "Marías" and "bôa" with them.
func identityTokens(value string) map[string]struct{} {
	tokens := map[string]struct{}{}
	for _, field := range strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r))
	}) {
		tokens[field] = struct{}{}
	}
	return tokens
}

func subset(a, b map[string]struct{}) bool {
	for token := range a {
		if _, ok := b[token]; !ok {
			return false
		}
	}
	return true
}

// durationGap is how far the recording's length is from the file's, in
// seconds, or +Inf when either is unknown so the unknown sorts last.
func durationGap(recording acoustidRecording, evidence identityEvidence) float64 {
	if recording.Duration <= 0 || evidence.DurationSeconds <= 0 {
		return math.Inf(1)
	}
	return math.Abs(recording.Duration - float64(evidence.DurationSeconds))
}

// bestReleaseGroup returns the MBID and title of the release group that best
// represents the track's OWN record, so the caller can both name the album
// and fetch its cover art. Ranking: a clean (underived) Album, then
// a clean Single, then a clean EP, then anything else clean, and only as a
// last resort a derived release group (Compilation/Live/Remix/...). Either
// value may be empty.
func bestReleaseGroup(groups []acoustidReleaseGrp) (id, title string) {
	bestRank := len(releaseGroupRankOrder) + 2
	for _, group := range groups {
		if strings.TrimSpace(group.Title) == "" {
			continue
		}
		rank := releaseGroupRank(group.Type, group.SecondaryTypes)
		if rank < bestRank {
			bestRank = rank
			id, title = strings.TrimSpace(group.ID), strings.TrimSpace(group.Title)
		}
	}
	return id, title
}

// releaseGroupRankOrder is the primary-type preference for clean (underived)
// release groups. Lower index wins.
var releaseGroupRankOrder = []string{"album", "single", "ep"}

// derivedSecondaryTypes are the MusicBrainz secondary types that make a
// release group somebody else's record of the song rather than the artist's
// own: a sampler, a live tape, a remix package, a DJ mix, a film's licensed
// soundtrack. The other secondary types describe what the artist's own
// record IS — "Mixtape/Street" (More Life, Sweet Boy), "Demo", "Spokenword"
// — and ranking those as derived filed mixtape tracks under whichever single
// or sampler the recording also appeared on.
var derivedSecondaryTypes = map[string]bool{
	"compilation": true,
	"live":        true,
	"remix":       true,
	"dj-mix":      true,
	"soundtrack":  true,
}

// releaseGroupDerived reports whether a release group's secondary types mark
// it as somebody else's record of the song (derivedSecondaryTypes).
func releaseGroupDerived(secondaryTypes []string) bool {
	for _, secondary := range secondaryTypes {
		if derivedSecondaryTypes[strings.ToLower(strings.TrimSpace(secondary))] {
			return true
		}
	}
	return false
}

// releaseGroupRank scores a release group for bestReleaseGroup /
// fetchRecordingReleaseRefs: clean primaries by preference order, any other
// clean primary next, derived groups (releaseGroupDerived) last.
func releaseGroupRank(primaryType string, secondaryTypes []string) int {
	if releaseGroupDerived(secondaryTypes) {
		return len(releaseGroupRankOrder) + 1
	}
	primary := strings.ToLower(strings.TrimSpace(primaryType))
	for index, name := range releaseGroupRankOrder {
		if primary == name {
			return index
		}
	}
	return len(releaseGroupRankOrder)
}
