package api

import (
	"io/fs"

	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// This test is the enforcement half of the catalog write contract. The helper
// in catalog_commit.go makes the right thing easy; this makes the wrong thing
// fail the build.
//
// It exists because the contract had already been written down, understood, and
// implemented correctly once — for playlists — and fifteen other mutations
// still shipped without it. Documentation did not carry the fix across, and
// neither did the reviewer who wrote the correct version. A test does.
//
// If this fails on a route you just added, you have two honest options: call
// s.commitCatalog (see catalog_commit.go), or add the handler to
// nonCatalogMutations below with a reason. Do not delete the assertion.

// nonCatalogMutations are mutating routes that genuinely change nothing any
// client displays from the catalog, so they owe no projection update and no
// notification.
//
// Every entry is a decision, not an exemption granted for convenience. If you
// are adding one because the test failed and you are not sure, it is a catalog
// mutation — the failure mode of guessing wrong is silent stale data on every
// other device, which nobody will report as a bug for weeks.
var nonCatalogMutations = map[string]string{
	// Per-user playback state. Never part of the shared catalog projection, and
	// the clients own their own copy.
	"patchPlayback":     "per-user playback position",
	"putPlayback":       "per-user playback position",
	"postScrobbleEvent": "per-user listen history",

	// Credentials and account plumbing.
	"createUser":               "account, not catalog",
	"updateUser":               "account, not catalog",
	"deleteUser":               "account, not catalog",
	"createUserToken":          "credential, not catalog",
	"revokeUserToken":          "credential, not catalog",
	"createSubsonicCredential": "credential, not catalog",
	"deleteSubsonicCredential": "credential, not catalog",
	"issueStreamToken":         "ephemeral credential",
	"loginUser":                "session, not catalog",
	"createSetupAdmin":         "first-run account creation",
	"completeSetup":            "first-run marker",

	// Scrobbler links. These change the server's outbound integrations, not
	// anything a client renders from the catalog.
	"beginLastFMAuth":        "external account link",
	"completeLastFMAuth":     "external account link",
	"clearLastFMConfig":      "external account link",
	"disconnectLastFM":       "external account link",
	"flushLastFMQueue":       "external queue drain",
	"connectListenBrainz":    "external account link",
	"disconnectListenBrainz": "external account link",
	"flushListenBrainzQueue": "external queue drain",

	// samo-radio devices are playback endpoints, not catalog rows.
	"createSamoRadioDevice":         "device registry",
	"updateSamoRadioDevice":         "device registry",
	"deleteSamoRadioDevice":         "device registry",
	"pairSamoRadioDevice":           "device registry",
	"playToSamoRadioDevice":         "device transport",
	"seekSamoRadioDevice":           "device transport",
	"setSamoRadioDeviceVolume":      "device transport",
	"updateSamoRadioDeviceSettings": "device settings",

	// Scan lifecycle publishes its own scan-job events, which the clients
	// already subscribe to; the completion hook reloads the projection.
	"runSetupScan":     "scan lifecycle, publishes scan-job events",
	"cancelScanJob":    "scan lifecycle",
	"cancelActiveScan": "scan lifecycle",

	// Caches and derived artefacts, invisible to the catalog projection.
	"cachePodcastEpisode":       "download cache",
	"deletePodcastEpisodeCache": "download cache",
	"clearPodcastCache":         "download cache",
	"cancelArtistImageBackfill": "publishes artist-images events",

	// Bookmarks are per-user annotations on an audiobook, not catalog rows.
	"createAudiobookBookmark": "per-user annotation",
	"updateBookmark":          "per-user annotation",
	"deleteBookmark":          "per-user annotation",

	// Explo configuration; the keep path itself does commit.
	"clearExploConfig":   "server configuration",
	"putExploConfig":     "server configuration",
	"postExploReprocess": "re-runs the scan, which reloads",

	// --- domains the catalog projection does not carry -------------------
	//
	// catalogState (catalog/service.go) holds music, audiobooks, podcasts and
	// their images. Collections, channels and radio stations are not in it, and
	// neither client mirrors them: the desktop's invalidateLibraryQueries covers
	// albums/artists/songs only, and the Android mirror has no table for them.
	//
	// So these deliberately do NOT commit. Publishing for them would be worse
	// than silence — Android ignores the scope and answers any catalog-changed
	// event with a full re-sync, so a collection rename would drag the entire
	// library down the phone for a row the phone does not store.
	//
	// The real fix for these is client-side first: mirror them, then give them a
	// scope here. Until then, notifying is a pessimisation, not a fix.
	"createCollection": "not in catalogState; no client mirrors collections",
	"updateCollection": "not in catalogState; no client mirrors collections",
	"deleteCollection": "not in catalogState; no client mirrors collections",

	"createInternetRadioStation": "not in catalogState; fetched live",
	"updateInternetRadioStation": "not in catalogState; fetched live",
	"deleteInternetRadioStation": "not in catalogState; fetched live",
	"uploadInternetRadioCover":   "not in catalogState; fetched live",
	"probeInternetRadioStation":  "probe status, fetched live",
	"runInternetRadioProbeCycle": "probe status, fetched live",

	"createRadioStation":     "programmed radio; not in catalogState",
	"updateRadioStation":     "programmed radio; not in catalogState",
	"deleteRadioStation":     "programmed radio; not in catalogState",
	"addRadioStationItem":    "programmed radio; not in catalogState",
	"deleteRadioStationItem": "programmed radio; not in catalogState",

	"createChannel":             "channels are their own domain; not in catalogState",
	"updateChannel":             "channels are their own domain; not in catalogState",
	"deleteChannel":             "channels are their own domain; not in catalogState",
	"createChannelSource":       "channels are their own domain; not in catalogState",
	"updateChannelSource":       "channels are their own domain; not in catalogState",
	"deleteChannelSource":       "channels are their own domain; not in catalogState",
	"createChannelScheduleRule": "channels are their own domain; not in catalogState",
	"deleteChannelScheduleRule": "channels are their own domain; not in catalogState",
	"putChannelPlan":            "channels are their own domain; not in catalogState",
	"deleteChannelPlan":         "channels are their own domain; not in catalogState",
	"uploadChannelCover":        "channels are their own domain; not in catalogState",
	"deleteChannelCover":        "channels are their own domain; not in catalogState",
	"clearChannelSkips":         "channels are their own domain; not in catalogState",
	"skipChannel":               "channel transport",
	"previousChannel":           "channel transport",
	"samoRadioDeviceCommand":    "device transport",

	// --- scans ------------------------------------------------------------
	//
	// A scan publishes scan-job events of its own throughout, and its
	// OnScanComplete hook reloads the projection. Committing here as well would
	// notify before the scan has found anything.
	"scanLibrary":              "publishes scan-job events; OnScanComplete reloads",
	"scanAllLibraries":         "publishes scan-job events; OnScanComplete reloads",
	"startArtistImageBackfill": "publishes artist-images events",

	// --- settings that change no row a client renders ---------------------
	"updateLibrary":         "library settings; the rows it holds are unchanged",
	"createLibrary":         "empty until it is scanned, and the scan notifies",
	"createSetupLibrary":    "first-run; the setup scan notifies",
	"updatePodcastFeed":     "poll settings, not feed content",
	"setPodcastCacheLimit":  "download cache sizing",
	"setPodcastPrewarm":     "download cache sizing",
	"setPodcastShowPrewarm": "download cache sizing",
	"updateExploConfig":     "server configuration",
	"updateLastFMConfig":    "external account link",
	"updateCurrentUser":     "account, not catalog",

	// Previews compute a result and write nothing.
	"previewMetadataApply": "read-only preview",
	"channelPreviewNext":   "read-only preview",
}

// catalogCommitCalls are the functions that satisfy the contract.
var catalogCommitCalls = map[string]bool{
	"commitCatalog":         true,
	"commitPlaylist":        true,
	"commitPlaylistRemoval": true,
}

func TestEveryMutatingRouteCommitsTheCatalog(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(info fs.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse package: %v", err)
	}
	pkg, ok := pkgs["api"]
	if !ok {
		t.Fatal(`package "api" not found in .`)
	}

	methods := serverMethods(pkg)
	handlers := mutatingRouteHandlers(pkg)
	if len(handlers) == 0 {
		t.Fatal("found no mutating routes; the route-table scan has stopped working")
	}

	var offenders []string
	for _, name := range handlers {
		if _, allowed := nonCatalogMutations[name]; allowed {
			continue
		}
		decl, ok := methods[name]
		if !ok {
			// Registered through something this scan cannot resolve. Not a
			// reason to pass silently.
			offenders = append(offenders, name+" (handler body not found)")
			continue
		}
		if !callsCommit(decl, methods, 3) {
			offenders = append(offenders, name)
		}
	}
	sort.Strings(offenders)

	if len(offenders) > 0 {
		t.Fatalf(`these mutating routes never commit a catalog change:

    %s

Each one either changes something a client displays — in which case call
s.commitCatalog (catalog_commit.go), which updates the projection and notifies
every connected client as one step — or it does not, in which case add it to
nonCatalogMutations in this file with a one-line reason.

Skipping the notification does not fail anything: this server stays correct and
every OTHER device silently serves stale data until it is restarted.`,
			strings.Join(offenders, "\n    "))
	}
}

// TestNothingPublishesCatalogChangesDirectly keeps commitCatalog the only door.
//
// publishCatalogChange on its own is the half of the contract that is easy to
// call and impossible to notice missing its partner. Everything outside
// catalog_commit.go goes through the commit helpers.
func TestNothingPublishesCatalogChangesDirectly(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(info fs.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse package: %v", err)
	}

	var offenders []string
	for name, file := range pkgs["api"].Files {
		if strings.HasSuffix(name, "catalog_commit.go") {
			continue
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if selectorName(call.Fun) == "publishCatalogChange" {
				offenders = append(offenders,
					name+":"+strconv.Itoa(fset.Position(call.Pos()).Line))
			}
			return true
		})
	}
	sort.Strings(offenders)

	if len(offenders) > 0 {
		t.Fatalf(`publishCatalogChange is called directly at:

    %s

Call s.commitCatalog instead. Publishing without updating the projection first
tells every client to refetch a change this server cannot serve yet, and
updating without publishing leaves every other device stale.`,
			strings.Join(offenders, "\n    "))
	}
}

