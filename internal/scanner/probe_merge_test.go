package scanner

import (
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

func TestMergeProbeInfoPrefersFFprobeChaptersOverOverdrive(t *testing.T) {
	native := probeInfo{
		Chapters: []catalog.AudioChapter{
			{Index: 1, Title: "Overdrive", StartSeconds: 503, EndSeconds: 600},
		},
	}
	ff := probeInfo{
		Chapters: []catalog.AudioChapter{
			{Index: 1, Title: "Embedded", StartSeconds: 528, EndSeconds: 600},
		},
	}

	merged := mergeProbeInfo(native, ff, true)
	if len(merged.Chapters) != 1 {
		t.Fatalf("chapters = %d, want 1", len(merged.Chapters))
	}
	if merged.Chapters[0].StartSeconds != 528 {
		t.Fatalf("start = %d, want embedded 528", merged.Chapters[0].StartSeconds)
	}
	if merged.Chapters[0].Title != "Embedded" {
		t.Fatalf("title = %q, want Embedded", merged.Chapters[0].Title)
	}
}
