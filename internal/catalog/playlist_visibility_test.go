package catalog

import "testing"

// System (server-managed) playlists must be visible to every user: they are
// curated for the whole server (e.g. the explo drop queue both home screens
// surface) and are typically owned by the internal bootstrap admin, an id no
// real user matches - without the system rule they would render for nobody.
func TestPlaylistVisibleToUserSystemPlaylist(t *testing.T) {
	system := MusicPlaylist{ID: "pl-explo", OwnerID: "user-server", Public: false, System: true}
	if !PlaylistVisibleToUser(system, "user-somebody") {
		t.Fatal("system playlist must be visible to a non-owner user")
	}
	if !PlaylistVisibleToUser(system, "") {
		t.Fatal("system playlist must be visible regardless of principal")
	}

	// The rule must not leak: a plain private playlist stays owner-only.
	private := MusicPlaylist{ID: "pl-mine", OwnerID: "user-a", Public: false}
	if PlaylistVisibleToUser(private, "user-b") {
		t.Fatal("private non-system playlist must stay owner-only")
	}
	if !PlaylistVisibleToUser(private, "user-a") {
		t.Fatal("private playlist must stay visible to its owner")
	}
}
