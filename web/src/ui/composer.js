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


// contentPickerHTML is the one control that replaced four separate "add a
// source" forms.
//
// Every kind of content a channel can hold is one dropdown and one field, so
// adding a podcast and adding a folder are the same gesture. The per-kind
// fields all render and all but the selected one start hidden; the change
// handler in app.js swaps them. That keeps the markup static and the
// interaction one line, rather than re-rendering a form on every selection.
// availableContentKinds is the single answer to "what can this channel be
// given", so the picker and the role default cannot disagree about which kind
// is selected when the form opens.
export function availableContentKinds(options) {
  options = options || {};
  return [
    ["podcast-subscription", "PODCAST", (options.podcasts || []).length > 0],
    ["internet-station", "INTERNET STATION", (options.stations || []).length > 0],
    ["music-playlist", "MUSIC PLAYLIST", (options.playlists || []).length > 0],
    ["file-pool", "FILES / FOLDER", true],
    ["live-stream", "LIVE URL", true],
  ].filter(([, , available]) => available);
}

export function defaultContentKind(options) {
  const kinds = availableContentKinds(options);
  return kinds.length > 0 ? kinds[0][0] : "file-pool";
}

// roleForKind is the role a piece of content almost always wants.
//
// A podcast added as filler is not a cosmetic mistake: only podcast-role
// sources supply fresh episodes, so it would quietly never air anything new.
export function roleForKind(kind) {
  return kind === "music-playlist" ? "music" : "talk";
}

export function contentPickerHTML(prefix, options) {
  options = options || {};
  const podcasts = options.podcasts || [];
  const stations = options.stations || [];
  const playlists = options.playlists || [];

  const kinds = availableContentKinds(options);
  const first = defaultContentKind(options);
  const kindOptions = kinds
    .map(([value, label]) => '<option value="' + attr(value) + '">' + escapeHTML(label) + '</option>')
    .join("");

  const group = (kind, inner) =>
    '<div class="composer-row" id="' + attr(prefix) + 'Fields-' + attr(kind) + '"' +
      (kind === first ? "" : " hidden") + '>' + inner + '</div>';

  const selectField = (id, label, items, render) =>
    '<label class="field full"><span class="field-label">' + escapeHTML(label) + '</span>' +
      '<select id="' + attr(id) + '">' +
        items.map(render).join("") +
      '</select></label>';

  let html =
    '<div class="composer-row">' +
      '<label class="field"><span class="field-label">Content</span>' +
        '<select id="' + attr(prefix) + 'Kind" data-action="composer-kind" data-prefix="' + attr(prefix) + '">' +
          kindOptions +
        '</select></label>' +
      fieldHTML(prefix + "Label", "Label (optional)", "leave blank to use its own name", "text", "") +
    '</div>';

  if (podcasts.length > 0) {
    html += group("podcast-subscription",
      selectField(prefix + "Podcast", "Podcast", podcasts,
        (p) => '<option value="' + attr(p.id) + '">' + escapeHTML(podcastTitle(p)) + '</option>'));
  }
  if (stations.length > 0) {
    html += group("internet-station",
      selectField(prefix + "Station", "Station", stations,
        (st) => '<option value="' + attr(st.id) + '">' + escapeHTML(st.name) + '</option>'));
  }
  if (playlists.length > 0) {
    html += group("music-playlist",
      selectField(prefix + "Playlist", "Playlist", playlists,
        (pl) => '<option value="' + attr(pl.id) + '">' + escapeHTML(pl.name) + '</option>'));
  }
  html += group("file-pool",
    textAreaHTML(prefix + "Paths", "Files, folders or globs (one per line)",
      "/mnt/data2tb/commercials\n/mnt/data2tb/oldies/*.mp3", "", "full"));
  html += group("live-stream",
    fieldHTML(prefix + "Url", "Stream URL", "https://example.com/live.mp3", "url", "", "full"));

  return html;
}

// composerChannelShow books a programme: content AND its slot, in one go.
//
// It used to be two disjoint forms — create a source, then create a rule that
// points at it — which meant holding an id in your head between them. A show
// is one thing, so it is one form.
export function composerChannelShow(channelID, options) {
  const body =
    contentPickerHTML("composerShow", options) +
    '<div class="composer-row">' +
      '<label class="field"><span class="field-label">Days</span><select id="composerShowDays">' +
        weekdayOptionsHTML() +
      '</select></label>' +
      fieldHTML("composerShowStart", "Start (HH:MM)", "16:00", "text", "") +
      fieldHTML("composerShowEnd", "End (HH:MM)", "17:00", "text", "") +
    '</div>' +
    '<div class="composer-actions">' +
      '<button class="btn primary" data-action="composer-submit" data-composer="channel-show" data-channel-id="' + attr(channelID) + '">ADD SHOW</button>' +
      '<button class="btn ghost" data-action="composer-toggle" data-composer="channel-show">CANCEL</button>' +
    '</div>';
  return composerHTML("channel-show", "BOOK A SHOW", body,
    "// a show only airs in its slot. Crossing midnight is fine — 22:00 to 06:00 is booked as two windows for you.");
}

// composerChannelContent adds something to the mix: what it is, and its role.
export function composerChannelContent(channelID, options) {
  const body =
    contentPickerHTML("composerContent", options) +
    '<div class="composer-row">' +
      roleSelectHTML("composerContentRole", "", defaultContentKind(options)) +
    '</div>' +
    '<div class="composer-actions">' +
      '<button class="btn primary" data-action="composer-submit" data-composer="channel-content" data-channel-id="' + attr(channelID) + '">ADD TO MIX</button>' +
      '<button class="btn ghost" data-action="composer-toggle" data-composer="channel-content">CANCEL</button>' +
    '</div>';
  return composerHTML("channel-content", "ADD TO THE MIX", body,
    "// the channel plays new episodes first, then falls back to reruns and music. You pick what each thing is; it works out the order.");
}

export function weekdayOptionsHTML() {
  return '<option value="127">EVERY DAY</option>' +
    '<option value="62">WEEKDAYS (MON–FRI)</option>' +
    '<option value="65">WEEKENDS (SAT+SUN)</option>' +
    '<option value="2">MONDAY</option>' +
    '<option value="4">TUESDAY</option>' +
    '<option value="8">WEDNESDAY</option>' +
    '<option value="16">THURSDAY</option>' +
    '<option value="32">FRIDAY</option>' +
    '<option value="64">SATURDAY</option>' +
    '<option value="1">SUNDAY</option>';
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


// roleSelectHTML is the one control that replaced weights-as-priority.
//
// You say what a thing IS; the engine owns the running order. That ordering is
// deliberately not exposed — letting it be tuned is how a music playlist ends
// up outranking a scheduled news block.
export function roleSelectHTML(id, selected, kind) {
  const roles = [
    ["talk", "TALK — podcasts and spoken word"],
    ["music", "MUSIC — counts toward the music share"],
    ["show", "SCHEDULED SHOW — only at its booked times"],
    ["commercial", "COMMERCIAL — padding between items"],
  ];
  const fallback = selected || roleForKind(kind);
  const options = roles.map(([value, label]) =>
    '<option value="' + attr(value) + '"' + (value === fallback ? " selected" : "") + '>' +
      escapeHTML(label) + '</option>').join("");
  // data-role-auto marks a role the form chose rather than the user. The kind
  // switcher keeps updating it while that is set, and stops the moment
  // somebody picks a role themselves.
  return '<label class="field"><span class="field-label">Role</span>' +
    '<select id="' + attr(id) + '" data-action="composer-role" data-role-auto="1">' +
      options +
    '</select></label>';
}
