package api

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// What a samo-server container sees: the published compose mounts the media
// folder the person named, the music folder inside it, samo's data volume, the
// database socket, the host clock and Docker's own files. Only the first two
// are somewhere to look for music.
func TestMountedMediaDirsAreTheHostFolders(t *testing.T) {
	mountinfo := filepath.Join(t.TempDir(), "mountinfo")
	lines := "" +
		"1234 1200 0:45 / / rw,relatime master:1 - overlay overlay rw,lowerdir=/x\n" +
		"1235 1234 0:50 / /proc rw,nosuid - proc proc rw\n" +
		"1236 1234 0:51 / /dev rw,nosuid - tmpfs tmpfs rw\n" +
		"1240 1234 254:1 /var/lib/docker/volumes/samo-server_samodata/_data /data rw - ext4 /dev/vda1 rw\n" +
		"1241 1234 254:1 /home/jake/media /home/jake/media ro - ext4 /dev/vda1 rw\n" +
		"1242 1241 254:1 /home/jake/media/Music /home/jake/media/Music rw - ext4 /dev/vda1 rw\n" +
		"1243 1234 254:1 /var/lib/docker/containers/abc/resolv.conf /etc/resolv.conf rw - ext4 /dev/vda1 rw\n" +
		"1244 1234 254:1 /usr/share/zoneinfo/America/Denver /etc/localtime ro - ext4 /dev/vda1 rw\n" +
		"1245 1234 254:1 /var/lib/docker/volumes/samo-server_pgsocket/_data /var/run/postgresql rw - ext4 /dev/vda1 rw\n" +
		"1246 1234 254:1 /srv/My\\040Music /srv/My\\040Music ro - ext4 /dev/vda1 rw\n"
	if err := os.WriteFile(mountinfo, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	got := mountedMediaDirs(mountinfo)
	want := []string{"/data", "/home/jake/media", "/home/jake/media/Music", "/srv/My Music"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mounted dirs = %q, want %q", got, want)
	}
	// /data is samo's own and defaultRootEntries drops it by SAMO_DATA_DIR;
	// listing it here is mountinfo's view, not a suggestion.
}

func TestMountedMediaDirsWithoutMountinfo(t *testing.T) {
	if got := mountedMediaDirs(filepath.Join(t.TempDir(), "absent")); got != nil {
		t.Fatalf("got %q from a missing mountinfo", got)
	}
}
