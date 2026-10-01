package musicrelease

import "testing"

func TestAlbumEligibility(t *testing.T) {
	for _, title := range []string{"Now That's What I Call Music 44", "NOW That’s What I Call Music! 44", "The Netherlands Top 100 Deezer", "Top 100 Netherlands Deezer"} {
		if !Rejected(title, "Album", nil) {
			t.Errorf("accepted unflagged compilation %q", title)
		}
	}
	for _, title := range []string{"SOS", "Now", "The Now Now", "Hot Fuss", "Collection", "Top Dog"} {
		if Rejected(title, "Album", nil) {
			t.Errorf("rejected original album %q", title)
		}
	}
	if !Rejected("Unremarkable Name", "Album", []string{"Compilation"}) {
		t.Error("accepted flagged compilation")
	}
	if Rejected("More Life", "Album", []string{"Mixtape/Street"}) {
		t.Error("rejected artist mixtape")
	}
}
