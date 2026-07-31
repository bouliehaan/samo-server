// The composer: the slide-out form behind every create/edit flow.
//
// One shell (composerHTML) and one field builder (fieldHTML) back a dozen
// concrete forms, so a change to field markup or focus handling lands in
// all of them at once.

import { attr, escapeHTML, setMessage } from "./html.js";
import { podcastTitle } from "./labels.js";

/* ---- Inline composer helpers ----
 * Add-flows live in the same view as the list they extend. composerHTML
 * renders a hidden panel; toggleComposer(id) reveals/hides it. The button
 * that triggers the composer carries data-composer="<id>" so the global
 * click handler can wire it up generically. */
export function composerHTML(id, head, body, hint) {
  return '<div class="composer" id="composer-' + attr(id) + '" hidden>' +
    '<div class="composer-head"><span>// ' + head + '</span>' +
      '<button type="button" class="composer-close" data-action="composer-toggle" data-composer="' + attr(id) + '" aria-label="Close">×</button>' +
    '</div>' +
    body +
    (hint ? '<div class="composer-hint">' + hint + '</div>' : "") +
    '<div class="status-line" id="composer-' + attr(id) + '-message" hidden></div>' +
  '</div>';
}

export function fieldHTML(id, label, placeholder, type, value, cls) {
  return '<label class="field ' + (cls || "") + '"><span class="field-label">' + escapeHTML(label) + '</span>' +
    '<input id="' + attr(id) + '" type="' + attr(type || "text") + '" placeholder="' + attr(placeholder || "") + '" value="' + attr(value || "") + '">' +
  '</label>';
}

export function textAreaHTML(id, label, placeholder, value, cls) {
  return '<label class="field ' + (cls || "") + '"><span class="field-label">' + escapeHTML(label) + '</span>' +
    '<textarea id="' + attr(id) + '" rows="8" placeholder="' + attr(placeholder || "") + '">' + escapeHTML(value || "") + '</textarea>' +
  '</label>';
}

export function toggleComposer(id) {
  const el = document.getElementById("composer-" + id);
  if (!el) return;
  const opening = el.hidden;
  el.hidden = !el.hidden;
  if (opening) {
    const first = el.querySelector("input, select, textarea");
    if (first) first.focus();
  }
}

export function composerClose(id) {
  const el = document.getElementById("composer-" + id);
  if (el) el.hidden = true;
}

export function composerMessage(id, message, bad) {
  setMessage("composer-" + id + "-message", message, bad);
}

export function composerLibrary() {
  const body =
    '<div class="composer-row">' +
      fieldHTML("composerLibPath", "Path", "/srv/media", "text", "", "full") +
    '</div>' +
    '<div class="composer-row">' +
      '<label class="field"><span class="field-label">Kind</span><select id="composerLibKind">' +
        '<option value="mixed">Mixed (auto-detect)</option>' +
        '<option value="music">Music only</option>' +
        '<option value="audiobook">Audiobooks</option>' +
        '<option value="podcast">Podcasts</option>' +
      '</select></label>' +
      fieldHTML("composerLibName", "Name", "autodetect", "text", "") +
    '</div>' +
    '<div class="composer-actions">' +
      '<button class="btn primary" data-action="composer-submit" data-composer="library">ATTACH LIBRARY</button>' +
      '<button class="btn ghost" data-action="composer-toggle" data-composer="library">CANCEL</button>' +
    '</div>';
  return composerHTML("library", "ATTACH MEDIA FOLDER", body,
    "// pick Mixed if you're not sure — Samo will classify subfolders for you");
}

