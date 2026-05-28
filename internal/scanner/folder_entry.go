package scanner

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"time"
)

// albumFolder groups audio files under one album directory for Navidrome-style
// folder-level incremental scanning.
type albumFolder struct {
	relPath  string
	absPath  string
	files    []string
	modTime  time.Time
	prevHash string
}

func groupFilesByAlbumFolder(root string, files []string) []albumFolder {
	groups := map[string]*albumFolder{}
	order := make([]string, 0)
	root = filepath.Clean(root)
	for _, file := range files {
		rel, err := filepath.Rel(root, file)
		if err != nil {
			rel = file
		}
		relAlbumDir := filepath.ToSlash(filepath.Dir(rel))
		key := albumIdentityDir(relAlbumDir)
		if key == "." {
			key = ""
		}
		group, ok := groups[key]
		if !ok {
			absPath := filepath.Join(root, filepath.FromSlash(key))
			if key == "" {
				absPath = root
			}
			group = &albumFolder{relPath: key, absPath: absPath}
			groups[key] = group
			order = append(order, key)
		}
		group.files = append(group.files, file)
		info, err := statWithTimeout(file, pathStatTimeout)
		if err == nil && info.ModTime().After(group.modTime) {
			group.modTime = info.ModTime()
		}
	}
	out := make([]albumFolder, 0, len(order))
	for _, key := range order {
		g := groups[key]
		sort.Strings(g.files)
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].relPath < out[j].relPath })
	return out
}

func (f *albumFolder) hash() string {
	h := md5.New()
	_, _ = fmt.Fprintf(h, "%s", f.modTime.UTC())
	audio := append([]string(nil), f.files...)
	sort.Strings(audio)
	for _, path := range audio {
		_, _ = io.WriteString(h, path)
		if info, err := statWithTimeout(path, pathStatTimeout); err == nil {
			_, _ = fmt.Fprintf(h, ":%d:%s", info.Size(), info.ModTime().UTC())
		}
	}
	if cover := findCoverImage(f.absPath); cover != nil {
		_, _ = io.WriteString(h, cover.Path)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (f *albumFolder) isOutdated(fullScan bool) bool {
	if fullScan {
		return true
	}
	if f.prevHash == "" {
		return len(f.files) > 0
	}
	return f.prevHash != f.hash()
}
