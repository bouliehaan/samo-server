import { api, isAdmin } from "./auth.js";
import { attr, escapeHTML } from "./html.js";
import { activeJob, jobLabel, jobStatus, libraryState, openJob, providerName } from "./explo_job.js";
import { formatDuration } from "./format.js";
import { exploArtURL } from "./stream.js";

// Retain active requests across visits to Search, without browser credentials
// or a second connection to Explo. The server owns every integration request.
const jobs = new Map();
// Whole albums: album id → {album, view}, where album is what the search
// result showed and view is samo's record of the request, one job per track.
const albumJobs = new Map();
function restore(key, into, valid) {
  try { for (const item of JSON.parse(sessionStorage.getItem(key) || "[]")) { if (valid(item)) into.set(item.id || item.album.id, item); } } catch { /* Browser storage can be unavailable. */ }
}
restore("explo-downloads", jobs, (job) => job.id && job.song);
restore("explo-album-downloads", albumJobs, (item) => item.album && item.album.id && item.view);
function saveJobs() {
  try {
    sessionStorage.setItem("explo-downloads", JSON.stringify([...jobs.values()].slice(-100)));
    sessionStorage.setItem("explo-album-downloads", JSON.stringify([...albumJobs.values()].slice(-20)));
  } catch { /* Keep in-memory tracking. */ }
}
// Covers already shown, and covers that failed to load, so a re-render
// neither flashes the placeholder nor asks again at once. A failure is only
// remembered for a while: it may be an album with no cover, or a source that
// refused the server for a few minutes (Deezer's image CDN does that to the
// samo box's VPN), and the next search after that should see the art.
const loadedCovers = new Set();
const missingCovers = new Map();
const MISSING_COVER_RETRY_MS = 2 * 60 * 1000;
const coverMissing = (albumId) => Date.now() - (missingCovers.get(albumId) || 0) < MISSING_COVER_RETRY_MS;

// albumId: a MusicBrainz release group, or deezer-<n> for an album only
// Deezer lists; samo fetches either catalog's cover.
function cover(albumId) {
  if (!albumId || coverMissing(albumId)) return '<div class="list-thumb explo-cover empty" aria-hidden="true"></div>';
  return '<div class="list-thumb explo-cover' + (loadedCovers.has(albumId) ? "" : " empty") + '" aria-hidden="true">' +
    '<img alt="" loading="lazy" decoding="async" data-cover="' + attr(albumId) + '" src="' + attr(exploArtURL(albumId)) + '"></div>';
}
// Results MusicBrainz does not list come from Deezer, and are identified from
// that listing rather than by their audio; say so where they appear.
const fromDeezer = (item) => item.source === "deezer" ? "Deezer" : "";

const tracksOf = (view) => (view && view.tracks) || [];
function albumCounts(view) {
  const counts = {total: tracksOf(view).length, library: 0, review: 0, failed: 0, downloading: 0, queued: 0, identifying: 0};
  for (const track of tracksOf(view)) {
    const job = track.job;
    if (!job) continue;
    const library = libraryState(job);
    if (job.state === "failed") counts.failed++;
    else if (job.state === "queued") counts.queued++;
    else if (job.state === "downloading") counts.downloading++;
    else if (library === "in-library") counts.library++;
    else if (library === "needs-review") counts.review++;
    else counts.identifying++;
  }
  return counts;
}
const albumOpen = (view) => tracksOf(view).some((track) => track.job && openJob(track.job));
const albumActive = (view) => tracksOf(view).some((track) => track.job && activeJob(track.job));
function albumLabel(view) {
  if (!view) return "ADD ALBUM";
  const counts = albumCounts(view);
  if (albumOpen(view)) return "ADDING…";
  // An action on the failed tracks, not a report that a retry failed.
  if (counts.failed) return "RETRY " + counts.failed + (counts.failed === 1 ? " TRACK" : " TRACKS");
  if (counts.review) return "NEEDS REVIEW";
  return "IN LIBRARY";
}
function albumStatus(view) {
  const c = albumCounts(view);
  return [c.library + " of " + c.total + " in library", c.downloading && c.downloading + " downloading", c.queued && c.queued + " waiting",
    c.identifying && c.identifying + " identifying", c.review && c.review + " need review", c.failed && c.failed + " failed"].filter(Boolean).join(" · ");
}
function albumMeta(album) {
  return [album.artist, album.year, [album.type, ...(album.secondaryTypes || [])].filter(Boolean).join(" + "), fromDeezer(album)].filter(Boolean).join(" · ");
}
const trackNumber = (number, disc, discs) => (discs ? disc + "-" : "") + String(number || 0).padStart(2, "0");

