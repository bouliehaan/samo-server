package main

import (
	"context"
	"log"
	"net/http"
	"path/filepath"

	"github.com/bouliehaan/samo-server/internal/api"
	"github.com/bouliehaan/samo-server/internal/bookmarks"
	"github.com/bouliehaan/samo-server/internal/catalog"
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

	scan := scanner.NewWithOptions(db, scanner.Options{
		Covers:      coverService,
		FFprobePath: tools.FFprobe,
	})
	libraryService := libraries.New(db, scan)
	libraryService.SetBackgroundContext(ctx)
	if err := libraryService.SyncConfigured(ctx, cfg.Libraries); err != nil {
		log.Fatal(err)
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

	if cfg.ScanOnStart {
		log.Printf("scanning configured libraries on startup")
		// Startup scan is async too — we don't block server boot on it.
		// The goroutine runs against the lifecycle ctx and reloads the
		// catalog on completion via the OnScanComplete hook below.
		if _, err := libraryService.ScanAll(ctx, libraries.TriggerStartup); err != nil {
			log.Fatal(err)
		}
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
	playlistService := playlists.New(db)
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
	sourceService := sources.New(db, sources.Options{PodcastCache: podcastCacheService})
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
	reloadCatalog := func(ctx context.Context) error {
		seed, err := catalog.LoadSeedFromDB(ctx, db)
		if err != nil {
			return err
		}
		catalogService.Replace(seed)
		searchService.Rebuild(seed)
		return nil
	}
	// Refresh catalog projection whenever an async scan finishes (success
	// or failure — failed scans still touch the DB before bailing). Failures
	// log the underlying reload error but don't block the next scan.
	libraryService.OnScanComplete(func(ctx context.Context, job libraries.ScanJob) {
		if err := reloadCatalog(ctx); err != nil {
			log.Printf("catalog reload after scan %s failed: %v", job.ID, err)
		}
	})
	handler := api.NewServer(api.ServerOptions{
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
		Users:         userService,
		ReloadCatalog: reloadCatalog,
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
			DB:      db,
			Catalog: catalogService,
			Scan: func(ctx context.Context) (libraries.ScanResult, error) {
				return libraryService.ScanFilesystem(ctx)
			},
			ListLibraries: func(ctx context.Context) ([]string, error) {
				scannerLibraries, err := libraryService.ScannerLibraries(ctx)
				if err != nil {
					return nil, err
				}
				paths := make([]string, 0, len(scannerLibraries))
				for _, library := range scannerLibraries {
					paths = append(paths, library.Path)
				}
				return paths, nil
			},
			Debounce: cfg.WatchDebounce,
			Logger:   log.Default(),
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
