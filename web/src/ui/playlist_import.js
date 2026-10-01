// A playlist imported from YouTube Music with Explo: the songs the library
// lacked are downloaded one by one and join the playlist as each lands. This
// panel follows them on the playlist's own page.
import { attr, escapeHTML } from "./html.js";
import { formatDuration } from "./format.js";

const STATE_LABEL = {
  "queued": "WAITING",
  "downloading": "DOWNLOADING",
  "identifying": "IDENTIFYING",
  "in-library": "IN LIBRARY",
  "needs-review": "NEEDS REVIEW",
  "failed": "FAILED",
  "unavailable": "UNAVAILABLE",
};

export function importCounts(view) {
  const counts = { total: 0, library: 0, waiting: 0, downloading: 0, identifying: 0, review: 0, failed: 0 };
  for (const track of (view && view.tracks) || []) {
    counts.total++;
    switch (track.state) {
      case "in-library": counts.library++; break;
      case "queued": counts.waiting++; break;
      case "downloading": counts.downloading++; break;
      case "identifying": counts.identifying++; break;
      case "needs-review": counts.review++; break;
      default: counts.failed++;
    }
  }
  return counts;
}

// Anything that still moves on its own, so the page keeps following it.
export const importOpen = (view) => {
  const c = importCounts(view);
  return c.waiting + c.downloading + c.identifying > 0;
};

export function playlistImportPanel(view, canRetry) {
  const c = importCounts(view);
  const summary = [c.library + " of " + c.total + " in library", c.downloading && c.downloading + " downloading", c.waiting && c.waiting + " waiting",
    c.identifying && c.identifying + " identifying", c.review && c.review + " need review", c.failed && c.failed + " failed",
    view.unavailable && view.unavailable + " not playable on YouTube Music"].filter(Boolean).join(" · ");
  const source = view.sourceUrl ? ' · <a href="' + attr(view.sourceUrl) + '" target="_blank" rel="noopener noreferrer">open on youtube music</a>' : "";
  const pending = view.tracks.filter((track) => track.state !== "in-library");
  const rows = pending.map((track) => {
    const meta = [track.artist, track.album, track.durationMs > 0 ? formatDuration(track.durationMs / 1000) : ""].filter(Boolean).join(" · ");
    return '<div class="list-row">' +
      '<div class="main"><div class="name">' + String(track.position + 1).padStart(2, "0") + " · " + escapeHTML(track.title) + '</div>' +
        '<div class="meta">' + escapeHTML(meta) + '</div>' +
        (track.message ? '<div class="meta" role="status">' + escapeHTML(track.message) + '</div>' : "") +
      '</div>' +
      '<span class="pill">' + (STATE_LABEL[track.state] || escapeHTML(String(track.state).toUpperCase())) + '</span>' +
    '</div>';
  }).join("");
  const retry = canRetry && c.failed ?
    '<div class="view-actions"><button class="btn ghost btn-small" data-action="playlist-import-retry" data-id="' + attr(view.playlistId) + '">RETRY ' + c.failed + (c.failed === 1 ? " SONG" : " SONGS") + '</button></div>' : "";
  return '<div class="section-row" id="playlistImportPanel" data-playlist-id="' + attr(view.playlistId) + '">' +
    '<div class="section-label">// from youtube music' + source + '</div>' +
    '<div class="status-line" role="status" aria-live="polite">' + escapeHTML(summary) +
      (pending.length ? " · songs join the playlist in YouTube Music's order as they reach your library" : "") + '</div>' +
    retry +
    (rows ? '<div class="list">' + rows + '</div>' : "") +
  '</div>';
}
