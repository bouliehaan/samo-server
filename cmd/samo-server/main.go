package main

import (
	// The runtime image is debian-slim with no /usr/share/zoneinfo, so the tz
	// database is compiled in. Without it LoadLocation fails and every channel
	// schedule silently falls back to UTC — the bug this whole change fixes.
	_ "time/tzdata"

	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bouliehaan/samo-server/internal/api"
	"github.com/bouliehaan/samo-server/internal/artistimages"
	"github.com/bouliehaan/samo-server/internal/artistmeta"
	"github.com/bouliehaan/samo-server/internal/bookmarks"
	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/catalogstore"
	"github.com/bouliehaan/samo-server/internal/channels"
	"github.com/bouliehaan/samo-server/internal/config"
	"github.com/bouliehaan/samo-server/internal/covers"
	"github.com/bouliehaan/samo-server/internal/discovery"
	"github.com/bouliehaan/samo-server/internal/events"
	"github.com/bouliehaan/samo-server/internal/explo"
	"github.com/bouliehaan/samo-server/internal/files"
	"github.com/bouliehaan/samo-server/internal/lastfm"
	"github.com/bouliehaan/samo-server/internal/libraries"
	"github.com/bouliehaan/samo-server/internal/log"
	"github.com/bouliehaan/samo-server/internal/loudness"
	"github.com/bouliehaan/samo-server/internal/metadata"
	"github.com/bouliehaan/samo-server/internal/playback"
	"github.com/bouliehaan/samo-server/internal/playlists"
	"github.com/bouliehaan/samo-server/internal/podcastcache"
	"github.com/bouliehaan/samo-server/internal/podcaststream"
	"github.com/bouliehaan/samo-server/internal/radio"
	"github.com/bouliehaan/samo-server/internal/safego"
	"github.com/bouliehaan/samo-server/internal/samoradio"
	"github.com/bouliehaan/samo-server/internal/scanner"
	"github.com/bouliehaan/samo-server/internal/search"
	"github.com/bouliehaan/samo-server/internal/serverid"
	"github.com/bouliehaan/samo-server/internal/sources"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/internal/toolchain"
	"github.com/bouliehaan/samo-server/internal/users"
	"github.com/bouliehaan/samo-server/internal/watch"
)

