package explo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// creepRecordings is the recording list AcoustID returned for the fingerprint
// of "Radiohead - Pablo Honey - 02 - Creep.flac" on 2026-09-17 (track
// cfe630f8-8122-423d-93da-aadc49f14267), trimmed to the entries that matter
// and in the order the API sent them — by recording id, which put a
// one-source mis-tagged upload first. Sources and durations are the real
// numbers.
func creepRecordings() []acoustidRecording {
	return []acoustidRecording{
		{ID: "1387bfbc-6a6e-4ed5-9e60-e990ed6d3cab", Title: "Radiohead - Creep", Artists: []acoustidArtist{{Name: "Klangsberg"}},
			ReleaseGroups: []acoustidReleaseGrp{{ID: "33677a84-71af-4be9-8f99-672ab463f74b", Title: "Klangsberg"}}, Sources: 1, Duration: 238.004},
		{ID: "13fbfb61-7d5f-4b0c-9a34-1f1f8d8f5a0e", Title: "Creep (friendly)", Artists: []acoustidArtist{{Name: "Radiohead"}},
			ReleaseGroups: []acoustidReleaseGrp{{ID: "rg-towering", Title: "Towering Above the Rest", Type: "Album", SecondaryTypes: []string{"Compilation"}}}, Sources: 47, Duration: 240},
		{ID: "1781f830-2f8b-4d64-8c5f-0a0a0a0a0a0a", Title: "Ветер", Artists: []acoustidArtist{{Name: "Brainstorm"}},
			ReleaseGroups: []acoustidReleaseGrp{{ID: "rg-best-of", Title: "The Best Of", Type: "Album", SecondaryTypes: []string{"Compilation"}}}, Sources: 1, Duration: 231},
		{ID: "672cc0e1-5b6b-4f4c-9a5f-2b2b2b2b2b2b", Title: "Creep", Artists: []acoustidArtist{{Name: "Radiohead"}},
			ReleaseGroups: []acoustidReleaseGrp{{ID: "rg-pablo-honey", Title: "Pablo Honey", Type: "Album"}}, Sources: 27, Duration: 237},
		{ID: "6c405275-8d8d-4e4e-9f9f-3c3c3c3c3c3c", Title: "Creep (cover Radiohead)", Artists: []acoustidArtist{{Name: "Radio Tapok"}},
			ReleaseGroups: []acoustidReleaseGrp{{ID: "rg-release-10", Title: "Release 10", Type: "Album"}}, Sources: 1, Duration: 240.984},
		{ID: "70595637-9310-45f2-a266-58f8de4874a7", Title: "Creep", Artists: []acoustidArtist{{Name: "Radiohead"}},
			ReleaseGroups: []acoustidReleaseGrp{
				{ID: "rg-no1-rock", Title: "The No.1 All Time Rock Album", Type: "Album", SecondaryTypes: []string{"Compilation"}},
				{ID: "rg-pablo-honey", Title: "Pablo Honey", Type: "Album"},
			}, Sources: 9972, Duration: 237},
		{ID: "c6ec9be0-1a1a-4b4b-8c8c-4d4d4d4d4d4d", Title: "Creep (live at Town & Country club)", Artists: []acoustidArtist{{Name: "Radiohead"}},
			ReleaseGroups: []acoustidReleaseGrp{{ID: "rg-towering", Title: "Towering Above the Rest", Type: "Album", SecondaryTypes: []string{"Compilation"}}}, Sources: 33, Duration: 244},
		{ID: "f901067c-5e5e-4f4f-9a9a-5e5e5e5e5e5e", Title: "La Primavera", Artists: []acoustidArtist{{Name: "Sash!"}},
			ReleaseGroups: []acoustidReleaseGrp{{ID: "rg-best-of-sash", Title: "Best of Sash!", Type: "Album", SecondaryTypes: []string{"Compilation"}}}, Sources: 1, Duration: 215.986},
		{ID: "fec5d8cf-6f6f-4a4a-8b8b-6f6f6f6f6f6f", Title: "Creep", Artists: []acoustidArtist{{Name: "Radiohead"}},
			ReleaseGroups: []acoustidReleaseGrp{{ID: "rg-pablo-honey", Title: "Pablo Honey", Type: "Album"}}, Sources: 1302, Duration: 237.88},
	}
}