export function composerPlaylist() {
  const body =
    '<div class="composer-row">' +
      fieldHTML("composerPlaylistName", "Name", "Road mix", "text", "") +
      '<label class="field checkbox"><input id="composerPlaylistPublic" type="checkbox"><span>Public</span></label>' +
    '</div>' +
    '<div class="composer-row">' +
      fieldHTML("composerPlaylistDescription", "Description", "optional", "text", "", "full") +
    '</div>' +
    '<div class="composer-actions">' +
      '<button class="btn primary" data-action="composer-submit" data-composer="playlist">CREATE PLAYLIST</button>' +
      '<button class="btn ghost" data-action="composer-toggle" data-composer="playlist">CANCEL</button>' +
    '</div>';
  return composerHTML("playlist", "NEW SERVER PLAYLIST", body,
    "// create an empty playlist here, then import or patch track IDs through the API");
}

export function composerPlaylistEdit(playlist) {
  const body =
    '<input type="hidden" id="composerPlaylistEditId" value="' + attr(playlist.id || "") + '">' +
    '<div class="composer-row">' +
      fieldHTML("composerPlaylistEditName", "Name", "Road mix", "text", playlist.name || "") +
      '<label class="field checkbox"><input id="composerPlaylistEditPublic" type="checkbox"' + (playlist.public ? " checked" : "") + '><span>Public</span></label>' +
    '</div>' +
    '<div class="composer-row">' +
      textAreaHTML("composerPlaylistEditDescription", "Description", "optional", playlist.description || "", "full") +
    '</div>' +
    '<div class="composer-actions">' +
      '<button class="btn primary" data-action="composer-submit" data-composer="playlist-edit">SAVE PLAYLIST</button>' +
      '<button class="btn ghost" data-action="composer-toggle" data-composer="playlist-edit">CANCEL</button>' +
    '</div>';
  return composerHTML("playlist-edit", "EDIT PLAYLIST", body,
    "// rename, set description, and upload a cover from the artwork slot above");
}

export function composerPlaylistImport() {
  const body =
    '<div class="composer-row">' +
      fieldHTML("composerImportName", "Playlist Name", "Imported mix", "text", "") +
      '<label class="field"><span class="field-label">Format</span><select id="composerImportSource">' +
        '<option value="auto">Auto-detect</option>' +
        '<option value="csv">CSV</option>' +
        '<option value="m3u">M3U / M3U8</option>' +
        '<option value="plain">Plain text</option>' +
        '<option value="json">JSON</option>' +
        '<option value="youtube">YouTube Music URL</option>' +
      '</select></label>' +
      '<label class="field checkbox"><input id="composerImportPublic" type="checkbox"><span>Public</span></label>' +
    '</div>' +
    '<div class="composer-row">' +
      fieldHTML("composerImportURL", "URL", "https://music.youtube.com/playlist?list=...", "url", "", "full") +
      textAreaHTML("composerImportContent", "Pasted Content", "CSV rows, #EXTM3U content, JSON, or plain Artist - Title lines", "", "full") +
    '</div>' +
    '<div class="composer-actions">' +
      '<button class="btn primary" data-action="composer-submit" data-composer="playlist-import">IMPORT</button>' +
      '<button class="btn ghost" data-action="composer-toggle" data-composer="playlist-import">CANCEL</button>' +
    '</div>';
  return composerHTML("playlist-import", "IMPORT PLAYLIST", body,
    "// Samo matches imported metadata to your local music. It does not download remote tracks.");
}

export function composerPodcastFeed() {
  const body =
    '<div class="composer-row">' +
      fieldHTML("composerPodcastTitle", "Title", "optional", "text", "") +
      fieldHTML("composerPodcastURL", "Feed URL", "https://example.com/feed.xml", "url", "") +
    '</div>' +
    '<div class="composer-row">' +
      '<label class="field checkbox full"><input id="composerPodcastAutoDownload" type="checkbox"><span>Auto-download new episodes</span></label>' +
    '</div>' +
    '<div class="composer-actions">' +
      '<button class="btn primary" data-action="composer-submit" data-composer="podcast-feed">ADD FEED</button>' +
      '<button class="btn ghost" data-action="composer-toggle" data-composer="podcast-feed">CANCEL</button>' +
    '</div>';
  return composerHTML("podcast-feed", "NEW PODCAST FEED", body,
    "// the title field is optional — Samo will read it from the RSS feed");
}