const (
	// shutdownTimeout bounds the whole graceful stop: draining HTTP requests
	// plus waiting for background workers to unwind. systemd's default
	// TimeoutStopSec is 90s, so this leaves plenty of headroom before SIGKILL.
	shutdownTimeout = 20 * time.Second

	// streamDrainGrace is how long ordinary in-flight requests get to finish
	// normally before request contexts are cancelled outright. The endless
	// streaming handlers (radio, channels) never finish on their own, so
	// without that cancel a single listener would hold Shutdown open for the
	// full timeout on every restart — and install.sh restarts on every deploy.
	streamDrainGrace = 2 * time.Second

	// backgroundDrainTimeout bounds the wait for background workers after HTTP
	// has stopped. They watch the signal context and should exit promptly; this
	// only stops a wedged worker from blocking the process forever.
	backgroundDrainTimeout = 10 * time.Second
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if payload := scanner.PayloadPathFromArgs(os.Args[1:]); payload != "" {
		runScanSubprocess(ctx, payload)
		return
	}

	// Run a channel's programming forward without broadcasting it. Reads the
	// real plan and catalog, writes nothing.
	if len(os.Args) > 1 && os.Args[1] == "radio-sim" {
		os.Exit(runRadioSim(ctx, os.Args[2:]))
	}

	if len(os.Args) > 1 && os.Args[1] == "chapters-inspect" {
		os.Exit(runChaptersInspect(ctx, os.Args[2:]))
	}

	// Probe-only mode for container/systemd health checks. Must stay ahead of
	// config loading: the probe has to work even when the reason the server is
	// unhealthy is its own configuration.
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(runHealthcheck(ctx, os.Args[2:]))
	}

	cfg, err := config.LoadEnv()
	if err != nil {
		log.Fatal(err)
	}
	// Apply the verbosity dial before anything else logs. The package reads
	// SAMO_LOG_LEVEL itself at init so even earlier lines are covered; this
	// re-applies it from validated config.
	log.SetLevel(cfg.LogLevel)

	// background tracks every long-lived worker so shutdown can wait for them
	// to unwind before the deferred db.Close() pulls the pool out from under an
	// in-flight write (a scan, an explo pass, a last.fm flush). Workers are also
	// started by events — a finishing scan launches more — so it has to tolerate
	// a start racing the shutdown wait; see safego.Group.
	var background safego.Group
	bg := background.Go

	db, err := openAndMigrate(ctx, cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	log.Infof("database: %s", redactDSN(cfg.DBDSN))

	readDB, err := storage.OpenReadOnly(ctx, cfg.DBDSN)
	if err != nil {
		log.Fatal(err)
	}
	defer readDB.Close()

	tools, err := toolchain.Resolve(toolchain.Options{DataDir: cfg.DataDir})
	if err != nil {
		log.Fatal(err)
	}

	// fpcalc is optional - only the explo folder feature needs it, and its
	// absence must not block startup the way a missing ffmpeg does. Resolve it
	// unconditionally (not just when SAMO_EXPLO_DIRS is preset) so the folder
	// can be enabled later from the web UI without a restart+rebundle.
	var fpcalcPath string
	if path, err := toolchain.ResolveFpcalc(toolchain.Options{DataDir: cfg.DataDir}); err != nil {
		// Always log — the folder can be configured from the web UI (DB), so
		// gating this on SAMO_EXPLO_DIRS hid the one line that explained why
		// a UI-configured explo pipeline never ran.
		log.Infof("explo: fpcalc not available (folder feature needs it): %v", err)
	} else {
		fpcalcPath = path
		log.Infof("fpcalc: %s", path)
	}

	coverDir := filepath.Join(cfg.DataDir, "covers")
	coverService, err := covers.New(db, covers.Options{
		CoverDir:    coverDir,
		FFmpegPath:  tools.FFmpeg,
		FFprobePath: tools.FFprobe,
	})
	if err != nil {
		log.Fatal(err)
	}

	playlistService := playlists.New(db)
	scan := scanner.NewWithOptions(db, scanner.Options{
		Covers:              coverService,
		FFprobePath:         tools.FFprobe,
		FFmpegPath:          tools.FFmpeg,
		PlaylistImport:      playlistScanBridge{db: db, svc: playlistService},
		AutoImportPlaylists: cfg.AutoImportPlaylists,
		ExternalScanner:     cfg.ScannerExternal,
		UseFFprobeForScan:   cfg.ScanFFprobe,
		ChapterProvider:     chapterProviderForConfig(cfg.MetadataProviders, cfg.AudibleRegion),
	})
	libraryService := libraries.New(db, scan)
	libraryService.SetBackgroundContext(ctx)
	// Non-fatal: a configured folder that is missing or not yet mounted (a NAS
	// that comes up after us, a typo in SAMO_MUSIC_DIRS) must not stop the
	// server from booting — the web UI is how the operator would fix it.
	if err := libraryService.SyncConfigured(ctx, cfg.Libraries); err != nil {
		log.Warnf("library sync from environment failed (server continues): %v", err)
	}

	// install.sh restarts the service on every deploy, which kills any
	// in-flight scan goroutine and leaves its scan_jobs row stuck in
	// "running" forever — the dashboard then shows ghost scans the
	// operator can't cancel. Sweep those out before accepting any new
	// scan requests.
	if reconciled, err := libraryService.ReconcileOrphanScans(ctx); err != nil {
		log.Warnf("reconcile orphan scan jobs failed: %v", err)
	} else if reconciled > 0 {
		log.Infof("reconciled %d orphan scan job(s) from previous run", reconciled)
	}

	// Always refresh aggregate counts at startup. Scans normally do this at
	// the tail of every run, but rows can drift between scans — migrations
	// that move data, schema-rewriting refactors, and crashed scans all
	// leave libraries.item_count / music_artists.album_count at stale
	// values. Recomputing here means the catalog reload below sees current
	// counts even before the next scan.
	if err := scan.RefreshStats(ctx); err != nil {
		log.Warnf("startup stat refresh failed: %v", err)
	}

	catalogSeed, err := catalogstore.LoadSeedFromDB(ctx, readDB)
	if err != nil {
		log.Fatal(err)
	}

	// Radio config is a hand-editable JSON file and DB-backed station rows are
	// hand-editable through the UI. Neither is allowed to brick the box: a
	// malformed station degrades to "no radio stations" and everything else —
	// including the UI that fixes it — still comes up.
	radioConfig, err := radio.LoadConfigFile(cfg.RadioConfigPath)
	if err != nil {
		log.Warnf("radio config unusable, starting with no stations: %v", err)
		radioConfig = radio.Config{}
	}

	radioService, err := radio.NewServiceFromDB(ctx, db, radioConfig)
	if err != nil {
		log.Warnf("radio stations failed to load, starting with no stations: %v", err)
		radioService, err = radio.NewService(radio.Config{})
		if err != nil {
			log.Fatalf("radio service could not be initialised empty: %v", err)
		}
	}

	catalogService := catalog.NewService(catalogSeed)
	playbackService := playback.NewWithReadDB(db, readDB)
	metadataService := metadata.NewDefaultService(cfg.MetadataProviders, cfg.MetadataUserAgent)
	coverService.SetRemoteOptions(covers.RemoteOptions{})
	metadataApplyService := metadata.NewMetadataApplyServiceWithOptions(db, metadata.MetadataApplyOptions{
		CoverDownloader: coverService,
		Logger:          log.Printf,
	})
	podcastStreamService := podcaststream.New()
	searchService := search.New()
	searchService.Rebuild(catalogSeed)
	bookmarksService := bookmarks.New(db, readDB)
	podcastCacheService, err := podcastcache.New(db, podcastcache.Options{
		CacheDir:            filepath.Join(cfg.DataDir, "podcast-cache"),
		Enabled:             cfg.PodcastCache,
		MaxBytes:            cfg.PodcastCacheMaxBytes,
		MaxAge:              cfg.PodcastCacheMaxAge,
		MaxFileBytes:        cfg.PodcastCacheMaxFile,
		DefaultPrewarmCount: cfg.PodcastPrewarmCount,
		Stream:              podcastStreamService,
		BaseContext:         ctx,
	})
	if err != nil {
		log.Fatal(err)
	}
	filesService := files.New(db, coverService.CoverDir(), podcastCacheService.CacheDir())
	sourceService := sources.New(db, sources.Options{
		Covers:              coverService,
		PodcastCache:        podcastCacheService,
		DefaultAutoDownload: cfg.PodcastAutoDownload,
		BaseContext:         ctx,
	})
	userService := users.New(users.ServiceOptions{
		DB:             db,
		ReadDB:         readDB,
		LegacyAPIToken: cfg.APIToken,
	})
	// Bootstrap only creates an admin when env vars supply credentials. When
	// the operator leaves them empty, first-run setup is handed off to the
	// /setup wizard instead of a logged auto-generated password.
	bootstrapResult, err := userService.BootstrapWithResult(ctx, users.BootstrapInput{
		AdminUsername: cfg.BootstrapUsername,
		AdminPassword: cfg.BootstrapPassword,
	})
	if err != nil {
		log.Fatal(err)
	}
	if bootstrapResult.CreatedAdmin {
		log.Infof("created bootstrap admin user: %s", bootstrapResult.AdminUsername)
		if bootstrapResult.GeneratedPassword != "" {
			log.Infof("generated bootstrap admin password for %s: %s", bootstrapResult.AdminUsername, bootstrapResult.GeneratedPassword)
			log.Infof("set SAMO_BOOTSTRAP_PASSWORD to choose a password explicitly, then rotate this generated password after first login")
		}
	}
	if bootstrapResult.UpdatedPassword {
		log.Infof("updated bootstrap password for user: %s", bootstrapResult.AdminUsername)
	}
	if bootstrapResult.EnsuredServerToken {
		log.Infof("legacy SAMO_API_TOKEN mapped to bootstrap server user")
	}
	setupHintNeeded := false
	if !bootstrapResult.CreatedAdmin && !bootstrapResult.UpdatedPassword {
		if existingUsers, err := userService.List(ctx); err == nil {
			hasAdmin := false
			for _, user := range existingUsers {
				if user.ID == users.BootstrapUserID {
					continue
				}
				if user.Role == users.RoleAdmin {
					hasAdmin = true
					break
				}
			}
			setupHintNeeded = !hasAdmin
		}
	}

	lastfmService := lastfm.NewService(lastfm.ServiceOptions{
		DB:           db,
		APIKey:       cfg.LastFMAPIKey,
		SharedSecret: cfg.LastFMSharedSecret,
		Logger:       log.Printf,
	})
	if err := lastfmService.LoadConfig(ctx); err != nil {
		log.Warnf("last.fm config load failed: %v", err)
	}
	artistImageService := artistimages.NewService(artistimages.ServiceOptions{
		DB:      db,
		LastFM:  lastfmService,
		Covers:  coverService,
		Catalog: catalogService,
		Logger:  log.Printf,
	})
	artistImageService.SetBackgroundContext(ctx)
	artistMetaService := artistmeta.NewService(artistmeta.ServiceOptions{
		DB:      db,
		LastFM:  lastfmService,
		Catalog: catalogService,
		Logger:  log.Printf,
	})
	artistMetaService.SetBackgroundContext(ctx)
	var catalogReloadMu sync.Mutex
	reloadCatalog := func(ctx context.Context) error {
		catalogReloadMu.Lock()
		defer catalogReloadMu.Unlock()
		var seed catalog.Seed
		if err := storage.Retry(ctx, 8, func() error {
			var loadErr error
			seed, loadErr = catalogstore.LoadSeedFromDB(ctx, readDB)
			return loadErr
		}); err != nil {
			return err
		}
		catalogService.Replace(seed)
		searchService.Rebuild(seed)
		return nil
	}

	exploService := explo.NewService(explo.ServiceOptions{
		DB:             db,
		Dirs:           cfg.ExploDirs,
		AcoustIDAPIKey: cfg.AcoustIDAPIKey,
		FpcalcPath:     fpcalcPath,
		MetadataApply:  metadataApplyService,
		// Fallback identification (filename + duration-gated text search)
		// when AcoustID can't identify a file. Reuses the same search
		// providers (MusicBrainz etc.) already configured via
		// SAMO_METADATA_PROVIDERS for the manual "apply metadata" feature.
		Metadata:  metadataService,
		Playlists: playlistService,
		// The cover store verifies every downloaded cover as local bytes and
		// holds the generated placeholder tiles; without it the cover engine
		// stays off.
		Covers:        coverService,
		ReloadCatalog: reloadCatalog,
		PlaylistName:  cfg.ExploPlaylistName,
		Logger:        log.Printf,
	})
	// Overlay any admin config persisted via the web UI onto the env defaults.
	if err := exploService.LoadConfig(ctx); err != nil {
		log.Warnf("explo: config load failed: %v", err)
	}
	switch {
	case exploService.Enabled():
		log.Infof("explo: folder feature enabled")
	default:
		// Covers env AND web-UI configured folders; silent only when explo
		// was never configured at all.
		if reason := exploService.DisabledReason(ctx); reason != "" {
			log.Infof("explo: folder configured but the feature is disabled - %s", reason)
		}
	}
	// One-shot cleanup at boot: re-sync explo's hidden flags / ledger / playlist
	// to the currently-configured folder. Unconditional (not gated on Enabled)
	// so that narrowing or clearing the folder recovers Recently Added on the
	// next boot even if the key/fpcalc are now absent.
	bg("explo startup pass", func() {
		if err := exploService.ReconcileRecentlyAdded(ctx); err != nil {
			log.Warnf("explo: startup reconcile failed: %v", err)
		}
		// Prune ghosts the exporter rotated out (deleted files whose rows linger)
		// before the identify pass, so it doesn't fpcalc missing files. Unconditional
		// like the reconcile above — a no-op when the folder is unconfigured.
		if pruned, err := exploService.PruneRotatedOutFiles(ctx); err != nil {
			log.Warnf("explo: startup prune of rotated-out files failed: %v", err)
		} else if pruned > 0 {
			log.Infof("explo: startup pruned %d rotated-out file(s)", pruned)
		}
		// Identification pass at boot too — this is what retries previously
		// unmatched/errored drops (fresh releases AcoustID couldn't identify
		// yet). Without it, retries only ran when a scan happened to fire.
		// No-op when nothing is due, so it's free on ordinary boots.
		if exploService.Enabled() {
			if _, err := exploService.ProcessNewTracks(ctx); err != nil {
				log.Warnf("explo: startup identify pass failed: %v", err)
			}
		}
		// Backfill album art for any identified explo albums still missing it
		// (e.g. matched before cover support). Network-bound, so kept off the
		// reconcile's critical path.
		if err := exploService.BackfillCovers(ctx); err != nil {
			log.Warnf("explo: startup cover backfill failed: %v", err)
		}
	})

	// Periodic driver for the explo retry ladders. Identification and cover
	// retries are scheduled in SQL (front-loaded backoff per row), but until
	// this ticker existed nothing RAN between scans and restarts, so "retry
	// in 1 hour" silently meant "retry at the next scan or reboot" — the
	// main reason a weekly drop took days to fill in. Every pass is a cheap
	// no-op (two indexed SELECTs) when nothing is due.
	bg("explo periodic pass", func() {
		ticker := time.NewTicker(30 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			if pruned, err := exploService.PruneRotatedOutFiles(ctx); err != nil {
				log.Warnf("explo: periodic prune of rotated-out files failed: %v", err)
			} else if pruned > 0 {
				log.Infof("explo: periodic pass pruned %d rotated-out file(s)", pruned)
			}
			if exploService.Enabled() {
				if _, err := exploService.ProcessNewTracks(ctx); err != nil {
					log.Warnf("explo: periodic identify pass failed: %v", err)
				}
			}
			if err := exploService.BackfillCovers(ctx); err != nil {
				log.Warnf("explo: periodic cover pass failed: %v", err)
			}
		}
	})

	libraryService.OnScanComplete(func(ctx context.Context, job libraries.ScanJob, stats scanner.ScanStats) {
		if err := reloadCatalog(ctx); err != nil {
			log.Warnf("catalog reload after scan %s failed: %v", job.ID, err)
		}
		if job.Status != libraries.ScanStatusCompleted {
			return
		}
		// Artist biographies + similar artists: warm new artists, backfill the
		// long tail on a full scan. Runs in the background so scans stay fast.
		if artistMetaService.Enabled() {
			if len(stats.NewArtistIDs) > 0 {
				bg("artist meta warm after scan", func() {
					artistMetaService.FetchArtistsByIDs(ctx, stats.NewArtistIDs)
				})
			} else if job.ScanMode == libraries.ScanModeFull {
				bg("artist meta backfill after scan", func() {
					if err := artistMetaService.BackfillMissing(ctx); err != nil {
						log.Warnf("artist meta backfill after full scan failed: %v", err)
					}
				})
			}
		}
		// Explo folder enrichment: identify + playlist-route any newly
		// scanned drops. Runs in the background so scans stay fast; safe to
		// run every scan since it's a no-op once nothing new is pending.
		if exploService.Enabled() {
			bg("explo pass after scan", func() {
				// A scan just reconciled the folder; prune any files the exporter
				// rotated out before identifying so we don't fpcalc missing files.
				if pruned, err := exploService.PruneRotatedOutFiles(ctx); err != nil {
					log.Warnf("explo: prune of rotated-out files after scan %s failed: %v", job.ID, err)
				} else if pruned > 0 {
					log.Infof("explo: pruned %d rotated-out file(s) after scan %s", pruned, job.ID)
				}
				if _, err := exploService.ProcessNewTracks(ctx); err != nil {
					log.Warnf("explo: process new tracks after scan %s failed: %v", job.ID, err)
				}
				if err := exploService.BackfillCovers(ctx); err != nil {
					log.Warnf("explo: cover backfill after scan %s failed: %v", job.ID, err)
				}
			})
		}
		if !cfg.ArtistImagesOnScan || !artistImageService.Enabled() {
			return
		}
		if len(stats.NewArtistIDs) > 0 {
			artistImageService.FetchArtistsByIDs(ctx, stats.NewArtistIDs)
			return
		}
		if job.ScanMode == libraries.ScanModeFull {
			if _, err := artistImageService.StartBackfill(ctx, artistimages.BackfillModeMissing); err != nil {
				log.Warnf("artist image backfill after full scan failed: %v", err)
			}
		}
	})
	// Deliberately no audiobook chapter analysis is attached to server scans.
	// Chapter tooling remains isolated for explicit/manual use, but library scans
	// should be strict metadata/file scans and must not rewrite navigation data.
	if cfg.ScanOnStart {
		log.Infof("scanning configured libraries on startup")
		// Non-fatal: a scan that can't start (unreadable folder, a job already
		// running) is a reason to log and serve the existing catalog, not a
		// reason to refuse to boot.
		if _, err := libraryService.ScanAll(ctx, libraries.TriggerStartup, ""); err != nil {
			log.Infof("startup scan did not start (server continues): %v", err)
		}
	}
	// Where a channel's schedule is read, unless the channel names its own.
	// SAMO_TIMEZONE first, then the process zone — which in a container is
	// UTC, so anyone outside UTC has to set one for schedules to mean
	// anything.
	scheduleLocation := time.Local
	if zone := strings.TrimSpace(os.Getenv("SAMO_TIMEZONE")); zone != "" {
		if loc, err := time.LoadLocation(zone); err == nil {
			scheduleLocation = loc
		} else {
			log.Warnf("SAMO_TIMEZONE %q is not a known zone; channel schedules fall back to %s", zone, scheduleLocation)
		}
	}
	log.Infof("channel schedules are read in %s", scheduleLocation)

	// Loudness levelling, shared by the channel streamer and the samo-radio
	// queue resolver so both sides of the radio agree on how loud things are.
	// One EBU R128 measurement per file, cached; playback applies a constant
	// gain and nothing else. See internal/loudness.
	loudnessTarget, loudnessOn := envLoudnessTarget()
	var loudnessService *loudness.Service
	if loudnessOn {
		loudnessService = loudness.NewService(loudness.ServiceOptions{
			DB:         db,
			FFmpegPath: tools.FFmpeg,
			Target:     loudnessTarget,
			// Info, not debug. The channel streamer logs at debug because its
			// output is ffmpeg stderr — per-item noise. This is the opposite:
			// a handful of lines saying how far the library sweep has got and
			// what level a newly-measured item came in at. Those are the only
			// evidence levelling is working at all, and hiding them behind a
			// log level nobody runs is how you end up guessing.
			Logger:      log.StdLogger(log.LevelInfo),
			BaseContext: ctx,
		})
		log.Infof("radio loudness levelling on, target %.1f LUFS with %.1f dBTP headroom",
			loudnessTarget.LUFS, loudnessTarget.CeilingDBTP)
		// Measure the library in the background so the first airing of
		// anything is already levelled. Deliberately slow; see Backfill.
		safego.Go("loudness backfill", func() {
			loudness.Backfill{Service: loudnessService}.Run(ctx)
		})
	} else {
		log.Infof("radio loudness levelling off (SAMO_LOUDNESS_TARGET=off)")
	}

	channelsService := channels.NewService(channels.ServiceOptions{
		DB:               db,
		Catalog:          catalogService,
		Cache:            podcastCacheAdapter{service: podcastCacheService},
		InternetStations: internetStationAdapter{service: sourceService},
		Listened:         channelListenedAdapter{service: playbackService},
		DefaultLocation:  scheduleLocation,
		DefaultTalkShare: envTalkShare(),
		FFmpegPath:       tools.FFmpeg,
		// Channel ffmpeg stderr is per-item detail, not an event.
		Logger:      log.StdLogger(log.LevelDebug),
		BaseContext: ctx,
		Loudness:    loudnessPlanner(loudnessService),
	})

	// samo-radio devices: headless players on a machine with a sound card.
	// The token minter lets the service hand a device a durable Samo
	// credential without knowing anything about how accounts work.
	samoRadioService := samoradio.NewService(samoradio.ServiceOptions{
		DB:     db,
		Tokens: api.SamoRadioTokenMinter{Users: userService},
	})

	// One hub, shared by the services that report progress and the SSE
	// endpoint that fans it out. Wired here rather than inside NewServer so
	// the publishers and the subscriber are demonstrably the same hub.
	eventHub := events.NewHub()
	libraryService.SetEventHub(eventHub)
	artistImageService.SetEventHub(eventHub)

	handler := api.NewServer(api.ServerOptions{
		DB:            db,
		APIToken:      cfg.APIToken,
		Catalog:       catalogService,
		Libraries:     libraryService,
		Playback:      playbackService,
		Covers:        coverService,
		Files:         filesService,
		Metadata:      metadataService,
		MetadataApply: metadataApplyService,
		Playlists:     playlistService,
		PodcastStream: podcastStreamService,
		PodcastCache:  podcastCacheService,
		Search:        searchService,
		Bookmarks:     bookmarksService,
		Radio:         radioService,
		Sources:       sourceService,
		LastFM:        lastfmService,
		Explo:         exploService,
		ArtistImages:  artistImageService,
		Events:        eventHub,
		ArtistMeta:    artistMetaService,
		Users:         userService,
		Channels:      channelsService,
		SamoRadio:     samoRadioService,
		Loudness:      loudnessService,
		ListenAddr:    cfg.Addr,
		ReloadCatalog: reloadCatalog,
		StartedAt:     time.Now(),
		BaseContext:   ctx,
	})

	// Started regardless of whether credentials exist right now: they can be
	// saved through the UI at any time, and the poller is what eventually
	// delivers every scrobble a network outage held back.
	if cfg.LastFMPoll {
		poller := lastfm.NewPoller(lastfm.PollerOptions{
			Service: lastfmService,
			Tick:    cfg.LastFMPollTick,
			Logger:  log.Printf,
		})
		bg("last.fm queue poller", func() {
			if err := poller.Run(ctx); err != nil && err != context.Canceled {
				log.Warnf("last.fm queue poller stopped: %v", err)
			}
		})
	}

	if cfg.PodcastPoll {
		poller := sources.NewPoller(sources.PollerOptions{
			Sources:       sourceService,
			ReloadCatalog: reloadCatalog,
			Tick:          cfg.PodcastPollTick,
			Logger:        log.Printf,
		})
		bg("podcast feed poller", func() {
			if err := poller.Run(ctx); err != nil && err != context.Canceled {
				log.Warnf("podcast feed poller stopped: %v", err)
			}
		})
	}

	if cfg.InternetRadioProbe {
		probe := sources.NewProbePoller(sources.ProbePollerOptions{
			Sources: sourceService,
			Tick:    cfg.InternetRadioProbeTick,
			Logger:  log.Printf,
		})
		bg("internet radio probe poller", func() {
			if err := probe.Run(ctx); err != nil && err != context.Canceled {
				log.Warnf("internet radio probe poller stopped: %v", err)
			}
		})
	}

	if cfg.WatchLibraries {
		watcher := watch.New(watch.Options{
			DB: db,
			ScanSubpaths: func(ctx context.Context, libraryID string, subpaths []string) (libraries.ScanResult, error) {
				return libraryService.ScanFilesystem(ctx, libraryID, subpaths)
			},
			ListLibraries: func(ctx context.Context) ([]watch.LibraryRoot, error) {
				scannerLibraries, err := libraryService.ScannerLibraries(ctx)
				if err != nil {
					return nil, err
				}
				roots := make([]watch.LibraryRoot, 0, len(scannerLibraries))
				for _, library := range scannerLibraries {
					roots = append(roots, watch.LibraryRoot{ID: library.ID, Path: library.Path})
				}
				return roots, nil
			},
			ScanInProgress: libraryService.ScanInProgress,
			Debounce:       cfg.WatchDebounce,
			Logger:         log.StdLogger(log.LevelDebug),
		})
		bg("library watcher", func() {
			if err := watcher.Run(ctx); err != nil && err != context.Canceled {
				log.Warnf("library watcher stopped: %v", err)
			}
		})
	}

	listener, err := listenWithFallback(cfg.Addr, 20)
	if err != nil {
		log.Fatal(err)
	}
	actualAddr := listener.Addr().String()
	if !sameListenPort(cfg.Addr, actualAddr) {
		log.Infof("requested %s was in use; samo-server bound to %s instead", cfg.Addr, actualAddr)
	}
	log.Infof("samo-server listening on %s", actualAddr)
	if setupHintNeeded {
		log.Infof("no admin user configured; open http://localhost%s/setup in a browser to finish first-run setup", normalizedDisplayPort(actualAddr))
	}
	log.Infof("ffmpeg: %s", tools.FFmpeg)
	log.Infof("ffprobe: %s", tools.FFprobe)
	if cfg.ScanFFprobe {
		log.Infof("library scan metadata: ffprobe only (SAMO_SCAN_FFPROBE=1)")
	} else {
		log.Infof("library scan metadata: native tags + ffprobe fallback for duration/technical fields")
	}
	log.Infof("log level: %s (set SAMO_LOG_LEVEL=debug|info|warn|error)", log.Level())
	log.Infof("cover cache: %s", coverDir)
	log.Infof("radio config: %s (%d station(s))", cfg.RadioConfigPath, radioService.StationCount())
	if lastfmService.Enabled() {
		log.Infof("last.fm scrobbling: enabled")
	} else {
		log.Infof("last.fm scrobbling: disabled (set SAMO_LASTFM_API_KEY and SAMO_LASTFM_SHARED_SECRET)")
	}

	_, portStr, err := net.SplitHostPort(actualAddr)
	var serverPort int
	if err == nil {
		fmt.Sscanf(portStr, "%d", &serverPort)
		discoveryServerID, err := serverid.Ensure(ctx, db)
		if err != nil {
			log.Warnf("discovery: server identity unavailable: %v", err)
		}
		broadcaster := discovery.NewBroadcaster(serverPort, discoveryServerID)
		bg("discovery broadcaster", func() {
			if err := broadcaster.Run(ctx); err != nil && err != context.Canceled {
				log.Warnf("discovery broadcaster stopped: %v", err)
			}
		})
	}

	// Every request context descends from serveCtx, which lets shutdown be
	// two-phase (see below). It is deliberately NOT the signal context: that
	// would kill in-flight requests the instant SIGTERM lands.
	serveCtx, stopServing := context.WithCancel(context.Background())
	defer stopServing()

	srv := &http.Server{
		Handler:     handler,
		BaseContext: func(net.Listener) context.Context { return serveCtx },
		// Slowloris defence. Without it, an unauthenticated client can hold
		// connections (and their goroutines and fds) open indefinitely by
		// dribbling headers — /health, /login and /setup are all reachable
		// before any credential check.
		ReadHeaderTimeout: 10 * time.Second,
		// Reap half-open keep-alive connections from sleeping phones and
		// laptops instead of accumulating them until the fd limit.
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 1 << 20,
		// ReadTimeout and WriteTimeout stay zero on purpose. WriteTimeout would
		// cut every stream off mid-playback, and ReadTimeout applies to the
		// whole request — including the body — which interacts badly with
		// long-lived streaming handlers. Upload bodies are bounded by
		// MaxBytesReader on the three routes that accept them.
		// net/http's own connection errors are noisy and rarely actionable.
		ErrorLog: log.StdLogger(log.LevelDebug),
	}

	safego.Go("http server", func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server error: %v", err)
		}
	})

	<-ctx.Done()
	log.Infof("shutting down gracefully...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()

	// Phase two of the drain. Ordinary requests finish within the grace window
	// on their own; the endless streaming handlers (radio, channels) never do,
	// so cancelling serveCtx is what actually lets Shutdown return. Otherwise a
	// single listener stalls every restart for the full shutdown timeout.
	stopServingTimer := time.AfterFunc(streamDrainGrace, stopServing)
	defer stopServingTimer.Stop()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Warnf("http server shutdown error: %v", err)
	}

	// Reap the per-channel transcoders. Their contexts are already cancelled
	// with the signal context; this waits for the processes to actually die so
	// a restart can't leave orphans behind.
	channelsService.Close(shutdownCtx)

	// Background workers watch the signal context and are already unwinding.
	// Waiting for them here is what keeps the deferred db.Close() from yanking
	// the pool out from under an in-flight scan or explo write.
	background.Wait(backgroundDrainTimeout)

	log.Infof("samo-server stopped")
}

