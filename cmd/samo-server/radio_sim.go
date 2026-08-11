package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/catalogstore"
	"github.com/bouliehaan/samo-server/internal/channels"
	"github.com/bouliehaan/samo-server/internal/config"
	"github.com/bouliehaan/samo-server/internal/playback"
	"github.com/bouliehaan/samo-server/internal/podcastcache"
	"github.com/bouliehaan/samo-server/internal/sources"
)

// runRadioSim is the `samo-server radio-sim` subcommand: run a channel's
// programming forward against a virtual clock and print what it would do.
//
// It writes NOTHING. No play-log rows, no programme state, no decisions — the
// history it fills in lives in memory and is thrown away when the process
// exits. That is the whole point: a station is a set of rules about time, and
// before this the only way to find out whether a plan produced good radio was
// to broadcast it and listen. Now you can look at three days of it, with the
// reasoning attached to every choice, before anything goes near the aux port.
func runRadioSim(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("radio-sim", flag.ContinueOnError)
	var (
		channelID = fs.String("channel", "", "channel id to simulate (omit to list channels)")
		hours     = fs.Float64("hours", 24, "how many hours of programming to simulate")
		at        = fs.String("at", "", "wall-clock start, RFC3339 or 2006-01-02T15:04 in the channel's zone (default: now)")
		seed      = fs.Int64("seed", 0, "override the plan's seed, for repeatable runs")
		planPath  = fs.String("plan", "", "simulate a plan from a file instead of the stored one")
		verbose   = fs.Bool("verbose", false, "print the full running order")
		explain   = fs.Int("explain", -1, "print the full reasoning for one item, by its index in the running order")
		asJSON    = fs.Bool("json", false, "emit the whole result as JSON")
		warmTalk  = fs.String("warmup", "", "seed history before the run, e.g. \"talk:8h\" or \"talk:6h,music:20m\"")
	)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: samo-server radio-sim --channel <id> [--hours 72] [--seed 42] [--verbose]")
		fmt.Fprintln(os.Stderr, "       samo-server radio-sim --channel <id> --plan draft.json --hours 48")
		fmt.Fprintln(os.Stderr, "       samo-server radio-sim --channel <id> --explain 12")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := config.LoadEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 1
	}
	db, err := openAndMigrate(ctx, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "database: %v\n", err)
		return 1
	}
	defer db.Close()

	if strings.TrimSpace(*channelID) == "" {
		return listChannelsForSim(ctx, db)
	}

	channel, err := channels.LoadChannel(ctx, db, *channelID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "channel %s: %v\n", *channelID, err)
		return 1
	}
	sourceRows, err := channels.ListChannelSources(ctx, db, channel.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sources: %v\n", err)
		return 1
	}
	enabled := make([]channels.Source, 0, len(sourceRows))
	for _, src := range sourceRows {
		if src.Enabled {
			enabled = append(enabled, src)
		}
	}
	if len(enabled) == 0 {
		fmt.Fprintf(os.Stderr, "channel %s has no enabled sources\n", channel.ID)
		return 1
	}

	plan, planSource, err := simPlan(ctx, db, channel, enabled, *planPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "plan: %v\n", err)
		return 1
	}

	location := simLocation(channel, cfg)
	start, err := simStart(*at, location)
	if err != nil {
		fmt.Fprintf(os.Stderr, "--at: %v\n", err)
		return 1
	}

	// The catalog is read live and read-only: simulating against a fixture
	// would answer questions about the fixture.
	seedData, err := catalogstore.LoadSeedFromDB(ctx, db)
	if err != nil {
		fmt.Fprintf(os.Stderr, "catalog: %v\n", err)
		return 1
	}
	catalogService := catalog.NewService(seedData)
	// Read-only slice of the cache: the simulator only ever asks "is this
	// episode already local", never downloads.
	cacheService, err := podcastcache.New(db, podcastcache.Options{
		CacheDir:    filepath.Join(cfg.DataDir, "podcast-cache"),
		Enabled:     cfg.PodcastCache,
		BaseContext: ctx,
	})
	if err != nil {
		cacheService = nil
	}
	sourceService := sources.New(db, sources.Options{BaseContext: ctx})
	playbackService := playback.NewWithReadDB(db, db)

	warmup, err := parseWarmup(*warmTalk, enabled, start)
	if err != nil {
		fmt.Fprintf(os.Stderr, "--warmup: %v\n", err)
		return 1
	}

	engine := &channels.Engine{
		Plan:    plan,
		Channel: channel,
		Sources: enabled,
		History: channels.NewMemoryHistory(),
		// In-memory obligations: the run notices the same new episodes the
		// live station would, and works through them, without touching what
		// the live station has already surfaced.
		Obligations: channels.NewMemoryObligations(),
		Catalog:     catalogService,
		Cache:       podcastCacheAdapter{service: cacheService},
		Stations:    internetStationAdapter{service: sourceService},
		Listened:    channelListenedAdapter{service: playbackService},
		Skips:       channels.NewSkipRegistry(func() time.Time { return start }),
		Location:    location,
	}

	result, err := channels.Simulate(ctx, engine, channels.SimOptions{
		Start:    start,
		Duration: time.Duration(*hours * float64(time.Hour)),
		Seed:     *seed,
		Warmup:   warmup,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "simulate: %v\n", err)
		return 1
	}

	if *explain >= 0 {
		text := result.ExplainStep(*explain)
		if text == "" {
			fmt.Fprintf(os.Stderr, "no item at index %d (the run has %d)\n", *explain, len(result.Steps))
			return 1
		}
		fmt.Print(text)
		return 0
	}
	if *asJSON {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(result); err != nil {
			fmt.Fprintf(os.Stderr, "encode: %v\n", err)
			return 1
		}
		return 0
	}

	fmt.Printf("CHANNEL      %s (%s)\n", channel.Name, channel.ID)
	fmt.Printf("PLAN         %s · %d blocks · %d pools · %d categories\n",
		planSource, len(plan.Blocks), len(plan.Pools), len(plan.Categories))
	fmt.Printf("CLOCK        %s\n\n", location)
	fmt.Print(result.Format(*verbose))
	if !*verbose {
		fmt.Printf("\n(--verbose for the running order, --explain <n> for one item's reasoning)\n")
	}
	return 0
}

