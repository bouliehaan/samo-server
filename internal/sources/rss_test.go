package sources

import (
	"strings"
	"testing"
)

func TestParsePodcastFeedXMLReadsITunesMetadata(t *testing.T) {
	feed, err := parsePodcastFeedXML(strings.NewReader(`<?xml version="1.0"?>
<rss version="2.0" xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd">
  <channel>
    <title>Night Signals</title>
    <link>https://example.com/show</link>
    <description>Late radio stories</description>
    <language>en-us</language>
    <itunes:author>Ada Archive</itunes:author>
    <itunes:explicit>yes</itunes:explicit>
    <itunes:image href="https://example.com/cover.jpg" />
    <itunes:owner><itunes:name>Ada</itunes:name><itunes:email>ada@example.com</itunes:email></itunes:owner>
    <itunes:category text="Fiction"><itunes:category text="Drama" /></itunes:category>
    <item>
      <title>Episode One</title>
      <itunes:subtitle>The opener</itunes:subtitle>
      <itunes:duration>01:02:03</itunes:duration>
      <itunes:season>2</itunes:season>
      <itunes:episode>4</itunes:episode>
      <guid>episode-1</guid>
      <pubDate>Fri, 22 May 2026 12:00:00 -0600</pubDate>
      <enclosure url="https://cdn.example.com/ep1.mp3" type="audio/mpeg" length="1234" />
    </item>
  </channel>
</rss>`))
	if err != nil {
		t.Fatal(err)
	}

	if feed.Title != "Night Signals" {
		t.Fatalf("title = %q, want Night Signals", feed.Title)
	}
	if !feed.Explicit {
		t.Fatal("feed should be explicit")
	}
	if got := strings.Join(feed.Categories, ","); got != "Fiction,Drama" {
		t.Fatalf("categories = %q, want Fiction,Drama", got)
	}
	if len(feed.Episodes) != 1 {
		t.Fatalf("episodes = %d, want 1", len(feed.Episodes))
	}
	episode := feed.Episodes[0]
	if episode.DurationSeconds != 3723 {
		t.Fatalf("duration = %d, want 3723", episode.DurationSeconds)
	}
	if episode.Season != 2 || episode.Episode != 4 {
		t.Fatalf("season/episode = %d/%d, want 2/4", episode.Season, episode.Episode)
	}
	if episode.EnclosureBytes != 1234 {
		t.Fatalf("enclosure bytes = %d, want 1234", episode.EnclosureBytes)
	}
}
