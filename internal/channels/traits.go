package channels

import "strings"

// Traits are the media characteristics the engine is allowed to branch on.
//
// They exist so that no scheduling rule ever has to ask whether a category is
// called "talk". Categories are the station owner's vocabulary — a station may
// have comedy, audiobook, old-time-radio, sports, whatever — and the moment the
// engine compares one to a literal, that vocabulary stops being the owner's.
// Every behaviour that genuinely depends on WHAT KIND OF MEDIA something is
// reads a trait instead, and every trait below is something the engine actually
// does something different about.
type Traits struct {
	// SupportsFreshness means items carry a publication date, so a new one is
	// an event rather than just another item. Podcasts have this; a folder of
	// station idents does not.
	SupportsFreshness bool
	// Shuffled means this source is a bag, not a strand: its items are
	// interchangeable, they come round in a random order, and none of them
	// comes round twice until the rest have had their turn. What separates
	// them is the length of their own queue rather than a configured window,
	// because "do not repeat a song" means "not until the playlist is done",
	// not "not for eight hours".
	Shuffled bool
	// HasCreator means items carry a person-level attribution worth keeping
	// APART — a host presenting twice inside an hour.
	//
	// It used to cover recording artists too, on the reading that "two shows
	// with the same host" and "two songs by the same artist" are the same
	// mistake. Only the first one is a mistake. The second is a record
	// collection having a shape, and spacing it means an artist's share of the
	// hour can never match their share of the shelf — any gap at all forces
	// somebody else in between, which caps them at p/(1+p) however much of the
	// collection is theirs. So playlists no longer set this, and a station that
	// wants the old behaviour asks for it per source.
	HasCreator bool
	// SharedCreator means every item from this source carries the SAME
	// attribution — it is one show, not a container of many.
	//
	// This is what tells "another episode of the podcast I just played" apart
	// from "another track off the playlist I just played". The first is the
	// station repeating itself and wants keeping apart; the second is what a
	// music set IS. Without the distinction, one separation rule either lets a
	// show run back to back or makes it impossible to play two songs in a row.
	SharedCreator bool
	// Continuous means the item has no natural end. Nothing downstream would
	// ever move off it, so a play window has to be imposed on the way in.
	Continuous bool
	// Interstitial means this is a separator rather than programming: a spot,
	// an ident, a sweeper. It is never chosen as "what's on next" and its
	// airtime is kept out of the format balance, because a stopset is not
	// something the station is programming toward.
	Interstitial bool
}

// TraitsFor works out what a source is, from its kind and role, and lets the
// source override any of it.
//
// Derived first, because almost nothing needs to be configured: a podcast
// subscription has dated episodes and a host, an internet station never ends.
// The overrides exist for the genuinely ambiguous cases — a file pool could be
// a folder of oldies (creators) or a folder of jingles (interstitial), and
// nothing about "file-pool" reveals which.
func TraitsFor(src Source) Traits {
	traits := Traits{}
	switch src.Kind {
	case SourcePodcastSubscription:
		traits.SupportsFreshness = true
		traits.HasCreator = true
		traits.SharedCreator = true
	case SourceMusicPlaylist:
		// A playlist is not programmed, it is shuffled.
		//
		// Artist separation used to be on here, on the reasoning that "two shows
		// with the same host" and "two songs by the same artist" are the same
		// mistake. They are not. A host presenting twice in an hour is the
		// station repeating itself; two songs by one artist is a record
		// collection having a shape. Spacing the second is not a refinement of
		// the running order, it is a correction applied to the one statement of
		// taste the operator actually made — and the arithmetic to decide how
		// much correction each artist deserves runs to a page, all of it
		// undoing a decision somebody already made by putting the songs in the
		// playlist.
		//
		// So a playlist shuffles. Every track once before any of them twice,
		// and whatever proportions the shelf has are the proportions you hear.
		// A station that genuinely wants its artists spaced can say so on the
		// source — `"traits": {"hasCreator": true}` — and get the old rule back.
		traits.HasCreator = false
		traits.Shuffled = true
	case SourceLiveStream, SourceInternetStation:
		traits.Continuous = true
		traits.SharedCreator = true
	}
	if src.Role == RoleCommercial {
		traits.Interstitial = true
		// A spot pool is separator inventory; whatever attribution its files
		// carry is not something to program around.
		traits.HasCreator = false
	}
	for key, value := range boolOverrides(src.Config) {
		switch key {
		case "shuffled":
			traits.Shuffled = value
		case "supportsFreshness":
			traits.SupportsFreshness = value
		case "hasCreator":
			traits.HasCreator = value
		case "sharedCreator":
			traits.SharedCreator = value
		case "continuous":
			traits.Continuous = value
		case "interstitial":
			traits.Interstitial = value
		}
	}
	return traits
}

// boolOverrides reads the `traits` object off a source's config, if present.
func boolOverrides(cfg map[string]any) map[string]bool {
	out := map[string]bool{}
	if cfg == nil {
		return out
	}
	raw, ok := cfg["traits"].(map[string]any)
	if !ok {
		return out
	}
	for key, value := range raw {
		if flag, ok := value.(bool); ok {
			out[key] = flag
		}
	}
	return out
}

// CreatorOf is the person-level attribution used for separation.
//
// Falls back to the source label on purpose, so the common case needs no
// configuration at all: a show with no shared host separates exactly as its
// source does, and only genuinely shared-host shows — two podcasts, one
// presenter — need the field filled in. Track-level artists override this per
// item during enumeration.
func CreatorOf(src Source) string {
	if creator := strings.TrimSpace(stringFromConfig(src.Config, "creator")); creator != "" {
		return creator
	}
	return strings.TrimSpace(src.Label)
}

// FamilyOf groups sources that share a producer or network, for stations that
// want a coarser separation than creator. Empty means "no family", which is not
// the same as sharing one — an empty family never conflicts with anything.
func FamilyOf(src Source) string {
	return strings.TrimSpace(stringFromConfig(src.Config, "family"))
}

// ShowOf is the identity of the PROGRAMME behind a source, which is not the
// same thing as the source row.
//
// One show routinely arrives as two sources: the episodes sitting on disk and
// the same show's RSS feed, added separately so both are reachable. Every rule
// about how often a show may come round — rationing a six-hour epic, resting a
// host — has to key on the show. Keyed on the source, airing it from the feed
// leaves the on-disk copy completely unrested, and a rule that reads like a
// weekly limit turns into no limit at all.
//
// The content id comes first because it is an exact identity rather than a
// judgement: two sources pointing at the same podcast ARE the same show, no
// matter what either row happens to be labelled. Labels are frequently empty,
// which is precisely when a fallback to creator would silently stop grouping.
func ShowOf(src Source) string {
	for _, key := range []string{"podcastId", "stationId", "playlistId"} {
		if id := strings.TrimSpace(stringFromConfig(src.Config, key)); id != "" {
			return key + ":" + id
		}
	}
	if family := FamilyOf(src); family != "" {
		return "family:" + family
	}
	if creator := CreatorOf(src); creator != "" {
		return "creator:" + creator
	}
	return "source:" + src.ID
}
