package artistmeta

import (
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

type fakeCatalog struct {
	byName    map[string]string
	artists   map[string]catalog.MusicArtist
	metaCalls int
}

func (f *fakeCatalog) MusicArtist(id string) (catalog.MusicArtist, error) {
	if artist, ok := f.artists[id]; ok {
		return artist, nil
	}
	return catalog.MusicArtist{}, catalog.ErrNotFound
}

func (f *fakeCatalog) MusicArtistIDByName(name string) (string, bool) {
	id, ok := f.byName[name]
	return id, ok
}

func (f *fakeCatalog) SetMusicArtistMeta(string, string, []catalog.SimilarArtistRef) {
	f.metaCalls++
}

func named(names ...string) []similarCandidate {
	out := make([]similarCandidate, len(names))
	for i, name := range names {
		out[i] = similarCandidate{Name: name}
	}
	return out
}

func TestResolveSimilarRefsHydratesLocalKeepsExternalDedupedCapped(t *testing.T) {
	cat := &fakeCatalog{
		byName: map[string]string{
			"boygenius":    "a-2",
			"Lucy Dacus":   "a-3",
			"Julien Baker": "a-3", // same local artist as Lucy Dacus -> dedupe
			"Self":         "a-1", // self -> excluded
			"Conor Oberst": "a-4",
			// "Unknown Act" intentionally absent -> kept as an EXTERNAL ref.
		},
		artists: map[string]catalog.MusicArtist{
			"a-2": {ID: "a-2", Name: "boygenius", Images: []catalog.Image{{Path: "/x.jpg"}}},
			"a-3": {ID: "a-3", Name: "Lucy Dacus"},
			"a-4": {ID: "a-4", Name: "Conor Oberst"},
		},
	}
	svc := &Service{catalog: cat}

	refs := svc.resolveSimilarRefs("a-1", []similarCandidate{
		{Name: "boygenius"},
		{Name: "Self"},
		{Name: "Lucy Dacus"},
		{Name: "Julien Baker"},
		{Name: "Unknown Act", ImageURL: "https://img/unknown.jpg"},
		{Name: "Conor Oberst"},
	})

	// boygenius (local), Lucy Dacus (local), Unknown Act (external), Conor Oberst (local).
	if len(refs) != 4 {
		t.Fatalf("ref count = %d, want 4; got %#v", len(refs), refs)
	}
	if refs[0].ID != "a-2" || refs[0].Name != "boygenius" || len(refs[0].Images) != 1 || refs[0].External {
		t.Fatalf("first ref not hydrated from local artist: %#v", refs[0])
	}

	var external *catalog.SimilarArtistRef
	seenLocal := map[string]int{}
	for i := range refs {
		if refs[i].External {
			external = &refs[i]
			continue
		}
		seenLocal[refs[i].ID]++
	}
	if external == nil {
		t.Fatalf("Unknown Act dropped; external similar artist not kept: %#v", refs)
	}
	if external.ID != "" || external.Name != "Unknown Act" || external.ImageURL != "https://img/unknown.jpg" {
		t.Fatalf("external ref malformed: %#v", external)
	}
	// "Julien Baker" must not produce a second a-3 entry.
	if seenLocal["a-3"] != 1 {
		t.Fatalf("a-3 appeared %d times, want 1 (deduped)", seenLocal["a-3"])
	}
	if seenLocal["a-1"] != 0 {
		t.Fatalf("self artist leaked into similar list")
	}
}

func TestResolveSimilarRefsCapsTotal(t *testing.T) {
	svc := &Service{catalog: &fakeCatalog{byName: map[string]string{}}}
	candidates := make([]similarCandidate, 0, maxSimilar+5)
	for i := 0; i < maxSimilar+5; i++ {
		candidates = append(candidates, similarCandidate{Name: "Artist " + string(rune('A'+i))})
	}
	refs := svc.resolveSimilarRefs("self", candidates)
	if len(refs) != maxSimilar {
		t.Fatalf("ref count = %d, want cap %d", len(refs), maxSimilar)
	}
}
