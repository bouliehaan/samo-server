package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultAddr    = ":6969"
	defaultDataDir = "data"
)

var defaultMetadataProviders = []string{"audible", "openlibrary", "googlebooks", "itunes", "musicbrainz"}

// Config contains process-level server settings. Feature-specific settings live
// in their own packages so modules can grow independently.
type Config struct {
	Addr                   string
	DataDir                string
	DBDSN                  string // Postgres connection string
	RadioConfigPath        string
	APIToken               string
	BootstrapUsername      string
	BootstrapPassword      string
	Libraries              []Library
	MetadataProviders      []string
	MetadataUserAgent      string
	AudibleRegion          string
	ScanOnStart            bool
	WatchLibraries         bool
	WatchDebounce          time.Duration
	PodcastPoll            bool
	PodcastPollTick        time.Duration
	LastFMAPIKey           string
	LastFMSharedSecret     string
	LastFMPoll             bool
	LastFMPollTick         time.Duration
	PodcastCache           bool
	PodcastCacheMaxBytes   int64
	PodcastCacheMaxAge     time.Duration
	PodcastCacheMaxFile    int64
	PodcastPrewarmCount    int
	PodcastAutoDownload    bool
	InternetRadioProbe     bool
	InternetRadioProbeTick time.Duration
	ArtistImagesOnScan     bool
	AutoImportPlaylists    bool
	ScannerExternal        bool
	ScanFFprobe            bool
	ExploDirs              []string
	AcoustIDAPIKey         string
	ExploPlaylistName      string
}

type Library struct {
	Name      string
	Kind      string
	MediaType string
	Path      string
}

func LoadEnv() (Config, error) {
	dataDir := envOrDefault("SAMO_DATA_DIR", defaultDataDir)
	// The SQLite backend is gone. Fail loudly on config that still asks for it,
	// instead of silently starting against the wrong database.
	if backend := strings.ToLower(strings.TrimSpace(os.Getenv("SAMO_DB_BACKEND"))); backend != "" && backend != "postgres" && backend != "postgresql" {
		return Config{}, fmt.Errorf("SAMO_DB_BACKEND=%q is no longer supported: samo-server is Postgres-only (the SQLite backend was removed; use a pre-removal release to migrate an old samo.db)", backend)
	}
	radioConfigPath := strings.TrimSpace(os.Getenv("SAMO_RADIO_CONFIG"))
	if radioConfigPath == "" {
		radioConfigPath = filepath.Join(dataDir, "radio.json")
	}

	cfg := Config{
		Addr:                   envOrDefault("SAMO_ADDR", defaultAddr),
		DataDir:                dataDir,
		DBDSN:                  strings.TrimSpace(os.Getenv("SAMO_DB_DSN")),
		RadioConfigPath:        radioConfigPath,
		APIToken:               strings.TrimSpace(os.Getenv("SAMO_API_TOKEN")),
		BootstrapUsername:      strings.TrimSpace(os.Getenv("SAMO_BOOTSTRAP_USERNAME")),
		BootstrapPassword:      strings.TrimSpace(os.Getenv("SAMO_BOOTSTRAP_PASSWORD")),
		Libraries:              loadLibraries(),
		MetadataProviders:      envCSVOrDefault("SAMO_METADATA_PROVIDERS", defaultMetadataProviders),
		MetadataUserAgent:      envOrDefault("SAMO_METADATA_USER_AGENT", "SamoServer/0.1 (https://github.com/bouliehaan/samo-server)"),
		AudibleRegion:          envOrDefault("SAMO_AUDIBLE_REGION", "us"),
		ScanOnStart:            envBool("SAMO_SCAN_ON_START", false),
		WatchLibraries:         envBool("SAMO_WATCH_LIBRARIES", true),
		WatchDebounce:          envDuration("SAMO_WATCH_DEBOUNCE", 3*time.Second),
		PodcastPoll:            envBool("SAMO_PODCAST_POLL", true),
		PodcastPollTick:        envDuration("SAMO_PODCAST_POLL_TICK", time.Minute),
		LastFMAPIKey:           strings.TrimSpace(os.Getenv("SAMO_LASTFM_API_KEY")),
		LastFMSharedSecret:     strings.TrimSpace(os.Getenv("SAMO_LASTFM_SHARED_SECRET")),
		LastFMPoll:             envBool("SAMO_LASTFM_POLL", true),
		LastFMPollTick:         envDuration("SAMO_LASTFM_POLL_TICK", time.Minute),
		PodcastCache:           envBool("SAMO_PODCAST_CACHE", true),
		PodcastCacheMaxBytes:   envInt64("SAMO_PODCAST_CACHE_MAX_BYTES", 10<<30),
		PodcastCacheMaxAge:     envDuration("SAMO_PODCAST_CACHE_MAX_AGE", 30*24*time.Hour),
		PodcastCacheMaxFile:    envInt64("SAMO_PODCAST_CACHE_MAX_FILE_BYTES", 500<<20),
		PodcastPrewarmCount:    int(envInt64("SAMO_PODCAST_PREWARM_COUNT", 3)),
		PodcastAutoDownload:    envBool("SAMO_PODCAST_AUTO_DOWNLOAD", false),
		InternetRadioProbe:     envBool("SAMO_INTERNET_RADIO_PROBE", true),
		InternetRadioProbeTick: envDuration("SAMO_INTERNET_RADIO_PROBE_TICK", time.Minute),
		ArtistImagesOnScan:     envBool("SAMO_ARTIST_IMAGES_ON_SCAN", true),
		AutoImportPlaylists:    envBool("SAMO_AUTO_IMPORT_PLAYLISTS", true),
		ScannerExternal:        envBool("SAMO_SCANNER_EXTERNAL", false),
		ScanFFprobe:            envBool("SAMO_SCAN_FFPROBE", false),
		ExploDirs:              envPathList("SAMO_EXPLO_DIRS"),
		AcoustIDAPIKey:         strings.TrimSpace(os.Getenv("SAMO_ACOUSTID_API_KEY")),
		ExploPlaylistName:      envOrDefault("SAMO_EXPLO_PLAYLIST_NAME", "Explore"),
	}

	return cfg.Validate()
}