export function composerPodcastAttachFeed(podcastID, suggestedURL) {
  const body =
    '<input type="hidden" id="composerPodcastAttachShowId" value="' + attr(podcastID || "") + '">' +
    '<div class="composer-row">' +
      fieldHTML("composerPodcastAttachURL", "RSS feed URL", "https://feeds.example.com/podcast.xml", "url", suggestedURL || "", "full") +
    '</div>' +
    '<div class="composer-row">' +
      '<label class="field checkbox full"><input id="composerPodcastAttachAutoDownload" type="checkbox"><span>Auto-download new episodes</span></label>' +
    '</div>' +
    '<div class="composer-actions">' +
      '<button class="btn primary" data-action="composer-submit" data-composer="podcast-attach-feed">LINK RSS FEED</button>' +
      '<button class="btn ghost" data-action="composer-toggle" data-composer="podcast-attach-feed">CANCEL</button>' +
    '</div>';
  return composerHTML("podcast-attach-feed", "LINK RSS TO LIBRARY PODCAST", body,
    "// keeps your downloaded files · matches RSS episodes to local files · fixes release dates from the feed");
}

/* Composer markup factories. Each returns the HTML for a self-contained
 * panel. The shared submit handler reads field values, posts to the API,
 * then closes the composer and refreshes the view. */
export function composerRadioStation() {
  const body =
    '<div class="composer-row">' +
      fieldHTML("composerRadioName", "Name", "WFMU", "text", "") +
      fieldHTML("composerRadioStream", "Stream URL", "https://example.com/live.mp3", "url", "") +
    '</div>' +
    '<div class="composer-row">' +
      fieldHTML("composerRadioHomepage", "Homepage", "https://example.com", "url", "") +
      '<label class="field"><span class="field-label">Cover image</span><input id="composerRadioCover" type="file" accept="image/*"></label>' +
    '</div>' +
    '<div class="composer-row">' +
      '<label class="field"><span class="field-label">Tags</span>' +
        '<input id="composerRadioTags" type="text" placeholder="jazz, late night" data-tags-target="composerRadioTagsPreview">' +
        '<div class="tag-preview" id="composerRadioTagsPreview"><span class="tag-preview-empty">// chips appear as you type</span></div>' +
      '</label>' +
    '</div>' +
    '<div class="composer-actions">' +
      '<button class="btn primary" data-action="composer-submit" data-composer="radio-station">ATTACH STATION</button>' +
      '<button class="btn ghost" data-action="composer-toggle" data-composer="radio-station">CANCEL</button>' +
    '</div>';
  return composerHTML("radio-station", "NEW INTERNET RADIO STATION", body,
    "// stream URL is required · Samo will probe it for live metadata after attach");
}

export function composerChannel() {
  const body =
    '<div class="composer-row">' +
      fieldHTML("composerChannelName", "Name", "Jake's Radio", "text", "") +
      fieldHTML("composerChannelDescription", "Description", "optional", "text", "") +
    '</div>' +
    '<div class="composer-row">' +
      '<label class="field"><span class="field-label">Codec</span><select id="composerChannelCodec">' +
        '<option value="mp3">MP3 (broad compatibility)</option>' +
        '<option value="aac">AAC</option>' +
        '<option value="opus">OPUS</option>' +
      '</select></label>' +
      fieldHTML("composerChannelBitrate", "Bitrate (kbps)", "192", "number", "192") +
    '</div>' +
    '<div class="composer-actions">' +
      '<button class="btn primary" data-action="composer-submit" data-composer="channel">CREATE CHANNEL</button>' +
      '<button class="btn ghost" data-action="composer-toggle" data-composer="channel">CANCEL</button>' +
    '</div>';
  return composerHTML("channel", "NEW PERSONAL CHANNEL", body,
    "// pick a codec your clients support — MP3 is the safest default for browsers and most apps");
}

