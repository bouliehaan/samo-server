package artistimages

import (
	"strings"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// The cases here are real artist names taken from the backfill run that failed
// 123 of 123, where every failure was a lookup that found nothing because the
// credit was filed as one long artist name.
func TestLookupArtistNamesYieldsTheLeadArtist(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		sortName string
		wantLead string
	}{
		{name: "Aesop Rock with El‐P", wantLead: "Aesop Rock"},
		{name: "Aesop Rock with Breeze Brewin & Cage", wantLead: "Aesop Rock"},
		{name: "2Pac featuring K‐Ci & JoJo", wantLead: "2Pac"},
		{name: "Viktor Vaughn f/ Kool Keith", wantLead: "Viktor Vaughn"},
		{name: "Viktor Vaughn / MF DOOM", wantLead: "Viktor Vaughn"},
		{name: "Watsky w/Damon Elliott", wantLead: "Watsky"},
		{name: "Toby Keith Duet with Willie Nelson", wantLead: "Toby Keith"},
		{name: "Willie Nelson with Paula Nelson", wantLead: "Willie Nelson"},
		{name: "Andy Williams with Robert Mersey & His Orchestra", wantLead: "Andy Williams"},
		{name: "Annette Hanshaw Accomp. By The New Englanders", wantLead: "Annette Hanshaw"},
		{name: "Vieux Farka Touré et Khruangbin", wantLead: "Vieux Farka Touré"},
		{name: "deadmau5 + Imogen Heap", wantLead: "deadmau5"},
		{name: "JAY‐Z + Alicia Keys", wantLead: "JAY‐Z"},
		{name: "Anthony Doerr read by Zach Appelman", wantLead: "Anthony Doerr"},
		{name: "Someone ft. Another", wantLead: "Someone"},
		{name: "Someone FEATURING Another", wantLead: "Someone"},
		// The sort name is the only place the lead survives a comma flip.
		{name: "Watsky w/Damon Elliott", sortName: "Watsky w/Elliott, Damon", wantLead: "Watsky"},
	} {
		artist := catalog.MusicArtist{Name: testCase.name, SortName: testCase.sortName}
		names := lookupArtistNames(artist)

		if names[0] != testCase.name {
			t.Errorf("%q: first candidate = %q, want the full name to be tried first",
				testCase.name, names[0])
		}
		if !contains(names, testCase.wantLead) {
			t.Errorf("%q: candidates %q do not include the lead artist %q",
				testCase.name, names, testCase.wantLead)
		}
	}
}

// A joiner inside a real band name must not cost that band its own photo. The
// full name always leads, so these still match themselves; the shortened
// candidate is only reached once the full name has come back empty.
func TestLookupArtistNamesTriesTheWholeNameFirst(t *testing.T) {
	for _, name := range []string{
		"Earth, Wind & Fire",
		"Bob Marley and the Wailers",
		"Simon and Garfunkel",
		"Florence + the Machine",
		"Johnny Cash and The Tennessee Three",
		"Hall & Oates",
		"Nick Cave and the Bad Seeds",
		"Fitz and the Tantrums",
	} {
		names := lookupArtistNames(catalog.MusicArtist{Name: name})
		if names[0] != name {
			t.Errorf("%q: first candidate = %q, want the full name", name, names[0])
		}
	}

	// " and " is not a joiner, so these acts are never shortened at all.
	for _, name := range []string{"Bob Marley and the Wailers", "Simon and Garfunkel", "Frank Sinatra and Fred Waring"} {
		if lead := leadArtistName(name); lead != "" {
			t.Errorf("%q was split to %q; plain \"and\" must not be a joiner", name, lead)
		}
	}
}

func TestLeadArtistNameCutsAtTheEarliestJoiner(t *testing.T) {
	// " duet with " starts before the " with " inside it, so the cut must not
	// leave a stray "Duet" behind.
	if lead := leadArtistName("Toby Keith Duet with Willie Nelson"); lead != "Toby Keith" {
		t.Errorf("lead = %q, want %q", lead, "Toby Keith")
	}
	// No joiner at all.
	if lead := leadArtistName("Radiohead"); lead != "" {
		t.Errorf("lead = %q, want empty for a plain name", lead)
	}
	// A joiner-looking substring that is not a joiner, because it lacks the
	// surrounding space that makes it one.
	for _, name := range []string{"Ft. Lauderdale Sound", "Withered Hand", "Vsevolod"} {
		if lead := leadArtistName(name); lead != "" {
			t.Errorf("%q was split to %q; that is a substring, not a joiner", name, lead)
		}
	}
	// A leading joiner has nothing before it to keep.
	if lead := leadArtistName("with Someone"); lead != "" {
		t.Errorf("lead = %q, want empty when the joiner is at the start", lead)
	}
}

func TestLookupArtistNamesStaysDeduplicatedAndNonEmpty(t *testing.T) {
	names := lookupArtistNames(catalog.MusicArtist{
		Name:     "Aesop Rock with El‐P",
		SortName: "Aesop Rock with El‐P",
	})
	seen := map[string]int{}
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			t.Fatal("an empty candidate reached the lookup list")
		}
		seen[strings.ToLower(name)]++
	}
	for name, count := range seen {
		if count > 1 {
			t.Errorf("candidate %q appears %d times", name, count)
		}
	}
}

func contains(haystack []string, needle string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}