func (c Config) Validate() (Config, error) {
	c.Addr = strings.TrimSpace(c.Addr)
	c.DataDir = strings.TrimSpace(c.DataDir)
	c.DBDSN = strings.TrimSpace(c.DBDSN)
	c.RadioConfigPath = strings.TrimSpace(c.RadioConfigPath)
	c.APIToken = strings.TrimSpace(c.APIToken)
	c.MetadataUserAgent = strings.TrimSpace(c.MetadataUserAgent)

	switch {
	case c.Addr == "":
		return Config{}, errors.New("server address cannot be empty")
	case c.DataDir == "":
		return Config{}, errors.New("data directory cannot be empty")
	case c.DBDSN == "":
		return Config{}, errors.New("SAMO_DB_DSN is required (e.g. postgres://samo:pass@localhost:5432/samo?sslmode=disable)")
	case c.RadioConfigPath == "":
		return Config{}, errors.New("radio config path cannot be empty")
	default:
		for i := range c.Libraries {
			c.Libraries[i].Name = strings.TrimSpace(c.Libraries[i].Name)
			c.Libraries[i].Kind = strings.TrimSpace(c.Libraries[i].Kind)
			c.Libraries[i].MediaType = strings.TrimSpace(c.Libraries[i].MediaType)
			c.Libraries[i].Path = strings.TrimSpace(c.Libraries[i].Path)
			if c.Libraries[i].Path == "" {
				return Config{}, errors.New("library path cannot be empty")
			}
			if c.Libraries[i].Name == "" {
				c.Libraries[i].Name = filepath.Base(c.Libraries[i].Path)
			}
		}
		return c, nil
	}
}

func envOrDefault(key string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func envBool(key string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt64(key string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}

func envCSV(key string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			values = append(values, part)
		}
	}
	return values
}

func envCSVOrDefault(key string, fallback []string) []string {
	if strings.TrimSpace(os.Getenv(key)) != "" {
		return envCSV(key)
	}
	return append([]string(nil), fallback...)
}

// envPathList splits on the OS path separator, matching SAMO_MUSIC_DIRS and
// friends, so a folder path containing a comma isn't misread as two paths.
func envPathList(key string) []string {
	var paths []string
	for _, path := range filepath.SplitList(strings.TrimSpace(os.Getenv(key))) {
		path = strings.TrimSpace(path)
		if path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

func loadLibraries() []Library {
	var libraries []Library
	libraries = appendLibraries(libraries, "music", "", "SAMO_MUSIC_DIRS")
	libraries = appendLibraries(libraries, "audiobook", "", "SAMO_AUDIOBOOK_DIRS")
	libraries = appendLibraries(libraries, "podcast", "", "SAMO_PODCAST_DIRS")
	return libraries
}

func appendLibraries(libraries []Library, kind string, mediaType string, envKey string) []Library {
	for _, path := range filepath.SplitList(strings.TrimSpace(os.Getenv(envKey))) {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		libraries = append(libraries, Library{
			Name:      filepath.Base(path),
			Kind:      kind,
			MediaType: mediaType,
			Path:      path,
		})
	}
	return libraries
}
