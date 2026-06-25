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

func TestParsePodcastFeedXMLReadsMediaThumbnailAndFlexibleDate(t *testing.T) {
	feed, err := parsePodcastFeedXML(strings.NewReader(`<?xml version="1.0"?>
<rss version="2.0" xmlns:media="http://search.yahoo.com/mrss/">
  <channel>
    <title>Fresh Feed</title>
    <media:thumbnail url="https://example.com/show.png" />
    <item>
      <title>May Episode</title>
      <guid>may-episode</guid>
      <pubDate>11 May 2026 12:00:00 GMT</pubDate>
      <media:thumbnail url="https://example.com/episode.png" />
      <enclosure url="https://cdn.example.com/may.mp3" type="audio/mpeg" length="1234" />
    </item>
  </channel>
</rss>`))
	if err != nil {
		t.Fatal(err)
	}

	if feed.ImageURL != "https://example.com/show.png" {
		t.Fatalf("image url = %q, want media thumbnail", feed.ImageURL)
	}
	if len(feed.Episodes) != 1 || feed.Episodes[0].ImageURL != "https://example.com/episode.png" {
		t.Fatalf("episode image = %#v, want media thumbnail", feed.Episodes)
	}
	if feed.Episodes[0].PublishedAt == nil {
		t.Fatal("published date should parse")
	}
}

func TestParsePodcastFeedXMLReadsAnchorSpotifyEpisodeLinks(t *testing.T) {
	feed, err := parsePodcastFeedXML(strings.NewReader(`<?xml version="1.0" encoding="UTF-8"?>
<rss xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:atom="http://www.w3.org/2005/Atom" version="2.0" xmlns:anchor="https://anchor.fm/xmlns" xmlns:podcast="https://podcastindex.org/namespace/1.0" xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd">
  <channel>
    <title><![CDATA[The Dude Grows Show]]></title>
    <description><![CDATA[The Dude Grows Show brings you grow knowledge, news, and culture.]]></description>
    <link>https://www.RealDGC.com</link>
    <atom:link href="https://anchor.fm/s/bcf35298/podcast/rss" rel="self" type="application/rss+xml"/>
    <itunes:author>The Dude Grows Show</itunes:author>
    <itunes:image href="https://d3t3ozftmdmh3i.cloudfront.net/staging/podcast_uploaded_nologo/31600630/119702efad91d871.png"/>
    <item>
      <title><![CDATA[The Hidden Signs Your Cannabis Plants Are Ready to Flip to Flower]]></title>
      <description><![CDATA[<p>RealDGC.com</p>]]></description>
      <link>https://podcasters.spotify.com/pod/show/dudegrowsshow/episodes/The-Hidden-Signs-Your-Cannabis-Plants-Are-Ready-to-Flip-to-Flower-e3jp83f</link>
      <guid isPermaLink="false">5fc9da05-3463-435c-b63d-347db116ad94</guid>
      <dc:creator><![CDATA[The Dude Grows Show]]></dc:creator>
      <pubDate>Sat, 23 May 2026 15:10:00 GMT</pubDate>
      <enclosure url="https://anchor.fm/s/bcf35298/podcast/play/120413743/https%3A%2F%2Fd3ctxlq1ktw2nl.cloudfront.net%2Fstaging%2F2026-4-23%2F424721644-44100-2-a0bde5ce88cba.mp3" length="44977840" type="audio/mpeg"/>
      <itunes:summary>&lt;p&gt;RealDGC.com&lt;/p&gt;</itunes:summary>
      <itunes:duration>00:46:51</itunes:duration>
      <itunes:image href="https://d3t3ozftmdmh3i.cloudfront.net/staging/podcast_uploaded_nologo/31600630/119702efad91d871.png"/>
      <itunes:episodeType>full</itunes:episodeType>
    </item>
    <item>
      <title><![CDATA[Flip to Flower Like THIS for Bigger, Frostier Buds]]></title>
      <link>https://podcasters.spotify.com/pod/show/dudegrowsshow/episodes/Flip-to-Flower-Like-THIS-for-Bigger--Frostier-Buds-e3jp81a</link>
      <guid isPermaLink="false">f844e91b-37b0-4773-a4a8-21ff900c3664</guid>
      <pubDate>Sat, 23 May 2026 02:10:44 GMT</pubDate>
      <enclosure url="https://anchor.fm/s/bcf35298/podcast/play/120413674/https%3A%2F%2Fd3ctxlq1ktw2nl.cloudfront.net%2Fstaging%2F2026-4-23%2F424721588-44100-2-ffb46f5ae62a2.mp3" length="55528802" type="audio/mpeg"/>
      <itunes:duration>00:57:50</itunes:duration>
    </item>
  </channel>
</rss>`))
	if err != nil {
		t.Fatal(err)
	}

	if feed.Title != "The Dude Grows Show" {
		t.Fatalf("title = %q, want The Dude Grows Show", feed.Title)
	}
	if feed.ImageURL == "" {
		t.Fatal("feed image should parse")
	}
	if len(feed.Episodes) != 2 {
		t.Fatalf("episodes = %d, want 2", len(feed.Episodes))
	}
	first := feed.Episodes[0]
	if first.Link != "https://podcasters.spotify.com/pod/show/dudegrowsshow/episodes/The-Hidden-Signs-Your-Cannabis-Plants-Are-Ready-to-Flip-to-Flower-e3jp83f" {
		t.Fatalf("spotify link = %q", first.Link)
	}
	if first.EnclosureURL != "https://anchor.fm/s/bcf35298/podcast/play/120413743/https%3A%2F%2Fd3ctxlq1ktw2nl.cloudfront.net%2Fstaging%2F2026-4-23%2F424721644-44100-2-a0bde5ce88cba.mp3" {
		t.Fatalf("enclosure url = %q", first.EnclosureURL)
	}
	if first.DurationSeconds != 2811 {
		t.Fatalf("duration = %d, want 2811", first.DurationSeconds)
	}
	if first.PublishedAt == nil || first.PublishedAt.Year() != 2026 {
		t.Fatalf("published at = %#v, want 2026 date", first.PublishedAt)
	}
}