export function composerChannelSchedule(channelID, sources) {
  const sourceOptions = sources.map((s) => '<option value="' + attr(s.id) + '">' + escapeHTML(s.label || s.kind) + ' · ' + escapeHTML(s.kind) + '</option>').join("");
  const body =
    '<div class="composer-row">' +
      fieldHTML("composerSchedLabel", "Label", "ATC Weekdays", "text", "") +
      '<label class="field"><span class="field-label">Source</span><select id="composerSchedSource">' + sourceOptions + '</select></label>' +
    '</div>' +
    '<div class="composer-row">' +
      fieldHTML("composerSchedStart", "Start (HH:MM)", "16:00", "text", "") +
      fieldHTML("composerSchedEnd", "End (HH:MM)", "17:00", "text", "") +
      fieldHTML("composerSchedPriority", "Priority", "200", "number", "200") +
    '</div>' +
    '<div class="composer-row">' +
      '<label class="field"><span class="field-label">Days</span><select id="composerSchedDays">' +
        '<option value="127">EVERY DAY</option>' +
        '<option value="62">WEEKDAYS (MON–FRI)</option>' +
        '<option value="65">WEEKENDS (SAT+SUN)</option>' +
        '<option value="2">MONDAY</option>' +
        '<option value="4">TUESDAY</option>' +
        '<option value="8">WEDNESDAY</option>' +
        '<option value="16">THURSDAY</option>' +
        '<option value="32">FRIDAY</option>' +
        '<option value="64">SATURDAY</option>' +
        '<option value="1">SUNDAY</option>' +
      '</select></label>' +
    '</div>' +
    '<div class="composer-actions">' +
      '<button class="btn primary" data-action="composer-submit" data-composer="channel-schedule" data-channel-id="' + attr(channelID) + '">ADD RULE</button>' +
      '<button class="btn ghost" data-action="composer-toggle" data-composer="channel-schedule">CANCEL</button>' +
    '</div>';
  return composerHTML("channel-schedule", "NEW SCHEDULE RULE", body,
    "// when the rule's window is active it preempts rotation. Higher priority wins when multiple rules overlap. Cross-midnight windows? Add two rules.");
}

export function composerChannelSourcePodcast(channelID, podcasts) {
  if (!podcasts || podcasts.length === 0) {
    const body =
      '<div class="empty-state" style="margin: 0">// add a podcast feed under PODCASTS first, then come back here to subscribe a channel to it</div>' +
      '<div class="composer-actions">' +
        '<button class="btn ghost" data-action="composer-toggle" data-composer="channel-source-podcast">CLOSE</button>' +
      '</div>';
    return composerHTML("channel-source-podcast", "NEW PODCAST SUBSCRIPTION SOURCE", body, "");
  }
  const options = podcasts.map((p) => {
    const title = podcastTitle(p);
    return '<option value="' + attr(p.id) + '">' + escapeHTML(title) + '</option>';
  }).join("");
  const body =
    '<div class="composer-row">' +
      '<label class="field"><span class="field-label">Podcast</span><select id="composerSrcPodID">' + options + '</select></label>' +
      fieldHTML("composerSrcPodLabel", "Label (optional)", "leave blank to use show title", "text", "") +
    '</div>' +
    '<div class="composer-row">' +
      fieldHTML("composerSrcPodMaxAge", "Max age (days)", "30", "number", "30") +
      fieldHTML("composerSrcPodWeight", "Weight", "1", "number", "1") +
    '</div>' +
    '<div class="composer-actions">' +
      '<button class="btn primary" data-action="composer-submit" data-composer="channel-source-podcast" data-channel-id="' + attr(channelID) + '">SUBSCRIBE</button>' +
      '<button class="btn ghost" data-action="composer-toggle" data-composer="channel-source-podcast">CANCEL</button>' +
    '</div>';
  return composerHTML("channel-source-podcast", "NEW PODCAST SUBSCRIPTION SOURCE", body,
    "// the channel will play the freshest unplayed episode of this show. Max-age skips episodes older than the cutoff.");
}