// The file's own tags name Radiohead / Creep and carry the recording id
// 70595637; that recording is in the list, so it wins outright — over the
// mis-tag that happens to be listed first and over the other "Radiohead /
// Creep" duplicates.
func TestChooseRecordingTakesTheFilesOwnRecordingWhenListed(t *testing.T) {
	evidence := identityEvidence{
		Title: "Creep", Artist: "Radiohead",
		Path:                   "/drops/Radiohead - Pablo Honey - 02 - Creep.flac",
		MusicBrainzRecordingID: "70595637-9310-45f2-a266-58f8de4874a7",
		DurationSeconds:        235,
	}
	got, ok := chooseRecording(creepRecordings(), evidence)
	if !ok || got.ID != "70595637-9310-45f2-a266-58f8de4874a7" {
		t.Fatalf("chose %q %s / %s, want the file's own recording 70595637", got.ID, recordingArtist(got), got.Title)
	}
}

// Same file, tags only (no embedded id): the tags say Radiohead / Creep, and
// among the recordings that agree the crowd decides — 9,972 sources beats
// 1,302 beats 27, and the one-source mis-tag listed first never comes up.
func TestChooseRecordingPrefersTheBestSupportedAgreeingRecording(t *testing.T) {
	evidence := identityEvidence{Title: "Creep", Artist: "Radiohead", Path: "/drops/02 - Creep.flac", DurationSeconds: 235}
	got, ok := chooseRecording(creepRecordings(), evidence)
	if !ok || got.ID != "70595637-9310-45f2-a266-58f8de4874a7" {
		t.Fatalf("chose %q %s / %s, want Radiohead / Creep with 9972 sources", got.ID, recordingArtist(got), got.Title)
	}
}

// An untagged drop with an unhelpful filename says nothing about itself, so
// the crowd alone decides — which is still the real Creep, not the first
// entry in the list.
func TestChooseRecordingWithoutEvidenceFollowsTheSources(t *testing.T) {
	evidence := identityEvidence{Path: "/drops/02.flac", DurationSeconds: 235}
	got, ok := chooseRecording(creepRecordings(), evidence)
	if !ok || got.ID != "70595637-9310-45f2-a266-58f8de4874a7" {
		t.Fatalf("chose %q %s / %s, want the 9972-source recording", got.ID, recordingArtist(got), got.Title)
	}
}

// The old rule: first usable recording in the order AcoustID sent them. This
// is the exact input that filed Creep under Klangsberg; the case pins the
// regression so the list order can never decide again.
func TestChooseRecordingIgnoresListOrder(t *testing.T) {
	recordings := creepRecordings()
	if recordings[0].Artists[0].Name != "Klangsberg" {
		t.Fatal("fixture must list the mis-tag first, as AcoustID did")
	}
	got, _ := chooseRecording(recordings, identityEvidence{})
	if recordingArtist(got) == "Klangsberg" {
		t.Fatalf("the first-listed one-source mis-tag was chosen: %s / %s", recordingArtist(got), got.Title)
	}
}

// Tags win over sources when they disagree with the crowd: a file that says
// it is the live version gets the live version, however many more people
// submitted the studio one.
func TestChooseRecordingLetsTheTagsPickAmongVersions(t *testing.T) {
	evidence := identityEvidence{Title: "Creep (live at Town & Country club)", Artist: "Radiohead", Path: "/drops/x.flac", DurationSeconds: 244}
	got, ok := chooseRecording(creepRecordings(), evidence)
	if !ok || got.ID != "c6ec9be0-1a1a-4b4b-8c8c-4d4d4d4d4d4d" {
		t.Fatalf("chose %q %s / %s, want the live recording the tags name", got.ID, recordingArtist(got), got.Title)
	}
}

