package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/bouliehaan/samo-server/internal/api"
	"github.com/bouliehaan/samo-server/internal/artistimages"
	"github.com/bouliehaan/samo-server/internal/bookmarks"
	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/channels"
	"github.com/bouliehaan/samo-server/internal/config"
	"github.com/bouliehaan/samo-server/internal/covers"
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
	"github.com/bouliehaan/samo-server/migrations"
)

func main() {
	ctx := context.Background()

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

	db, err := storage.Open(ctx, cfg.DBPath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		log.Fatal(err)
	}

	tools, err := toolchain.Resolve(toolchain.Options{DataDir: cfg.DataDir})
	if err != nil {
		log.Fatal(err)
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

	catalogSeed, err := catalog.LoadSeedFromDB(ctx, db)
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
	playbackService := playback.New(db)
	metadataService := metadata.NewDefaultService(cfg.MetadataProviders, cfg.MetadataUserAgent)
	coverService.SetRemoteOptions(covers.RemoteOptions{})
	metadataApplyService := metadata.NewMetadataApplyServiceWithOptions(db, metadata.MetadataApplyOptions{
		CoverDownloader: coverService,
		Logger:          log.Printf,
	})
	podcastStreamService := podcaststream.New()
	searchService := search.New()
	searchService.Rebuild(catalogSeed)
	bookmarksService := bookmarks.New(db)
	podcastCacheService, err := podcastcache.New(db, podcastcache.Options{
		CacheDir:     filepath.Join(cfg.DataDir, "podcast-cache"),
		Enabled:      cfg.PodcastCache,
		MaxBytes:     cfg.PodcastCacheMaxBytes,
		MaxAge:       cfg.PodcastCacheMaxAge,
		MaxFileBytes: cfg.PodcastCacheMaxFile,
		Stream:       podcastStreamService,
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
	var catalogReloadMu sync.Mutex
	reloadCatalog := func(ctx context.Context) error {
		catalogReloadMu.Lock()
		defer catalogReloadMu.Unlock()
		seed, err := catalog.LoadSeedFromDB(ctx, db)
		if err != nil {
			return err
		}
		catalogService.Replace(seed)
		searchService.Rebuild(seed)
		return nil
	}
	// Audio-anchored chapter analysis runs AFTER a scan, in the background — it
	// decodes whole books, so it must never block the scan-complete callback. A
	// single-flight guard means overlapping scans don't stack passes; the next
	// completion catches up any books left stale. rootCtx outlives the per-scan
	// callback ctx so a long pass isn't cancelled when the callback returns.
	rootCtx := ctx
	var chapterPassMu sync.Mutex
	runChapterPass := func() {
		if !cfg.AudiobookChapterAnalysis || !scan.AudioChapterAnalysisEnabled() {
			return
		}
		if !chapterPassMu.TryLock() {
			log.Printf("audio chapter analysis: a pass is already running; will catch up after the next scan")
			return
		}
		go func() {
			defer chapterPassMu.Unlock()
			if _, _, err := scan.RunChapterAnalysisPass(rootCtx, false); err != nil {
				log.Printf("audio chapter analysis: pass error: %v", err)
			}
		}()
	}
	libraryService.OnScanComplete(func(ctx context.Context, job libraries.ScanJob, stats scanner.ScanStats) {
		if err := reloadCatalog(ctx); err != nil {
			log.Printf("catalog reload after scan %s failed: %v", job.ID, err)
		}
		if job.Status == libraries.ScanStatusCompleted {
			runChapterPass()
		}
		if job.Status != libraries.ScanStatusCompleted || !cfg.ArtistImagesOnScan || !artistImageService.Enabled() {
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
	// Kick a chapter-analysis pass on startup too: most installs run with
	// SAMO_SCAN_ON_START=false, so a scan-complete-only trigger would never fire
	// and the feature would appear dead. The signature cache makes this cheap
	// after the first boot (unchanged books are skipped without decoding).
	runChapterPass()
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
		ArtistImages:  artistImageService,
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
	if actualAddr != cfg.Addr {
		log.Printf("requested %s was in use; samo-server bound to %s instead", cfg.Addr, actualAddr)
	}
	log.Printf("samo-server listening on %s", actualAddr)
	if setupHintNeeded {
		log.Printf("no admin user configured; open http://localhost%s/setup in a browser to finish first-run setup", normalizedDisplayPort(actualAddr))
	}
	log.Printf("sqlite database: %s", cfg.DBPath)
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

	if err := http.Serve(listener, handler); err != nil {
		log.Fatal(err)
	}
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