// podcastCacheAdapter satisfies channels.EpisodeCacheLookup by forwarding
// to the real podcastcache.Service. The channels package can't depend on
// podcastcache directly without an import cycle, so the adapter lives
// here in main.
type podcastCacheAdapter struct {
	service *podcastcache.Service
}

func (a podcastCacheAdapter) Lookup(ctx context.Context, episodeID, enclosureURL string) (channels.LocalCachedFile, bool, error) {
	if a.service == nil {
		return channels.LocalCachedFile{}, false, nil
	}
	cached, ok, err := a.service.Lookup(ctx, episodeID, enclosureURL)
	if err != nil || !ok {
		return channels.LocalCachedFile{}, ok, err
	}
	return channels.LocalCachedFile{
		Path:        cached.Path,
		ContentType: cached.ContentType,
		SizeBytes:   cached.SizeBytes,
	}, true, nil
}

// internetStationAdapter exposes sources.Service.GetInternetRadioStation
// through the channels.InternetStationLookup interface. Same pattern as
// podcastCacheAdapter — keeps internal/channels free of a sources
// import and lets channels.InternetStation stay a minimal struct.
// envTalkShare reads SAMO_TALK_SHARE as a percentage (0-100). Zero leaves the
// package default of 75% spoken word.
func envTalkShare() float64 {
	raw := strings.TrimSpace(os.Getenv("SAMO_TALK_SHARE"))
	if raw == "" {
		return 0
	}
	percent, err := strconv.ParseFloat(raw, 64)
	if err != nil || percent <= 0 || percent >= 100 {
		log.Warnf("SAMO_TALK_SHARE %q is not a percentage between 1 and 99; using the default", raw)
		return 0
	}
	return percent / 100
}

