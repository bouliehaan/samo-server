// Stream tokens and the media URLs built from them.
//
// A stream token is a short-lived credential minted by the server. It exists
// because <audio src> and <img src> cannot carry an Authorization header, so
// the bearer would otherwise have to go in the URL — where it leaks via
// Referer and every access log. The stream token limits that blast radius to
// 30 minutes.
//
// This module is the only place that holds one, and every media URL in the UI
// is built here so none of them can forget to carry it.

import { api } from "./auth.js";

let streamToken = "";
let streamTokenExpiresAt = 0;
let streamTokenPromise = null;

export async function refreshStreamToken() {
  const result = await api("/api/v1/auth/stream-token", { method: "POST" });
  streamToken = result.token || "";
  streamTokenExpiresAt = new Date(result.expiresAt || 0).getTime();
  return streamToken;
}

export async function ensureStreamToken() {
  // 60s safety margin so requests in flight don't race expiry.
  if (streamToken && Date.now() < streamTokenExpiresAt - 60000) return streamToken;
  if (!streamTokenPromise) {
    // One refresh in flight at a time: a page that renders forty covers at
    // once would otherwise mint forty tokens.
    streamTokenPromise = refreshStreamToken().finally(() => { streamTokenPromise = null; });
  }
  return streamTokenPromise;
}

export function streamQuery() {
  return streamToken ? "?stream_token=" + encodeURIComponent(streamToken) : "";
}

// Every media URL the UI hands to <audio> or <img>. They live here so the
// stream token cannot be forgotten by one of them — radioCoverURL in
// particular has to splice it onto a URL the station supplied, which is why
// it reads the token rather than appending streamQuery().

export function musicStreamURL(id) {
  return "/api/v1/music/tracks/" + encodeURIComponent(id) + "/stream" + streamQuery();
}

export function musicCoverURL(id) {
  return "/api/v1/music/albums/" + encodeURIComponent(id) + "/cover" + streamQuery();
}

export function musicPlaylistCoverURL(id, bust) {
  let url = "/api/v1/music/playlists/" + encodeURIComponent(id) + "/cover" + streamQuery();
  if (bust) url += (url.includes("?") ? "&" : "?") + "_=" + bust;
  return url;
}

export function audiobookStreamURL(id) {
  return "/api/v1/audiobooks/" + encodeURIComponent(id) + "/stream" + streamQuery();
}

export function audiobookStreamURLAt(id, atSeconds) {
  const base = audiobookStreamURL(id);
  const at = Math.max(0, Math.floor(atSeconds || 0));
  if (at <= 0) return base;
  return base + (base.includes("?") ? "&" : "?") + "at=" + at;
}

export function audiobookCoverURL(id) {
  return "/api/v1/audiobooks/" + encodeURIComponent(id) + "/cover" + streamQuery();
}

export function podcastCoverURL(id, bust) {
  let url = "/api/v1/podcasts/shows/" + encodeURIComponent(id) + "/cover" + streamQuery();
  if (bust) url += (url.includes("?") ? "&" : "?") + "_=" + bust;
  return url;
}

export function radioCoverURL(station) {
  if (!station) return "";
  if (station.coverUrl) {
    if (streamToken) {
      const sep = station.coverUrl.includes("?") ? "&" : "?";
      return station.coverUrl + sep + "stream_token=" + encodeURIComponent(streamToken);
    }
    return station.coverUrl;
  }
  if (station.coverId) {
    return "/api/v1/media/covers/" + encodeURIComponent(station.coverId) + "/image" + streamQuery();
  }
  return station.imageUrl || "";
}

export function podcastEpisodeStreamURL(id) {
  return "/api/v1/podcasts/episodes/" + encodeURIComponent(id) + "/stream" + streamQuery();
}

export function podcastEpisodeStreamURLAt(id, atSeconds) {
  const base = podcastEpisodeStreamURL(id);
  const at = Math.max(0, Math.floor(atSeconds || 0));
  if (at <= 0) return base;
  return base + (base.includes("?") ? "&" : "?") + "offsetSeconds=" + at;
}

export function channelStreamURL(channelID) {
  return "/channels/" + encodeURIComponent(channelID) + "/stream" + streamQuery();
}
