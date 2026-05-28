package main

import (
	"context"
	"database/sql"
	"log"
	"os"
	"path/filepath"

	"github.com/bouliehaan/samo-server/internal/config"
	"github.com/bouliehaan/samo-server/internal/covers"
	"github.com/bouliehaan/samo-server/internal/playlists"
	"github.com/bouliehaan/samo-server/internal/scanner"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/internal/toolchain"
	"github.com/bouliehaan/samo-server/migrations"
)

func runScanSubprocess(ctx context.Context, payloadPath string) {
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
	coverService, err := covers.New(db, covers.Options{
		CoverDir:    filepath.Join(cfg.DataDir, "covers"),
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
		PlaylistImport:      playlistScanBridge{db: db, svc: playlistService},
		AutoImportPlaylists: cfg.AutoImportPlaylists,
		ExternalScanner:     false,
	})
	if err := scanner.RunSubprocessScan(ctx, scan, payloadPath); err != nil {
		log.Fatal(err)
	}
	os.Exit(0)
}

type playlistScanBridge struct {
	db  *sql.DB
	svc *playlists.Service
}

func (p playlistScanBridge) ImportM3UFromPath(ctx context.Context, ownerID, path string) (bool, error) {
	return p.svc.ImportM3UFromPath(ctx, ownerID, path)
}

func (p playlistScanBridge) FirstAdminOwnerID(ctx context.Context) (string, error) {
	return playlists.FirstAdminOwnerID(ctx, p.db)
}
