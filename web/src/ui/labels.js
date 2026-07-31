// Naming and small display decisions: what to call a thing, and which
// field to fall back to when the obvious one is empty.

import { attr, escapeHTML } from "./html.js";

export function audiobookTitle(item) {
  if (!item) return "Untitled";
  if (item.book && item.book.title) return item.book.title;
  return item.title || "Untitled";
}

export function audiobookSub(item) {
  if (!item) return "";
  if (item.book && item.book.authors && item.book.authors.length > 0) {
    return item.book.authors.map((author) => author.name).join(", ");
  }
  return "AUDIOBOOK";
}

export function podcastTitle(item) {
  if (!item) return "Untitled";
  if (item.podcast && item.podcast.title) return item.podcast.title;
  return item.title || "Untitled";
}

export function podcastSub(item) {
  if (!item) return "";
  if (item.podcast && item.podcast.author) return item.podcast.author;
  return "PODCAST";
}

export function libraryKindLabel(lib) {
  if (!lib) return "UNKNOWN";
  switch (lib.kind) {
    case "mixed":     return "MIXED";
    case "music":     return "MUSIC";
    case "audiobook": return "AUDIOBOOKS";
    case "podcast":   return "PODCASTS";
  }
  return String(lib.kind || "unknown").toUpperCase();
}

export function recentlyAddedKindLabel(kind) {
  switch (kind) {
    case "music-album": return "ALBUM";
    case "audiobook": return "AUDIOBOOK";
    case "podcast": return "PODCAST";
    default: return "";
  }
}

export function browseAlbums(data) {
  if (!data) return [];
  if (Array.isArray(data.items)) return data.items;
  return data.albums || [];
}

export function browseTracks(data) {
  if (!data) return [];
  if (Array.isArray(data.items)) return data.items;
  return data.tracks || [];
}

export function browseResultCount(data) {
  if (!data) return 0;
  return ((data.albums && data.albums.length) || 0) +
    ((data.tracks && data.tracks.length) || 0) +
    ((data.artists && data.artists.length) || 0) +
    ((data.playlists && data.playlists.length) || 0);
}

export function isLibraryFolderPodcast(item) {
  const path = String((item && item.path) || "");
  return Boolean(path && !path.startsWith("samo://"));
}

export function podcastHasLinkedFeed(item) {
  return Boolean(item && item.rssFeed && item.rssFeed.id);
}

export function librarySupportsRepair(lib) {
  return lib && (lib.kind === "music" || lib.kind === "mixed");
}

export function scanPruneSummary(job) {
  if (!job) return "";
  const parts = [];
  if (job.filesMarked) parts.push(job.filesMarked + " missing files");
  if (job.filesPruned) parts.push(job.filesPruned + " stale files");
  if (job.itemsPruned) parts.push(job.itemsPruned + " orphan items");
  return parts.length ? " · " + parts.join(" · ") : "";
}

export function candidateFeedURL(candidate) {
  if (!candidate) return "";
  for (const raw of (candidate.externalIds && candidate.externalIds.urls) || []) {
    const trimmed = String(raw || "").trim();
    if (!trimmed) continue;
    const lower = trimmed.toLowerCase();
    if (lower.endsWith(".xml") || lower.includes("/feed") || lower.includes("rss")) {
      return trimmed;
    }
  }
  for (const link of candidate.links || []) {
    const label = String((link && link.label) || "").toLowerCase();
    if (label.includes("rss") && link.url) return String(link.url).trim();
  }
  return "";
}

export function musicPaginationFooter(loaded, total) {
  if (!total || loaded >= total) return "";
  return '<div class="section-row"><button class="btn ghost btn-small" data-action="music-load-more">LOAD MORE (' + loaded + " / " + total + ')</button></div>';
}

export function nowPlayingLine(now, liveText, idleLabel) {
  if (now && liveText) {
    return '<div class="now-playing"><span class="dot"></span><span class="np-label">NOW</span><span class="np-text">' + escapeHTML(liveText) + '</span></div>';
  }
  return '<div class="now-playing idle"><span class="dot"></span><span class="np-label">' + escapeHTML(idleLabel) + '</span></div>';
}

export function playlistCoverBlock(id, coverURL, canEdit) {
  const style = coverURL ? 'style="background-image:url(&quot;' + attr(coverURL) + '&quot;)"' : 'style="background-color:#0a0a0a"';
  if (!canEdit) {
    return '<div class="detail-cover" ' + style + '></div>';
  }
  return '<label class="detail-cover radio-cover-upload" ' + style + ' title="Upload custom artwork">' +
    '<input type="file" class="radio-cover-input" accept="image/*" data-playlist-id="' + attr(id) + '">' +
    '<span class="radio-cover-hint">UPLOAD</span>' +
  '</label>';
}