// --- AST helpers ---------------------------------------------------------

// serverMethods indexes every method on *Server by name.
func serverMethods(pkg *ast.Package) map[string]*ast.FuncDecl {
	methods := map[string]*ast.FuncDecl{}
	for _, file := range pkg.Files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || len(fn.Recv.List) == 0 {
				continue
			}
			star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
			if !ok {
				continue
			}
			if ident, ok := star.X.(*ast.Ident); ok && ident.Name == "Server" {
				methods[fn.Name.Name] = fn
			}
		}
	}
	return methods
}

// mutatingRouteHandlers reads the route table and returns the handler name for
// every POST/PUT/PATCH/DELETE route.
func mutatingRouteHandlers(pkg *ast.Package) []string {
	seen := map[string]bool{}
	for _, file := range pkg.Files {
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) < 2 {
				return true
			}
			switch selectorName(call.Fun) {
			case "handleAPI", "HandleFunc", "Handle":
			default:
				return true
			}
			pattern, ok := stringLit(call.Args[0])
			if !ok || !isMutatingPattern(pattern) {
				return true
			}
			if name := handlerName(call.Args[1]); name != "" {
				seen[name] = true
			}
			return true
		})
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func isMutatingPattern(pattern string) bool {
	for _, method := range []string{"POST ", "PUT ", "PATCH ", "DELETE "} {
		if strings.HasPrefix(pattern, method) {
			return true
		}
	}
	return false
}