// Equal support, no evidence: the recording whose length matches the file's
// wins the tie, and a recording with no known length sorts last.
func TestChooseRecordingBreaksTiesByDuration(t *testing.T) {
	recordings := []acoustidRecording{
		{ID: "a", Title: "Song", Artists: []acoustidArtist{{Name: "Band"}}, Sources: 3, Duration: 0},
		{ID: "b", Title: "Song (extended)", Artists: []acoustidArtist{{Name: "Band"}}, Sources: 3, Duration: 412},
		{ID: "c", Title: "Song (radio edit)", Artists: []acoustidArtist{{Name: "Band"}}, Sources: 3, Duration: 201},
	}
	got, _ := chooseRecording(recordings, identityEvidence{DurationSeconds: 200})
	if got.ID != "c" {
		t.Fatalf("chose %q, want the 201s recording for a 200s file", got.ID)
	}
}

// Recordings with no title or no artist are still skipped, whatever their
// support, because nothing usable can be applied from them.
func TestChooseRecordingSkipsUnusableEntries(t *testing.T) {
	recordings := []acoustidRecording{
		{ID: "blank", Title: "", Artists: []acoustidArtist{{Name: "Someone"}}, Sources: 5000},
		{ID: "noartist", Title: "Song", Sources: 5000},
		{ID: "ok", Title: "Song", Artists: []acoustidArtist{{Name: "Band"}}, Sources: 1},
	}
	got, ok := chooseRecording(recordings, identityEvidence{})
	if !ok || got.ID != "ok" {
		t.Fatalf("chose %q (ok=%v), want the only usable recording", got.ID, ok)
	}
	if _, ok := chooseRecording(recordings[:2], identityEvidence{}); ok {
		t.Fatal("no usable recording must be reported as no match")
	}
}

func TestTokenAgreementFoldsOrderCaseAndPunctuation(t *testing.T) {
	cases := []struct {
		known, candidate string
		want             int
	}{
		{"The xx", "xx, The", 2},
		{"Creep", "Creep", 2},
		{"creep", "CREEP", 2},
		{"Runaway (feat. Pusha T)", "Runaway", 1},
		{"Creep", "Creep (live at Town & Country club)", 1},
		{"Running Up That Hill (A Deal With God)", "Running Up That Hill", 1},
		{"Radiohead", "Klangsberg", 0},
		// A substring test would find "ye" inside "Kanye"; words do not.
		{"Kanye West", "Ye, Pusha T", 0},
		{"Do I Wanna Know?", "No.1 Party Anthem", 0},
		{"", "Creep", 0},
		{"Creep", "", 0},
		// Diacritics are kept, and both sides usually carry them.
		{"The Marías", "The Marías", 2},
		{"bôa", "･ﾟ✧(=✪ ᆺ ✪=)-･ﾟ✧", 0},
	}
	for _, tc := range cases {
		if got := tokenAgreement(tc.known, tc.candidate); got != tc.want {
			t.Errorf("tokenAgreement(%q, %q) = %d, want %d", tc.known, tc.candidate, got, tc.want)
		}
	}
}

// The seed used for agreement is the same one the text-search fallback uses:
// tags first, the filename only when the tags are blank.
func TestRecordingAgreementFallsBackToTheFilename(t *testing.T) {
	recording := acoustidRecording{ID: "r", Title: "Creep", Artists: []acoustidArtist{{Name: "Radiohead"}}}
	fromTags := recordingAgreement(recording, identityEvidence{Title: "Creep", Artist: "Radiohead", Path: "/drops/01.flac"})
	fromName := recordingAgreement(recording, identityEvidence{Path: "/drops/Radiohead - Creep.flac"})
	if fromTags != 4 || fromName != 4 {
		t.Fatalf("agreement from tags = %d, from filename = %d; want 4 and 4", fromTags, fromName)
	}
	if got := recordingAgreement(recording, identityEvidence{Path: "/drops/01.flac"}); got != 0 {
		t.Fatalf("no evidence must score 0, got %d", got)
	}
	withID := recordingAgreement(recording, identityEvidence{Path: "/drops/01.flac", MusicBrainzRecordingID: "R"})
	if withID < 100 {
		t.Fatalf("the file's own recording id (case-insensitively) must dominate, got %d", withID)
	}
}