export function composerChannelSourceInternet(channelID, stations) {
  if (!stations || stations.length === 0) {
    const body =
      '<div class="empty-state" style="margin: 0">// add an internet radio station under RADIO → INTERNET first, then come back here</div>' +
      '<div class="composer-actions">' +
        '<button class="btn ghost" data-action="composer-toggle" data-composer="channel-source-internet">CLOSE</button>' +
      '</div>';
    return composerHTML("channel-source-internet", "NEW INTERNET STATION SOURCE", body, "");
  }
  const options = stations.map((st) => (
    '<option value="' + attr(st.id) + '">' + escapeHTML(st.name) + '</option>'
  )).join("");
  const body =
    '<div class="composer-row">' +
      '<label class="field"><span class="field-label">Station</span><select id="composerSrcInetID">' + options + '</select></label>' +
      fieldHTML("composerSrcInetLabel", "Label (optional)", "leave blank to use station name", "text", "") +
    '</div>' +
    '<div class="composer-row">' +
      '<label class="field checkbox"><input id="composerSrcInetRotation" type="checkbox"><span>Eligible for rotation when no schedule rule is active</span></label>' +
    '</div>' +
    '<div class="composer-actions">' +
      '<button class="btn primary" data-action="composer-submit" data-composer="channel-source-internet" data-channel-id="' + attr(channelID) + '">ATTACH STATION</button>' +
      '<button class="btn ghost" data-action="composer-toggle" data-composer="channel-source-internet">CANCEL</button>' +
    '</div>';
  return composerHTML("channel-source-internet", "NEW INTERNET STATION SOURCE", body,
    "// reuses an existing internet radio station. When the channel cuts to this source, ffmpeg proxies the station's stream URL live.");
}

export function composerChannelSourceFile(channelID) {
  const body =
    '<div class="composer-row">' +
      fieldHTML("composerSrcFileLabel", "Label", "Commercials", "text", "") +
      fieldHTML("composerSrcFileWeight", "Weight", "1", "number", "1") +
    '</div>' +
    '<div class="composer-row">' +
      textAreaHTML("composerSrcFilePaths", "Paths (one per line — files, folders, or globs)", "/data/media/commercials\n/data/media/oldies/*.mp3", "", "full") +
    '</div>' +
    '<div class="composer-actions">' +
      '<button class="btn primary" data-action="composer-submit" data-composer="channel-source-file" data-channel-id="' + attr(channelID) + '">ADD FILE POOL</button>' +
      '<button class="btn ghost" data-action="composer-toggle" data-composer="channel-source-file">CANCEL</button>' +
    '</div>';
  return composerHTML("channel-source-file", "NEW FILE POOL SOURCE", body,
    "// folders are scanned one level deep; globs use shell-style patterns. Paths must be readable by samo-server.");
}

export function composerChannelSourceLive(channelID) {
  const body =
    '<div class="composer-row">' +
      fieldHTML("composerSrcLiveLabel", "Label", "NPR Live", "text", "") +
      fieldHTML("composerSrcLiveURL", "Stream URL", "https://example.com/live.mp3", "url", "") +
    '</div>' +
    '<div class="composer-row">' +
      '<label class="field checkbox"><input id="composerSrcLiveRotation" type="checkbox"><span>Eligible for rotation when no schedule rule is active</span></label>' +
    '</div>' +
    '<div class="composer-actions">' +
      '<button class="btn primary" data-action="composer-submit" data-composer="channel-source-live" data-channel-id="' + attr(channelID) + '">ATTACH LIVE STREAM</button>' +
      '<button class="btn ghost" data-action="composer-toggle" data-composer="channel-source-live">CANCEL</button>' +
    '</div>';
  return composerHTML("channel-source-live", "NEW LIVE STREAM SOURCE", body,
    "// schedule this source via a rule to cut in at specific times (e.g. NPR at 16:00–17:00). Leaving rotation off keeps it from playing outside its window.");
}
