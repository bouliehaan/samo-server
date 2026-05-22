package main

import (
	"context"
	"log"
	"net/http"

	"github.com/bouliehaan/samo-server/internal/api"
	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/config"
	"github.com/bouliehaan/samo-server/internal/metadata"
	"github.com/bouliehaan/samo-server/internal/radio"
	"github.com/bouliehaan/samo-server/internal/scanner"
	"github.com/bouliehaan/samo-server/internal/sources"
	"github.com/bouliehaan/samo-server/internal/storage"
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

	if cfg.ScanOnStart && len(cfg.Libraries) > 0 {
		log.Printf("scanning %d configured library path(s)", len(cfg.Libraries))
		if err := scanner.New(db).Scan(ctx, scannerLibraries(cfg.Libraries)); err != nil {
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
		Metadata:      metadataService,
		Radio:         radioService,
		Sources:       sourceService,
		ReloadCatalog: reloadCatalog,
	})

	if cfg.WatchLibraries && len(cfg.Libraries) > 0 {
		watcher := watch.New(watch.Options{
			DB:        db,
			Catalog:   catalogService,
			Scanner:   scanner.New(db),
			Libraries: scannerLibraries(cfg.Libraries),
			Debounce:  cfg.WatchDebounce,
			Logger:    log.Default(),
		})
		go func() {
			if err := watcher.Run(ctx); err != nil && err != context.Canceled {
				log.Printf("library watcher stopped: %v", err)
			}
		}()
	}

	log.Printf("samo-server listening on %s", cfg.Addr)
	log.Printf("sqlite database: %s", cfg.DBPath)
	log.Printf("radio config: %s (%d station(s))", cfg.RadioConfigPath, radioService.StationCount())

	if err := http.ListenAndServe(cfg.Addr, handler); err != nil {
		log.Fatal(err)
	}
}

func scannerLibraries(input []config.Library) []scanner.Library {
	libraries := make([]scanner.Library, 0, len(input))
	for _, library := range input {
		libraries = append(libraries, scanner.Library{
			Name:      library.Name,
			Kind:      library.Kind,
			MediaType: library.MediaType,
			Path:      library.Path,
		})
	}
	return libraries
}