// envLoudnessTarget reads SAMO_LOUDNESS_TARGET, the level the radio aims every
// item at, in LUFS. "off" disables levelling entirely and goes back to playing
// everything at whatever level it was mastered at.
//
// The default of -16 LUFS is the streaming and podcast convention. Lower is
// quieter and leaves more headroom for dynamic material; higher is louder and
// makes the peak limiter work harder, which is the one part of this that can
// actually be heard. Outside -30..-8 there is no sensible reading, so a value
// out of range is refused rather than obeyed.
func envLoudnessTarget() (loudness.Target, bool) {
	target := loudness.DefaultTarget
	raw := strings.TrimSpace(os.Getenv("SAMO_LOUDNESS_TARGET"))
	if strings.EqualFold(raw, "off") || strings.EqualFold(raw, "false") {
		return target, false
	}
	if raw == "" {
		return target, true
	}
	lufs, err := strconv.ParseFloat(strings.TrimSuffix(strings.ToUpper(raw), " LUFS"), 64)
	if err != nil || lufs < -30 || lufs > -8 {
		log.Warnf("SAMO_LOUDNESS_TARGET %q is not a level between -30 and -8 LUFS; using %.0f",
			raw, target.LUFS)
		return target, true
	}
	target.LUFS = lufs
	return target, true
}

