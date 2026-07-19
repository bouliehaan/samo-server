package catalog

import (
	"testing"

	"github.com/bouliehaan/samo-server/internal/users"
)

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

// A non-system playlist owned by the reserved bootstrap account (user-server)
// is server-managed (filesystem m3u imports, admin-owner-before-a-human-exists)
// - no human authenticates as that id, so it must be visible to every real
// user or the Playlists tab is silently emptied. Regression guard for the
// "playlists page totally blank on Android" report: the mirror syncs
// /music/playlists, which is filtered by exactly this predicate.
func TestPlaylistVisibleToUserBootstrapOwnedPlaylist(t *testing.T) {
	imported := MusicPlaylist{ID: "pl-import", OwnerID: users.BootstrapUserID, Public: false, System: false}
	if !PlaylistVisibleToUser(imported, "user-human") {
		t.Fatal("bootstrap-owned playlist must be visible to a real user")
	}
	if !PlaylistVisibleToUser(imported, "") {
		t.Fatal("bootstrap-owned playlist must be visible regardless of principal")
	}

	// A whitespace-padded owner id must still match the reserved account.
	padded := MusicPlaylist{ID: "pl-import2", OwnerID: " " + users.BootstrapUserID + " "}
	if !PlaylistVisibleToUser(padded, "user-human") {
		t.Fatal("bootstrap-owned playlist (padded id) must be visible")
	}
}
