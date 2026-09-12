package explo

import (
	"context"
	"database/sql"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/libraries"
	"github.com/bouliehaan/samo-server/internal/watch"
)

// fakeFFmpeg stands in for the real binary where the remux itself is not what
// is under test: it copies the first -i input to the last argument and
// ignores every flag in between.
func fakeFFmpeg(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ffmpeg")
	script := `#!/bin/sh
src=""
while [ $# -gt 1 ]; do
  if [ "$1" = "-i" ] && [ -z "$src" ]; then src="$2"; fi
  shift
done
cp "$src" "$1"
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// keepFixture is a drop folder holding one track and a library with the
// track's destination folder already in place, so the watcher (which attaches
// to folders as it finds them) is certainly watching the folder the temp file
// lands in before the keep starts. Without that the test could pass by the
// watcher simply not looking.
type keepFixture struct {
	root, drop, source, albumDir string
	track                        catalog.MusicTrack
}

func newKeepFixture(t *testing.T, ext string) keepFixture {
	t.Helper()
	base := t.TempDir()
	f := keepFixture{
		root:     filepath.Join(base, "library"),
		drop:     filepath.Join(base, "explo"),
		albumDir: filepath.Join(base, "library", "Artist", "Album"),
	}
	f.source = filepath.Join(f.drop, "Artist - Song"+ext)
	for _, dir := range []string{f.albumDir, f.drop} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.track = catalog.MusicTrack{
		ID:            "drop-1",
		Title:         "Song",
		DisplayArtist: "Artist",
		AlbumTitle:    "Album",
		TrackNumber:   1,
		AudioFiles:    []catalog.AudioFile{{Path: f.source}},
	}
	return f
}

// service is the smallest Service that can run keepOne: no database (the
// twin and ledger lookups both stand aside without one, and the album title
// falls back to the catalog's), just the drop folder, a track and an ffmpeg.
func (f keepFixture) service(ffmpegPath string) *Service {
	return &Service{
		dirs:       []string{f.drop},
		ffmpegPath: ffmpegPath,
		trackByID:  func(string) (catalog.MusicTrack, error) { return f.track, nil },
		logger:     func(string, ...any) {},
	}
}

func (f keepFixture) keep(t *testing.T, s *Service) string {
	t.Helper()
	var res KeepResult
	dest, err := s.keepOne(context.Background(), f.track.ID, f.root, []string{f.drop}, &res)
	if err != nil {
		t.Fatalf("keep failed: %v", err)
	}
	if filepath.Dir(dest) != f.albumDir {
		t.Fatalf("copy landed in %s, but the fixture prepared %s", filepath.Dir(dest), f.albumDir)
	}
	return dest
}

type scanRecorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *scanRecorder) subpaths(_ context.Context, _ string, paths []string) (libraries.ScanResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range paths {
		r.calls = append(r.calls, "subpath:"+p)
	}
	return libraries.ScanResult{}, nil
}

func (r *scanRecorder) library(_ context.Context, libraryID string) (libraries.ScanResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, "library:"+libraryID)
	return libraries.ScanResult{}, nil
}

func (r *scanRecorder) got() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

// watchLibrary runs the real library watcher over root and reports every scan
// it asks for. Timings match the watcher's own behaviour tests.
func watchLibrary(t *testing.T, root string) *scanRecorder {
	t.Helper()
	rec := &scanRecorder{}
	w := watch.New(watch.Options{
		DB:           new(sql.DB),
		ScanSubpaths: rec.subpaths,
		ScanLibrary:  rec.library,
		ListLibraries: func(context.Context) ([]watch.LibraryRoot, error) {
			return []watch.LibraryRoot{{ID: "music", Path: root}}, nil
		},
		Debounce: 200 * time.Millisecond,
		Resync:   300 * time.Millisecond,
		Logger:   log.New(io.Discard, "", 0),
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = w.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	time.Sleep(400 * time.Millisecond)
	return rec
}

// A keep must not make the watcher reconcile the whole library.
//
// The copy is written to a temp name in the destination folder and renamed
// into place, and to fsnotify a rename is the old name being removed. While
// the temp name ended in the audio extension, the watcher saw an audio file
// appear, get written and then leave the library — and a file leaving is the
// one event it answers with a library-wide reconcile, because only that scan
// can mark files missing. Every keep therefore cost a full quick scan of the
// entire music library (18.7k files on the live server, 2026-09-10), for a
// copy Keep's own subpath scan had already catalogued.
//
// The copy itself, once renamed into place, IS a new audio file, and the
// watcher owes its folder an incremental scan: that one has to happen.
func TestKeepTempFileDoesNotMakeTheWatcherReconcileTheLibrary(t *testing.T) {
	f := newKeepFixture(t, ".flac")
	if err := os.WriteFile(f.source, []byte("fLaC not really"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := watchLibrary(t, f.root)

	dest := f.keep(t, f.service(fakeFFmpeg(t)))
	time.Sleep(1500 * time.Millisecond)

	got := rec.got()
	t.Logf("scans: %v", got)
	for _, call := range got {
		if strings.HasPrefix(call, "library:") {
			t.Fatalf("keeping one track made the watcher reconcile the whole library: %v", got)
		}
	}
	if !slices.Contains(got, "subpath:"+filepath.Dir(dest)) {
		t.Fatalf("the copy's folder never got its incremental scan: %v", got)
	}
	if entries, _ := filepath.Glob(filepath.Join(f.albumDir, "*samo-keep-tmp*")); len(entries) != 0 {
		t.Fatalf("temp file left behind: %v", entries)
	}
}

// keepContainers is every container the scanner catalogues, with a codec any
// ffmpeg can synthesize a source in, the bytes the container starts with, and
// whether ffmpeg's muxer can hold an attached picture (embedCoverArgs).
var keepContainers = []struct {
	ext, codec string
	magic      func([]byte) bool
	embeds     bool
}{
	{".flac", "flac", hasPrefix("fLaC"), true},
	{".mp3", "libmp3lame", hasPrefix("ID3"), true},
	{".m4a", "aac", atOffset(4, "ftyp"), true},
	{".m4b", "aac", atOffset(4, "ftyp"), true},
	// Ogg FLAC: the ogg muxer takes it, and unlike Vorbis every ffmpeg can
	// encode it.
	{".ogg", "flac", hasPrefix("OggS"), false},
	{".opus", "libopus", hasPrefix("OggS"), false},
	{".aac", "aac", func(b []byte) bool { return len(b) > 1 && b[0] == 0xFF && b[1]&0xF0 == 0xF0 }, false},
	{".wav", "pcm_s16le", hasPrefix("RIFF"), false},
	{".aif", "pcm_s16be", hasPrefix("FORM"), true},
	{".aiff", "pcm_s16be", hasPrefix("FORM"), true},
	{".wma", "wmav2", hasPrefix("\x30\x26\xB2\x75"), false},
}

func ffmpegOrSkip(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not on PATH; skipping real-remux test")
	}
	return path
}

// synthesize runs ffmpeg to produce a fixture, skipping the test when this
// build lacks the encoder — a missing libopus should skip that one case, not
// fail it.
func synthesize(t *testing.T, ffmpeg string, args ...string) {
	t.Helper()
	all := append([]string{"-nostdin", "-y", "-loglevel", "error"}, args...)
	if out, err := exec.Command(ffmpeg, all...).CombinedOutput(); err != nil {
		t.Skipf("this ffmpeg cannot write %s (%s): %v", args[len(args)-1], strings.TrimSpace(string(out)), err)
	}
}

// The reason the temp name needs -f: ffmpeg picks the container from the
// output extension, and the temp name has none. So with the real ffmpeg, a
// kept copy of each container the scanner accepts must still come out AS
// that container, written straight to the extension-less temp name.
func TestKeepRemuxWritesEachContainerToTheExtensionlessTempName(t *testing.T) {
	ffmpeg := ffmpegOrSkip(t)
	for _, tc := range keepContainers {
		t.Run(strings.TrimPrefix(tc.ext, "."), func(t *testing.T) {
			f := newKeepFixture(t, tc.ext)
			synthesize(t, ffmpeg, "-f", "lavfi", "-i", "sine=f=440:d=0.2", "-c:a", tc.codec, f.source)

			dest := f.keep(t, f.service(ffmpeg))
			head := make([]byte, 16)
			file, err := os.Open(dest)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			n, _ := io.ReadFull(file, head)
			if !tc.magic(head[:n]) {
				t.Fatalf("%s is not a %s container (starts % x)", dest, tc.ext, head[:n])
			}
		})
	}
}

// pngMagic is what a PNG starts with — and what a container that embeds one
// stores verbatim, so its presence in a copy means the cover is inside.
const pngMagic = "\x89PNG\r\n\x1a\n"

// A kept copy carries samo's cover in every container: inside the file where
// the container can hold a picture, and as the cover.<ext> sidecar the
// scanner reads first where it cannot. Before this, an Ogg, Opus, AAC or WAV
// drop with a cover could not be kept at all (the muxers refuse the picture
// stream), an AIFF copy silently lost it, and a WMA copy came out with the
// picture as a one-frame video stream.
func TestKeepCarriesTheCoverInEveryContainer(t *testing.T) {
	ffmpeg := ffmpegOrSkip(t)
	for _, tc := range keepContainers {
		t.Run(strings.TrimPrefix(tc.ext, "."), func(t *testing.T) {
			f := newKeepFixture(t, tc.ext)
			cover := filepath.Join(f.drop, "art.png")
			synthesize(t, ffmpeg, "-f", "lavfi", "-i", "sine=f=440:d=0.2", "-c:a", tc.codec, f.source)
			synthesize(t, ffmpeg, "-f", "lavfi", "-i", "color=c=red:s=32x32", "-frames:v", "1", cover)
			f.track.Images = []catalog.Image{{Path: cover}}

			dest := f.keep(t, f.service(ffmpeg))
			data, err := os.ReadFile(dest)
			if err != nil {
				t.Fatal(err)
			}
			if !tc.magic(data) {
				t.Fatalf("copy is not a %s container (starts % x)", tc.ext, data[:min(len(data), 16)])
			}
			inside := strings.Contains(string(data), pngMagic)
			sidecar, sidecarErr := os.ReadFile(filepath.Join(f.albumDir, "cover.png"))
			switch {
			case tc.embeds && !inside:
				t.Fatalf("%s can hold a picture but the copy carries none", tc.ext)
			case tc.embeds && sidecarErr == nil:
				t.Fatalf("%s holds the picture itself; no sidecar should be written", tc.ext)
			case !tc.embeds && inside:
				t.Fatalf("%s cannot hold a picture, yet one is inside the copy (a video stream)", tc.ext)
			case !tc.embeds && sidecarErr != nil:
				t.Fatalf("%s cannot hold a picture and none was placed beside the copy: %v", tc.ext, sidecarErr)
			case !tc.embeds && !strings.HasPrefix(string(sidecar), pngMagic):
				t.Fatalf("sidecar beside the %s copy is not the cover", tc.ext)
			}
			if leftovers, _ := filepath.Glob(filepath.Join(f.albumDir, "*samo-keep-tmp*")); len(leftovers) != 0 {
				t.Fatalf("temp files left behind: %v", leftovers)
			}
		})
	}
}

func hasPrefix(magic string) func([]byte) bool {
	return func(b []byte) bool { return strings.HasPrefix(string(b), magic) }
}

func atOffset(offset int, magic string) func([]byte) bool {
	return func(b []byte) bool {
		return len(b) >= offset+len(magic) && string(b[offset:offset+len(magic)]) == magic
	}
}