// listChannelsForSim prints what there is to simulate, so the first run of the
// command is useful rather than a usage error.
func listChannelsForSim(ctx context.Context, db *sql.DB) int {
	items, err := channels.ListChannels(ctx, db)
	if err != nil {
		fmt.Fprintf(os.Stderr, "channels: %v\n", err)
		return 1
	}
	if len(items) == 0 {
		fmt.Println("no channels yet")
		return 0
	}
	fmt.Println("CHANNELS")
	for _, channel := range items {
		state := "enabled"
		if !channel.Enabled {
			state = "disabled"
		}
		fmt.Printf("  %-28s %-28s %s\n", channel.ID, channel.Name, state)
	}
	fmt.Println("\nsamo-server radio-sim --channel <id> --hours 48")
	return 0
}

// simPlan resolves which plan to simulate: an explicit file, the stored one, or
// the one the channel's own configuration already describes.
func simPlan(ctx context.Context, db *sql.DB, channel channels.Channel, sourceRows []channels.Source, path string) (channels.Plan, string, error) {
	if strings.TrimSpace(path) != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return channels.Plan{}, "", err
		}
		plan, err := channels.ParsePlan(raw)
		if err != nil {
			return channels.Plan{}, "", err
		}
		return plan, "from " + path, nil
	}
	if plan, ok, err := channels.LoadPlan(ctx, db, channel.ID); err != nil {
		return channels.Plan{}, "", err
	} else if ok {
		return plan, "stored", nil
	}
	rules, err := channels.ListScheduleRules(ctx, db, channel.ID)
	if err != nil {
		return channels.Plan{}, "", err
	}
	share := channel.TalkShare
	if share <= 0 || share >= 1 {
		share = channels.DefaultTalkShare
	}
	return channels.DerivePlan(channel, sourceRows, rules, share), "derived from sources and slots", nil
}

// simLocation is the wall clock the simulated schedule is read in.
func simLocation(channel channels.Channel, cfg config.Config) *time.Location {
	if zone := strings.TrimSpace(channel.Timezone); zone != "" {
		if loc, err := time.LoadLocation(zone); err == nil {
			return loc
		}
	}
	if zone := strings.TrimSpace(os.Getenv("SAMO_TIMEZONE")); zone != "" {
		if loc, err := time.LoadLocation(zone); err == nil {
			return loc
		}
	}
	_ = cfg
	return time.UTC
}

// simStart parses --at in the channel's own zone, because a schedule is written
// in wall-clock time and "07:00" means nothing without one.
func simStart(raw string, loc *time.Location) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Now().In(loc), nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02"} {
		if parsed, err := time.ParseInLocation(layout, raw, loc); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("%q is not a time (try 2026-08-10T06:00)", raw)
}

// parseWarmup fabricates history so a run can start from an interesting place
// rather than from a station that has never played anything.
//
// "talk:8h" means the eight hours before the start were spoken word — which is
// exactly the state the station was in on the night that started all of this,
// and the state in which a scheduler's behaviour is most worth checking.
func parseWarmup(spec string, sourceRows []channels.Source, start time.Time) ([]channels.MemoryPlay, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, nil
	}
	type segment struct {
		category string
		length   time.Duration
	}
	segments := []segment{}
	total := time.Duration(0)
	for _, part := range strings.Split(spec, ",") {
		category, value, ok := strings.Cut(strings.TrimSpace(part), ":")
		if !ok {
			return nil, fmt.Errorf("%q should look like talk:8h", part)
		}
		length, err := time.ParseDuration(strings.TrimSpace(value))
		if err != nil || length <= 0 {
			return nil, fmt.Errorf("%q is not a duration", value)
		}
		segments = append(segments, segment{category: strings.TrimSpace(category), length: length})
		total += length
	}

	// Attribute each segment to a real source in that category, so separation
	// and per-source balance see something plausible rather than a ghost id.
	pick := func(category string) channels.Source {
		for _, src := range sourceRows {
			if string(channels.SourceCategory(src)) == category {
				return src
			}
		}
		if len(sourceRows) > 0 {
			return sourceRows[0]
		}
		return channels.Source{}
	}

	out := []channels.MemoryPlay{}
	cursor := start.Add(-total)
	for index, seg := range segments {
		src := pick(seg.category)
		out = append(out, channels.MemoryPlay{
			SourceID:        src.ID,
			ItemRef:         "warmup:" + strconv.Itoa(index),
			Category:        channels.CategoryID(seg.category),
			StartedAt:       cursor,
			EndedAt:         cursor.Add(seg.length),
			DurationSeconds: int(seg.length / time.Second),
		})
		cursor = cursor.Add(seg.length)
	}
	return out, nil
}