// loudnessPlanner adapts the service to the interface internal/channels wants,
// returning a genuinely nil interface when levelling is off. Assigning a typed
// nil pointer straight into an interface field produces a non-nil interface
// holding nothing, and every `if x != nil` guard downstream then lies.
func loudnessPlanner(service *loudness.Service) channels.LoudnessPlanner {
	if service == nil {
		return nil
	}
	return service
}

// channelListenedAdapter lets the channel scheduler ask "has anyone here
// already heard this episode" without importing the playback package's shape.
type channelListenedAdapter struct {
	service *playback.Service
}

func (a channelListenedAdapter) EpisodeProgress(
	ctx context.Context,
	episodeIDs []string,
) (map[string]channels.EpisodeProgress, error) {
	if a.service == nil {
		return nil, nil
	}
	states, err := a.service.AnyListenerByIDs(ctx, playback.TargetPodcastEpisode, episodeIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[string]channels.EpisodeProgress, len(states))
	for id, state := range states {
		out[id] = channels.EpisodeProgress{
			Completed:       state.Completed,
			ProgressSeconds: state.ProgressSeconds,
		}
	}
	return out, nil
}

type internetStationAdapter struct {
	service *sources.Service
}

func (a internetStationAdapter) GetInternetRadioStation(ctx context.Context, stationID string) (channels.InternetStation, error) {
	if a.service == nil {
		return channels.InternetStation{}, fmt.Errorf("sources service unavailable")
	}
	station, err := a.service.GetInternetRadioStation(ctx, stationID)
	if err != nil {
		return channels.InternetStation{}, err
	}
	return channels.InternetStation{
		ID:        station.ID,
		Name:      station.Name,
		StreamURL: station.StreamURL,
	}, nil
}