// lookupAcoustID end to end against a stub serving the real response shape:
// the match carries the chosen recording, its artist credit and its clean
// release group, not the first-listed mis-tag.
func TestLookupAcoustIDChoosesByEvidenceAndSources(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status": "ok", "results": [{"id": "cfe630f8", "score": 0.9999, "recordings": [
			{"id": "1387bfbc", "sources": 1, "duration": 238.0, "title": "Radiohead - Creep", "artists": [{"name": "Klangsberg"}],
			 "releasegroups": [{"id": "33677a84", "title": "Klangsberg"}]},
			{"id": "70595637", "sources": 9972, "duration": 237.0, "title": "Creep", "artists": [{"name": "Radiohead"}],
			 "releasegroups": [{"id": "rg-comp", "title": "The No.1 All Time Rock Album", "type": "Album", "secondarytypes": ["Compilation"]},
			                   {"id": "rg-ph", "title": "Pablo Honey", "type": "Album"}]}
		]}]}`))
	}))
	defer server.Close()
	orig := acoustidLookupURL
	acoustidLookupURL = server.URL
	t.Cleanup(func() { acoustidLookupURL = orig })

	match, ok, err := lookupAcoustID(context.Background(), server.Client(), "test-key", Fingerprint{DurationSeconds: 235, Value: "AQAA"},
		identityEvidence{Title: "Creep", Artist: "Radiohead", Path: "/drops/Radiohead - Pablo Honey - 02 - Creep.flac", DurationSeconds: 235})
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if match.Artist != "Radiohead" || match.Title != "Creep" || match.MusicBrainzRecordingID != "70595637" {
		t.Fatalf("match = %+v", match)
	}
	if match.Album != "Pablo Honey" || match.MusicBrainzReleaseGroupID != "rg-ph" {
		t.Fatalf("album = %q (%s), want the clean Pablo Honey release group", match.Album, match.MusicBrainzReleaseGroupID)
	}
}

// A guest credited in the title is part of the artist the file names, so the
// recording credited to both outranks the plain-credit duplicate that only
// lives on a compilation (the "Flashing Lights (feat. Dwele)" replay case).
func TestRecordingAgreementCountsAFeaturedCreditAsArtist(t *testing.T) {
	credited := acoustidRecording{ID: "a", Title: "Flashing Lights", Artists: []acoustidArtist{{Name: "Kanye West"}, {Name: "Dwele"}}, Sources: 400}
	plain := acoustidRecording{ID: "b", Title: "Flashing Lights", Artists: []acoustidArtist{{Name: "Kanye West"}}, Sources: 900}
	evidence := identityEvidence{Title: "Flashing Lights (feat. Dwele)", Artist: "Kanye West", Path: "/d/x.flac"}
	if a, b := recordingAgreement(credited, evidence), recordingAgreement(plain, evidence); a <= b {
		t.Fatalf("credited = %d, plain = %d; the recording credited to both must fit better", a, b)
	}
	got, _ := chooseRecording([]acoustidRecording{plain, credited}, evidence)
	if got.ID != "a" {
		t.Fatalf("chose %q, want the recording credited to Kanye West and Dwele", got.ID)
	}
	// A credit the artist tag already carries is not doubled.
	title, artist := identityEvidence{Title: "Runaway (feat. Pusha T)", Artist: "Kanye West, Pusha T"}.names()
	if title != "Runaway (feat. Pusha T)" || artist != "Kanye West, Pusha T" {
		t.Fatalf("names = %q / %q", title, artist)
	}
}

// The file's own album tag keeps the track on its record when a duplicate
// recording with more sources lives only on a compilation ("Earrings" on
// Sweet Boy vs a NOW sampler) — but only a CLEAN release group can agree, so
// a sharer's sampler tag cannot drag a hit onto the sampler's duplicate.
func TestRecordingAgreementUsesTheAlbumTagAgainstCleanReleaseGroupsOnly(t *testing.T) {
	own := acoustidRecording{ID: "own", Title: "Earrings", Artists: []acoustidArtist{{Name: "Malcolm Todd"}}, Sources: 40,
		ReleaseGroups: []acoustidReleaseGrp{{ID: "rg-sweet-boy", Title: "Sweet Boy", Type: "Album"}}}
	sampler := acoustidRecording{ID: "sampler", Title: "Earrings", Artists: []acoustidArtist{{Name: "Malcolm Todd"}}, Sources: 90,
		ReleaseGroups: []acoustidReleaseGrp{{ID: "rg-now", Title: "Now That’s What I Call Music! 124", Type: "Album", SecondaryTypes: []string{"Compilation"}}}}
	byTag := identityEvidence{Title: "Earrings", Artist: "Malcolm Todd", Album: "Sweet Boy", Path: "/d/x.flac"}
	if got, _ := chooseRecording([]acoustidRecording{sampler, own}, byTag); got.ID != "own" {
		t.Fatalf("chose %q, want the recording on the album the file names", got.ID)
	}
	// Tagged with the sampler's name: the sampler's group is derived, so the
	// tag agrees with nothing and the crowd decides.
	bySampler := identityEvidence{Title: "Earrings", Artist: "Malcolm Todd", Album: "Now That’s What I Call Music! 124", Path: "/d/x.flac"}
	if a, b := recordingAgreement(own, bySampler), recordingAgreement(sampler, bySampler); a != b {
		t.Fatalf("a sampler tag changed the agreement: own = %d, sampler = %d", a, b)
	}
	// No album tag at all: the crowd decides, as before.
	if got, _ := chooseRecording([]acoustidRecording{sampler, own}, identityEvidence{Title: "Earrings", Artist: "Malcolm Todd", Path: "/d/x.flac"}); got.ID != "sampler" {
		t.Fatalf("chose %q, want the better-supported recording when the file names no album", got.ID)
	}
}

// "Mixtape/Street" is what the artist's own mixtape is typed as; it is not a
// sampler. Ranking it as derived filed "Passionfruit" under its single rather
// than More Life, and let a NOW sampler's duplicate beat the mixtape's own
// recording of "Earrings" (both real replays). Compilations, live tapes,
// remix packages, DJ mixes and soundtracks stay derived.
func TestReleaseGroupDerivedOnlyForOtherPeoplesRecords(t *testing.T) {
	for _, derived := range []string{"Compilation", "Live", "Remix", "DJ-mix", "Soundtrack"} {
		if !releaseGroupDerived([]string{derived}) {
			t.Errorf("%s must count as derived", derived)
		}
	}
	for _, own := range []string{"Mixtape/Street", "Demo", "Spokenword"} {
		if releaseGroupDerived([]string{own}) {
			t.Errorf("%s is the artist's own record and must not count as derived", own)
		}
	}
	if id, title := bestReleaseGroup([]acoustidReleaseGrp{
		{ID: "single", Title: "Passionfruit", Type: "Single"},
		{ID: "mixtape", Title: "More Life", Type: "Album", SecondaryTypes: []string{"Mixtape/Street"}},
	}); id != "mixtape" || title != "More Life" {
		t.Fatalf("best release group = %q (%s), want the mixtape album over its single", title, id)
	}
	own := acoustidRecording{ID: "own", Title: "Earrings", Artists: []acoustidArtist{{Name: "Malcolm Todd"}}, Sources: 10,
		ReleaseGroups: []acoustidReleaseGrp{{ID: "rg-sweet-boy", Title: "Sweet Boy", Type: "Album", SecondaryTypes: []string{"Mixtape/Street"}}}}
	sampler := acoustidRecording{ID: "sampler", Title: "Earrings", Artists: []acoustidArtist{{Name: "Malcolm Todd"}}, Sources: 38,
		ReleaseGroups: []acoustidReleaseGrp{{ID: "rg-now", Title: "Now That’s What I Call Music! 124", Type: "Album", SecondaryTypes: []string{"Compilation"}}}}
	got, _ := chooseRecording([]acoustidRecording{sampler, own}, identityEvidence{Title: "Earrings", Artist: "Malcolm Todd", Album: "Sweet Boy", Path: "/d/x.flac"})
	if got.ID != "own" {
		t.Fatalf("chose %q, want the mixtape's own recording the album tag names", got.ID)
	}
}