// handlerName resolves the handler argument, which is either `s.foo` or a
// factory call `s.foo(arg)` that returns a handler.
func handlerName(expr ast.Expr) string {
	switch node := expr.(type) {
	case *ast.SelectorExpr:
		return node.Sel.Name
	case *ast.CallExpr:
		// s.requireUser(s.foo) wraps; look inside for the real handler.
		if name := selectorName(node.Fun); name == "requireUser" || name == "requireAPIAuth" {
			if len(node.Args) > 0 {
				return handlerName(node.Args[0])
			}
		}
		return selectorName(node.Fun)
	}
	return ""
}

// callsCommit reports whether fn, or a *Server method it calls within depth
// hops, satisfies the contract.
func callsCommit(fn *ast.FuncDecl, methods map[string]*ast.FuncDecl, depth int) bool {
	if fn == nil || depth <= 0 {
		return false
	}
	found := false
	ast.Inspect(fn, func(node ast.Node) bool {
		if found {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := selectorName(call.Fun)
		if catalogCommitCalls[name] {
			found = true
			return false
		}
		if next, ok := methods[name]; ok && next != fn {
			if callsCommit(next, methods, depth-1) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

func selectorName(expr ast.Expr) string {
	switch node := expr.(type) {
	case *ast.SelectorExpr:
		return node.Sel.Name
	case *ast.Ident:
		return node.Name
	}
	return ""
}

func stringLit(expr ast.Expr) (string, bool) {
	switch node := expr.(type) {
	case *ast.BasicLit:
		if node.Kind != token.STRING {
			return "", false
		}
		value, err := strconv.Unquote(node.Value)
		return value, err == nil
	case *ast.BinaryExpr:
		// Patterns built by concatenation: "POST /x/" + action.
		if left, ok := stringLit(node.X); ok {
			return left, true
		}
	}
	return "", false
}