func TestParsePodcastFeedXMLReadsAtomPodcastFeed(t *testing.T) {
	feed, err := parsePodcastFeedXML(strings.NewReader(`<?xml version="1.0"?>
<feed xmlns="http://www.w3.org/2005/Atom" xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd">
  <title>Atom Signals</title>
  <subtitle>Dispatches from old feeds</subtitle>
  <author><name>Ada Archive</name><email>ada@example.com</email></author>
  <link rel="alternate" href="https://example.com/show" />
  <logo>https://example.com/show.png</logo>
  <category term="History" />
  <entry>
    <title>Episode Atom</title>
    <id>tag:example.com,2026:episode-atom</id>
    <published>2026-05-23T15:10:00Z</published>
    <link rel="alternate" href="https://example.com/episodes/atom" />
    <link rel="enclosure" href="https://cdn.example.com/atom.mp3" type="audio/mpeg" length="321" />
    <summary>Atom summary</summary>
    <itunes:duration>00:10:05</itunes:duration>
  </entry>
</feed>`))
	if err != nil {
		t.Fatal(err)
	}

	if feed.Title != "Atom Signals" || feed.SiteURL != "https://example.com/show" {
		t.Fatalf("feed = %#v, want Atom show metadata", feed)
	}
	if got := strings.Join(feed.Categories, ","); got != "History" {
		t.Fatalf("categories = %q, want History", got)
	}
	if len(feed.Episodes) != 1 {
		t.Fatalf("episodes = %d, want 1", len(feed.Episodes))
	}
	episode := feed.Episodes[0]
	if episode.Link != "https://example.com/episodes/atom" {
		t.Fatalf("episode link = %q", episode.Link)
	}
	if episode.EnclosureURL != "https://cdn.example.com/atom.mp3" || episode.EnclosureBytes != 321 {
		t.Fatalf("enclosure = %q/%d", episode.EnclosureURL, episode.EnclosureBytes)
	}
	if episode.DurationSeconds != 605 {
		t.Fatalf("duration = %d, want 605", episode.DurationSeconds)
	}
}
