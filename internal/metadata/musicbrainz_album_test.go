package metadata

import "testing"

func TestRecordingAlbumSelectionRejectsChartAndKeepsMixtape(t *testing.T) {
	provider := &MusicBrainzProvider{}
	for _, secondary := range [][]string{nil, {"Mixtape/Street"}} {
		results := provider.recordingResults([]musicBrainzRecording{{Title: "Song", Releases: []musicBrainzRelease{
			{Title: "Top 100 Netherlands Deezer", ReleaseGroup: musicBrainzReleaseGroup{ID: "chart", PrimaryType: "Album"}},
			{Title: "Original Record", ReleaseGroup: musicBrainzReleaseGroup{ID: "original", PrimaryType: "Album", SecondaryTypes: secondary}},
		}}})
		if results[0].ExternalIDs.MusicBrainzReleaseGroupID != "original" || results[0].Raw["releaseIsDerived"] != false {
			t.Fatalf("picked %+v", results[0])
		}
	}
	results := provider.recordingResults([]musicBrainzRecording{{Title: "SOS", Releases: []musicBrainzRelease{
		{Title: "Top 100 Netherlands Deezer", ReleaseGroup: musicBrainzReleaseGroup{ID: "chart", PrimaryType: "Album"}},
	}}})
	if results[0].Raw["releaseIsDerived"] != true {
		t.Fatal("unflagged chart release was accepted")
	}
}
