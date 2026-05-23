package config

import (
	"errors"
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

// Config contains process-level server settings. Feature-specific settings live
// in their own packages so modules can grow independently.
type Config struct {
	Addr                   string
	DataDir                string
	DBPath                 string
	RadioConfigPath        string
	APIToken               string
	BootstrapUsername      string
	BootstrapPassword      string
	Libraries              []Library
	MetadataProviders      []string
	MetadataUserAgent      string
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
	InternetRadioProbe     bool
	InternetRadioProbeTick time.Duration
}

type Library struct {
	Name      string
	Kind      string
	MediaType string
	Path      string
}

func LoadEnv() (Config, error) {
	dataDir := envOrDefault("SAMO_DATA_DIR", defaultDataDir)
	dbPath := strings.TrimSpace(os.Getenv("SAMO_DB_PATH"))
	if dbPath == "" {
		dbPath = filepath.Join(dataDir, "samo.db")
	}
	radioConfigPath := strings.TrimSpace(os.Getenv("SAMO_RADIO_CONFIG"))
	if radioConfigPath == "" {
		radioConfigPath = filepath.Join(dataDir, "radio.json")
	}

	cfg := Config{
		Addr:                   envOrDefault("SAMO_ADDR", defaultAddr),
		DataDir:                dataDir,
		DBPath:                 dbPath,
		RadioConfigPath:        radioConfigPath,
		APIToken:               strings.TrimSpace(os.Getenv("SAMO_API_TOKEN")),
		BootstrapUsername:      strings.TrimSpace(os.Getenv("SAMO_BOOTSTRAP_USERNAME")),
		BootstrapPassword:      strings.TrimSpace(os.Getenv("SAMO_BOOTSTRAP_PASSWORD")),
		Libraries:              loadLibraries(),
		MetadataProviders:      envCSV("SAMO_METADATA_PROVIDERS"),
		MetadataUserAgent:      envOrDefault("SAMO_METADATA_USER_AGENT", "SamoServer/0.1 (https://github.com/bouliehaan/samo-server)"),
		ScanOnStart:            envBool("SAMO_SCAN_ON_START", true),
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
		InternetRadioProbe:     envBool("SAMO_INTERNET_RADIO_PROBE", true),
		InternetRadioProbeTick: envDuration("SAMO_INTERNET_RADIO_PROBE_TICK", time.Minute),
	}

	return cfg.Validate()
}

func (c Config) Validate() (Config, error) {
	c.Addr = strings.TrimSpace(c.Addr)
	c.DataDir = strings.TrimSpace(c.DataDir)
	c.DBPath = strings.TrimSpace(c.DBPath)
	c.RadioConfigPath = strings.TrimSpace(c.RadioConfigPath)
	c.APIToken = strings.TrimSpace(c.APIToken)
	c.MetadataUserAgent = strings.TrimSpace(c.MetadataUserAgent)

	switch {
	case c.Addr == "":
		return Config{}, errors.New("server address cannot be empty")
	case c.DataDir == "":
		return Config{}, errors.New("data directory cannot be empty")
	case c.DBPath == "":
		return Config{}, errors.New("database path cannot be empty")
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

func loadLibraries() []Library {
	var libraries []Library
	libraries = appendLibraries(libraries, "music", "", "SAMO_MUSIC_DIRS")
	libraries = appendLibraries(libraries, "shelf", "book", "SAMO_AUDIOBOOK_DIRS")
	libraries = appendLibraries(libraries, "shelf", "podcast", "SAMO_PODCAST_DIRS")
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