export function mountExploSearch(host, library, libraryInput) {
  let available = false;
  let albumsAvailable = false;
  let mode = "library";
  // The kind last chosen, shown once this Explo can search albums at all.
  let preferredKind = "songs";
  try { if (sessionStorage.getItem("explo-search-kind") === "albums") preferredKind = "albums"; } catch { /* default */ }
  let kind = "songs";
  let revision = 0;
  let songs = [];
  let albums = [];
  let providers = [];
  let searching = false;
  const adding = new Set();
  const expanded = new Set();
  // Track lists of albums not requested yet: album id → {tracks} | {loading} | {error}.
  const details = new Map();
  host.innerHTML = '<p class="panel-sub" data-connection-status role="status" hidden></p>' + '<div class="actions" id="searchModes" hidden>' +
    '<button type="button" class="pill active" data-mode="library" aria-pressed="true">MY LIBRARY</button>' +
    '<button type="button" class="pill" data-mode="new" aria-pressed="false">SEARCH FOR NEW</button></div>' +
    '<div class="explo-song-search" hidden>' +
    '<p class="panel-sub">Find songs and whole albums through Explo. samo identifies each download, fetches its artwork and adds it to your library.</p>' +
    '<div class="pill-bar explo-kinds" hidden>' +
    '<button type="button" class="pill" data-kind="songs" aria-pressed="false">SONGS</button>' +
    '<button type="button" class="pill" data-kind="albums" aria-pressed="false">ALBUMS</button></div>' +
    '<form class="search-form"><input type="search" aria-label="Search for new music" minlength="2" maxlength="200" required>' +
    '<button class="btn primary" type="submit">SEARCH</button></form>' +
    '<label class="explo-provider">Download with <select aria-label="Download provider"><option value="auto">Auto · YouTube first, then Soulseek</option></select></label>' +
    '<div class="status-line" role="status" aria-live="polite" hidden></div>' +
    '<div class="explo-song-results"></div></div>';
  const modes = host.querySelector("#searchModes");
  const connectionStatus = host.querySelector("[data-connection-status]");
  const panel = host.querySelector(".explo-song-search");
  const kinds = panel.querySelector(".explo-kinds");
  const form = panel.querySelector("form");
  const input = form.querySelector("input");
  const submit = form.querySelector("button");
  const message = panel.querySelector("[role=status]");
  const results = panel.querySelector(".explo-song-results");
  const provider = panel.querySelector("select");

  function showMessage(text) {
    message.textContent = text;
    message.hidden = !text;
  }
  function select(next) {
    mode = next;
    panel.hidden = mode !== "new";
    library.hidden = mode !== "library";
    modes.querySelectorAll("button").forEach((button) => {
      const active = button.dataset.mode === mode;
      button.classList.toggle("active", active);
      button.setAttribute("aria-pressed", String(active));
    });
    if (mode === "new") {
      if (!input.value) input.value = libraryInput.value;
      input.focus();
    } else libraryInput.focus();
  }
  modes.addEventListener("click", (event) => {
    const button = event.target.closest("button[data-mode]");
    if (button && (available || jobs.size || albumJobs.size)) select(button.dataset.mode);
  });
  function showKind() {
    const albumsShown = albumsAvailable || albumJobs.size > 0;
    kind = albumsShown ? preferredKind : "songs";
    kinds.hidden = !albumsShown;
    kinds.querySelectorAll("button").forEach((button) => {
      const active = button.dataset.kind === kind;
      button.classList.toggle("active", active);
      button.setAttribute("aria-pressed", String(active));
    });
    input.placeholder = kind === "albums" ? "Artist or album title" : "Artist or song title";
  }
  kinds.addEventListener("click", (event) => {
    const button = event.target.closest("button[data-kind]");
    if (!button || button.dataset.kind === kind) return;
    preferredKind = button.dataset.kind;
    try { sessionStorage.setItem("explo-search-kind", preferredKind); } catch { /* default next time */ }
    showKind();
    showMessage("");
    render();
    if (input.value.trim().length >= 2 && available) form.requestSubmit();
    input.focus();
  });
  // Covers are <img>s so a missing one can fall back to the placeholder.
  results.addEventListener("load", (event) => {
    const img = event.target;
    if (img.tagName !== "IMG" || !img.dataset.cover) return;
    loadedCovers.add(img.dataset.cover);
    img.parentElement.classList.remove("empty");
  }, true);
  results.addEventListener("error", (event) => {
    const img = event.target;
    if (img.tagName !== "IMG" || !img.dataset.cover) return;
    missingCovers.set(img.dataset.cover, Date.now());
    img.remove();
  }, true);

  function renderSongs() {
    const items = new Map(songs.map((song) => [song.id, song]));
    jobs.forEach((job) => items.set(job.id, job.song));
    return [...items.values()].map((song) => {
      const job = jobs.get(song.id);
      const busy = adding.has(song.id) || (job && job.state !== "failed");
      const label = jobLabel(job);
      const album = job && libraryState(job) === "in-library" && job.library.libraryAlbumId;
      const meta = [song.artist, song.album, song.durationMs > 0 ? formatDuration(song.durationMs / 1000) : "Duration unknown", fromDeezer(song)].filter(Boolean).join(" · ");
      return '<div class="list-row explo-song-row">' + cover(song.albumId) + '<div class="main"><div class="name">' + escapeHTML(song.title) + '</div>' +
        '<div class="meta">' + escapeHTML(meta) + '</div>' +
        (job ? '<div class="meta" role="status">' + escapeHTML(jobStatus(job)) +
        (album ? ' · <a href="#music/album/' + attr(encodeURIComponent(album)) + '">open album</a>' : '') + '</div>' +
        (job.attempts || []).filter((attempt) => !(job.message || "").includes(attempt.message)).map((attempt) => '<div class="meta">' + escapeHTML(attempt.message) + '</div>').join("") : '') +
        '</div><button type="button" class="btn ghost btn-mini" data-song="' + attr(song.id) + '"' + (busy || !available ? ' disabled' : '') + '>' + label + '</button></div>';
    }).join("");
  }
  function renderTracks(album) {
    const entry = albumJobs.get(album.id);
    const detail = details.get(album.id);
    let tracks = [];
    if (entry) {
      tracks = tracksOf(entry.view).map((track) => ({number: track.albumTrack && track.albumTrack.number, disc: track.albumTrack && track.albumTrack.disc,
        title: track.title, artist: track.artist, durationMs: track.durationMs, job: track.job}));
    } else if (detail && detail.tracks) {
      tracks = detail.tracks.map((track) => ({number: track.albumTrack && track.albumTrack.number, disc: track.albumTrack && track.albumTrack.disc,
        title: track.title, artist: track.artist, durationMs: track.durationMs, job: track.job}));
    } else {
      return '<div class="explo-album-tracks"><div class="meta">' + escapeHTML(detail && detail.error ? detail.error : "Loading the track list…") + '</div></div>';
    }
    const discs = tracks.some((track) => track.disc > 1);
    const credited = album.artist || (entry && entry.view.artist) || "";
    return '<div class="explo-album-tracks">' + tracks.map((track) => {
      const meta = [track.artist && track.artist !== credited ? track.artist : "", track.durationMs > 0 ? formatDuration(track.durationMs / 1000) : ""].filter(Boolean);
      // The album's own row links to it in the library; every track goes there.
      const status = track.job ? jobStatus(track.job) : "";
      return '<div class="list-row explo-track-row"><div class="num">' + trackNumber(track.number, track.disc, discs) + '</div>' +
        '<div class="main"><div class="name">' + escapeHTML(track.title) + '</div>' +
        (meta.length ? '<div class="meta">' + escapeHTML(meta.join(" · ")) + '</div>' : '') +
        (status ? '<div class="meta" role="status">' + escapeHTML(status) + '</div>' : '') +
        '</div></div>';
    }).join("") + '</div>';
  }
  function renderAlbums() {
    const items = new Map(albums.map((album) => [album.id, album]));
    albumJobs.forEach((entry, id) => { if (!items.has(id)) items.set(id, entry.album); });
    return [...items.values()].map((album) => {
      const entry = albumJobs.get(album.id);
      const view = entry && entry.view;
      const counts = view ? albumCounts(view) : null;
      const busy = adding.has(album.id) || (view && (albumOpen(view) || !counts.failed));
      const open = expanded.has(album.id);
      const libraryAlbum = view && tracksOf(view).map((track) => track.job && libraryState(track.job) === "in-library" && track.job.library.libraryAlbumId).find(Boolean);
      return '<div class="explo-album' + (open ? ' open' : '') + '">' +
        '<div class="list-row explo-song-row explo-album-row">' + cover(album.id) +
        '<div class="main"><div class="name">' + escapeHTML(album.title) + '</div>' +
        '<div class="meta">' + escapeHTML(albumMeta(album)) + '</div>' +
        (view ? '<div class="meta" role="status">' + escapeHTML(view.message || albumStatus(view)) +
          (libraryAlbum ? ' · <a href="#music/album/' + attr(encodeURIComponent(libraryAlbum)) + '">open album</a>' : '') + '</div>' : '') +
        '</div><div class="actions">' +
        '<button type="button" class="btn ghost btn-mini" data-tracks="' + attr(album.id) + '" aria-expanded="' + open + '">' + (open ? "HIDE TRACKS" : "TRACKS") + '</button>' +
        '<button type="button" class="btn ghost btn-mini" data-album="' + attr(album.id) + '"' + (busy || !available ? ' disabled' : '') + '>' + albumLabel(view) + '</button>' +
        '</div></div>' + (open ? renderTracks(album) : '') + '</div>';
    }).join("");
  }
  function render() {
    const html = kind === "albums" ? renderAlbums() : renderSongs();
    results.innerHTML = html ? '<div class="list">' + html + '</div>' : "";
  }

  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    const query = input.value.trim();
    if (!available || query.length < 2) return;
    const request = ++revision;
    const searchingFor = kind;
    searching = true;
    submit.disabled = true;
    showMessage(searchingFor === "albums" ? "Searching for albums…" : "Searching for new songs…");
    try {
      if (searchingFor === "albums") {
        const data = await api("/api/v1/explo/albums?q=" + encodeURIComponent(query));
        if (!host.isConnected || request !== revision) return;
        albums = data.albums || [];
        showMessage(albums.length ? "Open an album's tracks, or add the whole album." : "No albums found. Try an artist and album title.");
      } else {
        const data = await api("/api/v1/explo/search?q=" + encodeURIComponent(query));
        if (!host.isConnected || request !== revision) return;
        songs = data.songs || [];
        showMessage(songs.length ? "Choose a song to download." : "No songs found. Try an artist and song title.");
      }
      render();
    } catch (err) {
      if (host.isConnected && request === revision) showMessage(err.message);
    } finally {
      if (request === revision) { searching = false; submit.disabled = !available; }
    }
  });
  async function loadTracks(id) {
    if (albumJobs.has(id) || (details.has(id) && !details.get(id).error)) return;
    details.set(id, {loading: true});
    render();
    try {
      const album = await api("/api/v1/explo/albums/" + encodeURIComponent(id));
      details.set(id, {tracks: album.tracks || []});
    } catch (err) {
      details.set(id, {error: err.message});
    }
    if (host.isConnected) render();
  }
  results.addEventListener("click", async (event) => {
    const toggle = event.target.closest("button[data-tracks]");
    if (toggle) {
      const id = toggle.dataset.tracks;
      if (expanded.has(id)) expanded.delete(id);
      else { expanded.add(id); loadTracks(id); }
      render();
      return;
    }
    const albumButton = event.target.closest("button[data-album]");
    if (albumButton) {
      if (albumButton.disabled || !available) return;
      const id = albumButton.dataset.album;
      if (adding.has(id)) return;
      const album = albums.find((item) => item.id === id) || (albumJobs.get(id) || {}).album;
      if (!album) return;
      adding.add(id);
      albumButton.disabled = true;
      albumButton.textContent = "ADDING…";
      showMessage("Asking Explo for every track of " + album.title + "… this looks the album up first, so it can take a few seconds.");
      try {
        const view = await api("/api/v1/explo/albums/" + encodeURIComponent(id) + "/downloads", {method: "POST", body: {provider: provider.value}});
        albumJobs.set(id, {album, view});
        expanded.add(id);
        saveJobs();
        if (host.isConnected) showMessage(view.message || "Album requested. Its tracks download one at a time; follow them below.");
      } catch (err) {
        if (host.isConnected) showMessage(err.message);
      } finally { adding.delete(id); if (host.isConnected) { showKind(); render(); } }
      return;
    }
    const button = event.target.closest("button[data-song]");
    if (!button || button.disabled || !available) return;
    const id = button.dataset.song;
    if (adding.has(id)) return;
    adding.add(id);
    button.disabled = true;
    button.textContent = "ADDING…";
    try {
      const job = await api("/api/v1/explo/downloads", {method: "POST", body: {id, provider: provider.value}});
      jobs.set(job.id, job);
      saveJobs();
      if (host.isConnected) { showMessage("Request accepted. Follow its status below."); render(); }
    } catch (err) {
      if (host.isConnected) { showMessage(err.message); button.disabled = false; button.textContent = "RETRY"; }
    } finally { adding.delete(id); if (host.isConnected) render(); }
  });
  // The first refresh reads every album kept from earlier, settled or not:
  // a retry from another tab, or a keep that finished while this page was
  // closed, would otherwise never show.
  let firstRefresh = true;
  const anyActive = () => [...jobs.values()].some(activeJob) || [...albumJobs.values()].some((entry) => albumActive(entry.view));
  async function refresh() {
    if (!host.isConnected) return;
    try {
      const status = await api("/api/v1/explo/discovery/status");
      if (!host.isConnected) return;
      available = status.available === true;
      albumsAvailable = available && status.albums === true;
      connectionStatus.hidden = available || !status.configured || !isAdmin();
      connectionStatus.textContent = status.reason || "";
      modes.hidden = !available && jobs.size === 0 && albumJobs.size === 0;
      showKind();
      if (!available && mode === "new") showMessage(status.reason || "Explo is temporarily unavailable. Download history is retained.");
      if (JSON.stringify(providers) !== JSON.stringify(status.providers || [])) {
        providers = status.providers || [];
        const selected = provider.value;
        provider.innerHTML = '<option value="auto">Auto · ' + escapeHTML(providers.map(providerName).join(" → ")) + '</option>' + providers.map((p) => '<option value="' + attr(p) + '">' + escapeHTML(providerName(p)) + '</option>').join("");
        if (providers.includes(selected)) provider.value = selected;
      }
      submit.disabled = !available || searching;
      await Promise.all([
        ...[...jobs.values()].filter((job) => openJob(job)).map(async (job) => {
          try { jobs.set(job.id, await api("/api/v1/explo/downloads/" + encodeURIComponent(job.id))); }
          catch (err) {
            if (err.status === 404) jobs.set(job.id, {...job, state: "failed", message: "Neither Explo nor samo tracks this request any more. Search again before retrying."});
            else if (host.isConnected) showMessage(err.message);
          }
        }),
        ...[...albumJobs.entries()].filter(([, entry]) => firstRefresh || albumOpen(entry.view)).map(async ([id, entry]) => {
          try { albumJobs.set(id, {album: entry.album, view: {...await api("/api/v1/explo/albums/" + encodeURIComponent(id) + "/download"), message: ""}}); }
          catch (err) {
            if (err.status === 404) albumJobs.delete(id);
            else if (host.isConnected) showMessage(err.message);
          }
        }),
      ]);
      firstRefresh = false;
      saveJobs();
      if (host.isConnected) render();
    } catch {
      available = false;
      modes.hidden = jobs.size === 0 && albumJobs.size === 0;
      submit.disabled = true;
      render();
      if (mode === "new") showMessage("Connection interrupted. Retrying; download history is retained.");
    } finally {
      if (host.isConnected) setTimeout(refresh, anyActive() ? 2000 : 10000);
    }
  }
  showKind();
  render();
  refresh();
}
