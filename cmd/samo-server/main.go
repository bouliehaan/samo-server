package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/bouliehaan/samo-server/internal/api"
	"github.com/bouliehaan/samo-server/internal/artistimages"
	"github.com/bouliehaan/samo-server/internal/artistmeta"
	"github.com/bouliehaan/samo-server/internal/bookmarks"
	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/channels"
	"github.com/bouliehaan/samo-server/internal/config"
	"github.com/bouliehaan/samo-server/internal/covers"
	"github.com/bouliehaan/samo-server/internal/discovery"
	"github.com/bouliehaan/samo-server/internal/explo"
	"github.com/bouliehaan/samo-server/internal/files"
	"github.com/bouliehaan/samo-server/internal/lastfm"
	"github.com/bouliehaan/samo-server/internal/libraries"
	"github.com/bouliehaan/samo-server/internal/metadata"
	"github.com/bouliehaan/samo-server/internal/playback"
	"github.com/bouliehaan/samo-server/internal/playlists"
	"github.com/bouliehaan/samo-server/internal/podcastcache"
	"github.com/bouliehaan/samo-server/internal/podcaststream"
	"github.com/bouliehaan/samo-server/internal/radio"
	"github.com/bouliehaan/samo-server/internal/scanner"
	"github.com/bouliehaan/samo-server/internal/search"
	"github.com/bouliehaan/samo-server/internal/sources"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/internal/toolchain"
	"github.com/bouliehaan/samo-server/internal/users"
	"github.com/bouliehaan/samo-server/internal/watch"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if payload := scanner.PayloadPathFromArgs(os.Args[1:]); payload != "" {
		runScanSubprocess(ctx, payload)
		return
	}

	if len(os.Args) > 1 && os.Args[1] == "chapters-inspect" {
		os.Exit(runChaptersInspect(ctx, os.Args[2:]))
	}

	cfg, err := config.LoadEnv()
	if err != nil {
		log.Fatal(err)
	}

	db, err := openAndMigrate(ctx, cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	log.Printf("database: %s", redactDSN(cfg.DBDSN))

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
		log.Printf("explo: fpcalc not available (folder feature needs it): %v", err)
	} else {
		fpcalcPath = path
		log.Printf("fpcalc: %s", path)
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
	if err := libraryService.SyncConfigured(ctx, cfg.Libraries); err != nil {
		log.Fatal(err)
	}

	// install.sh restarts the service on every deploy, which kills any
	// in-flight scan goroutine and leaves its scan_jobs row stuck in
	// "running" forever — the dashboard then shows ghost scans the
	// operator can't cancel. Sweep those out before accepting any new
	// scan requests.
	if reconciled, err := libraryService.ReconcileOrphanScans(ctx); err != nil {
		log.Printf("reconcile orphan scan jobs failed: %v", err)
	} else if reconciled > 0 {
		log.Printf("reconciled %d orphan scan job(s) from previous run", reconciled)
	}

	// Always refresh aggregate counts at startup. Scans normally do this at
	// the tail of every run, but rows can drift between scans — migrations
	// that move data, schema-rewriting refactors, and crashed scans all
	// leave libraries.item_count / music_artists.album_count at stale
	// values. Recomputing here means the catalog reload below sees current
	// counts even before the next scan.
	if err := scan.RefreshStats(ctx); err != nil {
		log.Printf("startup stat refresh failed: %v", err)
	}

	catalogSeed, err := catalog.LoadSeedFromDB(ctx, readDB)
	if err != nil {
		log.Fatal(err)
	}

	radioConfig, err := radio.LoadConfigFile(cfg.RadioConfigPath)
	if err != nil {
		log.Fatal(err)
	}

	radioService, err := radio.NewServiceFromDB(ctx, db, radioConfig)
	if err != nil {
		log.Fatal(err)
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
	})
	if err != nil {
		log.Fatal(err)
	}
	filesService := files.New(db, coverService.CoverDir(), podcastCacheService.CacheDir())
	sourceService := sources.New(db, sources.Options{
		Covers:              coverService,
		PodcastCache:        podcastCacheService,
		DefaultAutoDownload: cfg.PodcastAutoDownload,
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
		log.Printf("created bootstrap admin user: %s", bootstrapResult.AdminUsername)
		if bootstrapResult.GeneratedPassword != "" {
			log.Printf("generated bootstrap admin password for %s: %s", bootstrapResult.AdminUsername, bootstrapResult.GeneratedPassword)
			log.Printf("set SAMO_BOOTSTRAP_PASSWORD to choose a password explicitly, then rotate this generated password after first login")
		}
	}
	if bootstrapResult.UpdatedPassword {
		log.Printf("updated bootstrap password for user: %s", bootstrapResult.AdminUsername)
	}
	if bootstrapResult.EnsuredServerToken {
		log.Printf("legacy SAMO_API_TOKEN mapped to bootstrap server user")
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
		log.Printf("last.fm config load failed: %v", err)
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
			seed, loadErr = catalog.LoadSeedFromDB(ctx, readDB)
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
		log.Printf("explo: config load failed: %v", err)
	}
	switch {
	case exploService.Enabled():
		log.Printf("explo: folder feature enabled")
	default:
		// Covers env AND web-UI configured folders; silent only when explo
		// was never configured at all.
		if reason := exploService.DisabledReason(ctx); reason != "" {
			log.Printf("explo: folder configured but the feature is disabled - %s", reason)
		}
	}
	// One-shot cleanup at boot: re-sync explo's hidden flags / ledger / playlist
	// to the currently-configured folder. Unconditional (not gated on Enabled)
	// so that narrowing or clearing the folder recovers Recently Added on the
	// next boot even if the key/fpcalc are now absent.
	go func() {
		if err := exploService.ReconcileRecentlyAdded(ctx); err != nil {
			log.Printf("explo: startup reconcile failed: %v", err)
		}
		// Prune ghosts the exporter rotated out (deleted files whose rows linger)
		// before the identify pass, so it doesn't fpcalc missing files. Unconditional
		// like the reconcile above — a no-op when the folder is unconfigured.
		if pruned, err := exploService.PruneRotatedOutFiles(ctx); err != nil {
			log.Printf("explo: startup prune of rotated-out files failed: %v", err)
		} else if pruned > 0 {
			log.Printf("explo: startup pruned %d rotated-out file(s)", pruned)
		}
		// Identification pass at boot too — this is what retries previously
		// unmatched/errored drops (fresh releases AcoustID couldn't identify
		// yet). Without it, retries only ran when a scan happened to fire.
		// No-op when nothing is due, so it's free on ordinary boots.
		if exploService.Enabled() {
			if _, err := exploService.ProcessNewTracks(ctx); err != nil {
				log.Printf("explo: startup identify pass failed: %v", err)
			}
		}
		// Backfill album art for any identified explo albums still missing it
		// (e.g. matched before cover support). Network-bound, so kept off the
		// reconcile's critical path.
		if err := exploService.BackfillCovers(ctx); err != nil {
			log.Printf("explo: startup cover backfill failed: %v", err)
		}
	}()

	// Periodic driver for the explo retry ladders. Identification and cover
	// retries are scheduled in SQL (front-loaded backoff per row), but until
	// this ticker existed nothing RAN between scans and restarts, so "retry
	// in 1 hour" silently meant "retry at the next scan or reboot" — the
	// main reason a weekly drop took days to fill in. Every pass is a cheap
	// no-op (two indexed SELECTs) when nothing is due.
	go func() {
		ticker := time.NewTicker(30 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			if pruned, err := exploService.PruneRotatedOutFiles(ctx); err != nil {
				log.Printf("explo: periodic prune of rotated-out files failed: %v", err)
			} else if pruned > 0 {
				log.Printf("explo: periodic pass pruned %d rotated-out file(s)", pruned)
			}
			if exploService.Enabled() {
				if _, err := exploService.ProcessNewTracks(ctx); err != nil {
					log.Printf("explo: periodic identify pass failed: %v", err)
				}
			}
			if err := exploService.BackfillCovers(ctx); err != nil {
				log.Printf("explo: periodic cover pass failed: %v", err)
			}
		}
	}()

	libraryService.OnScanComplete(func(ctx context.Context, job libraries.ScanJob, stats scanner.ScanStats) {
		if err := reloadCatalog(ctx); err != nil {
			log.Printf("catalog reload after scan %s failed: %v", job.ID, err)
		}
		if job.Status != libraries.ScanStatusCompleted {
			return
		}
		// Artist biographies + similar artists: warm new artists, backfill the
		// long tail on a full scan. Runs in the background so scans stay fast.
		if artistMetaService.Enabled() {
			if len(stats.NewArtistIDs) > 0 {
				go artistMetaService.FetchArtistsByIDs(ctx, stats.NewArtistIDs)
			} else if job.ScanMode == libraries.ScanModeFull {
				go func() {
					if err := artistMetaService.BackfillMissing(ctx); err != nil {
						log.Printf("artist meta backfill after full scan failed: %v", err)
					}
				}()
			}
		}
		// Explo folder enrichment: identify + playlist-route any newly
		// scanned drops. Runs in the background so scans stay fast; safe to
		// run every scan since it's a no-op once nothing new is pending.
		if exploService.Enabled() {
			go func() {
				// A scan just reconciled the folder; prune any files the exporter
				// rotated out before identifying so we don't fpcalc missing files.
				if pruned, err := exploService.PruneRotatedOutFiles(ctx); err != nil {
					log.Printf("explo: prune of rotated-out files after scan %s failed: %v", job.ID, err)
				} else if pruned > 0 {
					log.Printf("explo: pruned %d rotated-out file(s) after scan %s", pruned, job.ID)
				}
				if _, err := exploService.ProcessNewTracks(ctx); err != nil {
					log.Printf("explo: process new tracks after scan %s failed: %v", job.ID, err)
				}
				if err := exploService.BackfillCovers(ctx); err != nil {
					log.Printf("explo: cover backfill after scan %s failed: %v", job.ID, err)
				}
			}()
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
				log.Printf("artist image backfill after full scan failed: %v", err)
			}
		}
	})
	// Deliberately no audiobook chapter analysis is attached to server scans.
	// Chapter tooling remains isolated for explicit/manual use, but library scans
	// should be strict metadata/file scans and must not rewrite navigation data.
	if cfg.ScanOnStart {
		log.Printf("scanning configured libraries on startup")
		if _, err := libraryService.ScanAll(ctx, libraries.TriggerStartup, ""); err != nil {
			log.Fatal(err)
		}
	}
	channelsService := channels.NewService(channels.ServiceOptions{
		DB:               db,
		Catalog:          catalogService,
		Cache:            podcastCacheAdapter{service: podcastCacheService},
		InternetStations: internetStationAdapter{service: sourceService},
		FFmpegPath:       tools.FFmpeg,
		Logger:           log.Default(),
	})

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
		ArtistMeta:    artistMetaService,
		Users:         userService,
		Channels:      channelsService,
		ReloadCatalog: reloadCatalog,
		StartedAt:     time.Now(),
	})

	if cfg.LastFMPoll && lastfmService.Enabled() {
		poller := lastfm.NewPoller(lastfm.PollerOptions{
			Service: lastfmService,
			Tick:    cfg.LastFMPollTick,
			Logger:  log.Printf,
		})
		go func() {
			if err := poller.Run(ctx); err != nil && err != context.Canceled {
				log.Printf("last.fm queue poller stopped: %v", err)
			}
		}()
	}

	if cfg.PodcastPoll {
		poller := sources.NewPoller(sources.PollerOptions{
			Sources:       sourceService,
			ReloadCatalog: reloadCatalog,
			Tick:          cfg.PodcastPollTick,
			Logger:        log.Printf,
		})
		go func() {
			if err := poller.Run(ctx); err != nil && err != context.Canceled {
				log.Printf("podcast feed poller stopped: %v", err)
			}
		}()
	}

	if cfg.InternetRadioProbe {
		probe := sources.NewProbePoller(sources.ProbePollerOptions{
			Sources: sourceService,
			Tick:    cfg.InternetRadioProbeTick,
			Logger:  log.Printf,
		})
		go func() {
			if err := probe.Run(ctx); err != nil && err != context.Canceled {
				log.Printf("internet radio probe poller stopped: %v", err)
			}
		}()
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
			Logger:         log.Default(),
		})
		go func() {
			if err := watcher.Run(ctx); err != nil && err != context.Canceled {
				log.Printf("library watcher stopped: %v", err)
			}
		}()
	}

	listener, err := listenWithFallback(cfg.Addr, 20)
	if err != nil {
		log.Fatal(err)
	}
	actualAddr := listener.Addr().String()
	if !sameListenPort(cfg.Addr, actualAddr) {
		log.Printf("requested %s was in use; samo-server bound to %s instead", cfg.Addr, actualAddr)
	}
	log.Printf("samo-server listening on %s", actualAddr)
	if setupHintNeeded {
		log.Printf("no admin user configured; open http://localhost%s/setup in a browser to finish first-run setup", normalizedDisplayPort(actualAddr))
	}
	log.Printf("ffmpeg: %s", tools.FFmpeg)
	log.Printf("ffprobe: %s", tools.FFprobe)
	if cfg.ScanFFprobe {
		log.Printf("library scan metadata: ffprobe only (SAMO_SCAN_FFPROBE=1)")
	} else {
		log.Printf("library scan metadata: native tags + ffprobe fallback for duration/technical fields")
	}
	log.Printf("cover cache: %s", coverDir)
	log.Printf("radio config: %s (%d station(s))", cfg.RadioConfigPath, radioService.StationCount())
	if lastfmService.Enabled() {
		log.Printf("last.fm scrobbling: enabled")
	} else {
		log.Printf("last.fm scrobbling: disabled (set SAMO_LASTFM_API_KEY and SAMO_LASTFM_SHARED_SECRET)")
	}

	_, portStr, err := net.SplitHostPort(actualAddr)
	var serverPort int
	if err == nil {
		fmt.Sscanf(portStr, "%d", &serverPort)
		broadcaster := discovery.NewBroadcaster(serverPort)
		go func() {
			if err := broadcaster.Run(ctx); err != nil && err != context.Canceled {
				log.Printf("discovery broadcaster stopped: %v", err)
			}
		}()
	}

	srv := &http.Server{
		Handler: handler,
	}

	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down gracefully...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("http server shutdown error: %v", err)
	}

	log.Println("samo-server stopped")
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
