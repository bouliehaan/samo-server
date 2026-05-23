package catalog

import "testing"

func TestSelectStreamTargetUsesPlaybackProgressAcrossFiles(t *testing.T) {
	files := []AudioFile{
		{ID: "file-1", RelativePath: "01-01.mp3", DurationSeconds: 100},
		{ID: "file-2", RelativePath: "01-02.mp3", DurationSeconds: 200},
		{ID: "file-3", RelativePath: "02-01.mp3", DurationSeconds: 50, EmbeddedTags: Tags{"discnumber": []string{"2"}}},
	}

	target, err := SelectStreamTarget(files, PlaybackState{ProgressSeconds: 150}, StreamSelectQuery{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if target.FileID != "file-2" {
		t.Fatalf("file = %q, want file-2", target.FileID)
	}
	if target.OffsetSeconds != 50 {
		t.Fatalf("offset = %d, want 50", target.OffsetSeconds)
	}
}

func TestSelectStreamTargetHonorsDiscQuery(t *testing.T) {
	files := []AudioFile{
		{ID: "file-1", RelativePath: "disc1/track.mp3", DurationSeconds: 100, EmbeddedTags: Tags{"discnumber": []string{"1"}}},
		{ID: "file-2", RelativePath: "disc2/track.mp3", DurationSeconds: 100, EmbeddedTags: Tags{"discnumber": []string{"2"}}},
	}

	target, err := SelectStreamTarget(files, PlaybackState{}, StreamSelectQuery{DiscNumber: 2}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if target.FileID != "file-2" {
		t.Fatalf("file = %q, want file-2", target.FileID)
	}
}

func TestSortAudioFilesOrdersByDiscAndTrack(t *testing.T) {
	files := SortAudioFiles([]AudioFile{
		{ID: "c", RelativePath: "disc2/02.mp3", EmbeddedTags: Tags{"discnumber": []string{"2"}, "tracknumber": []string{"2"}}},
		{ID: "a", RelativePath: "disc1/01.mp3", EmbeddedTags: Tags{"discnumber": []string{"1"}, "tracknumber": []string{"1"}}},
		{ID: "b", RelativePath: "disc2/01.mp3", EmbeddedTags: Tags{"discnumber": []string{"2"}, "tracknumber": []string{"1"}}},
	})
	if files[0].ID != "a" || files[1].ID != "b" || files[2].ID != "c" {
		t.Fatalf("order = %#v", files)
	}
}
