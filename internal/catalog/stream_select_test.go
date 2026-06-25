package catalog

import (
	"net/http/httptest"
	"testing"
)

func TestStreamSelectQueryFromRequestHonorsExplicitZero(t *testing.T) {
	req := httptest.NewRequest("GET", "/stream?progressSeconds=0", nil)
	query := StreamSelectQueryFromRequest(req)
	if !query.HasProgressSeconds {
		t.Fatal("expected HasProgressSeconds for progressSeconds=0")
	}
	if query.ProgressSeconds != 0 {
		t.Fatalf("progress = %d, want 0", query.ProgressSeconds)
	}
}

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

func TestSelectStreamTargetHonorsExplicitZeroProgress(t *testing.T) {
	files := []AudioFile{
		{ID: "file-1", RelativePath: "book.m4b", DurationSeconds: 3600},
	}

	target, err := SelectStreamTarget(
		files,
		PlaybackState{ProgressSeconds: 900},
		StreamSelectQuery{ProgressSeconds: 0, HasProgressSeconds: true},
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	if target.FileID != "file-1" {
		t.Fatalf("file = %q, want file-1", target.FileID)
	}
	if target.OffsetSeconds != 0 {
		t.Fatalf("offset = %d, want 0 (explicit restart)", target.OffsetSeconds)
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
