package main

import (
	"context"
	"log"
	"net/http"
	"path/filepath"

	"github.com/bouliehaan/samo-server/internal/api"
	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/config"
	"github.com/bouliehaan/samo-server/internal/covers"
	"github.com/bouliehaan/samo-server/internal/files"
	"github.com/bouliehaan/samo-server/internal/libraries"
	"github.com/bouliehaan/samo-server/internal/metadata"
	"github.com/bouliehaan/samo-server/internal/playback"
	"github.com/bouliehaan/samo-server/internal/radio"
	"github.com/bouliehaan/samo-server/internal/scanner"
	"github.com/bouliehaan/samo-server/internal/sources"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/internal/toolchain"
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
	if err := libraryService.SyncConfigured(ctx, cfg.Libraries); err != nil {
		log.Fatal(err)
	}

	if cfg.ScanOnStart {
		log.Printf("scanning configured libraries on startup")
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

	radioService, err := radio.NewService(radioConfig)
	if err != nil {
		log.Fatal(err)
	}

	catalogService := catalog.NewService(catalogSeed)
	playbackService := playback.New(db)
	filesService := files.New(db, coverService.CoverDir())
	metadataService := metadata.NewDefaultService(cfg.MetadataProviders, cfg.MetadataUserAgent)
	sourceService := sources.New(db)
	reloadCatalog := func(ctx context.Context) error {
		seed, err := catalog.LoadSeedFromDB(ctx, db)
		if err != nil {
			return err
		}
		catalogService.Replace(seed)
		return nil
	}
	handler := api.NewServer(api.ServerOptions{
		APIToken:      cfg.APIToken,
		Catalog:       catalogService,
		Libraries:     libraryService,
		Playback:      playbackService,
		Covers:        coverService,
		Files:         filesService,
		Metadata:      metadataService,
		Radio:         radioService,
		Sources:       sourceService,
		ReloadCatalog: reloadCatalog,
	})

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

	log.Printf("samo-server listening on %s", cfg.Addr)
	log.Printf("sqlite database: %s", cfg.DBPath)
	log.Printf("ffmpeg: %s", tools.FFmpeg)
	log.Printf("ffprobe: %s", tools.FFprobe)
	log.Printf("cover cache: %s", coverDir)
	log.Printf("radio config: %s (%d station(s))", cfg.RadioConfigPath, radioService.StationCount())

	if err := http.ListenAndServe(cfg.Addr, handler); err != nil {
		log.Fatal(err)
	}
}
