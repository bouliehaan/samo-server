package scanner

import "testing"

func TestLibraryKindFromPath(t *testing.T) {
	if got := LibraryKindFromPath("/mnt/data2tb/Podcasts"); got != "podcast" {
		t.Fatalf("kind = %q, want podcast", got)
	}
	if got := LibraryKindFromPath("/media/Audiobooks"); got != "audiobook" {
		t.Fatalf("kind = %q, want audiobook", got)
	}
	if got := LibraryKindFromPath("/srv/music"); got != "music" {
		t.Fatalf("kind = %q, want music", got)
	}
}

func TestClassifyFolderAsPodcastLargeEpisodeBundle(t *testing.T) {
	files := make([]string, 10)
	for i := range files {
		files[i] = "episode-" + string(rune('0'+i)) + ".mp3"
	}
	if !classifyFolderAsPodcast("/shows/johnny-dollar", files) {
		t.Fatal("expected large episode bundle to classify as podcast")
	}
}
