// Entry point for the app page. Vite bundles this into
// internal/api/web/build/, which go:embed compiles into the binary.
//
// The stylesheet is imported rather than linked so the bundler owns it too:
// it emits one hashed .css per entry alongside the .js. base.css comes in
// via an @import at the top of that stylesheet, which keeps it first in the
// cascade — as a separate shared chunk its order would not be guaranteed.
import "./app.css";

import { api, currentUser, isAdmin, lastFMPendingStorageKey, legacyLastFMPendingKey, loginRedirect, setCurrentUser, token, tokenKey } from "./ui/auth.js";
import { audiobookCoverURL, audiobookStreamURL, audiobookStreamURLAt, channelStreamURL, ensureStreamToken, musicCoverURL, musicPlaylistCoverURL, musicStreamURL, podcastCoverURL, podcastEpisodeStreamURL, podcastEpisodeStreamURLAt, radioCoverURL, refreshStreamToken } from "./ui/stream.js";

import { activityBody, activityPanel, audio, identifyForm, identifyModal, identifyResults, main, nav, nowPlayingBtn, nowPlayingSub, playerDock, playerSeek, playerStop, playerSub, playerTitle, playerToggle, scanPanel } from "./ui/elements.js";

import { connect as connectEvents } from "./ui/events.js";
import { cancelActiveScan, closeScanPanel, configureScanUI, ensureScanAvailable, handleScanJobEvent, openScanPanel, rememberLibraries, triggerLibraryRepair, triggerLibraryScan, triggerScan, updateRefreshUI, watchScanJob } from "./ui/scan.js";
import { applyStreamResumeSeek, clearPlayerTarget, flushPlaybackProgress, patchPlayback, playbackGlobalSeconds, playURL, playerTarget, refreshSeekUI, reportPlaybackProgress, resetProgressThrottle, seekFromPointer, setPlayerGlyph } from "./ui/player.js";
import { closeIdentifyModal, identifyCandidates, identifyContext, openIdentifyModal, runIdentifySearch } from "./ui/identify.js";
import { channelBalanceBody, channelCard, channelOnAirHeader, channelScheduleStatusBody, channelScheduleTimeline } from "./ui/channels.js";
import { owedPanel, planPanel, sourceComposer, whyPanel } from "./ui/plan.js";
import { rankPanel, rankableSource } from "./ui/rank.js";
import { composerSamoRadioDevice, samoRadioDeviceCard, samoRadioSendBar } from "./ui/samo_radio.js";
import { composerChannel, composerChannelContent, composerChannelSchedule, composerChannelShow, composerClose, roleForKind, composerLibrary, composerMessage, composerPlaylist, composerPlaylistEdit, composerPlaylistImport, composerPodcastAttachFeed, composerPodcastFeed, composerRadioStation, fieldHTML, toggleComposer } from "./ui/composer.js";
import { formatDataSize, formatDate, formatDuration, formatUptime, minuteToHHMM, parseHHMM, weekdayMaskToLabel } from "./ui/format.js";
import { attr, escapeHTML, progressBar, setMessage, setStatus, splitTags, tagsLine } from "./ui/html.js";
import { audiobookSub, audiobookTitle, browseAlbums, browseResultCount, candidateFeedURL, isLibraryFolderPodcast, libraryKindLabel, musicPaginationFooter, nowPlayingLine, playlistCoverBlock, podcastHasLinkedFeed, podcastSub, podcastTitle, recentlyAddedKindLabel, scanPruneSummary } from "./ui/labels.js";
import { globalScanActionsHTML, libraryKindScanActionsHTML, libraryScanActionsHTML, withButton } from "./ui/scan_actions.js";

(function () {

  if (!token) { loginRedirect(); return; }

  async function findPodcastLinkedFeed(podcastId) {
    const data = await api("/api/v1/podcasts/feeds?limit=500").catch(() => ({ items: [] }));
    const items = (data && data.items) || [];
    return items.find((feed) => feed.podcastId === podcastId) || null;
  }

  function adminDeleteButton(action, id, name, label) {
    if (!isAdmin()) return "";
    return '<button class="btn danger btn-small" data-action="' + attr(action) + '" data-id="' + attr(id) + '" data-name="' + attr(name) + '">' + escapeHTML(label || "DELETE") + '</button>';
  }

  async function deleteCatalogItem(path, name, kindLabel, afterDelete) {
    if (!confirm("Remove " + kindLabel + " \"" + name + "\" from Samo and try to delete its files from disk? If file deletion fails (e.g. read-only mount), the item is still removed from your library.")) return;
    const result = await api(path, { method: "DELETE", body: { deleteFiles: true } });
    if (result && result.fileErrors && result.fileErrors.length > 0) {
      setStatus("REMOVED FROM LIBRARY · " + result.fileErrors.length + " file(s) could not be deleted. Check mount permissions or delete them manually.");
    }
    if (typeof afterDelete === "function") await afterDelete();
  }
  let activeTab = "";
  let musicMode = "recent";
  let playlistTracksBulkEditId = "";
  const playlistTracksBulkSelected = new Set();
  let musicSort = "recent";
  let musicDirection = "desc";
  const MUSIC_PAGE_SIZE = 80;
  let musicListOffset = 0;
  let musicListTotal = 0;
  let musicListItems = [];
  let audiobooksMode = "titles";
  let podcastsMode = "shows";
  let settingsMode = "libraries";
  // Default RADIO sub-mode is INTERNET — the stations you tune into, which is
  // what the tab is reached for. CHANNELS is the programming surface: you go
  // there to build something, not to listen, and it costs a click to say so.
  let radioMode = "internet";
  // Which section of a channel's programming screen is open. Module state, not
  // DOM state, because the 8-second poll re-renders the whole view — anything
  // held in the markup would snap shut under the user every few seconds.
  let channelSection = "mix";
  // Collapsed sub-sections inside the plan, and whether the pools the plan
  // generator writes for booked shows are showing.
  const planCollapsed = new Set(["behaviour"]);
  let showAutoPools = false;
  // The plan currently on screen, plus which row an editor is editing. Held
  // here rather than re-fetched per keystroke: the editors mutate this object
  // and PUT the whole document, so the server validates one coherent plan
  // instead of a stream of half-applied edits.
  let activePlan = null;
  let activePlanSources = [];
  let planEditIndex = { block: -1, pool: -1, category: -1 };
  let planEditSourceID = "";
  // The RANK surface, and what it is mid-way through doing. rankView is what
  // the tier list was drawn from, kept so a drop can redraw it without a round
  // trip; rankPickedID is the card lifted by a click; rankDragID is the one
  // under the pointer; rankSaves counts tier PATCHes still in flight.
  let rankView = null;
  let rankPickedID = "";
  let rankDragID = "";
  let rankSaves = 0;
  let whyLimit = 1;
  let activeChannelID = "";
  // Which samo-radio device has its settings drawer open, and the device list
  // itself — cached between polls so the "play to" buttons on detail views do
  // not each cost a round trip.
  let samoRadioExpandedID = "";
  let samoRadioDevices = [];
  let samoRadioDevicesPrimed = false;
  let searchQuery = "";

  function renderLoading() {
    main.innerHTML = "<div class=\"boot-line\">// loading...</div>";
  }

  function renderError(message) {
    main.innerHTML = "<div class=\"empty-state\">// " + escapeHTML(message) + "</div>";
  }

  function statCard(label, value, accent) {
    return '<div class="stat-card"><span class="label">' + label + '</span>' +
      '<span class="value' + (accent ? " accent" : "") + '">' + (value || 0) + '</span></div>';
  }

  function musicSortQuery() {
    return "sort=" + encodeURIComponent(musicSort) + "&direction=" + encodeURIComponent(musicDirection);
  }

  function musicSortToolbar() {
    const recentActive = musicSort === "recent";
    const azActive = musicSort === "az";
    const ascActive = musicDirection === "asc";
    const descActive = musicDirection === "desc";
    return '<div class="sort-toolbar">' +
      '<div class="sort-group"><span class="sort-label">SORT</span>' +
        '<button class="pill ' + (recentActive ? "active" : "") + '" data-action="music-sort" data-sort="recent">RECENTS</button>' +
        '<button class="pill ' + (azActive ? "active" : "") + '" data-action="music-sort" data-sort="az">A-Z</button>' +
      '</div>' +
      '<div class="sort-group"><span class="sort-label">ORDER</span>' +
        '<button class="pill ' + (descActive ? "active" : "") + '" data-action="music-direction" data-direction="desc">DESC</button>' +
        '<button class="pill ' + (ascActive ? "active" : "") + '" data-action="music-direction" data-direction="asc">ASC</button>' +
      '</div>' +
    '</div>';
  }

  function asBool(raw) {
    return raw === "true";
  }

  async function loadLibraries() {
    const data = await api("/api/v1/libraries").catch(() => ({ items: [] }));
    rememberLibraries((data && data.items) || []);
    return (data && data.items) || [];
  }

  async function triggerGlobalScan(mode) {
    if (!(await ensureScanAvailable())) return;
    await triggerScan(() => api("/api/v1/scan", { method: "POST", body: { mode: mode } }));
  }

  function closeActivityPanel() { if (activityPanel) activityPanel.hidden = true; }

  async function openActivityPanel() {
    if (!activityPanel || !activityBody) return;
    closeScanPanel();
    activityPanel.hidden = false;
    activityBody.innerHTML = '<div class="boot-line">// loading activity...</div>';
    try {
      const data = await api("/api/v1/server/activity");
      const catalog = data.catalog || {};
      const music = catalog.music || {};
      const audiobook = catalog.audiobook || {};
      const podcast = catalog.podcast || {};
      const last = data.lastScan || null;
      const lastType = last ? ((last.scope || "all").toUpperCase() + (last.libraryId ? " · " + last.libraryId : "")) : "—";
      const lastWhen = last && last.finishedAt ? formatDate(last.finishedAt) : (last && last.startedAt ? formatDate(last.startedAt) : "—");
      activityBody.innerHTML =
        '<div class="activity-stat-grid">' +
          '<div class="activity-stat"><span class="label">UPTIME</span><span class="value">' + escapeHTML(formatUptime(data.uptimeSeconds)) + '</span></div>' +
          '<div class="activity-stat"><span class="label">LIBRARY ITEMS</span><span class="value">' + escapeHTML(String(data.totalItems || 0)) + '</span></div>' +
          '<div class="activity-stat"><span class="label">LAST SCAN</span><span class="value">' + escapeHTML(lastType) + '</span></div>' +
          '<div class="activity-stat"><span class="label">LAST SCAN AT</span><span class="value">' + escapeHTML(lastWhen) + '</span></div>' +
        '</div>' +
        '<div class="scan-history-head">// catalog totals</div>' +
        '<div class="activity-stat-grid">' +
          '<div class="activity-stat"><span class="label">MUSIC TRACKS</span><span class="value">' + (music.trackCount || 0) + '</span></div>' +
          '<div class="activity-stat"><span class="label">AUDIOBOOKS</span><span class="value">' + (audiobook.audiobookCount || 0) + '</span></div>' +
          '<div class="activity-stat"><span class="label">PODCAST SHOWS</span><span class="value">' + (podcast.podcastCount || 0) + '</span></div>' +
          '<div class="activity-stat"><span class="label">PODCAST EPISODES</span><span class="value">' + (podcast.episodeCount || 0) + '</span></div>' +
        '</div>' +
        (last && last.error ? '<div class="status-line bad">' + escapeHTML(last.error) + '</div>' : '');
    } catch (err) {
      activityBody.innerHTML = '<div class="empty-state">// ' + escapeHTML(err.message) + '</div>';
    }
  }

  async function resumeActiveScan() {
    try {
      const jobs = await api("/api/v1/scan/jobs?limit=6");
      const items = (jobs && jobs.items) || [];
      const active = items.find((job) => job.status === "running" || job.status === "pending");
      if (active && active.id) watchScanJob(active.id);
    } catch {
      // Best effort: no job list means nothing to resume watching.
    }
  }

  function updateArtistImageJobPanel(job) {
    const panel = document.getElementById("artistImageJobPanel");
    if (!panel) return;
    panel.innerHTML = renderArtistImageJobPanel(job);
  }

  function renderArtistImageJobPanel(job) {
    if (!job) {
      return '<div class="empty-state">// no artist photo download has run yet</div>';
    }
    const total = job.total || 0;
    const processed = job.processed || 0;
    const running = job.status === "running" || job.status === "pending";
    let html = '<div class="list"><div class="list-row">' +
      '<div class="num">' + (running ? "…" : "·") + '</div>' +
      '<div class="main"><div class="name">' + escapeHTML((job.status || "unknown").toUpperCase()) + '</div>' +
      '<div class="meta">' + processed + " / " + total + " ARTISTS · " +
      (job.found || 0) + " FOUND · " + (job.failed || 0) + " FAILED · " + (job.skipped || 0) + " SKIPPED · STARTED " + formatDate(job.startedAt) +
      (job.error ? " · " + escapeHTML(job.error) : "") + '</div></div>';
    if (running) {
      html += '<div class="actions"><button class="btn ghost btn-mini" data-action="cancel-artist-images">CANCEL</button></div>';
    }
    html += '</div></div>';
    return html;
  }

  // One fetch to render current state; everything after it arrives on the
  // event stream.
  async function watchArtistImageBackfill() {
    try {
      const data = await api("/api/v1/music/artists/images/backfill");
      updateArtistImageJobPanel(data && data.job);
    } catch {
      // No job to show yet.
    }
  }

  async function resumeArtistImageBackfill() {
    try {
      const data = await api("/api/v1/music/artists/images/backfill");
      const job = data && data.job;
      if (job && (job.status === "running" || job.status === "pending")) watchArtistImageBackfill();
    } catch {
      // No backfill job to resume.
    }
  }

  async function uploadRadioCover(stationID, file) {
    if (!stationID || !file) return;
    const form = new FormData();
    form.append("cover", file);
    const headers = {};
    if (token) headers["Authorization"] = "Bearer " + token;
    const res = await fetch("/api/v1/internet-radio/stations/" + encodeURIComponent(stationID) + "/cover", {
      method: "POST",
      headers: headers,
      body: form,
    });
    if (res.status === 401) {
      localStorage.removeItem(tokenKey);
      loginRedirect();
      throw new Error("unauthorized");
    }
    const body = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(body.error || ("upload failed: " + res.status));
    return body;
  }

  async function uploadPodcastCover(podcastID, file) {
    if (!podcastID || !file) return;
    const form = new FormData();
    form.append("cover", file);
    const headers = {};
    if (token) headers["Authorization"] = "Bearer " + token;
    const res = await fetch("/api/v1/podcasts/shows/" + encodeURIComponent(podcastID) + "/cover", {
      method: "POST",
      headers: headers,
      body: form,
    });
    if (res.status === 401) {
      localStorage.removeItem(tokenKey);
      loginRedirect();
      throw new Error("unauthorized");
    }
    const body = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(body.error || ("upload failed: " + res.status));
    return body;
  }

  async function uploadMusicPlaylistCover(playlistID, file) {
    if (!playlistID || !file) return;
    const form = new FormData();
    form.append("cover", file);
    const headers = {};
    if (token) headers["Authorization"] = "Bearer " + token;
    const res = await fetch("/api/v1/music/playlists/" + encodeURIComponent(playlistID) + "/cover", {
      method: "POST",
      headers: headers,
      body: form,
    });
    if (res.status === 401) {
      localStorage.removeItem(tokenKey);
      loginRedirect();
      throw new Error("unauthorized");
    }
    const body = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(body.error || ("upload failed: " + res.status));
    return body;
  }

  function podcastCoverBlock(id, coverURL) {
    if (!isAdmin()) {
      return '<div class="detail-cover" style="background-image:url(&quot;' + attr(coverURL) + '&quot;)"></div>';
    }
    return '<label class="detail-cover radio-cover-upload" style="background-image:url(&quot;' + attr(coverURL) + '&quot;)" title="Upload custom artwork">' +
      '<input type="file" class="radio-cover-input" accept="image/*" data-podcast-id="' + attr(id) + '">' +
      '<span class="radio-cover-hint">UPLOAD</span>' +
    '</label>';
  }

  identifyForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (!identifyContext) return;
    await runIdentifySearch();
  });
  identifyModal.addEventListener("click", (event) => {
    if (event.target === identifyModal) closeIdentifyModal();
  });
  function isTypingTarget(target) {
    if (!target) return false;
    const tag = (target.tagName || "").toUpperCase();
    if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return true;
    if (target.isContentEditable) return true;
    return false;
  }

  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape") {
      if (!identifyModal.hidden) { closeIdentifyModal(); return; }
      if (scanPanel && !scanPanel.hidden) { closeScanPanel(); return; }
      if (activityPanel && !activityPanel.hidden) { closeActivityPanel(); return; }
    }
    if (event.code === "Space" && !isTypingTarget(event.target) && !playerDock.hidden && audio.src) {
      event.preventDefault();
      if (audio.paused) audio.play().catch(() => {});
      else audio.pause();
    }
  });

  /* runBulkIdentify iterates every audiobook (or podcast) in the catalog
   * and calls metadata search + apply for each one. The top candidate
   * across providers wins. Skips items with no usable title or no match.
   * Shows live progress in the scan banner so the user can keep working
   * while it runs. */
  async function runBulkIdentify(button, rawKind) {
    const kind = rawKind === "podcast" ? "podcast" : "audiobook";
    const niceKind = kind === "podcast" ? "podcasts" : "audiobooks";
    const listURL = kind === "podcast" ? "/api/v1/podcasts" : "/api/v1/audiobooks";
    if (!confirm("Scan every " + (kind === "podcast" ? "podcast" : "audiobook") + " for metadata? Existing fields are overwritten when a match is found.")) return;
    let applied = 0;
    let skipped = 0;
    let failed = 0;
    let processed = 0;
    updateRefreshUI("running", "METADATA", "fetching " + niceKind + "...");
    try {
      const pageSize = 100;
      let offset = 0;
      let total = 0;
      let first = true;
      while (true) {
        const page = await api(listURL + "?limit=" + pageSize + "&offset=" + offset);
        const items = (page && page.items) || [];
        if (first) { total = page && page.total || items.length; first = false; }
        if (items.length === 0) break;
        for (const item of items) {
          processed++;
          const title = kind === "podcast" ? podcastTitle(item) : audiobookTitle(item);
          const author = kind === "podcast" ? podcastSub(item) : audiobookSub(item);
          const query = [title, author].filter(Boolean).join(" ").trim();
          updateRefreshUI("running", "METADATA", processed + " of " + total + " · " + (title || "untitled"));
          if (!query) { skipped++; continue; }
          try {
            const response = await api("/api/v1/metadata/search?kind=" + encodeURIComponent(kind) + "&q=" + encodeURIComponent(query) + "&limit=1");
            const candidates = (response && response.results) || [];
            if (candidates.length === 0) { skipped++; continue; }
            await api("/api/v1/metadata/apply", {
              method: "POST",
              body: { targetKind: kind, targetId: item.id, candidate: candidates[0], fields: [] },
            });
            applied++;
          } catch {
            // One item failing must not abort the batch; it is counted instead.
            failed++;
          }
        }
        offset += items.length;
        if (offset >= total) break;
      }
      const summary = applied + " applied · " + skipped + " skipped" + (failed > 0 ? " · " + failed + " failed" : "");
      updateRefreshUI(failed > 0 ? "error" : "ok", "METADATA", summary);
      if (failed === 0) setTimeout(() => updateRefreshUI("idle", "SCAN", "READY"), 5000);
      // Reload the view so the user sees the new metadata.
      if (activeTab && views[activeTab]) await views[activeTab]();
    } catch (err) {
      updateRefreshUI("error", "METADATA", err.message || "scan failed");
    }
  }

  async function applyIdentifyCandidate(kind, id, candidate) {
    if (!candidate) {
      identifyResults.innerHTML = '<div class="empty-state">// choose a match first</div>';
      return;
    }
    const targetKind = kind === "podcast" ? "podcast" : "audiobook";
    // Pass an empty Fields slice so the server applies every allowed field
    // for the target — most users hitting FIND MATCH want a wholesale
    // adoption of the chosen candidate, not field-by-field cherry-picking.
    const fields = [];
    const body = { targetKind: targetKind, targetId: id, candidate: candidate, fields: fields };
    if (targetKind === "podcast" && candidateFeedURL(candidate)) {
      body.linkFeed = true;
      body.syncEpisodeMetadata = true;
    }
    try {
      await api("/api/v1/metadata/apply", {
        method: "POST",
        body: body,
      });
      closeIdentifyModal();
      // Reload the detail page so the new metadata shows immediately.
      if (targetKind === "podcast") {
        await openPodcast(id);
      } else {
        await openAudiobook(id);
      }
    } catch (err) {
      identifyResults.innerHTML = '<div class="empty-state">// ' + escapeHTML(err.message || "apply failed") + '</div>';
    }
  }

  async function playTrack(id, title, subtitle, duration) {
    playURL(musicStreamURL(id), title || "Track", subtitle || "Music", { kind: "music-track", id: id, duration: duration || 0 });
    try {
      await patchPlayback("music-track", id, { incrementPlayCount: true, touchLastPlayedAt: true });
    } catch (err) {
      setStatus("PLAYBACK · " + err.message);
    }
  }

  async function playPodcastEpisode(id, title, subtitle, duration, progressSeconds) {
    let resume = Math.max(0, Math.floor(progressSeconds || 0));
    try {
      const state = await api("/api/v1/playback/podcast-episode/" + encodeURIComponent(id));
      if (state && !state.completed && state.progressSeconds != null) {
        resume = Math.max(resume, Math.floor(state.progressSeconds || 0));
      }
    } catch {
      // No stored position — start from wherever the caller asked.
    }
    playURL(podcastEpisodeStreamURLAt(id, resume), title || "Episode", subtitle || "Podcast", {
      kind: "podcast-episode",
      id: id,
      duration: duration || 0,
      globalBase: resume,
    });
    try {
      await patchPlayback("podcast-episode", id, { incrementPlayCount: true, touchLastPlayedAt: true });
    } catch (err) {
      setStatus("PLAYBACK · " + err.message);
    }
  }

  async function playAudiobook(id, title, subtitle, duration, progressSeconds) {
    const resume = Math.max(0, Math.floor(progressSeconds || 0));
    playURL(audiobookStreamURLAt(id, resume), title || "Audiobook", subtitle || "Audiobook", {
      kind: "audiobook",
      id: id,
      duration: duration || 0,
      globalBase: resume,
    });
    try {
      await patchPlayback("audiobook", id, { incrementPlayCount: true, touchLastPlayedAt: true });
    } catch (err) {
      setStatus("PLAYBACK · " + err.message);
    }
  }

  playerToggle.addEventListener("click", () => {
    if (!audio.src) return;
    if (audio.paused) {
      audio.play().catch((err) => setStatus("PLAYER · " + (err.message || "blocked")));
    } else {
      audio.pause();
    }
  });

  if (playerStop) {
    playerStop.addEventListener("click", () => {
      flushPlaybackProgress();
      audio.pause();
      audio.currentTime = 0;
      audio.removeAttribute("src");
      audio.load();
      playerDock.hidden = true;
      if (nowPlayingBtn && nowPlayingSub) {
        nowPlayingBtn.hidden = true;
        nowPlayingSub.textContent = "IDLE";
      }
      setPlayerGlyph(true);
      refreshSeekUI();
      clearPlayerTarget();
    });
  }

  if (playerSeek) {
    let scrubbing = false;
    playerSeek.addEventListener("pointerdown", (event) => {
      scrubbing = true;
      playerSeek.setPointerCapture(event.pointerId);
      seekFromPointer(event);
    });
    playerSeek.addEventListener("pointermove", (event) => {
      if (!scrubbing) return;
      seekFromPointer(event);
    });
    const releaseScrub = (event) => {
      if (!scrubbing) return;
      scrubbing = false;
      try {
        playerSeek.releasePointerCapture(event.pointerId);
      } catch {
        // Capture was already released (pointercancel, or the element went away).
      }
    };
    playerSeek.addEventListener("pointerup", releaseScrub);
    playerSeek.addEventListener("pointercancel", releaseScrub);
    playerSeek.addEventListener("keydown", (event) => {
      const dur = isFinite(audio.duration) ? audio.duration : 0;
      if (dur <= 0) return;
      if (event.key === "ArrowLeft") { audio.currentTime = Math.max(0, audio.currentTime - 5); event.preventDefault(); }
      else if (event.key === "ArrowRight") { audio.currentTime = Math.min(dur, audio.currentTime + 5); event.preventDefault(); }
      else if (event.key === "Home") { audio.currentTime = 0; event.preventDefault(); }
      else if (event.key === "End") { audio.currentTime = dur; event.preventDefault(); }
      else return;
      refreshSeekUI();
    });
  }

  audio.addEventListener("play", () => setPlayerGlyph(false));
  audio.addEventListener("pause", () => setPlayerGlyph(true));
  audio.addEventListener("loadedmetadata", () => {
    applyStreamResumeSeek();
    refreshSeekUI();
  });
  audio.addEventListener("durationchange", () => {
    applyStreamResumeSeek();
    refreshSeekUI();
  });
  audio.addEventListener("canplay", applyStreamResumeSeek);
  audio.addEventListener("timeupdate", () => {
    refreshSeekUI();
    reportPlaybackProgress();
  });
  audio.addEventListener("ended", () => {
    setPlayerGlyph(true);
    if (!playerTarget) return;
    if (playerTarget.kind === "audiobook") {
      const fileDuration = Math.floor(audio.duration || 0);
      const globalNow = (playerTarget.globalBase || 0) + fileDuration;
      const total = playerTarget.duration || 0;
      if (total > 0 && globalNow < total - 2) {
        playerTarget.globalBase = globalNow;
        resetProgressThrottle();
        playURL(audiobookStreamURLAt(playerTarget.id, globalNow), playerTitle.textContent, playerSub.textContent, playerTarget);
        audio.play().catch((err) => {
          setPlayerGlyph(true);
          setStatus("PLAYER · " + (err.message || "blocked"));
        });
        return;
      }
      patchPlayback("audiobook", playerTarget.id, { completed: true, progressSeconds: globalNow, touchLastPlayedAt: true }).catch(() => {});
      return;
    }
    if (playerTarget.kind === "podcast-episode") {
      const globalNow = playbackGlobalSeconds(playerTarget);
      patchPlayback("podcast-episode", playerTarget.id, {
        completed: true,
        progressSeconds: globalNow,
        touchLastPlayedAt: true,
      }).catch(() => {});
      return;
    }
    if (playerTarget.kind !== "music-track") return;
    patchPlayback(playerTarget.kind, playerTarget.id, { completed: true, touchLastPlayedAt: true }).catch(() => {});
  });

  /* -------- HOME -------- */
  async function viewHome() {
    renderLoading();
    try {
      const [overview, recentlyAdded, newestReleases, libraries, internetRadio] = await Promise.all([
        api("/api/v1/catalog/overview"),
        api("/api/v1/catalog/recently-added?limit=50"),
        api("/api/v1/music/albums?limit=10&sort=release&direction=desc"),
        api("/api/v1/libraries").catch(() => ({ items: [], total: 0 })),
        api("/api/v1/internet-radio/stations?limit=500").catch(() => ({ items: [] })),
      ]);
      const musicCounts = overview.music || {};
      const audiobookCounts = overview.audiobook || {};
      const podcastCounts = overview.podcast || {};
      const libCount = (libraries && libraries.total) || 0;
      const stationCount = (internetRadio && internetRadio.total) || ((internetRadio && internetRadio.items) || []).length;

      let html = '<section class="view">';
      html += '<div class="view-head"><h1>HOME</h1><div class="view-actions">' +
        globalScanActionsHTML() +
        '<button class="btn ghost btn-small" data-action="composer-toggle" data-composer="library">+ ADD LIBRARY</button>' +
        '<button class="btn ghost btn-small" data-action="go-tab" data-tab="search">SEARCH</button>' +
        '</div></div>' + composerLibrary();
      html += '<div class="stat-grid">';
      html += statCard("ARTISTS", musicCounts.artistCount || 0);
      html += statCard("ALBUMS", musicCounts.albumCount || 0, true);
      html += statCard("TRACKS", musicCounts.trackCount || 0);
      html += statCard("AUDIOBOOKS", audiobookCounts.audiobookCount || 0);
      html += statCard("PODCASTS", podcastCounts.podcastCount || 0);
      html += statCard("RADIO", stationCount);
      html += statCard("LIBRARIES", libCount);
      html += '</div>';

      const recentItems = ((recentlyAdded && recentlyAdded.items) || []).slice(0, 12);
      html += '<div class="section-row">';
      html += '<div class="section-label">// recently added</div>';
      if (recentItems.length > 0) {
        html += '<div class="album-grid">' + recentItems.map(recentlyAddedCard).join("") + '</div>';
      } else {
        html += '<div class="empty-state">// nothing indexed yet - add a library folder and wait for the scan</div>';
      }
      html += '</div>';

      const releaseAlbums = browseAlbums(newestReleases);
      html += '<div class="section-row">';
      html += '<div class="section-label">// newest releases</div>';
      if (releaseAlbums.length > 0) {
        html += '<div class="album-row">' + releaseAlbums.map(albumCard).join("") + '</div>';
      } else {
        html += '<div class="empty-state">// no release dates in catalog yet</div>';
      }
      html += '</div>';

      const libs = (libraries && libraries.items) || [];
      rememberLibraries(libs);
      if (libs.length > 0) {
        html += '<div class="section-row">';
        html += '<div class="section-label">// attached libraries</div>';
        html += '<div class="list">';
        libs.slice(0, 8).forEach((lib) => {
          html += '<div class="list-row">' +
            '<div class="num">·</div>' +
            '<div class="main">' +
              '<div class="name">' + escapeHTML(lib.name) + '</div>' +
              '<div class="meta">' + escapeHTML(lib.path) + ' · ' + libraryKindLabel(lib) + ' · ' + (lib.itemCount || 0) + ' ITEMS</div>' +
            '</div>' +
            '<div class="actions">' + libraryScanActionsHTML(lib) + '</div>' +
          '</div>';
        });
        html += '</div></div>';
      }
      html += '</section>';
      main.innerHTML = html;
    } catch (err) { renderError(err.message); }
  }

  function albumCard(album) {
    const id = album.id || "";
    const cover = id ? musicCoverURL(id) : "";
    const style = cover ? 'style="background-image:url(&quot;' + attr(cover) + '&quot;)"' : "";
    const empty = cover ? "" : "empty";
    return '<a class="album-card" href="#music" data-action="album-detail" data-id="' + attr(id) + '">' +
      '<div class="cover ' + empty + '" ' + style + '></div>' +
      '<div class="title">' + escapeHTML(album.title || album.name || "Untitled") + '</div>' +
      '<div class="sub">' + escapeHTML(album.displayArtist || album.artist || "Various") + '</div>' +
    '</a>';
  }

  function recentlyAddedCard(entry) {
    const kind = entry.kind || "";
    const id = entry.id || "";
    const title = entry.title || "Untitled";
    const kindLabel = recentlyAddedKindLabel(kind);
    const sub = [entry.subtitle, kindLabel].filter(Boolean).join(" · ");
    if (kind === "music-album") {
      const cover = musicCoverURL(id);
      const style = cover ? 'style="background-image:url(&quot;' + attr(cover) + '&quot;)"' : "";
      const empty = cover ? "" : "empty";
      return '<a class="album-card" href="#music" data-action="album-detail" data-id="' + attr(id) + '">' +
        '<div class="cover ' + empty + '" ' + style + '></div>' +
        '<div class="title">' + escapeHTML(title) + '</div>' +
        '<div class="sub">' + escapeHTML(sub) + '</div>' +
      '</a>';
    }
    if (kind === "audiobook") {
      const cover = audiobookCoverURL(id);
      return '<a class="album-card" href="#audiobooks" data-action="audiobook-detail" data-id="' + attr(id) + '">' +
        '<div class="cover" style="background-image:url(&quot;' + attr(cover) + '&quot;)"></div>' +
        '<div class="title">' + escapeHTML(title) + '</div>' +
        '<div class="sub">' + escapeHTML(sub) + '</div>' +
      '</a>';
    }
    if (kind === "podcast") {
      const cover = podcastCoverURL(id);
      return '<a class="album-card" href="#podcasts" data-action="podcast-detail" data-id="' + attr(id) + '">' +
        '<div class="cover" style="background-image:url(&quot;' + attr(cover) + '&quot;)"></div>' +
        '<div class="title">' + escapeHTML(title) + '</div>' +
        '<div class="sub">' + escapeHTML(sub) + '</div>' +
      '</a>';
    }
    return "";
  }

  /* -------- MUSIC -------- */
  async function viewMusic(append) {
    renderLoading();
    try {
      await ensureStreamToken();
      if (!append) {
        musicListOffset = 0;
        musicListItems = [];
        musicListTotal = 0;
      }

      const pills = '<div class="pill-bar">' +
        '<button class="pill ' + (musicMode === "recent" ? "active" : "") + '" data-action="music-mode" data-mode="recent">RECENT</button>' +
        '<button class="pill ' + (musicMode === "albums" ? "active" : "") + '" data-action="music-mode" data-mode="albums">ALBUMS</button>' +
        '<button class="pill ' + (musicMode === "tracks" ? "active" : "") + '" data-action="music-mode" data-mode="tracks">TRACKS</button>' +
        '<button class="pill ' + (musicMode === "artists" ? "active" : "") + '" data-action="music-mode" data-mode="artists">ARTISTS</button>' +
        '<button class="pill ' + (musicMode === "playlists" ? "active" : "") + '" data-action="music-mode" data-mode="playlists">PLAYLISTS</button>' +
        '<button class="pill ' + (musicMode === "favorites" ? "active" : "") + '" data-action="music-mode" data-mode="favorites">FAVORITES</button>' +
      '</div>';

      let body = "";
      const sortQuery = musicSortQuery();
      const pageQuery = "limit=" + MUSIC_PAGE_SIZE + "&offset=" + musicListOffset;

      if (musicMode === "albums" || musicMode === "recent") {
        const data = await api("/api/v1/music/albums?" + pageQuery + "&" + sortQuery);
        const items = (data && data.items) || [];
        musicListTotal = (data && data.total) || items.length;
        musicListItems = append ? musicListItems.concat(items) : items;
        musicListOffset += items.length;
        const label = musicMode === "recent" ? "recently added albums" : "albums";
        body = '<div class="section-row"><div class="section-label">// ' + escapeHTML(label) + '</div>' +
          albumGridFromList(musicListItems) + musicPaginationFooter(musicListItems.length, musicListTotal) + '</div>';
      } else if (musicMode === "tracks") {
        const data = await api("/api/v1/music/tracks?" + pageQuery + "&" + sortQuery);
        const items = (data && data.items) || [];
        musicListTotal = (data && data.total) || items.length;
        musicListItems = append ? musicListItems.concat(items) : items;
        musicListOffset += items.length;
        body = trackList(musicListItems) + musicPaginationFooter(musicListItems.length, musicListTotal);
      } else if (musicMode === "artists") {
        const data = await api("/api/v1/music/artists?" + pageQuery + "&" + sortQuery);
        const items = (data && data.items) || [];
        musicListTotal = (data && data.total) || items.length;
        musicListItems = append ? musicListItems.concat(items) : items;
        musicListOffset += items.length;
        body = artistList(musicListItems) + musicPaginationFooter(musicListItems.length, musicListTotal);
      } else if (musicMode === "playlists") {
        const data = await api("/api/v1/music/playlists?" + pageQuery);
        const items = (data && data.items) || [];
        musicListTotal = (data && data.total) || items.length;
        musicListItems = append ? musicListItems.concat(items) : items;
        musicListOffset += items.length;
        body = playlistList(musicListItems) + musicPaginationFooter(musicListItems.length, musicListTotal);
      } else if (musicMode === "favorites") {
        const data = await api("/api/v1/music/browse/favorites?" + pageQuery);
        musicListTotal = (data && data.total) || 0;
        if (!append) {
          musicListItems = { albums: [], tracks: [], artists: [], playlists: [] };
        }
        const bucket = musicListItems;
        bucket.albums = bucket.albums.concat((data && data.albums) || []);
        bucket.tracks = bucket.tracks.concat((data && data.tracks) || []);
        bucket.artists = bucket.artists.concat((data && data.artists) || []);
        bucket.playlists = bucket.playlists.concat((data && data.playlists) || []);
        musicListOffset += browseResultCount(data);
        body = musicMixedResults(bucket, "favorites") +
          musicPaginationFooter(musicListOffset, musicListTotal);
      }
      const sortControls = (musicMode === "albums" || musicMode === "recent" || musicMode === "tracks" || musicMode === "artists") ? musicSortToolbar() : "";
      const musicActions = musicMode === "playlists" ?
        '<div class="view-actions"><button class="btn primary btn-small" data-action="composer-toggle" data-composer="playlist-import">IMPORT PLAYLIST</button><button class="btn ghost btn-small" data-action="composer-toggle" data-composer="playlist">+ NEW PLAYLIST</button></div>' :
        '<span class="crumb">// library</span>';
      const playlistComposers = musicMode === "playlists" ? composerPlaylistImport() + composerPlaylist() : "";
      main.innerHTML = '<section class="view">' +
        '<div class="view-head"><h1>MUSIC</h1>' + musicActions + '</div>' +
        playlistComposers + pills + sortControls + body +
      '</section>';
    } catch (err) { renderError(err.message); }
  }

  function albumGridFromList(items) {
    if (items.length === 0) return '<div class="empty-state">// no albums to show yet</div>';
    return '<div class="album-grid">' + items.map(albumCard).join("") + '</div>';
  }

  function trackList(items) {
    if (!items || items.length === 0) return '<div class="empty-state">// no tracks to show yet</div>';
    return '<div class="list">' + items.map((track, idx) => {
      const playback = track.playback || {};
      const artist = track.displayArtist || (track.artistNames || []).join(", ");
      const meta = [artist, track.albumTitle, formatDuration(track.durationSeconds)].filter(Boolean).join(" · ");
      return '<div class="list-row">' +
        '<div class="num">' + String(track.trackNumber || idx + 1).padStart(2, "0") + '</div>' +
        '<div class="main">' +
          '<div class="name">' + escapeHTML(track.title || "Untitled") + '</div>' +
          '<div class="meta">' + escapeHTML(meta) + '</div>' +
          progressBar(playback, track.durationSeconds || 0) +
        '</div>' +
        '<div class="actions">' +
          '<button class="btn primary btn-mini" data-action="play-track" data-id="' + attr(track.id) + '" data-title="' + attr(track.title || "Untitled") + '" data-sub="' + attr(artist || track.albumTitle || "") + '" data-duration="' + (track.durationSeconds || 0) + '">PLAY</button>' +
          '<button class="btn ghost btn-mini" data-action="toggle-playback" data-kind="music-track" data-id="' + attr(track.id) + '" data-field="favorite" data-value="' + (!(playback.favorite || playback.starred)) + '">' + ((playback.favorite || playback.starred) ? "UNFAV" : "FAV") + '</button>' +
        '</div>' +
      '</div>';
    }).join("") + '</div>';
  }

  function playlistTrackList(playlistID, items, canEdit, bulkMode) {
    if (!items || items.length === 0) return '<div class="empty-state">// no tracks in this playlist yet</div>';
    return '<div class="list">' + items.map((track, idx) => {
      const artist = track.displayArtist || (track.artistNames || []).join(", ");
      const meta = [artist, track.albumTitle, formatDuration(track.durationSeconds)].filter(Boolean).join(" · ");
      const removeButton = canEdit && !bulkMode ? '<button class="btn danger btn-mini" data-action="remove-playlist-track" data-playlist-id="' + attr(playlistID) + '" data-track-id="' + attr(track.id) + '">REMOVE</button>' : "";
      const indexCell = bulkMode && canEdit ?
        '<label class="track-select"><input type="checkbox" data-action="playlist-track-select" data-playlist-id="' + attr(playlistID) + '" data-track-id="' + attr(track.id) + '"' + (playlistTracksBulkSelected.has(track.id) ? " checked" : "") + '></label>' :
        '<div class="num">' + String(idx + 1).padStart(2, "0") + '</div>';
      return '<div class="list-row">' +
        indexCell +
        '<div class="main">' +
          '<div class="name">' + escapeHTML(track.title || "Untitled") + '</div>' +
          '<div class="meta">' + escapeHTML(meta) + '</div>' +
        '</div>' +
        '<div class="actions">' +
          '<button class="btn primary btn-mini" data-action="play-track" data-id="' + attr(track.id) + '" data-title="' + attr(track.title || "Untitled") + '" data-sub="' + attr(artist || track.albumTitle || "") + '" data-duration="' + (track.durationSeconds || 0) + '">PLAY</button>' +
          removeButton +
        '</div>' +
      '</div>';
    }).join("") + '</div>';
  }

  function artistList(items) {
    if (items.length === 0) return '<div class="empty-state">// no artists yet</div>';
    return '<div class="list">' + items.map((artist, idx) => (
      '<div class="list-row clickable" data-action="open-artist" data-id="' + attr(artist.id) + '">' +
        '<div class="num">' + String(idx + 1).padStart(2, "0") + '</div>' +
        '<div class="main">' +
          '<div class="name">' + escapeHTML(artist.name || artist.displayName) + '</div>' +
          '<div class="meta">' + (artist.albumCount || 0) + ' ALBUMS · ' + (artist.trackCount || 0) + ' TRACKS · ' + formatDuration(artist.durationSeconds || 0) + '</div>' +
        '</div>' +
        '<div class="actions"><button class="btn ghost btn-mini" data-action="open-artist" data-id="' + attr(artist.id) + '">OPEN &rarr;</button></div>' +
      '</div>'
    )).join("") + '</div>';
  }

  function playlistList(items) {
    if (items.length === 0) return '<div class="empty-state">// no playlists yet - create one or import from csv/m3u/text/youtube metadata</div>';
    return '<div class="list">' + items.map((playlist, idx) => {
      const bust = (playlist.images && playlist.images.length > 0) ? playlist.images[0].id : null;
      const cover = playlist.id ? musicPlaylistCoverURL(playlist.id, bust) : "";
      const thumbStyle = cover ? 'style="background-image:url(&quot;' + attr(cover) + '&quot;)"' : "";
      return '<div class="list-row clickable" data-action="open-playlist" data-id="' + attr(playlist.id) + '">' +
        '<div class="list-thumb ' + (cover ? "" : "empty") + '" ' + thumbStyle + '></div>' +
        '<div class="main">' +
          '<div class="name">' + escapeHTML(playlist.name || "Untitled Playlist") + '</div>' +
          '<div class="meta">' + (playlist.trackCount || 0) + ' TRACKS · ' + formatDuration(playlist.durationSeconds || 0) + ' · ' + (playlist.public ? "PUBLIC" : "PRIVATE") + '</div>' +
        '</div>' +
        '<div class="actions">' +
          '<button class="btn ghost btn-mini" data-action="open-playlist" data-id="' + attr(playlist.id) + '">OPEN &rarr;</button>' +
          (playlistDeletableByCurrentUser(playlist) ? '<button class="btn danger btn-mini" data-action="delete-playlist" data-id="' + attr(playlist.id) + '" data-name="' + attr(playlist.name || "playlist") + '">DELETE</button>' : "") +
        '</div>' +
      '</div>';
    }).join("") + '</div>';
  }

  function playlistOwnedByCurrentUser(playlist) {
    if (!playlist) return false;
    if (!playlist.ownerId) return true;
    return currentUser && playlist.ownerId === currentUser.id;
  }

  // Deletion is broader than editing: admins may remove any non-system
  // playlist. Filesystem imports and migrated rows are owned by the internal
  // bootstrap account no human can log in as - owner-only gating made those
  // rows undeletable from every surface.
  function playlistDeletableByCurrentUser(playlist) {
    if (!playlist || playlist.system) return false;
    return playlistOwnedByCurrentUser(playlist) || isAdmin();
  }

  function musicMixedResults(data, label) {
    const albums = (data && data.albums) || [];
    const tracks = (data && data.tracks) || [];
    const artists = (data && data.artists) || [];
    const playlists = (data && data.playlists) || [];
    let html = "";
    if (albums.length > 0) {
      html += '<div class="section-row"><div class="section-label">// ' + escapeHTML(label) + ' / albums</div>' + albumGridFromList(albums) + '</div>';
    }
    if (tracks.length > 0) {
      html += '<div class="section-row"><div class="section-label">// ' + escapeHTML(label) + ' / tracks</div>' + trackList(tracks) + '</div>';
    }
    if (artists.length > 0) {
      html += '<div class="section-row"><div class="section-label">// ' + escapeHTML(label) + ' / artists</div>' + artistList(artists) + '</div>';
    }
    if (playlists.length > 0) {
      html += '<div class="section-row"><div class="section-label">// ' + escapeHTML(label) + ' / playlists</div>' + playlistList(playlists) + '</div>';
    }
    if (!html) return '<div class="empty-state">// no ' + escapeHTML(label) + ' yet</div>';
    return html;
  }

  async function openArtist(id) {
    renderLoading();
    try {
      const [artist, albumsPage] = await Promise.all([
        api("/api/v1/music/artists/" + encodeURIComponent(id)),
        api("/api/v1/music/artists/" + encodeURIComponent(id) + "/albums").catch(() => ({ items: [] })),
      ]);
      const albums = (albumsPage && albumsPage.items) || [];
      const cover = musicCoverURL(artist.id);
      let html = '<section class="view">' +
        '<div class="view-head"><h1>MUSIC</h1><div class="view-actions">' +
          '<button class="btn ghost btn-small" data-action="back-music">BACK</button>' +
        '</div></div>' +
        '<div class="detail-shell">' +
          '<div class="detail-cover" style="background-image:url(&quot;' + attr(cover) + '&quot;)"></div>' +
          '<div class="detail-meta">' +
            '<div class="card-head"><span class="caret">&gt;</span> ARTIST</div>' +
            '<h2>' + escapeHTML(artist.name || artist.displayName || "Unknown") + '</h2>' +
            (artist.sortName && artist.sortName !== artist.name ? '<div class="artist">' + escapeHTML(artist.sortName) + '</div>' : "") +
            '<div class="stats">' +
              '<span>' + (artist.albumCount || albums.length) + ' ALBUMS</span>' +
              '<span>' + (artist.trackCount || 0) + ' TRACKS</span>' +
              '<span>' + formatDuration(artist.durationSeconds || 0) + '</span>' +
            '</div>' +
            tagsLine(artist.genres || artist.tags || []) +
          '</div>' +
        '</div>' +
        '<div class="section-row"><div class="section-label">// discography</div>' + albumGridFromList(albums) + '</div>' +
      '</section>';
      main.innerHTML = html;
    } catch (err) { renderError(err.message); }
  }

  async function openAuthor(id) {
    renderLoading();
    try {
      const author = await api("/api/v1/contributors/" + encodeURIComponent(id) + "?include=audiobooks&limit=120");
      const items = (author.audiobooks && author.audiobooks.items) || [];
      let html = '<section class="view">' +
        '<div class="view-head"><h1>AUDIOBOOKS</h1><div class="view-actions">' +
          '<button class="btn ghost btn-small" data-action="back-tab" data-tab="audiobooks">BACK</button>' +
        '</div></div>' +
        '<div class="detail-shell">' +
          '<div class="detail-cover" style="background-color: #0a0a0a"></div>' +
          '<div class="detail-meta">' +
            '<div class="card-head"><span class="caret">&gt;</span> AUTHOR</div>' +
            '<h2>' + escapeHTML(author.name || "Unknown") + '</h2>' +
            (author.sortName && author.sortName !== author.name ? '<div class="artist">' + escapeHTML(author.sortName) + '</div>' : "") +
            '<div class="stats">' +
              '<span>' + (author.audiobookCount || items.length) + ' TITLES</span>' +
              '<span>' + formatDuration(author.durationSeconds || 0) + '</span>' +
            '</div>' +
            (author.description ? '<p class="lede" style="margin-top:14px; color: var(--text-dim)">' + escapeHTML(author.description) + '</p>' : "") +
          '</div>' +
        '</div>' +
        '<div class="section-row"><div class="section-label">// titles</div>' + audiobookGrid(items) + '</div>' +
      '</section>';
      main.innerHTML = html;
    } catch (err) { renderError(err.message); }
  }

  async function openSeries(id) {
    renderLoading();
    try {
      const series = await api("/api/v1/series/" + encodeURIComponent(id) + "?include=audiobooks&limit=120");
      const items = (series.audiobooks && series.audiobooks.items) || [];
      let html = '<section class="view">' +
        '<div class="view-head"><h1>AUDIOBOOKS</h1><div class="view-actions">' +
          '<button class="btn ghost btn-small" data-action="back-tab" data-tab="audiobooks">BACK</button>' +
        '</div></div>' +
        '<div class="detail-shell">' +
          '<div class="detail-cover" style="background-color: #0a0a0a"></div>' +
          '<div class="detail-meta">' +
            '<div class="card-head"><span class="caret">&gt;</span> SERIES</div>' +
            '<h2>' + escapeHTML(series.name || "Untitled Series") + '</h2>' +
            '<div class="stats">' +
              '<span>' + (series.audiobookCount || items.length) + ' TITLES</span>' +
              '<span>' + formatDuration(series.durationSeconds || 0) + '</span>' +
            '</div>' +
            (series.description ? '<p class="lede" style="margin-top:14px; color: var(--text-dim)">' + escapeHTML(series.description) + '</p>' : "") +
          '</div>' +
        '</div>' +
        '<div class="section-row"><div class="section-label">// titles in order</div>' + audiobookGrid(items) + '</div>' +
      '</section>';
      main.innerHTML = html;
    } catch (err) { renderError(err.message); }
  }

  async function openAlbum(id) {
    renderLoading();
    try {
      const [album, tracksPage] = await Promise.all([
        api("/api/v1/music/albums/" + encodeURIComponent(id)),
        api("/api/v1/music/albums/" + encodeURIComponent(id) + "/tracks").catch(() => ({ items: [] })),
      ]);
      const tracks = (tracksPage && tracksPage.items) || [];
      tracks.sort((a, b) => (a.discNumber || 0) - (b.discNumber || 0) || (a.trackNumber || 0) - (b.trackNumber || 0) || String(a.title || "").localeCompare(String(b.title || "")));
      const cover = musicCoverURL(album.id);
      let html = '<section class="view">' +
        '<div class="view-head"><h1>MUSIC</h1><div class="view-actions"><button class="btn ghost btn-small" data-action="back-music">BACK</button>' +
        (tracks[0] ? '<button class="btn primary btn-small" data-action="play-track" data-id="' + attr(tracks[0].id) + '" data-title="' + attr(tracks[0].title || album.title) + '" data-sub="' + attr(album.displayArtist || "") + '" data-duration="' + (tracks[0].durationSeconds || 0) + '">PLAY FIRST</button>' : "") +
        adminDeleteButton("delete-album", album.id, album.title || "album") +
        '</div></div>' +
        '<div class="detail-shell">' +
          '<div class="detail-cover" style="background-image:url(&quot;' + attr(cover) + '&quot;)"></div>' +
          '<div class="detail-meta">' +
            '<h2>' + escapeHTML(album.title || "Untitled") + '</h2>' +
            '<div class="artist">' + escapeHTML(album.displayArtist || "Various") + '</div>' +
            '<div class="stats"><span>' + (album.trackCount || tracks.length || 0) + ' TRACKS</span><span>' + formatDuration(album.durationSeconds || 0) + '</span><span>' + escapeHTML(album.releaseYear || album.releaseDate || "") + '</span></div>' +
            tagsLine(album.genres || album.tags || []) +
          '</div>' +
        '</div>' +
        samoRadioSendBar(primeSamoRadioDevices(), { type: "track", ids: tracks.map((track) => track.id) }) +
        '<div class="section-row"><div class="section-label">// tracks</div>' + trackList(tracks) + '</div>' +
      '</section>';
      main.innerHTML = html;
    } catch (err) { renderError(err.message); }
  }

  async function openPlaylist(id) {
    renderLoading();
    try {
      await ensureStreamToken();
      const [playlist, tracksPage] = await Promise.all([
        api("/api/v1/music/playlists/" + encodeURIComponent(id)),
        api("/api/v1/music/playlists/" + encodeURIComponent(id) + "/tracks").catch(() => ({ items: [] })),
      ]);
      const tracks = (tracksPage && tracksPage.items) || [];
      const canEdit = playlistOwnedByCurrentUser(playlist);
      const bulkMode = canEdit && playlistTracksBulkEditId === playlist.id;
      if (playlistTracksBulkEditId && playlistTracksBulkEditId !== playlist.id) {
        playlistTracksBulkEditId = "";
        playlistTracksBulkSelected.clear();
      }
      const ownerActions = (canEdit ?
        '<button class="btn primary btn-small" data-action="composer-toggle" data-composer="playlist-edit">EDIT PLAYLIST</button>' +
        '<button class="btn ghost btn-small" data-action="playlist-tracks-edit-toggle" data-id="' + attr(playlist.id) + '">' + (bulkMode ? "DONE EDITING TRACKS" : "EDIT TRACKS") + '</button>' +
        '<button class="btn ghost btn-small" data-action="toggle-playlist-public" data-id="' + attr(playlist.id) + '" data-public="' + (!playlist.public) + '">' + (playlist.public ? "MAKE PRIVATE" : "MAKE PUBLIC") + '</button>' :
        "") +
        (playlistDeletableByCurrentUser(playlist) ?
        '<button class="btn danger btn-small" data-action="delete-playlist" data-id="' + attr(playlist.id) + '" data-name="' + attr(playlist.name || "playlist") + '">DELETE</button>' :
        "");
      const bulkToolbar = bulkMode ?
        '<div class="view-actions" style="margin-bottom:0.75rem">' +
          '<button class="btn danger btn-small" data-action="remove-playlist-tracks-bulk" data-id="' + attr(playlist.id) + '">REMOVE SELECTED (' + playlistTracksBulkSelected.size + ')</button>' +
          '<button class="btn ghost btn-small" data-action="playlist-tracks-edit-done" data-id="' + attr(playlist.id) + '">CANCEL</button>' +
        '</div>' :
        "";
      let html = '<section class="view">' +
        '<div class="view-head"><h1>MUSIC</h1><div class="view-actions">' +
          '<button class="btn ghost btn-small" data-action="back-music">BACK</button>' +
          ownerActions +
        '</div></div>' +
        '<div class="detail-shell">' +
          playlistCoverBlock(playlist.id, musicPlaylistCoverURL(playlist.id, (playlist.images && playlist.images.length > 0) ? playlist.images[0].id : null), canEdit) +
          '<div class="detail-meta">' +
            '<div class="card-head"><span class="caret">&gt;</span> PLAYLIST</div>' +
            '<h2>' + escapeHTML(playlist.name || "Untitled Playlist") + '</h2>' +
            (playlist.description ? '<div class="artist">' + escapeHTML(playlist.description) + '</div>' : "") +
            '<div class="stats"><span>' + (playlist.trackCount || tracks.length || 0) + ' TRACKS</span><span>' + formatDuration(playlist.durationSeconds || 0) + '</span><span>' + (playlist.public ? "PUBLIC" : "PRIVATE") + '</span></div>' +
          '</div>' +
        '</div>' +
        composerPlaylistEdit(playlist) +
        '<div class="section-row"><div class="section-label">// tracks</div>' + bulkToolbar + playlistTrackList(playlist.id, tracks, canEdit, bulkMode) + '</div>' +
      '</section>';
      if (canEdit) {
        const editName = document.getElementById("composerPlaylistEditName");
        if (editName) editName.value = playlist.name || "";
        const editDesc = document.getElementById("composerPlaylistEditDescription");
        if (editDesc) editDesc.value = playlist.description || "";
        const editPublic = document.getElementById("composerPlaylistEditPublic");
        if (editPublic) editPublic.checked = Boolean(playlist.public);
        const editID = document.getElementById("composerPlaylistEditId");
        if (editID) editID.value = playlist.id || "";
      }
      main.innerHTML = html;
    } catch (err) { renderError(err.message); }
  }

  /* -------- AUDIOBOOKS -------- */
  async function viewAudiobooks() {
    renderLoading();
    try {
      const pills = '<div class="pill-bar">' +
        '<button class="pill ' + (audiobooksMode === "titles" ? "active" : "") + '" data-action="audiobooks-mode" data-mode="titles">TITLES</button>' +
        '<button class="pill ' + (audiobooksMode === "authors" ? "active" : "") + '" data-action="audiobooks-mode" data-mode="authors">AUTHORS</button>' +
        '<button class="pill ' + (audiobooksMode === "series" ? "active" : "") + '" data-action="audiobooks-mode" data-mode="series">SERIES</button>' +
      '</div>';

      let body = "";
      if (audiobooksMode === "authors") {
        const data = await api("/api/v1/contributors?limit=80");
        body = authorList((data && data.items) || []);
      } else if (audiobooksMode === "series") {
        const data = await api("/api/v1/series?limit=80");
        body = seriesList((data && data.items) || []);
      } else {
        const data = await api("/api/v1/audiobooks?limit=500");
        const items = (data && data.items) || [];
        const total = (data && data.total) || items.length;
        body = '<div class="section-row"><div class="section-label">// ' + total + ' titles</div>' + audiobookGrid(items) + '</div>';
      }
      const libs = await loadLibraries();
      const audiobookLib = libs.find((lib) => lib.kind === "audiobook");
      main.innerHTML = '<section class="view">' +
        '<div class="view-head"><h1>AUDIOBOOKS</h1><div class="view-actions">' +
          (audiobookLib ? libraryKindScanActionsHTML("audiobook") : "") +
          '<button class="btn ghost btn-small" data-action="bulk-identify" data-kind="audiobook" title="Match existing titles against metadata providers — does not walk disk">MATCH METADATA</button>' +
        '</div></div>' +
        pills + body +
      '</section>';
    } catch (err) { renderError(err.message); }
  }

  /* -------- PODCASTS -------- */
  async function viewPodcasts() {
    renderLoading();
    try {
      const pills = '<div class="pill-bar">' +
        '<button class="pill ' + (podcastsMode === "shows" ? "active" : "") + '" data-action="podcasts-mode" data-mode="shows">SHOWS</button>' +
        '<button class="pill ' + (podcastsMode === "episodes" ? "active" : "") + '" data-action="podcasts-mode" data-mode="episodes">EPISODES</button>' +
        '<button class="pill ' + (podcastsMode === "feeds" ? "active" : "") + '" data-action="podcasts-mode" data-mode="feeds">FEEDS</button>' +
      '</div>';

      let body = "";
      if (podcastsMode === "episodes") {
        const data = await api("/api/v1/podcasts/episodes?limit=80");
        body = episodeList((data && data.items) || []);
      } else if (podcastsMode === "feeds") {
        const data = await api("/api/v1/podcasts/feeds?limit=80").catch(() => ({ items: [] }));
        body = podcastFeedsList((data && data.items) || []);
      } else {
        const data = await api("/api/v1/podcasts?limit=80");
        body = podcastGrid((data && data.items) || []);
      }
      const libs = await loadLibraries();
      const podcastLib = libs.find((lib) => lib.kind === "podcast");
      main.innerHTML = '<section class="view">' +
        '<div class="view-head"><h1>PODCASTS</h1><div class="view-actions">' +
          (podcastLib ? libraryKindScanActionsHTML("podcast") : "") +
          '<button class="btn ghost btn-small" data-action="bulk-identify" data-kind="podcast" title="Match existing shows against metadata providers — does not walk disk">MATCH METADATA</button>' +
          '<button class="btn primary btn-small" data-action="composer-toggle" data-composer="podcast-feed">+ NEW PODCAST</button>' +
        '</div></div>' +
        composerPodcastFeed() +
        pills + body +
      '</section>';
    } catch (err) { renderError(err.message); }
  }

  function audiobookGrid(items) {
    if (!items || items.length === 0) return '<div class="empty-state">// no audiobooks yet</div>';
    return '<div class="album-grid">' + items.map((item) => {
      const cover = audiobookCoverURL(item.id);
      return '<a class="album-card" href="#audiobooks" data-action="audiobook-detail" data-id="' + attr(item.id) + '">' +
        '<div class="cover" style="background-image:url(&quot;' + attr(cover) + '&quot;)"></div>' +
        '<div class="title">' + escapeHTML(audiobookTitle(item)) + '</div>' +
        '<div class="sub">' + escapeHTML(audiobookSub(item)) + '</div>' +
      '</a>';
    }).join("") + '</div>';
  }

  function podcastFeedsList(feeds) {
    if (!feeds || feeds.length === 0) {
      return '<div class="empty-state">// no podcast feeds yet — use + NEW PODCAST above</div>';
    }
    return '<div class="list">' + feeds.map((feed) => (
      '<div class="list-row">' +
        '<div class="num">' + escapeHTML(String(feed.status || "NEW").toUpperCase()) + '</div>' +
        '<div class="main"><div class="name">' + escapeHTML(feed.title || feed.feedUrl) + '</div>' +
        '<div class="meta">' + escapeHTML(feed.feedUrl) + ' · ' + (feed.episodeCount || 0) + ' EPISODES · FETCHED ' + formatDate(feed.lastFetchedAt) + (feed.lastError ? ' · ' + escapeHTML(feed.lastError) : '') + '</div></div>' +
        '<div class="actions">' +
          '<label class="field checkbox" title="Auto-download new episodes"><input type="checkbox" data-action="toggle-feed-download" data-id="' + attr(feed.id) + '"' + (feed.autoDownloadEnabled ? ' checked' : '') + '><span>AUTO</span></label>' +
          '<button class="btn ghost btn-mini" data-action="refresh-feed" data-id="' + attr(feed.id) + '">REFRESH</button>' +
          '<button class="btn danger btn-mini" data-action="delete-feed" data-id="' + attr(feed.id) + '" data-name="' + attr(feed.title || feed.feedUrl) + '">DELETE</button>' +
        '</div>' +
      '</div>'
    )).join("") + '</div>';
  }

  function podcastGrid(items) {
    if (!items || items.length === 0) return '<div class="empty-state">// no podcast shows yet</div>';
    return '<div class="album-grid">' + items.map((item) => {
      const cover = podcastCoverURL(item.id);
      return '<a class="album-card" href="#podcasts" data-action="podcast-detail" data-id="' + attr(item.id) + '">' +
        '<div class="cover" style="background-image:url(&quot;' + attr(cover) + '&quot;)"></div>' +
        '<div class="title">' + escapeHTML(podcastTitle(item)) + '</div>' +
        '<div class="sub">' + escapeHTML(podcastSub(item)) + '</div>' +
      '</a>';
    }).join("") + '</div>';
  }

  function authorList(items) {
    if (!items || items.length === 0) return '<div class="empty-state">// no authors yet</div>';
    return '<div class="list">' + items.map((author, idx) => (
      '<div class="list-row clickable" data-action="open-author" data-id="' + attr(author.id) + '">' +
        '<div class="num">' + String(idx + 1).padStart(2, "0") + '</div>' +
        '<div class="main">' +
          '<div class="name">' + escapeHTML(author.name) + '</div>' +
          '<div class="meta">' + (author.audiobookCount || 0) + ' TITLES · ' + formatDuration(author.durationSeconds || 0) + '</div>' +
        '</div>' +
        '<div class="actions"><button class="btn ghost btn-mini" data-action="open-author" data-id="' + attr(author.id) + '">OPEN &rarr;</button></div>' +
      '</div>'
    )).join("") + '</div>';
  }

  function seriesList(items) {
    if (!items || items.length === 0) return '<div class="empty-state">// no series yet</div>';
    return '<div class="list">' + items.map((series, idx) => (
      '<div class="list-row clickable" data-action="open-series" data-id="' + attr(series.id) + '">' +
        '<div class="num">' + String(idx + 1).padStart(2, "0") + '</div>' +
        '<div class="main">' +
          '<div class="name">' + escapeHTML(series.name || "Untitled Series") + '</div>' +
          '<div class="meta">' + (series.audiobookCount || 0) + ' TITLES · ' + formatDuration(series.durationSeconds || 0) + '</div>' +
        '</div>' +
        '<div class="actions"><button class="btn ghost btn-mini" data-action="open-series" data-id="' + attr(series.id) + '">OPEN &rarr;</button></div>' +
      '</div>'
    )).join("") + '</div>';
  }

  function episodeList(items, showID) {
    if (!items || items.length === 0) return '<div class="empty-state">// no podcast episodes yet</div>';
    return '<div class="list">' + items.map((item, idx) => {
      const meta = [formatDate(item.publishedAt || item.addedAt), formatDuration(item.durationSeconds || 0)].filter(Boolean).join(" · ");
      const title = item.title || "Untitled";
      const cache = item.cache || {};
      const cached = cache.cached || cache.local;
      const cacheLabel = cache.local ? "LOCAL" : (cache.cached ? "CACHED" : "");
      const downloadBtn = (!cached && item.enclosureUrl)
        ? '<button class="btn ghost btn-mini" data-action="download-podcast-episode" data-id="' + attr(item.id) + '" data-show-id="' + attr(showID || "") + '">DOWNLOAD</button>'
        : (cacheLabel ? '<span class="kind-chip">' + cacheLabel + '</span>' : '');
      return '<div class="list-row">' +
        '<div class="num">' + String(idx + 1).padStart(2, "0") + '</div>' +
        '<div class="main">' +
          '<div class="name">' + escapeHTML(title) + '</div>' +
          '<div class="meta">' + escapeHTML(meta) + '</div>' +
          progressBar(item.progress || {}, item.durationSeconds || 0) +
        '</div>' +
        '<div class="actions">' +
          '<button class="btn primary btn-mini" data-action="play-podcast-episode" data-id="' + attr(item.id) + '" data-title="' + attr(title) + '" data-sub="Podcast episode" data-duration="' + attr(item.durationSeconds || 0) + '" data-progress="' + attr((item.progress && item.progress.progressSeconds) || 0) + '">PLAY</button>' +
          downloadBtn +
          '<a class="btn ghost btn-mini" href="' + attr(podcastEpisodeStreamURL(item.id)) + '" target="_blank">OPEN</a>' +
        '</div>' +
      '</div>';
    }).join("") + '</div>';
  }

  async function openAudiobook(id) {
    renderLoading();
    try {
      const item = await api("/api/v1/audiobooks/" + encodeURIComponent(id));
      const title = audiobookTitle(item);
      const sub = audiobookSub(item);
      const cover = audiobookCoverURL(id);
      const progress = item.progress || {};
      let html = '<section class="view">' +
        '<div class="view-head"><h1>AUDIOBOOKS</h1><div class="view-actions"><button class="btn ghost btn-small" data-action="back-tab" data-tab="audiobooks">BACK</button>' +
        '<button class="btn ghost btn-small" data-action="identify" data-kind="audiobook" data-id="' + attr(id) + '" data-title="' + attr(title) + '" data-author="' + attr(sub) + '">FIND MATCH</button>' +
        '<button class="btn primary btn-small" data-action="play-audiobook" data-id="' + attr(id) + '" data-title="' + attr(title) + '" data-sub="' + attr(sub) + '" data-duration="' + attr(item.durationSeconds || 0) + '" data-progress="' + attr(progress.progressSeconds || 0) + '">PLAY</button>' +
        '<a class="btn ghost btn-small" href="' + attr(audiobookStreamURL(id)) + '" target="_blank">OPEN STREAM</a>' +
        adminDeleteButton("delete-audiobook", id, title) +
        '</div></div>' +
        '<div class="detail-shell">' +
          '<div class="detail-cover" style="background-image:url(&quot;' + attr(cover) + '&quot;)"></div>' +
          '<div class="detail-meta">' +
            '<h2>' + escapeHTML(title) + '</h2>' +
            '<div class="artist">' + escapeHTML(sub) + '</div>' +
            '<div class="stats"><span>AUDIOBOOK</span><span>' + formatDuration(item.durationSeconds || 0) + '</span><span>' + (progress.completed ? "COMPLETE" : "IN PROGRESS") + '</span></div>' +
            progressBar(progress, item.durationSeconds || 0) +
            tagsLine((item.book && item.book.genres) || item.genres || []) +
          '</div>' +
        '</div>';
      if (item.chapters && item.chapters.length > 0) {
        html += '<div class="section-row"><div class="section-label">// chapters</div><div class="list">';
        item.chapters.forEach((chapter) => {
          html += '<div class="list-row"><div class="num">' + String(chapter.index || 0).padStart(2, "0") + '</div>' +
            '<div class="main"><div class="name">' + escapeHTML(chapter.title || "Chapter") + '</div>' +
            '<div class="meta">' + formatDuration(chapter.startSeconds || 0) + '</div></div></div>';
        });
        html += '</div></div>';
      }
      html += '</section>';
      main.innerHTML = html;
    } catch (err) { renderError(err.message); }
  }

  async function openPodcast(id, coverBust) {
    renderLoading();
    try {
      const [item, episodes] = await Promise.all([
        api("/api/v1/podcasts/shows/" + encodeURIComponent(id)),
        api("/api/v1/podcasts/shows/" + encodeURIComponent(id) + "/episodes?limit=200").catch(() => ({ items: [] })),
      ]);
      const title = podcastTitle(item);
      const sub = podcastSub(item);
      const cover = podcastCoverURL(id, coverBust);
      const items = (episodes && episodes.items) || [];
      const folderPodcast = isLibraryFolderPodcast(item);
      const linkedFeed = podcastHasLinkedFeed(item) ? item.rssFeed : await findPodcastLinkedFeed(id);
      const suggestedFeedURL = (item.podcast && item.podcast.feedUrl) || (linkedFeed && linkedFeed.feedUrl) || "";
      const feedActions = linkedFeed ?
        '<button class="btn ghost btn-small" data-action="refresh-feed" data-id="' + attr(linkedFeed.id) + '" data-show-id="' + attr(id) + '">REFRESH RSS</button>' :
        (folderPodcast ?
          '<button class="btn primary btn-small" data-action="composer-toggle" data-composer="podcast-attach-feed">LINK RSS FEED</button>' :
          "");
      const feedStatus = linkedFeed ?
        '<div class="stats" style="margin-top:10px"><span>RSS LINKED</span><span>' + escapeHTML(linkedFeed.feedUrl || linkedFeed.id) + '</span></div>' :
        (folderPodcast ?
          '<p class="lede" style="margin-top:14px; color: var(--text-dim)">// library folder podcast — link an RSS feed to fix episode dates and pull new releases while keeping your files</p>' :
          "");
      let html = '<section class="view">' +
        '<div class="view-head"><h1>PODCASTS</h1><div class="view-actions">' +
          '<button class="btn ghost btn-small" data-action="back-tab" data-tab="podcasts">BACK</button>' +
          feedActions +
          '<button class="btn ghost btn-small" data-action="identify" data-kind="podcast" data-id="' + attr(id) + '" data-title="' + attr(title) + '" data-author="' + attr(sub) + '">FIND MATCH</button>' +
          (folderPodcast ? adminDeleteButton("delete-podcast-show", id, title) : "") +
        '</div></div>' +
        '<div class="detail-shell">' +
          podcastCoverBlock(id, cover) +
          '<div class="detail-meta">' +
            '<h2>' + escapeHTML(title) + '</h2>' +
            '<div class="artist">' + escapeHTML(sub) + '</div>' +
            '<div class="stats"><span>PODCAST</span><span>' + items.length + ' EPISODES</span></div>' +
            feedStatus +
            (item.podcast && item.podcast.description ? '<p class="lede" style="margin-top:14px; color: var(--text-dim)">' + escapeHTML(item.podcast.description) + '</p>' : "") +
            tagsLine((item.podcast && item.podcast.categories) || item.genres || []) +
          '</div>' +
        '</div>' +
        (folderPodcast && !linkedFeed ? composerPodcastAttachFeed(id, suggestedFeedURL) : "") +
        '<div class="section-row"><div class="section-label">// episodes</div>' + episodeList(items, id) + '</div>' +
      '</section>';
      main.innerHTML = html;
      if (folderPodcast && !linkedFeed) {
        const urlInput = document.getElementById("composerPodcastAttachURL");
        if (urlInput && suggestedFeedURL && !urlInput.value) urlInput.value = suggestedFeedURL;
      }
    } catch (err) { renderError(err.message); }
  }

  /* -------- RADIO --------
   * Newly-created internet stations probe in the background on the server
   * (see createInternetRadioStation). The UI polls for fresh now-playing
   * data so the "WAITING FOR METADATA" placeholder flips to the real title
   * within a few seconds without forcing the user to navigate away. */
  let radioPollTimer = null;

  function stopRadioPolling() {
    if (radioPollTimer) { clearTimeout(radioPollTimer); radioPollTimer = null; }
  }

  function scheduleRadioPoll() {
    stopRadioPolling();
    radioPollTimer = setTimeout(async () => {
      if (activeTab !== "radio") return;
      // Don't blow away an open composer / form the user is filling in, or a
      // card mid-drag on the tier list. Reschedule so we try again next tick.
      if (hasOpenComposerOrModal() || rankIsBusy()) { scheduleRadioPoll(); return; }
      await renderRadio(true);
    }, 8000);
  }

  function hasOpenComposerOrModal() {
    const composers = main.querySelectorAll(".composer");
    for (const el of composers) {
      if (!el.hasAttribute("hidden")) return true;
    }
    return false;
  }

  async function viewRadio() { return renderRadio(false); }

  function radioSubPills() {
    return '<div class="pill-bar">' +
      '<button class="pill ' + (radioMode === "channels" ? "active" : "") + '" data-action="radio-mode" data-mode="channels">CHANNELS</button>' +
      '<button class="pill ' + (radioMode === "internet" ? "active" : "") + '" data-action="radio-mode" data-mode="internet">INTERNET</button>' +
      '<button class="pill ' + (radioMode === "samo-radio" ? "active" : "") + '" data-action="radio-mode" data-mode="samo-radio">SAMO-RADIO</button>' +
    '</div>';
  }

  async function renderRadio(isRefresh) {
    if (!isRefresh) renderLoading();
    try {
      if (activeChannelID) {
        await renderChannelDetail(activeChannelID, isRefresh);
        return;
      }
      if (radioMode === "samo-radio") {
        await renderSamoRadio(isRefresh);
        return;
      }
      if (radioMode === "channels") {
        await renderChannelsList(isRefresh);
        return;
      }
      await renderLegacyRadio(isRefresh);
    } catch (err) { renderError(err.message); }
  }

  async function renderLegacyRadio(isRefresh) {
    const internet = await api("/api/v1/internet-radio/stations").catch(() => ({ items: [] }));
    let html = '<section class="view">' +
      '<div class="view-head"><h1>RADIO</h1><div class="view-actions">' +
        '<button class="btn primary btn-small" data-action="composer-toggle" data-composer="radio-station">+ NEW STATION</button>' +
        '<button class="btn ghost btn-small" data-action="probe-all-radio">PROBE ALL</button>' +
      '</div></div>' +
      radioSubPills() +
      composerRadioStation();

    const inet = (internet && internet.items) || [];
    html += '<div class="section-row"><div class="section-label">// internet radio</div>';
    if (inet.length === 0) {
      html += '<div class="empty-state">// add an internet station with + NEW STATION</div>';
    } else {
      inet.forEach((station) => { html += internetRadioAdminCard(station); });
    }
    html += '</div></section>';
    main.innerHTML = html;
    if (activeTab === "radio") scheduleRadioPoll();
  }

  /* -------- SAMO-RADIO (the server's own audio output) -------- */

  // The device list is the expensive part of this view: Samo asks every device
  // for its live state, in parallel, with a short deadline. Outputs and the
  // channel list are only fetched for the one device whose settings drawer is
  // open, because enumerating sound cards shells out to aplay on the far end.
  async function renderSamoRadio(isRefresh) {
    const data = await api("/api/v1/samo-radio/devices").catch(() => ({ items: [] }));
    const devices = (data && data.items) || [];
    samoRadioDevices = devices;
    samoRadioDevicesPrimed = true;

    let outputs = null;
    let channels = [];
    let stations = [];
    if (samoRadioExpandedID && devices.some((device) => device.id === samoRadioExpandedID)) {
      // A station is anything the device can sit on: a programmed channel or
      // an internet stream. Both are offered as the fallback.
      const [outputsResult, channelsResult, stationsResult] = await Promise.all([
        api("/api/v1/samo-radio/devices/" + encodeURIComponent(samoRadioExpandedID) + "/outputs").catch(() => null),
        api("/api/v1/channels").catch(() => ({ items: [] })),
        api("/api/v1/internet-radio/stations?limit=200").catch(() => ({ items: [] })),
      ]);
      outputs = outputsResult;
      channels = (channelsResult && channelsResult.items) || [];
      stations = ((stationsResult && stationsResult.items) || []).filter((station) => station.enabled !== false);
    }

    let html = '<section class="view">' +
      '<div class="view-head"><h1>RADIO</h1><div class="view-actions">' +
        '<button class="btn primary btn-small" data-action="composer-toggle" data-composer="samo-radio-device">+ ADD DEVICE</button>' +
      '</div></div>' +
      radioSubPills() +
      composerSamoRadioDevice();

    html += '<div class="section-row"><div class="section-label">// audio outputs</div>';
    if (devices.length === 0) {
      html += '<div class="empty-state">// no devices — install samo-radio on the machine with the sound card, then ADD DEVICE with the control token it printed</div>';
    } else {
      devices.forEach((device) => {
        html += samoRadioDeviceCard(device, {
          expanded: device.id === samoRadioExpandedID,
          outputs: device.id === samoRadioExpandedID ? outputs : null,
          channels: channels,
          stations: stations,
        });
      });
    }
    html += '</div></section>';
    main.innerHTML = html;
    if (activeTab === "radio") scheduleRadioPoll();
  }

  // sendToSamoRadio is the whole "play to" gesture: resolve nothing on the
  // client, just name the catalog items and let the server build the URLs the
  // device will fetch.
  async function sendToSamoRadio(deviceID, type, ids, options) {
    options = options || {};
    if (type === "channel") {
      await api("/api/v1/samo-radio/devices/" + encodeURIComponent(deviceID) + "/play", {
        method: "POST",
        body: { mode: "channel", channelId: ids[0] },
      });
      return;
    }
    const items = (ids || []).filter(Boolean).map((id) => ({ type: type, id: id }));
    if (items.length === 0) return;
    await api("/api/v1/samo-radio/devices/" + encodeURIComponent(deviceID) + "/play", {
      method: "POST",
      body: { mode: "queue", items: items, append: Boolean(options.append) },
    });
  }

  // readContentPicker turns the shared picker into a LIST of source payloads.
  // Throws with a human message rather than returning a half-built body, so a
  // missing field surfaces in the composer's own status line.
  //
  // Always a list, even for one pick: the caller loops either way, and a single
  // return type is what keeps "add one podcast" and "add eleven stations" the
  // same code path rather than two that drift.
  function readContentPicker(prefix) {
    const kind = document.getElementById(prefix + "Kind").value;
    const labelInput = document.getElementById(prefix + "Label");
    const typed = labelInput ? labelInput.value.trim() : "";

    // Leaving the label blank promises "use its own name", so the name is
    // resolved here rather than stored empty and rendered as the raw kind
    // ("PODCAST-SUBSCRIPTION") everywhere it appears afterwards. A typed label
    // only applies to a single pick — stamping one name onto eight sources
    // would make them indistinguishable in every list that follows.
    const chosen = (id) => {
      const list = document.getElementById(prefix + id);
      if (list && list.dataset.pickerList) {
        return Array.from(list.querySelectorAll("input[type=checkbox]:checked"))
          .map((box) => ({ name: box.dataset.name || "", value: box.value }));
      }
      const option = list.options[list.selectedIndex];
      return list.value ? [{ name: option ? option.text.trim() : "", value: list.value }] : [];
    };
    const build = (picks, config) => picks.map((pick) => ({
      config: config(pick.value),
      kind: kind,
      label: picks.length === 1 && typed ? typed : pick.name,
    }));

    if (kind === "podcast-subscription") {
      const picks = chosen("Podcast");
      if (picks.length === 0) throw new Error("tick at least one podcast");
      // No maxAgeDays. It used to be stamped on every podcast added here and
      // the scheduler has never read it — the engine's age bound is
      // `rerunMaxAgeDays` — so it sat in the config of nineteen sources
      // reading like a thirty-day limit that was never once applied. A control
      // the station cannot honour is worse than no control, because it answers
      // the question "why is it playing something from years ago" with a lie.
      // How old back catalogue is now scores continuously; see recency in
      // internal/channels/score.go.
      return build(picks, (id) => ({ podcastId: id }));
    }
    if (kind === "internet-station") {
      const picks = chosen("Station");
      if (picks.length === 0) throw new Error("tick at least one station");
      return build(picks, (id) => ({ stationId: id }));
    }
    if (kind === "music-playlist") {
      const picks = chosen("Playlist");
      if (picks.length === 0) throw new Error("tick at least one playlist");
      return build(picks, (id) => ({ playlistId: id }));
    }
    if (kind === "file-pool") {
      // Every path is one pool, not one source each: a folder of station IDs
      // and its overflow folder are the same pile of content.
      const paths = document.getElementById(prefix + "Paths").value
        .split("\n").map((line) => line.trim()).filter(Boolean);
      if (paths.length === 0) throw new Error("add at least one path or folder");
      return [{ config: { paths: paths }, kind: kind, label: typed || folderName(paths[0]) }];
    }
    const urls = document.getElementById(prefix + "Url").value
      .split("\n").map((line) => line.trim()).filter(Boolean);
    if (urls.length === 0) throw new Error("a stream URL is required");
    return urls.map((url) => ({
      config: { url: url },
      kind: kind,
      label: urls.length === 1 && typed ? typed : hostName(url),
    }));
  }

  // A folder's own name is the last meaningful path segment, so
  // /mnt/data2tb/commercials becomes "commercials" rather than the raw kind.
  // A glob is skipped over — "oldies" is a name, "*.mp3" is not.
  function folderName(path) {
    const parts = String(path || "").split("/").filter(Boolean);
    while (parts.length > 0 && /[*?[\]]/.test(parts[parts.length - 1])) {
      parts.pop();
    }
    return parts.length > 0 ? parts[parts.length - 1] : "";
  }

  function hostName(url) {
    try {
      return new URL(url).host;
    } catch {
      return "";
    }
  }

  // scheduleWindows splits a booking into the windows the engine can store.
  //
  // Rule windows are minute-of-day and cannot wrap, so "lofi from 22:00 to
  // 06:00" is physically two rows. Making the user work that out was a papercut
  // on the one kind of programming people most want (overnight).
  function scheduleWindows(startMinute, endMinute) {
    if (endMinute > startMinute) {
      return [{ endMinute: endMinute, startMinute: startMinute }];
    }
    // Ending exactly at midnight wraps to a zero-length second window, which
    // would store a rule that can never match. Drop empties.
    return [
      { endMinute: 1440, startMinute: startMinute },
      { endMinute: endMinute, startMinute: 0 },
    ].filter((window) => window.endMinute > window.startMinute);
  }

  // "<kind>:<id>" is how the station selects encode their two id spaces.
  function parseStationValue(value) {
    const raw = String(value || "");
    const split = raw.indexOf(":");
    if (split < 0) return null;
    const kind = raw.slice(0, split);
    const id = raw.slice(split + 1);
    if (!id || (kind !== "channel" && kind !== "station")) return null;
    return { kind: kind, id: id };
  }

  function stationPlayBody(picked) {
    return picked.kind === "station"
      ? { mode: "station", stationId: picked.id }
      : { mode: "channel", channelId: picked.id };
  }

  // Devices are needed by any view that offers a "play to" button. Cached for
  // the render, refreshed lazily — a stale entry costs one failed command, and
  // asking on every album page would cost every device a state fetch.
  // Devices for the "play to" bars, read from cache and refreshed in the
  // background — never awaited on a render path.
  //
  // Listing devices makes the server probe each one for live state with a
  // 3-second deadline, which is fine on the SAMO-RADIO tab and completely
  // unacceptable on an album page: one unplugged device would stall every
  // album open by three seconds. An empty first answer costs at most a missing
  // send bar until the next navigation; a blocked render costs the page.
  function primeSamoRadioDevices() {
    if (samoRadioDevicesPrimed) return samoRadioDevices;
    samoRadioDevicesPrimed = true;
    api("/api/v1/samo-radio/devices")
      .then((data) => { samoRadioDevices = (data && data.items) || []; })
      .catch(() => { samoRadioDevices = []; });
    return samoRadioDevices;
  }

  /* -------- CHANNELS (24/7 programmed radio) -------- */

  async function renderChannelsList(isRefresh) {
    const data = await api("/api/v1/channels").catch(() => ({ items: [] }));
    const items = (data && data.items) || [];
    let html = '<section class="view">' +
      '<div class="view-head"><h1>RADIO</h1><div class="view-actions">' +
        '<button class="btn primary btn-small" data-action="composer-toggle" data-composer="channel">+ NEW CHANNEL</button>' +
      '</div></div>' +
      radioSubPills() +
      composerChannel();

    html += '<div class="section-row"><div class="section-label">// my channels</div>';
    if (items.length === 0) {
      html += '<div class="empty-state">// no channels yet — create one above and add podcast subscriptions, file pools, and live cut-ins</div>';
    } else {
      html += '<div class="channel-grid">';
      items.forEach((ch) => { html += channelCard(ch); });
      html += '</div>';
    }
    html += '</div></section>';
    main.innerHTML = html;
    if (activeTab === "radio") scheduleRadioPoll();
  }

  async function renderChannelDetail(channelID, isRefresh) {
    const [ch, sources, schedule, now, recent, podcasts, internet, playlists, scheduleStatus, plan, why, owed] = await Promise.all([
      api("/api/v1/channels/" + encodeURIComponent(channelID)).catch(() => null),
      api("/api/v1/channels/" + encodeURIComponent(channelID) + "/sources").catch(() => ({ items: [] })),
      api("/api/v1/channels/" + encodeURIComponent(channelID) + "/schedule").catch(() => ({ items: [] })),
      api("/api/v1/channels/" + encodeURIComponent(channelID) + "/now").catch(() => null),
      api("/api/v1/channels/" + encodeURIComponent(channelID) + "/recent?limit=8").catch(() => ({ items: [] })),
      api("/api/v1/podcasts?limit=200").catch(() => ({ items: [] })),
      api("/api/v1/internet-radio/stations?limit=200").catch(() => ({ items: [] })),
      api("/api/v1/music/playlists?limit=200").catch(() => ({ items: [] })),
      api("/api/v1/channels/" + encodeURIComponent(channelID) + "/schedule/status").catch(() => null),
      api("/api/v1/channels/" + encodeURIComponent(channelID) + "/plan").catch(() => null),
      api("/api/v1/channels/" + encodeURIComponent(channelID) + "/why?limit=" + whyLimit).catch(() => ({ items: [] })),
      api("/api/v1/channels/" + encodeURIComponent(channelID) + "/obligations").catch(() => ({ items: [], pending: 0 })),
    ]);
    if (!ch) { renderError("channel not found"); return; }
    const sourceItems = (sources && sources.items) || [];
    const ruleItems = (schedule && schedule.items) || [];
    const recentItems = (recent && recent.items) || [];
    const podcastItems = (podcasts && podcasts.items) || [];
    const internetItems = (internet && internet.items) || [];
    const playlistItems = (playlists && playlists.items) || [];
    const pickerOptions = { playlists: playlistItems, podcasts: podcastItems, stations: internetItems };
    const sourceLookup = {};
    sourceItems.forEach((s) => { sourceLookup[s.id] = s; });
    // Deep-copied so the editors can mutate freely and a cancelled edit leaves
    // nothing behind — the only plan that exists is the one that was saved.
    const planView = plan || { plan: { categories: [], pools: [], blocks: [] }, custom: false };
    activePlan = JSON.parse(JSON.stringify(planView.plan || {}));
    const planIsCustom = !!planView.custom;
    // Targets are relative, so show them normalised — that is how the engine
    // reads them, and "100 and 0" should not display as "100% and 0%" of
    // something that adds up to 100.
    const planCategories = (activePlan.categories || []).filter((c) => (c.target || 0) > 0);
    const planTotal = planCategories.reduce((sum, c) => sum + (c.target || 0), 0);
    const planShares = planTotal > 0
      ? planCategories
          .map((c) => Math.round(((c.target || 0) / planTotal) * 100) + '% ' + (c.label || c.id))
          .join(', ')
      : '';
    activePlanSources = sourceItems;
    planEditIndex = { block: -1, pool: -1, category: -1 };

    const mixItems = sourceItems.filter((src) => src.role !== "show");
    const pending = (owed && owed.pending) || 0;
    const sourceNames = resolveSourceNames(sourceItems, {
      podcasts: podcastItems, stations: internetItems, playlists: playlistItems,
    });

    let html = '<section class="view channel-view">' +
      '<div class="view-head"><h1>' + escapeHTML(ch.name) + '</h1><div class="view-actions">' +
        '<button class="btn primary btn-small" data-action="channel-tune-in" data-id="' + attr(ch.id) + '" data-name="' + attr(ch.name) + '">TUNE IN</button>' +
        '<button class="btn ghost btn-small" data-action="channel-back">BACK</button>' +
        '<button class="btn danger btn-small" data-action="channel-delete" data-id="' + attr(ch.id) + '" data-name="' + attr(ch.name) + '">DELETE</button>' +
      '</div></div>';

    // What is on air, right now, above everything — one band instead of two
    // panels a screen and a half apart.
    html += channelOnAirHeader(ch.id, now, scheduleStatus);
    html += samoRadioSendBar(primeSamoRadioDevices(), { type: "channel", ids: [ch.id] });

    // One section at a time. The whole surface open at once came to six
    // thousand pixels — nine screens — and everything you actually came here
    // to press was below the fold of whatever you were reading.
    // Rankable sources are counted off the full list, not the mix: a booked
    // show still publishes episodes, so it is still something the station can
    // owe you, and leaving it out would make RANK a place where SOME of your
    // shows can be ranked.
    const rankableItems = sourceItems.filter(rankableSource);
    html += channelSectionNav([
      ["mix", "MIX", mixItems.length],
      ["rank", "RANK", rankableItems.length],
      ["program", "PROGRAM", ruleItems.length],
      ["plan", "PLAN", (activePlan.blocks || []).length],
      ["owed", "OWED", pending],
      ["log", "LOG", null],
    ]);

    if (channelSection === "mix") {
      // THE MIX — everything the channel falls back on. The running order is
      // the engine's; this panel only says what each thing is.
      html += '<div class="panel panel-wide">' +
        '<div class="panel-head"><span>// THE MIX</span>' +
        '<button class="btn primary btn-mini" data-action="composer-toggle" data-composer="channel-content">+ ADD CONTENT</button>' +
        '</div>' +
        composerChannelContent(channelID, pickerOptions) +
        sourceComposer(activePlan.categories || []) +
        channelSourcesBody(mixItems, sourceNames, podcastItems, internetItems, playlistItems) +
        // The two knobs that govern the mix sit under it, as facts with an
        // edit button rather than two paragraphs above the thing they describe.
        //
        // The share line reads from the plan when there is one. It used to read
        // ch.talkShare — the column a derived plan is built FROM — so a station
        // whose plan said 100% talk was described as 75%, next to a CHANGE MIX
        // button writing a value the scheduler no longer read.
        '<div class="channel-knobs">' +
          '<div class="channel-knob">' +
            '<span class="knob-label">SHARE</span>' +
            '<span class="knob-value">' +
              (planIsCustom
                ? (planShares ? escapeHTML(planShares) : 'set by the plan')
                : Math.round((ch.talkShare || 0.75) * 100) + '% talk') +
            '</span>' +
            (planIsCustom
              ? '<span class="knob-hint">from CATEGORIES in the plan</span>'
              : '<button class="btn ghost btn-mini" data-action="channel-talk-share" data-id="' + attr(ch.id) + '" data-share="' + (ch.talkShare || 0.75) + '">CHANGE</button>') +
          '</div>' +
          '<div class="channel-knob">' +
            '<span class="knob-label">LISTENING DAY</span>' +
            '<span class="knob-value">' + escapeHTML(minuteToHHMM(ch.dayStartMinute ?? 480)) + '–' +
              escapeHTML(minuteToHHMM(ch.dayEndMinute ?? 1380)) + '</span>' +
            '<button class="btn ghost btn-mini" data-action="channel-listening-day" data-id="' + attr(ch.id) +
              '" data-start="' + (ch.dayStartMinute ?? 480) + '" data-end="' + (ch.dayEndMinute ?? 1380) + '">CHANGE</button>' +
            '<span class="knob-hint">a new episode aired outside these hours stays new</span>' +
          '</div>' +
        '</div>' +
      '</div>';
    }

    if (channelSection === "rank") {
      // Drawn from the same source rows the mix lists, so a tier set here and
      // a tier set from a source's SETTINGS are the same field.
      const podcastLookup = {};
      podcastItems.forEach((p) => { podcastLookup[p.id] = p; });
      rankView = {
        sources: sourceItems,
        names: sourceNames,
        podcasts: podcastLookup,
        surfacings: (activePlan.freshness || {}).surfacings || {},
      };
      html += '<div id="rankSurface">' + rankSurfaceHTML() + '</div>';
    }

    if (channelSection === "program") {
      html += '<div class="panel panel-wide">' +
        '<div class="panel-head"><span>// BOOKED SLOTS</span>' +
        '<span>' +
          '<button class="btn primary btn-mini" data-action="composer-toggle" data-composer="channel-show">+ ADD SHOW</button> ' +
          '<button class="btn ghost btn-mini" data-action="composer-toggle" data-composer="channel-schedule">+ SLOT FOR EXISTING</button>' +
        '</span></div>' +
        '<div class="panel-sub">In ' + escapeHTML(ch.effectiveTimezone || "UTC") + ' — ' +
          'a show only airs in its window, and beats everything in the mix. ' +
          '<button class="btn ghost btn-mini" data-action="channel-timezone" data-id="' + attr(ch.id) + '" data-tz="' + attr(ch.timezone || "") + '" data-effective="' + attr(ch.effectiveTimezone || "UTC") + '">CHANGE CLOCK</button>' +
        '</div>' +
        channelScheduleStatusBody(scheduleStatus, channelID) +
        composerChannelShow(channelID, pickerOptions) +
        composerChannelSchedule(channelID, sourceItems) +
        channelScheduleTimeline(ruleItems, sourceLookup) +
        channelScheduleBody(ruleItems, sourceLookup, sourceNames) +
      '</div>';
    }

    if (channelSection === "plan") {
      // The station plan: pools, blocks, categories. This is the whole
      // station-building surface, and it edits exactly the concepts the
      // scheduler runs on.
      html += planPanel({ plan: activePlan, custom: planView.custom }, sourceItems, channelID,
        (scheduleStatus && scheduleStatus.programming && scheduleStatus.programming.unreachable) || [],
        {
          collapsed: planCollapsed,
          showAutoPools: showAutoPools,
          sourceNames: sourceNames,
          // The measured mix belongs beside the targets it is measured against.
          balance: channelBalanceBody(scheduleStatus && scheduleStatus.programming),
        });
    }

    if (channelSection === "owed") {
      html += owedPanel((owed && owed.items) || [], pending);
    }

    if (channelSection === "log") {
      // "Why the hell did it play that" and "what did it play" are the same
      // question asked at two zoom levels, so they are one section.
      html += whyPanel((why && why.items) || []);
      html += '<div class="panel panel-wide">' +
        '<div class="panel-head"><span>// RECENT</span><span>' + recentItems.length + '</span></div>' +
        channelRecentBody(recentItems) +
      '</div>';
    }

    html += '</section>';
    main.innerHTML = html;
    if (activeTab === "radio") scheduleRadioPoll();
  }

  // channelSectionNav is the index for the programming screen. Counts live on
  // the pills so a collapsed section still says how much is inside it —
  // hiding something is only acceptable if you can see that it is there.
  function channelSectionNav(sections) {
    return '<div class="pill-bar section-nav">' + sections.map(([id, label, count]) =>
      '<button class="pill ' + (channelSection === id ? "active" : "") + '"' +
        ' data-action="channel-section" data-section="' + attr(id) + '">' +
        escapeHTML(label) +
        (count ? ' <span class="pill-count">' + count + '</span>' : '') +
      '</button>').join("") + '</div>';
  }

  /* -------- RANK --------
   * The tier list redraws itself from `rankView` on every change rather than
   * re-running the whole channel view: a drop has to land instantly, and
   * renderChannelDetail is twelve requests and a full replacement of the
   * screen you are dragging on. */
  function rankSurfaceHTML() {
    if (!rankView) return "";
    return rankPanel(rankView.sources, rankView.names, {
      picked: rankPickedID,
      podcasts: rankView.podcasts,
      surfacings: rankView.surfacings,
    });
  }

  function renderRankSurface() {
    const host = document.getElementById("rankSurface");
    if (host) host.innerHTML = rankSurfaceHTML();
  }

  // A poll landing mid-gesture rips the card out from under the pointer, and
  // one landing between a drop and its PATCH redraws the tier the server has
  // not been told about yet.
  function rankIsBusy() {
    return Boolean(rankDragID) || Boolean(rankPickedID) || rankSaves > 0;
  }

  // setSourceTier moves one show to one band. The card moves first and the
  // request follows: a tier list that waits for a round trip before the card
  // lands is a tier list nobody drags twice.
  async function setSourceTier(sourceID, tier) {
    const src = ((rankView && rankView.sources) || []).find((entry) => entry.id === sourceID);
    rankPickedID = "";
    if (!src) { renderRankSurface(); return; }
    const before = src.config || {};
    const next = String(tier || "").trim().toUpperCase();
    if (String(before.tier || "").trim().toUpperCase() === next) { renderRankSurface(); return; }

    // Merged, not replaced: podcastId and the rest of the source's own config
    // live in this object, and PATCH writes it whole.
    const config = Object.assign({}, before);
    if (next) config.tier = next;
    else delete config.tier;
    src.config = config;
    renderRankSurface();

    rankSaves += 1;
    try {
      await api("/api/v1/channels/" + encodeURIComponent(activeChannelID) +
        "/sources/" + encodeURIComponent(sourceID), {
        method: "PATCH",
        body: { config: config },
      });
    } catch (err) {
      src.config = before;
      renderRankSurface();
      setStatus("ERROR · could not save tier: " + (err.message || "unknown"));
    } finally {
      rankSaves -= 1;
    }
  }

  const SOURCE_KIND_LABEL = {
    "podcast-subscription": "PODCAST",
    "internet-station": "STATION",
    "music-playlist": "PLAYLIST",
    "file-pool": "FILES",
    "live-stream": "LIVE",
    "scheduled-show": "SHOW",
  };

  // resolveSourceNames answers "what is this source called" once, for every
  // screen that has to say so.
  //
  // A source stores a label, but rows created before labels were auto-filled
  // have none — and every list that fell back to the raw kind showed three
  // playlists as three rows reading "MUSIC-PLAYLIST". The name lives in the
  // podcast/station/playlist the config points at, so it is looked up here and
  // handed to the mix list AND the plan, which would otherwise disagree about
  // what the same source is called.
  function resolveSourceNames(sources, catalogs) {
    const pods = {}; (catalogs.podcasts || []).forEach((p) => { pods[p.id] = p; });
    const inet = {}; (catalogs.stations || []).forEach((s) => { inet[s.id] = s; });
    const lists = {}; (catalogs.playlists || []).forEach((pl) => { lists[pl.id] = pl; });

    const names = {};
    (sources || []).forEach((src) => {
      const cfg = src.config || {};
      let name = src.label || "";
      if (!name) {
        if (src.kind === "podcast-subscription") {
          const pod = pods[cfg.podcastId || ""];
          name = pod ? podcastTitle(pod) : "";
        } else if (src.kind === "internet-station") {
          const st = inet[cfg.stationId || ""];
          name = st ? st.name : "";
        } else if (src.kind === "music-playlist") {
          const pl = lists[cfg.playlistId || ""];
          name = pl ? pl.name : "";
        } else if (src.kind === "live-stream") {
          name = cfg.url || "";
        } else if (src.kind === "file-pool" || src.kind === "scheduled-show") {
          const paths = cfg.paths || (cfg.path ? [cfg.path] : []);
          name = paths.length > 0 ? folderName(paths[0]) : "";
        }
      }
      names[src.id] = name || SOURCE_KIND_LABEL[src.kind] || (src.kind || "").toUpperCase();
    });
    return names;
  }

  function channelSourcesBody(items, names, podcasts, internetStations, playlists) {
    if (!items || items.length === 0) {
      return '<div class="empty-state">// no sources — add a file pool, podcast subscription, or internet station above</div>';
    }
    const podLookup = {};
    (podcasts || []).forEach((p) => { podLookup[p.id] = p; });
    const inetLookup = {};
    (internetStations || []).forEach((s) => { inetLookup[s.id] = s; });
    const listLookup = {};
    (playlists || []).forEach((pl) => { listLookup[pl.id] = pl; });

    const KIND_LABEL = SOURCE_KIND_LABEL;

    // Sorted by role so the list reads as the shape of the station rather than
    // the order things happened to be added in.
    const ROLE_ORDER = { SHOW: 0, TALK: 1, MUSIC: 2, COMMERCIAL: 3 };
    const rows = items.slice().sort((a, b) => {
      const ra = ROLE_ORDER[(a.role || "talk").toUpperCase()] ?? 9;
      const rb = ROLE_ORDER[(b.role || "talk").toUpperCase()] ?? 9;
      if (ra !== rb) return ra - rb;
      return String(names[a.id] || "").localeCompare(String(names[b.id] || ""));
    });

    return '<div class="list">' + rows.map((src) => {
      const cfg = src.config || {};
      const kindLabel = KIND_LABEL[src.kind] || (src.kind || "").toUpperCase();
      const name = names[src.id] || kindLabel;
      // Detail is only for the things a name cannot carry: where the files
      // are, and the one case that has to shout — content the source points at
      // that no longer exists.
      let detail = "";
      if (src.kind === "podcast-subscription" && !podLookup[cfg.podcastId || ""]) {
        detail = "missing podcast " + (cfg.podcastId || "?");
      } else if (src.kind === "internet-station" && !inetLookup[cfg.stationId || ""]) {
        detail = "missing station " + (cfg.stationId || "?");
      } else if (src.kind === "music-playlist" && !listLookup[cfg.playlistId || ""]) {
        detail = "missing playlist " + (cfg.playlistId || "?");
      } else if (src.kind === "live-stream") {
        detail = cfg.url || "";
      } else if (src.kind === "file-pool" || src.kind === "scheduled-show") {
        const paths = cfg.paths || (cfg.path ? [cfg.path] : []);
        detail = (paths.length > 0 ? paths[0] : "") + (paths.length > 1 ? " +" + (paths.length - 1) + " more" : "");
      }

      // The role IS the running order, so it leads the row. WEIGHT is gone
      // from the summary: it only breaks ties inside a tier now, and showing
      // it here is what made people read it as priority.
      const role = (src.role || "talk").toUpperCase();
      const roleTag = { TALK: "TALK", MUSIC: "MUS", SHOW: "SHOW", COMMERCIAL: "AD" }[role] || "TALK";
      // The kind and what the scheduler will do with it. The role is NOT
      // repeated here — it is already the tag on the left, and "ENABLED" is
      // gone from every row because the button beside it says DISABLE. Only
      // the exception is worth ink.
      const meta = [
        cfg.category ? "category " + cfg.category : "",
        cfg.tier ? "tier " + String(cfg.tier).toUpperCase() : "",
        cfg.creator ? "by " + cfg.creator : "",
        cfg.family ? "family " + cfg.family : "",
      ].filter(Boolean).join(" · ");

      return '<div class="list-row' + (src.enabled ? "" : " off") + '">' +
        '<div class="num">' + roleTag + '</div>' +
        '<div class="main">' +
          '<div class="name">' + escapeHTML(name || kindLabel) +
            '<span class="row-chip">' + escapeHTML(kindLabel) + '</span>' +
            (src.enabled ? "" : '<span class="row-chip off">DISABLED</span>') +
          '</div>' +
          (meta || detail
            ? '<div class="meta">' + escapeHTML(meta || detail) + '</div>'
            : "") +
        '</div>' +
        '<div class="actions">' +
          '<button class="btn ghost btn-mini" data-action="plan-source-edit" data-id="' + attr(src.id) + '">SETTINGS</button>' +
          '<button class="btn ghost btn-mini" data-action="channel-source-toggle" data-id="' + attr(src.id) + '" data-enabled="' + (!src.enabled) + '">' + (src.enabled ? 'DISABLE' : 'ENABLE') + '</button>' +
          '<button class="btn danger btn-mini" data-action="channel-source-delete" data-id="' + attr(src.id) + '" data-name="' + attr(name || kindLabel) + '">DELETE</button>' +
        '</div>' +
      '</div>';
    }).join("") + '</div>';
  }

  function channelScheduleBody(rules, sourceLookup, names) {
    if (!rules || rules.length === 0) {
      return '<div class="empty-state">// no schedule rules — the channel plays new episodes, then reruns and music</div>';
    }
    // Window first: a booked slot is a time before it is anything else, so the
    // clock leads the row and the day and content follow.
    return '<div class="list">' + rules.map((rule) => {
      const src = sourceLookup[rule.sourceId];
      const days = weekdayMaskToLabel(rule.weekdayMask);
      const window = minuteToHHMM(rule.startMinute) + " → " + minuteToHHMM(rule.endMinute);
      const sourceName = (names || {})[rule.sourceId] || (src ? src.label || src.kind : "unknown source");
      const label = rule.label || sourceName;
      return '<div class="list-row' + (rule.enabled ? "" : " off") + '">' +
        '<div class="num">P' + (rule.priority || 100) + '</div>' +
        '<div class="main"><div class="name">' + escapeHTML(label) +
          (rule.enabled ? "" : '<span class="row-chip off">DISABLED</span>') + '</div>' +
        '<div class="meta">' + escapeHTML(window) + ' · ' + escapeHTML(days) +
          (label === sourceName ? "" : ' · ' + escapeHTML(sourceName)) + '</div></div>' +
        '<div class="actions">' +
          '<button class="btn danger btn-mini" data-action="channel-schedule-delete" data-id="' + attr(rule.id) + '" data-name="' + attr(label) + '">REMOVE</button>' +
        '</div>' +
      '</div>';
    }).join("") + '</div>';
  }

  function channelRecentBody(items) {
    if (!items || items.length === 0) {
      return '<div class="empty-state">// nothing played yet</div>';
    }
    return '<div class="list">' + items.map((entry, idx) => (
      '<div class="list-row">' +
        '<div class="num">' + String(idx + 1).padStart(2, "0") + '</div>' +
        '<div class="main"><div class="name">' + escapeHTML(entry.title || 'Untitled') + '</div>' +
        '<div class="meta">' + escapeHTML(entry.kind || '') + ' · ' + formatDate(entry.startedAt) + (entry.durationSeconds ? ' · ' + formatDuration(entry.durationSeconds) : '') + '</div></div>' +
      '</div>'
    )).join("") + '</div>';
  }

  /* -------- STATION PLAN EDITING -------- */

  // The editors mutate `activePlan` and then PUT the whole document. Saving a
  // plan a piece at a time would mean the server validating half-applied
  // states — a block referring to a pool that has not been added yet — and the
  // validation is the entire point of the endpoint.
  async function savePlan() {
    if (!activeChannelID || !activePlan) return;
    try {
      await api("/api/v1/channels/" + encodeURIComponent(activeChannelID) + "/plan", {
        method: "PUT",
        body: activePlan,
      });
    } catch (err) {
      alert("That plan was rejected:\n\n" + (err.message || "unknown error"));
      return;
    }
    await viewRadio();
  }

  function planField(id) {
    const el = document.getElementById(id);
    return el ? el.value.trim() : "";
  }

  function planChecked(id) {
    const el = document.getElementById(id);
    return Boolean(el && el.checked);
  }

  function planNumber(id) {
    const raw = planField(id);
    if (raw === "") return null;
    const value = Number(raw);
    return Number.isFinite(value) ? value : null;
  }

  function setPlanField(id, value) {
    const el = document.getElementById(id);
    if (el) el.value = value == null ? "" : String(value);
  }

  function setPlanChecked(id, value) {
    const el = document.getElementById(id);
    if (el) el.checked = Boolean(value);
  }

  function planCategories() { return (activePlan && activePlan.categories) || []; }
  function planPools() { return (activePlan && activePlan.pools) || []; }
  function planBlocks() { return (activePlan && activePlan.blocks) || []; }

  // Load a block into the editor. Every field is written, including the empty
  // ones — a form that keeps the last block's values is how you accidentally
  // give two blocks the same exit time.
  function fillBlockForm(block) {
    const enter = (block && block.enter) || {};
    const exit = (block && block.exit) || {};
    setPlanField("planBlockID", block ? block.id : "");
    setPlanField("planBlockLabel", block ? block.label : "");
    setPlanChecked("planBlockDefault", block ? block.default : false);
    setPlanField("planBlockAt", enter.at);
    setPlanField("planBlockDays", enter.days || "*");
    setPlanChecked("planBlockHard", enter.hard);
    setPlanField("planBlockStart", enter.start || "makeNext");
    setPlanField("planBlockGrace", enter.grace);
    setPlanField("planBlockAfter", enter.after || "");
    setPlanField("planBlockWhen", enter.when);
    setPlanField("planBlockExitAt", exit.at);
    setPlanField("planBlockExitDuration", exit.duration);
    setPlanField("planBlockExitTolerance", exit.tolerance);
    setPlanField("planBlockExitCount", exit.count || "");
    setPlanField("planBlockExitWhen", exit.when);
    setPlanChecked("planBlockExitAnchor", exit.atNextAnchor);
    setPlanField("planBlockNext", block ? (block.next || "") : "");

    const refs = {};
    ((block && block.pools) || []).forEach((ref) => { refs[ref.pool] = ref.weight || 1; });
    planPools().forEach((pool, index) => {
      setPlanChecked("planBlockPool" + index, refs[pool.id] != null);
      setPlanField("planBlockPoolWeight" + index, refs[pool.id] != null ? refs[pool.id] : 1);
    });

    setPlanField("planBlockExposure", block && block.exposure != null ? String(block.exposure) : "");

    const pattern = (block && block.pattern) || [];
    for (let index = 0; index < 4; index++) {
      setPlanField("planBlockPattern" + index, pattern[index] ? pattern[index].want : "");
    }

    const breaks = (block && block.breaks) || null;
    const target = (breaks && breaks.target) || {};
    const accept = (breaks && breaks.accept) || {};
    setPlanField("planBlockBreakTargetDuration", target.duration);
    setPlanField("planBlockBreakAcceptMin", (accept.duration || [])[0]);
    setPlanField("planBlockBreakAcceptMax", (accept.duration || [])[1]);
    setPlanField("planBlockBreakMinGap", breaks ? breaks.minGap : "");
    setPlanField("planBlockBreakBetween", ((breaks && breaks.between) || []).join(", "));
    const elements = {};
    ((breaks && breaks.elements) || []).forEach((element) => { elements[element.pool] = element; });
    planPools().forEach((pool, index) => {
      const element = elements[pool.id];
      const count = (element && element.count) || [0, 0];
      setPlanField("planBlockBreakMin" + index, element ? count[0] : 0);
      setPlanField("planBlockBreakMax" + index, element ? count[1] : 0);
      setPlanChecked("planBlockBreakFill" + index, Boolean(element && element.fill));
    });

    const balance = (block && block.balance) || {};
    const limits = (block && block.limits) || {};
    const maxByCategory = {};
    (limits.maxUnbroken || []).forEach((limit) => { maxByCategory[limit.category] = limit; });
    const minByCategory = {};
    (limits.minUnbroken || []).forEach((run) => { minByCategory[run.category] = run; });
    planCategories().forEach((category, index) => {
      const share = balance[category.id];
      setPlanField("planBlockBalance" + index, share == null ? "" : Math.round(share * 100));
      const max = maxByCategory[category.id] || {};
      const min = minByCategory[category.id] || {};
      setPlanField("planBlockMinRun" + index, min.min || "");
      setPlanField("planBlockMaxRun" + index, max.max || "");
      setPlanField("planBlockResetAfter" + index, max.resetAfter || min.resetAfter || "");
      setPlanField("planBlockMinItem" + index, max.minItem || "");
    });
  }

  function readBlockForm() {
    const id = planField("planBlockID");
    if (!id) {
      alert("A block needs an id.");
      return null;
    }
    const block = { id: id, label: planField("planBlockLabel"), enter: {}, exit: {}, pools: [] };
    if (planChecked("planBlockDefault")) block.default = true;

    const at = planField("planBlockAt");
    if (at) {
      block.enter.at = at;
      block.enter.days = planField("planBlockDays") || "*";
      if (planChecked("planBlockHard")) {
        block.enter.hard = true;
        block.enter.start = planField("planBlockStart") || "makeNext";
        const grace = planField("planBlockGrace");
        if (grace) block.enter.grace = grace;
      }
    }
    const after = planField("planBlockAfter");
    if (after) block.enter.after = after;
    const when = planField("planBlockWhen");
    if (when) block.enter.when = when;

    const exitAt = planField("planBlockExitAt");
    if (exitAt) block.exit.at = exitAt;
    const duration = planField("planBlockExitDuration");
    if (duration) block.exit.duration = duration;
    const tolerance = planField("planBlockExitTolerance");
    if (tolerance) block.exit.tolerance = tolerance;
    const count = planNumber("planBlockExitCount");
    if (count) block.exit.count = count;
    const exitWhen = planField("planBlockExitWhen");
    if (exitWhen) block.exit.when = exitWhen;
    if (planChecked("planBlockExitAnchor")) block.exit.atNextAnchor = true;

    const next = planField("planBlockNext");
    if (next && next !== id) block.next = next;

    planPools().forEach((pool, index) => {
      if (!planChecked("planBlockPool" + index)) return;
      const weight = planNumber("planBlockPoolWeight" + index);
      block.pools.push({ pool: pool.id, weight: weight && weight > 0 ? weight : 1 });
    });

    const balance = {};
    const maxUnbroken = [];
    const minUnbroken = [];
    planCategories().forEach((category, index) => {
      const share = planNumber("planBlockBalance" + index);
      if (share != null) balance[category.id] = share / 100;
      const max = planField("planBlockMaxRun" + index);
      const reset = planField("planBlockResetAfter" + index);
      const minItem = planField("planBlockMinItem" + index);
      if (max) {
        const limit = { category: category.id, max: max };
        if (reset) limit.resetAfter = reset;
        if (minItem) limit.minItem = minItem;
        maxUnbroken.push(limit);
      }
      const minRun = planField("planBlockMinRun" + index);
      if (minRun) {
        const run = { category: category.id, min: minRun };
        if (reset) run.resetAfter = reset;
        minUnbroken.push(run);
      }
    });
    if (Object.keys(balance).length > 0) block.balance = balance;
    if (maxUnbroken.length > 0 || minUnbroken.length > 0) {
      block.limits = {};
      if (maxUnbroken.length > 0) block.limits.maxUnbroken = maxUnbroken;
      if (minUnbroken.length > 0) block.limits.minUnbroken = minUnbroken;
    }

    const exposure = planField("planBlockExposure");
    if (exposure !== "") block.exposure = Number(exposure);

    const pattern = [];
    for (let index = 0; index < 4; index++) {
      const want = planField("planBlockPattern" + index);
      if (want) pattern.push({ want: want });
    }
    if (pattern.length > 0) block.pattern = pattern;

    // A break policy only exists if some element can actually contribute an
    // item. An empty policy is not "breaks off", it is a plan the validator
    // would refuse.
    const elements = [];
    planPools().forEach((pool, index) => {
      const min = planNumber("planBlockBreakMin" + index) || 0;
      const max = planNumber("planBlockBreakMax" + index) || 0;
      if (max <= 0) return;
      const element = { pool: pool.id, count: [min, max] };
      if (planChecked("planBlockBreakFill" + index)) element.fill = true;
      elements.push(element);
    });
    if (elements.length > 0) {
      const breaks = { elements: elements, target: {}, accept: {} };
      const targetDuration = planField("planBlockBreakTargetDuration");
      if (targetDuration) breaks.target.duration = targetDuration;
      const low = planField("planBlockBreakAcceptMin");
      const high = planField("planBlockBreakAcceptMax");
      if (low && high) breaks.accept.duration = [low, high];
      const minGap = planField("planBlockBreakMinGap");
      if (minGap) breaks.minGap = minGap;
      const between = planField("planBlockBreakBetween")
        .split(",").map((value) => value.trim()).filter(Boolean);
      if (between.length > 0) breaks.between = between;
      block.breaks = breaks;
    }
    return block;
  }

  function internetRadioAdminCard(station) {
    const now = station.nowPlaying || null;
    const liveText = now ? (now.raw || now.title || "") : "";
    const sub = station.description || station.homepageUrl || station.streamUrl || "";
    const image = radioCoverURL(station);
    const coverStyle = image ? 'style="background-image:url(&quot;' + attr(image) + '&quot;)"' : "";
    const coverEmpty = image ? "" : "empty";
    const coverInputID = "radio-cover-" + station.id;
    return '<div class="radio-card radio-card-admin">' +
      '<label class="radio-cover-upload ' + coverEmpty + '" ' + coverStyle + ' title="Upload thumbnail from your computer">' +
        '<input type="file" id="' + attr(coverInputID) + '" class="radio-cover-input" accept="image/*" data-station-id="' + attr(station.id) + '">' +
        '<span class="radio-cover-hint">UPLOAD</span>' +
      '</label>' +
      '<div class="radio-admin-meta">' +
        '<h3 class="name">' + escapeHTML(station.name) + '</h3>' +
        (sub ? '<p class="desc">' + escapeHTML(sub) + '</p>' : "") +
        nowPlayingLine(now, liveText, "WAITING FOR METADATA") +
        '<div class="meta radio-admin-status">' +
          (station.enabled ? "ENABLED" : "DISABLED") + ' · CHECKED ' + formatDate(station.lastCheckedAt) +
        '</div>' +
      '</div>' +
      '<div class="radio-admin-actions">' +
        '<button class="btn primary btn-mini" data-action="play-url" data-url="' + attr(station.publicStreamUrl || station.streamUrl) + '" data-title="' + attr(station.name) + '" data-sub="Internet radio">PLAY</button>' +
        '<button class="btn ghost btn-mini" data-action="probe-radio" data-id="' + attr(station.id) + '">PROBE</button>' +
        '<button class="btn ghost btn-mini" data-action="toggle-radio" data-id="' + attr(station.id) + '" data-enabled="' + (!station.enabled) + '">' + (station.enabled ? "DISABLE" : "ENABLE") + '</button>' +
        '<a class="btn ghost btn-mini" href="' + attr(station.playlistUrl || station.publicStreamUrl || "#") + '" target="_blank">M3U</a>' +
        '<button class="btn danger btn-mini" data-action="delete-radio" data-id="' + attr(station.id) + '" data-name="' + attr(station.name) + '">DELETE</button>' +
      '</div>' +
    '</div>';
  }

  /* -------- SEARCH -------- */
  async function viewSearch() {
    main.innerHTML = '<section class="view">' +
      '<div class="view-head"><h1>SEARCH</h1><span class="crumb">// catalog query</span></div>' +
      '<div class="search-shell">' +
        '<div class="search-form"><input type="text" id="searchInput" placeholder="// query: artist · album · track · book · podcast" value="' + escapeHTML(searchQuery) + '"></div>' +
        '<div id="searchResults"></div>' +
      '</div>' +
    '</section>';
    const input = document.getElementById("searchInput");
    input.focus();
    let debounce;
    input.addEventListener("input", (e) => {
      searchQuery = e.target.value;
      clearTimeout(debounce);
      debounce = setTimeout(() => runSearch(searchQuery), 250);
    });
    if (searchQuery) runSearch(searchQuery);
  }

  async function runSearch(query) {
    const out = document.getElementById("searchResults");
    if (!out) return;
    if (!query || query.trim().length < 2) { out.innerHTML = ""; return; }
    out.innerHTML = '<div class="boot-line">// querying...</div>';
    try {
      const [music, audiobooks, podcasts] = await Promise.all([
        api("/api/v1/music/search?q=" + encodeURIComponent(query) + "&limit=20"),
        api("/api/v1/audiobooks/search?q=" + encodeURIComponent(query) + "&limit=20").catch(() => ({})),
        api("/api/v1/podcasts/search?q=" + encodeURIComponent(query) + "&limit=20").catch(() => ({})),
      ]);
      let html = "";
      // Artists section is rendered first; one of the reported product bugs
      // was that artist matches were buried below album/track grids and felt
      // missing. Surfacing artists at the top makes "show me the artist
      // page" cases work without a second click.
      const artists = (music && music.artists) || [];
      if (artists.length) {
        html += '<div class="search-group"><div class="search-group-head">// ARTISTS</div>';
        html += artistList(artists.slice(0, 8));
        html += '</div>';
      }
      if (music && ((music.albums || []).length || (music.tracks || []).length)) {
        html += '<div class="search-group"><div class="search-group-head">// MUSIC</div>';
        if ((music.albums || []).length) html += albumGridFromList(music.albums.slice(0, 8));
        if ((music.tracks || []).length) html += trackList(music.tracks.slice(0, 12));
        html += '</div>';
      }
      const contributors = (audiobooks && audiobooks.contributors) || [];
      const audiobookSeries = (audiobooks && audiobooks.series) || [];
      const audiobookItems = (audiobooks && audiobooks.audiobooks) || [];
      if (audiobookItems.length || contributors.length || audiobookSeries.length) {
        html += '<div class="search-group"><div class="search-group-head">// AUDIOBOOKS</div>';
        if (audiobookItems.length) html += audiobookGrid(audiobookItems.slice(0, 8));
        if (contributors.length) html += authorList(contributors.slice(0, 8));
        if (audiobookSeries.length) html += seriesList(audiobookSeries.slice(0, 8));
        html += '</div>';
      }
      const podcastShows = (podcasts && podcasts.podcasts) || [];
      const podcastEpisodes = (podcasts && podcasts.episodes) || [];
      if (podcastShows.length || podcastEpisodes.length) {
        html += '<div class="search-group"><div class="search-group-head">// PODCASTS</div>';
        if (podcastShows.length) html += podcastGrid(podcastShows.slice(0, 8));
        if (podcastEpisodes.length) html += episodeList(podcastEpisodes.slice(0, 8));
        html += '</div>';
      }
      if (!html) html = '<div class="empty-state">// no matches for "' + escapeHTML(query) + '"</div>';
      out.innerHTML = html;
    } catch (err) { out.innerHTML = '<div class="empty-state">// ' + escapeHTML(err.message) + '</div>'; }
  }

  /* -------- SETTINGS -------- */
  async function viewSettings() {
    renderLoading();
    try {
      let body = "";
      if (settingsMode === "libraries") {
        body = await settingsLibraries();
      } else if (settingsMode === "radio") {
        body = await settingsRadio();
      } else if (settingsMode === "podcasts") {
        body = await settingsPodcasts();
      } else if (settingsMode === "explo") {
        body = await settingsExplo();
      } else {
        body = await settingsAccount();
      }
      const pills = '<div class="pill-bar">' +
        '<button class="pill ' + (settingsMode === "libraries" ? "active" : "") + '" data-action="settings-mode" data-mode="libraries">LIBRARIES</button>' +
        '<button class="pill ' + (settingsMode === "radio" ? "active" : "") + '" data-action="settings-mode" data-mode="radio">RADIO</button>' +
        '<button class="pill ' + (settingsMode === "podcasts" ? "active" : "") + '" data-action="settings-mode" data-mode="podcasts">PODCASTS</button>' +
        '<button class="pill ' + (settingsMode === "explo" ? "active" : "") + '" data-action="settings-mode" data-mode="explo">EXPLO</button>' +
        '<button class="pill ' + (settingsMode === "account" ? "active" : "") + '" data-action="settings-mode" data-mode="account">ACCOUNT</button>' +
      '</div>';
      main.innerHTML = '<section class="view">' +
        '<div class="view-head"><h1>SETTINGS</h1><span class="crumb">// control room</span></div>' +
        pills + body +
      '</section>';
      bindSettingsForms();
    } catch (err) { renderError(err.message); }
  }

  async function settingsLibraries() {
    const [libraries, jobs, artistBackfill, missingFiles] = await Promise.all([
      api("/api/v1/libraries"),
      api("/api/v1/scan/jobs?limit=8").catch(() => ({ items: [] })),
      api("/api/v1/music/artists/images/backfill").catch(() => ({ job: null })),
      api("/api/v1/missing-files?limit=100").catch(() => ({ items: [] })),
    ]);
    const libs = (libraries && libraries.items) || [];
    const artistJob = artistBackfill && artistBackfill.job;
    rememberLibraries(libs);
    let html = '<div class="panel-grid">';
    html += '<form class="panel panel-wide settings-form" id="libraryForm">' +
      '<div class="panel-head"><span>// add library</span></div>' +
      '<div class="form-grid">' +
        fieldHTML("libraryName", "Name", "Music", "text", "") +
        fieldHTML("libraryPath", "Path", "/srv/media/music", "text", "") +
        '<label class="field"><span class="field-label">Kind</span><select id="libraryKind"><option value="mixed">Mixed (auto-detect)</option><option value="music">Music only</option><option value="audiobook">Audiobooks</option><option value="podcast">Podcasts</option></select></label>' +
        fieldHTML("libraryDescription", "Description", "optional", "text", "", "full") +
      '</div>' +
      '<div class="actions"><button class="btn primary" type="submit">ADD LIBRARY</button>' + globalScanActionsHTML({ btnClass: "btn ghost", primaryClass: "btn ghost" }) + '</div>' +
      '<div class="status-line" id="libraryMessage" hidden></div>' +
    '</form>';

    html += '<div class="panel panel-wide"><div class="panel-head"><span>// attached libraries</span><span>' + libs.length + '</span></div>';
    html += '<div class="empty-state" style="margin-bottom:12px">// filesystem changes trigger incremental album scans · SCAN ALL / header REFRESH = quick rescan · FULL SCAN = re-probe every file · missing files are flagged during full scans and can be removed below · artist photos auto-fetch after full scans and when new artists appear</div>';
    if (libs.length === 0) {
      html += '<div class="empty-state">// no libraries attached yet</div>';
    } else {
      html += '<div class="list">';
      libs.forEach((lib) => {
        html += '<div class="list-row">' +
          '<div class="num">·</div>' +
          '<div class="main"><div class="name">' + escapeHTML(lib.name) + '</div>' +
          '<div class="meta">' + escapeHTML(lib.path) + ' · ' + libraryKindLabel(lib) + ' · ' + (lib.itemCount || 0) + ' ITEMS · LAST SCAN ' + formatDate(lib.lastScanAt) + '</div></div>' +
          '<div class="actions">' + libraryScanActionsHTML(lib) +
            '<button class="btn danger btn-mini" data-action="delete-library" data-id="' + attr(lib.id) + '" data-name="' + attr(lib.name) + '">DELETE</button>' +
          '</div>' +
        '</div>';
      });
      html += '</div>';
    }
    html += '</div>';

    const missingItems = (missingFiles && missingFiles.items) || [];
    const missingTotal = (missingFiles && typeof missingFiles.total === "number") ? missingFiles.total : missingItems.length;
    html += '<div class="panel panel-wide"><div class="panel-head"><span>// missing files</span><span>' + missingTotal + '</span>';
    if (missingTotal > 0) {
      html += '<button class="btn danger btn-mini" type="button" data-action="remove-all-missing-files" data-total="' + attr(String(missingTotal)) + '">DELETE ALL</button>';
    }
    html += '</div>';
    html += '<div class="empty-state" style="margin-bottom:12px">// files flagged when a scan cannot find them on disk — remove stale catalog rows individually or delete all at once</div>';
    if (missingItems.length === 0) {
      html += '<div class="empty-state">// no missing files reported</div>';
    } else {
      html += '<div class="list">';
      missingItems.forEach((file) => {
        const label = file.trackTitle || file.albumTitle || file.relativePath || file.path;
        html += '<div class="list-row">' +
          '<div class="num">!</div>' +
          '<div class="main"><div class="name">' + escapeHTML(label) + '</div>' +
          '<div class="meta">' + escapeHTML(file.path) + (file.missingDetectedAt ? ' · DETECTED ' + formatDate(file.missingDetectedAt) : '') + '</div></div>' +
          '<div class="actions"><button class="btn danger btn-mini" data-action="remove-missing-file" data-id="' + attr(file.id) + '" data-label="' + attr(label) + '">REMOVE</button></div>' +
        '</div>';
      });
      html += '</div>';
    }
    html += '</div>';

    const scanJobs = (jobs && jobs.items) || [];
    html += '<div class="panel panel-wide"><div class="panel-head"><span>// scan jobs</span><span>' + scanJobs.length + '</span></div>';
    if (scanJobs.length === 0) {
      html += '<div class="empty-state">// no scans have run yet</div>';
    } else {
      html += '<div class="list">';
      scanJobs.forEach((job, idx) => {
        const seen = job.filesSeen || 0;
        const total = job.filesTotal || 0;
        const filesText = total > 0 ? (seen + " / " + total + " FILES") : (seen + " FILES");
        html += '<div class="list-row"><div class="num">' + String(idx + 1).padStart(2, "0") + '</div>' +
          '<div class="main"><div class="name">' + escapeHTML(job.status || "unknown").toUpperCase() + ' · ' + escapeHTML(job.scope || "scan").toUpperCase() + '</div>' +
          '<div class="meta">' + filesText + scanPruneSummary(job) + ' · STARTED ' + formatDate(job.startedAt) + (job.error ? ' · ' + escapeHTML(job.error) : '') + '</div></div></div>';
      });
      html += '</div>';
    }
    html += '</div>';

    html += '<div class="panel panel-wide"><div class="panel-head"><span>// artist photos</span></div>';
    html += '<div class="empty-state" style="margin-bottom:12px">// downloads artist images from Deezer (and Last.fm when available) into the local cover cache · also runs automatically after full scans and when new artists are indexed</div>';
    html += '<div id="artistImageJobPanel">' + renderArtistImageJobPanel(artistJob) + '</div>';
    html += '</div></div>';
    if (artistJob && (artistJob.status === "running" || artistJob.status === "pending")) {
      watchArtistImageBackfill();
    }
    return html;
  }

  // Populated by settingsRadio() from GET /api/v1/station-directory, which is
  // discovered rather than configured — so this is null on virtually every
  // install and the panel below simply never renders. Held at module scope so
  // the filter box can re-render rows without re-fetching.
  let stationDirectory = null;

  // Genre and "show all" are view state, not data, so they survive the
  // re-render that follows adding a station — you stay where you were browsing.
  let stationDirGenre = "";
  let stationDirShowAll = false;

  // A directory can carry hundreds of stations, and repainting all of them on
  // every keystroke makes the page feel broken. So the list is capped — but
  // browsing is never a dead end: the cap always comes with a button that lifts
  // it, and the genre bar puts most of the catalogue under 40 rows anyway.
  const STATION_DIR_ROW_CAP = 80;

  function stationDirGenreOf(station) {
    return ((station && station.genre) || "").trim() || "Unsorted";
  }

  // Genres come from the data rather than a hardcoded list, so a directory that
  // supplies different ones — or none, in which case this is a single bar with
  // everything under "Unsorted" — still works.
  function stationDirectoryGenres() {
    const all = (stationDirectory && stationDirectory.stations) || [];
    const counts = new Map();
    all.forEach((s) => {
      const g = stationDirGenreOf(s);
      counts.set(g, (counts.get(g) || 0) + 1);
    });
    return Array.from(counts.entries()).sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
  }

  function stationDirectoryMatches() {
    const all = (stationDirectory && stationDirectory.stations) || [];
    const input = document.getElementById("stationDirFilter");
    const q = ((input && input.value) || "").trim().toLowerCase();
    return all.filter((s) => {
      if (stationDirGenre && stationDirGenreOf(s) !== stationDirGenre) return false;
      if (!q) return true;
      return (s.name || "").toLowerCase().includes(q) ||
        String(s.number == null ? "" : s.number).startsWith(q);
    });
  }

  function stationDirectoryGenreBar() {
    const total = ((stationDirectory && stationDirectory.stations) || []).length;
    let html = '<button class="pill ' + (stationDirGenre === "" ? "active" : "") + '" data-dir-genre="">' +
      'ALL <span class="pill-count">' + total + '</span></button>';
    stationDirectoryGenres().forEach((pair) => {
      html += '<button class="pill ' + (stationDirGenre === pair[0] ? "active" : "") + '" data-dir-genre="' + attr(pair[0]) + '">' +
        escapeHTML(pair[0]) + ' <span class="pill-count">' + pair[1] + '</span></button>';
    });
    return html;
  }

  function stationDirectoryRows() {
    const matched = stationDirectoryMatches();
    if (matched.length === 0) return '<div class="empty-state">// nothing matches that</div>';
    const cap = stationDirShowAll ? matched.length : STATION_DIR_ROW_CAP;
    let rows = "";
    matched.slice(0, cap).forEach((s) => {
      rows += '<div class="list-row">' +
        '<div class="num">' + (s.number == null ? "" : s.number) + '</div>' +
        '<div class="main"><div class="name">' + escapeHTML(s.name) + '</div>' +
        '<div class="meta">' + escapeHTML(s.description || s.streamUrl) + '</div></div>' +
        '<div class="actions">' +
          '<button class="btn primary btn-mini" data-action="station-dir-add" data-name="' + attr(s.name) + '" data-url="' + attr(s.streamUrl) + '">ADD</button>' +
          // previewUrl, not streamUrl: the browser must play samo's own proxied
          // URL. streamUrl points at the directory's host, which is loopback
          // when it is colocated with samo — unreachable from a listener's
          // machine. streamUrl is still what gets SAVED, since ffmpeg opens
          // that one server-side.
          '<button class="btn ghost btn-mini" data-action="play-url" data-url="' + attr(s.previewUrl || s.streamUrl) + '" data-title="' + attr(s.name) + '" data-sub="Station directory">PLAY</button>' +
        '</div>' +
      '</div>';
    });
    if (matched.length > cap) {
      rows += '<div class="actions" style="padding:12px 0">' +
        '<button class="btn ghost" data-dir-showall="1">SHOW ALL ' + matched.length + '</button>' +
        '</div>';
    }
    return rows;
  }

  // Repaints the bar and the list in place. Deliberately not a viewSettings()
  // call: that would refetch everything and throw away the filter text.
  function renderStationDirectory() {
    const bar = document.getElementById("stationDirGenres");
    if (bar) bar.innerHTML = stationDirectoryGenreBar();
    const list = document.getElementById("stationDirList");
    if (list) list.innerHTML = stationDirectoryRows();
  }

  function stationDirectoryPanel() {
    if (!stationDirectory || !stationDirectory.available) return "";
    const count = ((stationDirectory.stations) || []).length;
    return '<div class="panel panel-wide" id="stationDirPanel">' +
      '<div class="panel-head"><span>// station directory</span><span>' + count + '</span></div>' +
      '<div class="empty-state" style="margin-bottom:12px">// found a station directory on this host at ' +
        escapeHTML(stationDirectory.baseUrl) + ' — PLAY to audition, ADD to save it as an internet station</div>' +
      '<div class="pill-bar" id="stationDirGenres" style="margin-bottom:12px">' + stationDirectoryGenreBar() + '</div>' +
      '<label class="field"><span class="field-label">Filter</span>' +
        '<input id="stationDirFilter" type="text" placeholder="name or channel number" autocomplete="off"></label>' +
      '<div class="list" id="stationDirList">' + stationDirectoryRows() + '</div>' +
    '</div>';
  }

  async function settingsRadio() {
    // Fetched together so a directory probe never adds latency on top of the
    // station list. A failure here is normal, not exceptional: no directory
    // running is the default state.
    const [data, directory] = await Promise.all([
      api("/api/v1/internet-radio/stations").catch(() => ({ items: [] })),
      api("/api/v1/station-directory").catch(() => ({ available: false })),
    ]);
    stationDirectory = directory || { available: false };
    const stations = (data && data.items) || [];
    let html = '<div class="panel-grid">';
    html += stationDirectoryPanel();
    html += '<form class="panel panel-wide settings-form" id="internetRadioForm">' +
      '<div class="panel-head"><span>// add internet station</span></div>' +
      '<div class="form-grid">' +
        fieldHTML("radioName", "Name", "WFMU", "text", "") +
        fieldHTML("radioStream", "Stream URL", "https://example.com/live.mp3", "url", "") +
        fieldHTML("radioHomepage", "Homepage", "https://example.com", "url", "") +
        fieldHTML("radioImage", "Cover Image URL", "https://example.com/logo.png", "url", "") +
        fieldHTML("radioTags", "Tags", "jazz, late night", "text", "") +
        fieldHTML("radioDescription", "Description", "optional", "text", "", "full") +
        '<label class="field checkbox full"><input id="radioEnabled" type="checkbox" checked><span>Enabled</span></label>' +
      '</div>' +
      '<div class="actions"><button class="btn primary" type="submit">ADD STATION</button><button class="btn ghost" type="button" data-action="probe-all-radio">PROBE ALL</button></div>' +
      '<div class="status-line" id="radioMessage" hidden></div>' +
    '</form>';

    html += '<div class="panel panel-wide"><div class="panel-head"><span>// internet radio</span><span>' + stations.length + '</span></div>';
    if (stations.length === 0) {
      html += '<div class="empty-state">// no internet stations yet</div>';
    } else {
      html += '<div class="list">';
      stations.forEach((station) => {
        const np = station.nowPlaying || null;
        const imageInputID = "radio-image-" + station.id;
        html += '<div class="list-row">' +
          '<div class="num">' + (station.enabled ? "ON" : "OFF") + '</div>' +
          '<div class="main"><div class="name">' + escapeHTML(station.name) + '</div>' +
          '<div class="meta">' + escapeHTML(station.streamUrl) + ' · ' + (np ? escapeHTML(np.raw || np.title || "") : "NO METADATA") + ' · CHECKED ' + formatDate(station.lastCheckedAt) + '</div>' +
          '<div class="radio-image-edit">' +
            '<input id="' + attr(imageInputID) + '" type="url" placeholder="Thumbnail URL" value="' + attr(station.imageUrl || "") + '">' +
            '<button class="btn ghost btn-mini" data-action="save-radio-image" data-id="' + attr(station.id) + '" data-input="' + attr(imageInputID) + '">SAVE IMAGE</button>' +
          '</div></div>' +
          '<div class="actions">' +
            '<button class="btn primary btn-mini" data-action="play-url" data-url="' + attr(station.publicStreamUrl || station.streamUrl) + '" data-title="' + attr(station.name) + '" data-sub="Internet radio">PLAY</button>' +
            '<button class="btn ghost btn-mini" data-action="probe-radio" data-id="' + attr(station.id) + '">PROBE</button>' +
            '<button class="btn ghost btn-mini" data-action="toggle-radio" data-id="' + attr(station.id) + '" data-enabled="' + (!station.enabled) + '">' + (station.enabled ? "DISABLE" : "ENABLE") + '</button>' +
            '<button class="btn danger btn-mini" data-action="delete-radio" data-id="' + attr(station.id) + '" data-name="' + attr(station.name) + '">DELETE</button>' +
          '</div>' +
        '</div>';
      });
      html += '</div>';
    }
    html += '</div></div>';
    return html;
  }

  async function settingsPodcasts() {
    const [feedData, cacheData] = await Promise.all([
      api("/api/v1/podcasts/feeds?limit=80").catch(() => ({ items: [] })),
      api("/api/v1/podcasts/cache").catch(() => ({ enabled: false })),
    ]);
    const feeds = (feedData && feedData.items) || [];
    let html = '<div class="panel-grid">';
    html += '<div class="panel panel-wide">' +
      '<div class="panel-head"><span>// enclosure cache</span></div>' +
      '<div class="empty-state" style="margin-bottom:12px">// Samo stores downloaded RSS audio on disk before streaming. Clear this if episodes play silence or wrong audio after a server upgrade.</div>';
    if (!cacheData || !cacheData.enabled) {
      html += '<div class="empty-state">// podcast enclosure cache is disabled on this server</div>';
    } else {
      html += '<div class="empty-state" style="margin-bottom:12px">// ' +
        (cacheData.episodeCount || 0) + ' episodes · ' + formatDataSize(cacheData.totalBytes || 0) + ' on disk</div>' +
        '<div class="actions"><button class="btn danger" type="button" data-action="clear-podcast-cache">CLEAR ENCLOSURE CACHE</button></div>';
    }
    html += '</div>';
    html += '<form class="panel panel-wide settings-form" id="podcastFeedForm">' +
      '<div class="panel-head"><span>// add podcast feed</span></div>' +
      '<div class="form-grid">' +
        fieldHTML("podcastTitle", "Title", "optional", "text", "") +
        fieldHTML("podcastURL", "Feed URL", "https://example.com/feed.xml", "url", "", "full") +
      '</div>' +
      '<label class="field checkbox full"><input id="podcastAutoDownload" type="checkbox"><span>Auto-download new episodes</span></label>' +
      '<div class="actions"><button class="btn primary" type="submit">ADD FEED</button><button class="btn ghost" type="button" data-action="poll-podcasts">POLL ALL</button></div>' +
      '<div class="status-line" id="podcastMessage" hidden></div>' +
    '</form>';

    html += '<div class="panel panel-wide"><div class="panel-head"><span>// podcast feeds</span><span>' + feeds.length + '</span></div>';
    if (feeds.length === 0) {
      html += '<div class="empty-state">// no podcast feeds yet</div>';
    } else {
      html += '<div class="list">';
      feeds.forEach((feed) => {
        html += '<div class="list-row">' +
          '<div class="num">' + escapeHTML(feed.status || "NEW").toUpperCase() + '</div>' +
          '<div class="main"><div class="name">' + escapeHTML(feed.title || feed.feedUrl) + '</div>' +
          '<div class="meta">' + escapeHTML(feed.feedUrl) + ' · ' + (feed.episodeCount || 0) + ' EPISODES · FETCHED ' + formatDate(feed.lastFetchedAt) + (feed.lastError ? ' · ' + escapeHTML(feed.lastError) : '') + '</div></div>' +
          '<div class="actions">' +
            '<label class="field checkbox" title="Auto-download new episodes"><input type="checkbox" data-action="toggle-feed-download" data-id="' + attr(feed.id) + '"' + (feed.autoDownloadEnabled ? ' checked' : '') + '><span>AUTO</span></label>' +
            '<button class="btn ghost btn-mini" data-action="refresh-feed" data-id="' + attr(feed.id) + '">REFRESH</button>' +
            '<button class="btn danger btn-mini" data-action="delete-feed" data-id="' + attr(feed.id) + '" data-name="' + attr(feed.title || feed.feedUrl) + '">DELETE</button>' +
          '</div>' +
        '</div>';
      });
      html += '</div>';
    }
    html += '</div></div>';
    return html;
  }

  let exploBrowsePath = "";

  async function exploLoadDirs(path) {
    const url = "/api/v1/explo/directories" + (path ? "?path=" + encodeURIComponent(path) : "");
    const data = await api(url);
    renderExploDirs(data);
  }

  function renderExploDirs(data) {
    const list = document.getElementById("exploBrowseList");
    const head = document.getElementById("exploBrowsePath");
    if (!list) return;
    exploBrowsePath = data.path || "";
    if (head) head.textContent = data.path || "// suggested locations";
    list.innerHTML = "";
    (data.entries || []).forEach((entry) => {
      const row = document.createElement("div");
      row.className = "browser-row" + (entry.isParent ? " is-parent" : "");
      const left = document.createElement("div");
      left.textContent = entry.isParent ? ".. /" : entry.name;
      const right = document.createElement("div");
      right.className = "meta";
      if (entry.isParent) right.textContent = "PARENT";
      else if (entry.isRoot) right.textContent = "SHORTCUT";
      else if (entry.itemCount) right.textContent = entry.itemCount + " ITEMS";
      else right.textContent = "EMPTY";
      row.appendChild(left);
      row.appendChild(right);
      row.addEventListener("click", () => {
        exploLoadDirs(entry.path).catch((e) => setMessage("exploConfigMessage", e.message, true));
      });
      list.appendChild(row);
    });
  }

  async function settingsExplo() {
    const [me, config] = await Promise.all([
      api("/api/v1/users/me").catch(() => ({ role: "user" })),
      api("/api/v1/explo/config").catch(() => null),
    ]);
    const cfg = config || {};
    const isAdmin = me.role === "admin";
    const statusText = cfg.enabled ? "ENABLED" : "DISABLED";
    const sourceText = cfg.source === "ui" ? "UI" : (cfg.source === "environment" ? "ENV VAR" : "—");
    let html = '<div class="panel-grid">';
    html += '<div class="panel panel-wide">' +
      '<div class="panel-head"><span>// explo folder</span><span>' + statusText + '</span></div>' +
      '<div class="empty-state" style="margin-bottom:12px">// Point Samo at the folder your weekly &ldquo;explo&rdquo; exporter drops untagged tracks into. Samo fingerprints them (AcoustID, with a MusicBrainz fallback), fixes their metadata, gathers them into the &ldquo;Explo&rdquo; playlist on your apps, and keeps them out of Recently Added. Files on disk are never modified. The folder must sit inside a library Samo already scans.</div>';
    html += '<div class="empty-state" style="margin-bottom:12px">// folder: ' + (cfg.folder ? escapeHTML(cfg.folder) : "&lt;none set&gt;") +
      ' · source: ' + sourceText +
      ' · AcoustID key: ' + (cfg.hasApiKey ? "set" : "MISSING") +
      ' · fpcalc: ' + (cfg.fpcalcReady ? "ready" : "MISSING") + '</div>';
    if (!cfg.fpcalcReady) {
      html += '<div class="empty-state explo-warn" style="margin-bottom:12px">// fpcalc (chromaprint) is not bundled on this server, so the pipeline cannot run. Run <code>make bundle-chromaprint</code> before building the release, or install fpcalc on the host.</div>';
    }
    if (!isAdmin) {
      html += '<div class="panel-sub">// an admin must configure the explo folder</div></div></div>';
      return html;
    }
    html += '<form class="settings-form" id="exploConfigForm">' +
      '<div class="form-grid">' +
        '<label class="field full"><span class="field-label">Explo folder (absolute path on the server)</span>' +
          '<input id="exploFolder" type="text" placeholder="/srv/media/music/explo" value="' + attr(cfg.folder || "") + '"></label>' +
      '</div>' +
      '<div class="actions"><button class="btn ghost" type="button" id="exploBrowseToggle">BROWSE&hellip;</button></div>' +
      '<div id="exploBrowser" class="explo-browser" hidden>' +
        '<div class="browser-head"><span id="exploBrowsePath">// choose a folder</span></div>' +
        '<div class="browser-list" id="exploBrowseList"></div>' +
        '<div class="actions"><button class="btn primary btn-mini" type="button" id="exploBrowseUse">USE THIS FOLDER</button></div>' +
      '</div>' +
      '<div class="form-grid">' +
        fieldHTML("exploAPIKey", "AcoustID API key", (cfg.hasApiKey ? "leave blank to keep current" : "AcoustID application key"), "password", "", "full") +
      '</div>' +
      '<div class="actions"><button class="btn primary" type="submit">SAVE</button>' +
        '<button class="btn danger" type="button" id="exploClear">DISABLE &amp; CLEAR</button></div>' +
      '<div class="status-line" id="exploConfigMessage" hidden></div>' +
      '<div class="actions"><button class="btn ghost" type="button" id="exploReprocess" title="Retry identification for tracks that never matched (e.g. after an AcoustID outage) and re-fetch per-track cover art.">RE-SCAN &amp; RETRY METADATA</button></div>' +
      '<div class="status-line" id="exploReprocessMessage" hidden></div>' +
    '</form>';
    html += '</div></div>';
    return html;
  }

  async function settingsAccount() {
    const [me, tokens, lastfmStatus, lastfmConfig, users] = await Promise.all([
      api("/api/v1/users/me"),
      api("/api/v1/users/me/tokens").catch(() => ({ items: [] })),
      api("/api/v1/lastfm/status").catch(() => ({ enabled: false })),
      api("/api/v1/lastfm/config").catch(() => null),
      api("/api/v1/users").catch(() => null),
    ]);
    let html = '<div class="account-layout">' +
      '<div class="account-row account-row-2">';
    html += '<form class="panel" id="profileForm">' +
      '<div class="panel-head"><span>// profile</span><span>' + escapeHTML(me.role || "user").toUpperCase() + '</span></div>' +
      '<div class="form-grid">' +
        fieldHTML("displayName", "Display Name", "optional", "text", me.displayName || "") +
        fieldHTML("newPassword", "New Password", "leave blank", "password", "") +
      '</div>' +
      '<div class="actions"><button class="btn primary" type="submit">SAVE PROFILE</button></div>' +
      '<div class="status-line" id="profileMessage" hidden></div>' +
    '</form>';

    html += '<form class="panel" id="tokenForm">' +
      '<div class="panel-head"><span>// api tokens</span><span>' + (((tokens && tokens.items) || []).length) + '</span></div>' +
      fieldHTML("tokenLabel", "Label", "phone, desktop, script", "text", "") +
      '<div class="actions"><button class="btn primary" type="submit">ISSUE TOKEN</button></div>' +
      '<div class="secret-line" id="tokenSecret" hidden></div>' +
      '<div class="status-line" id="tokenMessage" hidden></div>' +
    '</form></div>' +
      '<div class="account-row">';

    const configReady = lastfmConfig && lastfmConfig.enabled;
    const configStatus = configReady ? "CREDENTIALS READY" : "CREDENTIALS NEEDED";
    const accountStatus = lastfmStatus.enabled ? (lastfmStatus.connected ? "ACCOUNT CONNECTED" : "ACCOUNT READY") : "ACCOUNT OFF";
    const utilityStatus = lastfmStatus.connected ? "CONNECTED" : (lastfmStatus.enabled ? "READY" : "SETUP NEEDED");
    const secretPlaceholder = lastfmConfig && lastfmConfig.hasSharedSecret ? "leave blank to keep current" : "shared secret";
    html += '<div class="panel panel-wide lastfm-utility">' +
      '<div class="panel-head"><span>// last.fm utility</span><span>' + utilityStatus + '</span></div>' +
      '<div class="lastfm-utility-grid">';
    if (me.role === "admin") {
      html += '<form class="lastfm-utility-section" id="lastfmConfigForm">' +
        '<div class="lastfm-utility-label">' + configStatus + '</div>' +
        '<div class="form-grid">' +
          fieldHTML("lastfmAPIKey", "API Key", "api key", "text", (lastfmConfig && lastfmConfig.apiKey) || "") +
          fieldHTML("lastfmSharedSecret", "Shared Secret", secretPlaceholder, "password", "") +
        '</div>' +
        '<div class="actions"><button class="btn primary" type="submit">SAVE CREDENTIALS</button>' +
          '<button class="btn danger" type="button" data-action="lastfm-clear-config">CLEAR CREDENTIALS</button></div>' +
        '<div class="status-line" id="lastfmConfigMessage" hidden></div>' +
      '</form>';
    } else {
      html += '<div class="lastfm-utility-section">' +
        '<div class="lastfm-utility-label">' + configStatus + '</div>' +
        '<div class="panel-sub">' + (configReady ? 'Server credentials are configured.' : 'An admin needs to add Last.fm credentials before account linking is available.') + '</div>' +
      '</div>';
    }
    html += '<div class="lastfm-utility-section">' +
      '<div class="lastfm-utility-label">' + accountStatus + '</div>' +
      '<div class="panel-sub">' + (lastfmStatus.connected ? 'Connected as ' + escapeHTML(lastfmStatus.username || "") + ' · queue ' + (lastfmStatus.queueSize || 0) : (lastfmStatus.enabled ? 'Connect this Samo user to a Last.fm account for scrobbling.' : 'Save credentials here first, then connect this user.')) + '</div>' +
      '<div class="actions">' +
        (lastfmStatus.enabled && !lastfmStatus.connected ? '<button class="btn primary" type="button" data-action="lastfm-begin">CONNECT LAST.FM</button><button class="btn ghost" type="button" data-action="lastfm-complete">COMPLETE LINK</button>' : '') +
        (lastfmStatus.enabled && lastfmStatus.connected ? '<button class="btn ghost" type="button" data-action="lastfm-flush">FLUSH QUEUE</button><button class="btn danger" type="button" data-action="lastfm-disconnect">DISCONNECT</button>' : '') +
      '</div>' +
      '<div class="status-line" id="lastfmMessage" hidden></div>' +
    '</div></div></div></div>' +
      '<div class="account-row">';

    const tokenItems = (tokens && tokens.items) || [];
    html += '<div class="panel panel-wide"><div class="panel-head"><span>// issued tokens</span><span>' + tokenItems.length + '</span></div>';
    if (tokenItems.length === 0) {
      html += '<div class="empty-state">// no tokens issued</div>';
    } else {
      html += '<div class="list">';
      tokenItems.forEach((item, idx) => {
        html += '<div class="list-row"><div class="num">' + String(idx + 1).padStart(2, "0") + '</div>' +
          '<div class="main"><div class="name">' + escapeHTML(item.label || "token") + '</div>' +
          '<div class="meta">CREATED ' + formatDate(item.createdAt) + ' · LAST USED ' + formatDate(item.lastUsedAt) + '</div></div>' +
          '<div class="actions"><button class="btn danger btn-mini" data-action="revoke-token" data-id="' + attr(item.id) + '">REVOKE</button></div></div>';
      });
      html += '</div>';
    }
    html += '</div></div>';

    if (users && Array.isArray(users.items)) {
      html += '<div class="account-row account-row-2">';
      html += '<form class="panel" id="userForm">' +
        '<div class="panel-head"><span>// create user</span></div>' +
        '<div class="form-grid">' +
          fieldHTML("newUsername", "Username", "alex", "text", "") +
          fieldHTML("newUserDisplay", "Display Name", "optional", "text", "") +
          fieldHTML("newUserPassword", "Password", "8+ characters", "password", "") +
          '<label class="field"><span class="field-label">Role</span><select id="newUserRole"><option value="user">User</option><option value="admin">Admin</option></select></label>' +
        '</div>' +
        '<div class="actions"><button class="btn primary" type="submit">CREATE USER</button></div>' +
        '<div class="status-line" id="userMessage" hidden></div>' +
      '</form>';
      html += '<div class="panel"><div class="panel-head"><span>// users</span><span>' + users.items.length + '</span></div><div class="list">';
      users.items.forEach((user, idx) => {
        html += '<div class="list-row"><div class="num">' + String(idx + 1).padStart(2, "0") + '</div>' +
          '<div class="main"><div class="name">' + escapeHTML(user.username) + '</div>' +
          '<div class="meta">' + escapeHTML(user.displayName || "") + ' · ' + escapeHTML(user.role || "user").toUpperCase() + '</div></div></div>';
      });
      html += '</div></div></div>';
    }

    html += '</div>';
    return html;
  }

  async function composerSubmit(name) {
    if (name === "samo-radio-device") {
      const deviceName = document.getElementById("composerRadioDeviceName").value.trim();
      const baseURL = document.getElementById("composerRadioDeviceURL").value.trim();
      if (!deviceName) return composerMessage(name, "name is required", true);
      if (!baseURL) return composerMessage(name, "control URL is required", true);
      const device = await api("/api/v1/samo-radio/devices", {
        method: "POST",
        body: {
          name: deviceName,
          baseUrl: baseURL,
          controlToken: document.getElementById("composerRadioDeviceToken").value.trim(),
        },
      });
      // Pair immediately: a registered-but-unpaired device cannot do anything,
      // so making it a second click would only create a state to get stuck in.
      // A failure here leaves the device registered and re-pairable.
      if (device && device.id) {
        try {
          await api("/api/v1/samo-radio/devices/" + encodeURIComponent(device.id) + "/pair", { method: "POST" });
        } catch (pairErr) {
          composerMessage(name, "added, but pairing failed: " + (pairErr.message || "unknown error"), true);
        }
      }
      composerClose(name);
      radioMode = "samo-radio";
      samoRadioDevices = [];
      await viewRadio();
    } else if (name === "radio-station") {
      const stream = document.getElementById("composerRadioStream").value.trim();
      const stationName = document.getElementById("composerRadioName").value.trim();
      if (!stream) return composerMessage(name, "stream URL is required", true);
      if (!stationName) return composerMessage(name, "name is required", true);
      const station = await api("/api/v1/internet-radio/stations", {
        method: "POST",
        body: {
          name: stationName,
          streamUrl: stream,
          homepageUrl: document.getElementById("composerRadioHomepage").value.trim(),
          tags: splitTags(document.getElementById("composerRadioTags").value),
          enabled: true,
        },
      });
      const coverInput = document.getElementById("composerRadioCover");
      if (coverInput && coverInput.files && coverInput.files[0] && station && station.id) {
        await uploadRadioCover(station.id, coverInput.files[0]);
      }
      composerClose(name);
      await viewRadio();
    } else if (name === "podcast-feed") {
      const url = document.getElementById("composerPodcastURL").value.trim();
      if (!url) return composerMessage(name, "feed URL is required", true);
      const autoDownload = document.getElementById("composerPodcastAutoDownload");
      await api("/api/v1/podcasts/feeds", {
        method: "POST",
        body: {
          url: url,
          title: document.getElementById("composerPodcastTitle").value.trim(),
          autoDownloadEnabled: autoDownload ? autoDownload.checked : false,
        },
      });
      composerClose(name);
      await viewPodcasts();
    } else if (name === "podcast-attach-feed") {
      const showID = document.getElementById("composerPodcastAttachShowId").value.trim();
      const url = document.getElementById("composerPodcastAttachURL").value.trim();
      if (!showID) return composerMessage(name, "podcast id is required", true);
      if (!url) return composerMessage(name, "feed URL is required", true);
      const autoDownload = document.getElementById("composerPodcastAttachAutoDownload");
      await api("/api/v1/podcasts/shows/" + encodeURIComponent(showID) + "/feeds", {
        method: "POST",
        body: {
          url: url,
          autoDownloadEnabled: autoDownload ? autoDownload.checked : false,
        },
      });
      composerClose(name);
      await openPodcast(showID);
    } else if (name === "library") {
      const path = document.getElementById("composerLibPath").value.trim();
      if (!path) return composerMessage(name, "path is required", true);
      const body = {
        name: document.getElementById("composerLibName").value.trim(),
        path: path,
        kind: document.getElementById("composerLibKind").value,
      };
      await api("/api/v1/libraries", { method: "POST", body: body });
      composerClose(name);
      await viewHome();
    } else if (name === "playlist") {
      const playlistName = document.getElementById("composerPlaylistName").value.trim();
      if (!playlistName) return composerMessage(name, "playlist name is required", true);
      const playlist = await api("/api/v1/music/playlists", {
        method: "POST",
        body: {
          name: playlistName,
          description: document.getElementById("composerPlaylistDescription").value.trim(),
          public: document.getElementById("composerPlaylistPublic").checked,
        },
      });
      composerClose(name);
      navigateTo("music/playlist/" + encodeURIComponent(playlist.id));
    } else if (name === "playlist-edit") {
      const playlistID = document.getElementById("composerPlaylistEditId").value.trim();
      const playlistName = document.getElementById("composerPlaylistEditName").value.trim();
      if (!playlistID) return composerMessage(name, "playlist id is required", true);
      if (!playlistName) return composerMessage(name, "playlist name is required", true);
      await api("/api/v1/music/playlists/" + encodeURIComponent(playlistID), {
        method: "PATCH",
        body: {
          name: playlistName,
          description: document.getElementById("composerPlaylistEditDescription").value.trim(),
          public: document.getElementById("composerPlaylistEditPublic").checked,
        },
      });
      composerClose(name);
      navigateTo("music/playlist/" + encodeURIComponent(playlistID));
    } else if (name === "channel") {
      const channelName = document.getElementById("composerChannelName").value.trim();
      if (!channelName) return composerMessage(name, "channel name is required", true);
      const bitrate = parseInt(document.getElementById("composerChannelBitrate").value || "192", 10) || 192;
      const created = await api("/api/v1/channels", {
        method: "POST",
        body: {
          name: channelName,
          description: document.getElementById("composerChannelDescription").value.trim(),
          codec: document.getElementById("composerChannelCodec").value,
          bitrateKbps: bitrate,
        },
      });
      composerClose(name);
      activeChannelID = created.id;
      await viewRadio();
    } else if (name === "channel-content") {
      const channelID = document.querySelector('[data-composer="channel-content"][data-action="composer-submit"]').dataset.channelId;
      let picked;
      try { picked = readContentPicker("composerContent"); }
      catch (pickError) { return composerMessage(name, pickError.message, true); }
      const role = document.getElementById("composerContentRole").value;
      // Sequential, not Promise.all: the failure a batch actually hits is the
      // server rejecting one of them, and a partially-applied parallel burst
      // leaves you guessing which. Whatever landed before the error stays,
      // and the message names the one that stopped it.
      let added = 0;
      for (const item of picked) {
        try {
          await api("/api/v1/channels/" + encodeURIComponent(channelID) + "/sources", {
            method: "POST",
            body: {
              config: item.config, enabled: true, kind: item.kind, label: item.label,
              defaultRotation: role !== "show", role: role,
            },
          });
        } catch (addError) {
          await viewRadio();
          return composerMessage(name,
            "added " + added + " of " + picked.length + " — \"" + item.label + "\" was rejected: " +
            (addError.message || "unknown error"), true);
        }
        added++;
      }
      composerClose(name);
      if (added > 1) setStatus("ADDED " + added + " TO THE MIX");
      await viewRadio();
    } else if (name === "channel-show") {
      const channelID = document.querySelector('[data-composer="channel-show"][data-action="composer-submit"]').dataset.channelId;
      let picks;
      try { picks = readContentPicker("composerShow"); }
      catch (pickError) { return composerMessage(name, pickError.message, true); }
      // The show picker is single-select: N sources sharing one window is a
      // rotation inside a slot, which the plan generator has no pool shape for.
      const picked = picks[0];
      const startMin = parseHHMM(document.getElementById("composerShowStart").value);
      const endMin = parseHHMM(document.getElementById("composerShowEnd").value);
      if (startMin < 0 || endMin < 0) return composerMessage(name, "start and end must be HH:MM, e.g. 16:00", true);
      if (startMin === endMin) return composerMessage(name, "start and end cannot be the same time", true);
      const mask = parseInt(document.getElementById("composerShowDays").value || "127", 10) || 127;
      // The content and its slot are created together: a show is one thing,
      // so a half-made show (a source with no window) is never left behind.
      const source = await api("/api/v1/channels/" + encodeURIComponent(channelID) + "/sources", {
        method: "POST",
        body: {
          config: picked.config, enabled: true, kind: picked.kind, label: picked.label,
          defaultRotation: false, role: "show",
        },
      });
      for (const window of scheduleWindows(startMin, endMin)) {
        await api("/api/v1/channels/" + encodeURIComponent(channelID) + "/schedule", {
          method: "POST",
          body: {
            enabled: true, endMinute: window.endMinute, label: picked.label,
            priority: 200, sourceId: source.id, startMinute: window.startMinute,
            weekdayMask: mask,
          },
        });
      }
      composerClose(name);
      await viewRadio();
    } else if (name === "channel-schedule") {
      const channelID = document.querySelector('[data-composer="channel-schedule"][data-action="composer-submit"]').dataset.channelId;
      const sourceID = document.getElementById("composerSchedSource").value;
      if (!sourceID) return composerMessage(name, "pick a source", true);
      const startMin = parseHHMM(document.getElementById("composerSchedStart").value);
      const endMin = parseHHMM(document.getElementById("composerSchedEnd").value);
      if (startMin < 0 || endMin < 0) return composerMessage(name, "start/end must be HH:MM (e.g. 16:00)", true);
      if (endMin <= startMin) return composerMessage(name, "end must be after start (for cross-midnight, add two rules)", true);
      const priority = parseInt(document.getElementById("composerSchedPriority").value || "100", 10) || 100;
      const mask = parseInt(document.getElementById("composerSchedDays").value || "127", 10) || 127;
      await api("/api/v1/channels/" + encodeURIComponent(channelID) + "/schedule", {
        method: "POST",
        body: {
          sourceId: sourceID,
          label: document.getElementById("composerSchedLabel").value.trim(),
          weekdayMask: mask,
          startMinute: startMin,
          endMinute: endMin,
          priority: priority,
          enabled: true,
        },
      });
      composerClose(name);
      await viewRadio();
    } else if (name === "playlist-import") {
      const playlistName = document.getElementById("composerImportName").value.trim();
      const url = document.getElementById("composerImportURL").value.trim();
      const content = document.getElementById("composerImportContent").value.trim();
      if (!playlistName) return composerMessage(name, "playlist name is required", true);
      if (!url && !content) return composerMessage(name, "paste content or provide a url", true);
      const result = await api("/api/v1/music/playlists/import", {
        method: "POST",
        body: {
          name: playlistName,
          sourceType: document.getElementById("composerImportSource").value,
          url: url,
          content: content,
          public: document.getElementById("composerImportPublic").checked,
        },
      });
      const summary = "matched " + (result.matchedCount || 0) + " of " + (result.parsedCount || 0) + " imported rows" + ((result.unmatchedCount || 0) ? " · " + result.unmatchedCount + " unmatched" : "");
      if (result.unmatchedCount) alert("Playlist imported with gaps: " + summary);
      composerClose(name);
      if (result.playlist && result.playlist.id) {
        navigateTo("music/playlist/" + encodeURIComponent(result.playlist.id));
      } else {
        composerMessage(name, summary, false);
      }
    }
  }

  function bindSettingsForms() {
    const libraryForm = document.getElementById("libraryForm");
    const libraryPathInput = document.getElementById("libraryPath");
    const libraryKindSelect = document.getElementById("libraryKind");
    if (libraryPathInput && libraryKindSelect) {
      libraryPathInput.addEventListener("change", () => {
        const base = String(libraryPathInput.value || "").trim().split("/").filter(Boolean).pop() || "";
        const lower = base.toLowerCase();
        if (lower.includes("podcast")) libraryKindSelect.value = "podcast";
        else if (lower.includes("audiobook") || lower === "books") libraryKindSelect.value = "audiobook";
        else if (lower.includes("music")) libraryKindSelect.value = "music";
      });
    }
    if (libraryForm) {
      libraryForm.addEventListener("submit", async (event) => {
        event.preventDefault();
        const body = {
          name: document.getElementById("libraryName").value.trim(),
          path: document.getElementById("libraryPath").value.trim(),
          description: document.getElementById("libraryDescription").value.trim(),
          kind: document.getElementById("libraryKind").value,
        };
        try {
          await api("/api/v1/libraries", { method: "POST", body: body });
          setMessage("libraryMessage", "library attached", false);
          await viewSettings();
        } catch (err) { setMessage("libraryMessage", err.message, true); }
      });
    }

    // Live filter over the discovered directory. Re-renders only the list, so
    // typing never rebuilds the whole settings view or re-hits the server.
    const stationDirFilter = document.getElementById("stationDirFilter");
    if (stationDirFilter) {
      stationDirFilter.addEventListener("input", renderStationDirectory);
    }

    // Genre and show-all are wired here rather than through the global
    // data-action handler because that handler re-runs viewSettings(), which
    // would refetch the directory and wipe the filter text on every click.
    // Delegated from the containers, which survive their own innerHTML swaps.
    const stationDirGenres = document.getElementById("stationDirGenres");
    if (stationDirGenres) {
      stationDirGenres.addEventListener("click", (event) => {
        const pill = event.target.closest("[data-dir-genre]");
        if (!pill) return;
        stationDirGenre = pill.dataset.dirGenre || "";
        // A fresh genre starts capped again, so switching back to ALL never
        // silently paints the whole catalogue.
        stationDirShowAll = false;
        renderStationDirectory();
      });
    }

    const stationDirList = document.getElementById("stationDirList");
    if (stationDirList) {
      stationDirList.addEventListener("click", (event) => {
        if (!event.target.closest("[data-dir-showall]")) return;
        stationDirShowAll = true;
        renderStationDirectory();
      });
    }

    const radioForm = document.getElementById("internetRadioForm");
    if (radioForm) {
      radioForm.addEventListener("submit", async (event) => {
        event.preventDefault();
        const body = {
          name: document.getElementById("radioName").value.trim(),
          streamUrl: document.getElementById("radioStream").value.trim(),
          homepageUrl: document.getElementById("radioHomepage").value.trim(),
          imageUrl: document.getElementById("radioImage").value.trim(),
          description: document.getElementById("radioDescription").value.trim(),
          tags: splitTags(document.getElementById("radioTags").value),
          enabled: document.getElementById("radioEnabled").checked,
        };
        try {
          await api("/api/v1/internet-radio/stations", { method: "POST", body: body });
          setMessage("radioMessage", "station added", false);
          await viewSettings();
        } catch (err) { setMessage("radioMessage", err.message, true); }
      });
    }

    const podcastForm = document.getElementById("podcastFeedForm");
    if (podcastForm) {
      podcastForm.addEventListener("submit", async (event) => {
        event.preventDefault();
        const body = {
          url: document.getElementById("podcastURL").value.trim(),
          title: document.getElementById("podcastTitle").value.trim(),
          autoDownloadEnabled: document.getElementById("podcastAutoDownload").checked,
        };
        try {
          await api("/api/v1/podcasts/feeds", { method: "POST", body: body });
          setMessage("podcastMessage", "feed added", false);
          await viewSettings();
        } catch (err) { setMessage("podcastMessage", err.message, true); }
      });
    }

    const lastfmConfigForm = document.getElementById("lastfmConfigForm");
    if (lastfmConfigForm) {
      lastfmConfigForm.addEventListener("submit", async (event) => {
        event.preventDefault();
        const body = {
          apiKey: document.getElementById("lastfmAPIKey").value.trim(),
          sharedSecret: document.getElementById("lastfmSharedSecret").value.trim(),
        };
        try {
          await api("/api/v1/lastfm/config", { method: "PUT", body: body });
          localStorage.removeItem(lastFMPendingStorageKey());
          localStorage.removeItem(legacyLastFMPendingKey);
          setMessage("lastfmConfigMessage", "last.fm keys saved", false);
          await viewSettings();
        } catch (err) { setMessage("lastfmConfigMessage", err.message, true); }
      });
    }

    const exploConfigForm = document.getElementById("exploConfigForm");
    if (exploConfigForm) {
      const browseToggle = document.getElementById("exploBrowseToggle");
      const browser = document.getElementById("exploBrowser");
      if (browseToggle && browser) {
        browseToggle.addEventListener("click", () => {
          const reveal = browser.hidden;
          browser.hidden = !reveal;
          if (reveal) {
            const seed = (document.getElementById("exploFolder").value || "").trim();
            exploLoadDirs(seed).catch((e) => setMessage("exploConfigMessage", e.message, true));
          }
        });
      }
      const useBtn = document.getElementById("exploBrowseUse");
      if (useBtn) {
        useBtn.addEventListener("click", () => {
          if (!exploBrowsePath) { setMessage("exploConfigMessage", "navigate into a folder first", true); return; }
          document.getElementById("exploFolder").value = exploBrowsePath;
          setMessage("exploConfigMessage", "folder selected: " + exploBrowsePath, false);
        });
      }
      const clearBtn = document.getElementById("exploClear");
      if (clearBtn) {
        clearBtn.addEventListener("click", async () => {
          if (!confirm("Disable explo and forget the configured folder? Files on disk are untouched.")) return;
          try {
            await api("/api/v1/explo/config", { method: "DELETE" });
            setMessage("exploConfigMessage", "explo disabled", false);
            await viewSettings();
          } catch (err) { setMessage("exploConfigMessage", err.message, true); }
        });
      }
      const reprocessBtn = document.getElementById("exploReprocess");
      if (reprocessBtn) {
        reprocessBtn.addEventListener("click", async () => {
          if (!confirm("Re-scan explo: retry failed identifications and re-fetch cover art for every track? Runs in the background; may take a few minutes.")) return;
          reprocessBtn.disabled = true;
          setMessage("exploReprocessMessage", "reprocessing - retrying identification and covers in the background...", false);
          try {
            const res = await api("/api/v1/explo/reprocess", { method: "POST" });
            const idn = (res && res.identificationReset) || 0;
            const cov = (res && res.coversReset) || 0;
            setMessage("exploReprocessMessage", "queued " + idn + " track(s) to re-identify and " + cov + " cover(s) to re-fetch - watch the server log for progress", false);
          } catch (err) { setMessage("exploReprocessMessage", err.message, true); }
          reprocessBtn.disabled = false;
        });
      }
      exploConfigForm.addEventListener("submit", async (event) => {
        event.preventDefault();
        const body = {
          folder: document.getElementById("exploFolder").value.trim(),
          apiKey: document.getElementById("exploAPIKey").value.trim(),
        };
        try {
          const saved = await api("/api/v1/explo/config", { method: "PUT", body: body });
          setMessage("exploConfigMessage", saved && saved.enabled ? "explo enabled - scanning the folder for new drops now" : "saved", false);
          await viewSettings();
        } catch (err) { setMessage("exploConfigMessage", err.message, true); }
      });
    }

    const profileForm = document.getElementById("profileForm");
    if (profileForm) {
      profileForm.addEventListener("submit", async (event) => {
        event.preventDefault();
        const body = { displayName: document.getElementById("displayName").value.trim() };
        const password = document.getElementById("newPassword").value;
        if (password) body.password = password;
        try {
          setCurrentUser(await api("/api/v1/users/me", { method: "PATCH", body: body }));
          document.getElementById("authUser").textContent = (currentUser.username || "-").toUpperCase();
          setMessage("profileMessage", "profile saved", false);
        } catch (err) { setMessage("profileMessage", err.message, true); }
      });
    }

    const tokenForm = document.getElementById("tokenForm");
    if (tokenForm) {
      tokenForm.addEventListener("submit", async (event) => {
        event.preventDefault();
        try {
          const issued = await api("/api/v1/users/me/tokens", { method: "POST", body: { label: document.getElementById("tokenLabel").value.trim() } });
          const secret = document.getElementById("tokenSecret");
          secret.hidden = false;
          secret.textContent = issued.secret || "";
          setMessage("tokenMessage", "token issued - this secret is shown once", false);
        } catch (err) { setMessage("tokenMessage", err.message, true); }
      });
    }

    const userForm = document.getElementById("userForm");
    if (userForm) {
      userForm.addEventListener("submit", async (event) => {
        event.preventDefault();
        const body = {
          username: document.getElementById("newUsername").value.trim(),
          displayName: document.getElementById("newUserDisplay").value.trim(),
          password: document.getElementById("newUserPassword").value,
          role: document.getElementById("newUserRole").value,
        };
        try {
          await api("/api/v1/users", { method: "POST", body: body });
          setMessage("userMessage", "user created", false);
          await viewSettings();
        } catch (err) { setMessage("userMessage", err.message, true); }
      });
    }
  }

  /* -------- actions -------- */

  /* Dragging a show between tiers. Delegated on `main` rather than bound to
   * the cards, because the surface is rebuilt from scratch on every drop and
   * per-card listeners would not survive it. */
  main.addEventListener("dragstart", (event) => {
    const card = event.target.closest && event.target.closest(".tier-card");
    if (!card) return;
    rankDragID = card.dataset.sourceId || "";
    rankPickedID = "";
    card.classList.add("dragging");
    if (event.dataTransfer) {
      event.dataTransfer.effectAllowed = "move";
      // Firefox refuses to start a drag at all with nothing on the transfer.
      event.dataTransfer.setData("text/plain", rankDragID);
    }
  });

  main.addEventListener("dragover", (event) => {
    if (!rankDragID) return;
    const row = event.target.closest && event.target.closest(".tier-row");
    if (!row) return;
    // Without this the drop never fires: the default answer to "may I land
    // here" is no.
    event.preventDefault();
    if (event.dataTransfer) event.dataTransfer.dropEffect = "move";
    if (!row.classList.contains("over")) {
      main.querySelectorAll(".tier-row.over").forEach((other) => other.classList.remove("over"));
      row.classList.add("over");
    }
  });

  main.addEventListener("dragleave", (event) => {
    const row = event.target.closest && event.target.closest(".tier-row");
    if (row && !row.contains(event.relatedTarget)) row.classList.remove("over");
  });

  main.addEventListener("drop", async (event) => {
    if (!rankDragID) return;
    const row = event.target.closest && event.target.closest(".tier-row");
    if (!row) return;
    event.preventDefault();
    const sourceID = rankDragID;
    rankDragID = "";
    await setSourceTier(sourceID, row.dataset.tier || "");
  });

  // Dropping outside a band, or pressing escape mid-drag, still ends the drag
  // — and the classes it painted on have to come off with it.
  main.addEventListener("dragend", () => {
    rankDragID = "";
    main.querySelectorAll(".tier-card.dragging").forEach((card) => card.classList.remove("dragging"));
    main.querySelectorAll(".tier-row.over").forEach((row) => row.classList.remove("over"));
  });

  // Live chip preview for any input with data-tags-target. Keeps the
  // comma-separated tag pattern visible without a chip-input library.
  main.addEventListener("input", (event) => {
    const el = event.target;
    if (!el || !el.dataset || !el.dataset.tagsTarget) return;
    const target = document.getElementById(el.dataset.tagsTarget);
    if (!target) return;
    const tags = splitTags(el.value);
    if (tags.length === 0) {
      target.innerHTML = '<span class="tag-preview-empty">// chips appear as you type</span>';
      return;
    }
    target.innerHTML = tags.map((tag) => '<span class="meta-chip">' + escapeHTML(tag) + '</span>').join("");
  });

  main.addEventListener("change", async (event) => {
    const el = event.target;
    if (el && el.dataset && el.dataset.action === "playlist-track-select") {
      const trackID = el.dataset.trackId || "";
      if (!trackID) return;
      if (el.checked) playlistTracksBulkSelected.add(trackID);
      else playlistTracksBulkSelected.delete(trackID);
      return;
    }
    if (!el || !el.classList || !el.classList.contains("radio-cover-input")) return;
    const file = el.files && el.files[0];
    if (!file) return;
    const podcastID = el.dataset.podcastId || "";
    const playlistID = el.dataset.playlistId || "";
    const stationID = el.dataset.stationId || "";
    try {
      if (playlistID) {
        await uploadMusicPlaylistCover(playlistID, file);
        await openPlaylist(playlistID);
      } else if (podcastID) {
        await uploadPodcastCover(podcastID, file);
        await openPodcast(podcastID, Date.now());
      } else if (stationID) {
        await uploadRadioCover(stationID, file);
        if (activeTab === "radio") await viewRadio();
      }
    } catch (err) {
      alert(err.message || "cover upload failed");
      el.value = "";
    }
  });

  // ---- the multi-select picker ----
  //
  // Filtering hides rows without unticking them, so you can search "kexp",
  // tick it, search "wfmu", tick that, and submit both. ALL and NONE act on
  // what is *visible*, which is what makes "filter to NPR, tick ALL" work.
  function pickerVisibleItems(listID) {
    const list = document.getElementById(listID);
    if (!list) return [];
    return Array.from(list.querySelectorAll(".picker-item")).filter((row) => !row.hidden);
  }

  function updatePickerCount(listID) {
    const list = document.getElementById(listID);
    const out = document.getElementById(listID + "-count");
    if (!list || !out) return;
    const total = list.querySelectorAll("input[type=checkbox]").length;
    const picked = list.querySelectorAll("input[type=checkbox]:checked").length;
    out.textContent = picked === 0 ? "none of " + total + " selected" : picked + " of " + total + " selected";
    out.classList.toggle("on", picked > 0);
    // A typed label cannot name more than one thing, so the field says so
    // rather than silently applying to the first of eight.
    const prefix = listID.replace(/(Podcast|Station|Playlist)$/, "");
    const label = document.getElementById(prefix + "Label");
    if (label) {
      const many = picked > 1;
      label.disabled = many;
      label.placeholder = many ? "each keeps its own name" : "leave blank to use its own name";
    }
  }

  document.addEventListener("input", (event) => {
    const el = event.target;
    if (!el || !el.classList || !el.classList.contains("picker-filter")) return;
    const needle = el.value.trim().toLowerCase();
    const list = document.getElementById(el.dataset.picker);
    if (!list) return;
    list.querySelectorAll(".picker-item").forEach((row) => {
      row.hidden = needle !== "" && (row.dataset.name || "").indexOf(needle) === -1;
    });
  });

  // samo-radio's controls are selects and a slider, so they arrive as change
  // events rather than clicks. "change" rather than "input" on the volume
  // slider on purpose: it fires once on release instead of on every pixel of
  // the drag, which would be one HTTP round trip per pixel.
  document.addEventListener("change", async (event) => {
    const el = event.target;
    if (!el || !el.dataset) return;
    // Ticking a picker box is checked BEFORE the data-action guard below: the
    // boxes carry data-picker and no data-action, so anything downstream of
    // that guard never sees them.
    if (el.dataset.picker && el.type === "checkbox") {
      updatePickerCount(el.dataset.picker);
      return;
    }
    if (!el.dataset.action) return;
    const action = el.dataset.action;
    // The content picker renders every kind's fields and hides all but one.
    // Swapping visibility beats re-rendering the form, which would throw away
    // anything already typed into the fields that are staying.
    if (action === "composer-kind") {
      const prefix = el.dataset.prefix;
      ["podcast-subscription", "internet-station", "music-playlist", "file-pool", "live-stream"]
        .forEach((kind) => {
          const group = document.getElementById(prefix + "Fields-" + kind);
          if (group) group.hidden = kind !== el.value;
        });
      // Follow the kind with the role, until somebody chooses one themselves.
      const role = document.getElementById(prefix + "Role");
      if (role && role.dataset.roleAuto === "1") {
        role.value = roleForKind(el.value);
      }
      return;
    }
    if (action === "composer-role") {
      el.dataset.roleAuto = "0";
      return;
    }
    if (action.indexOf("samoradio-") !== 0) return;
    const deviceID = el.dataset.id;
    if (!deviceID) return;
    const base = "/api/v1/samo-radio/devices/" + encodeURIComponent(deviceID);
    try {
      if (action === "samoradio-volume") {
        await api(base + "/volume", { method: "POST", body: { volume: Number(el.value) / 100 } });
        const readout = el.parentElement && el.parentElement.querySelector(".samoradio-volume-value");
        if (readout) readout.textContent = String(Math.round(Number(el.value)));
      } else if (action === "samoradio-output") {
        await api(base + "/settings", { method: "PATCH", body: { output: { device: el.value } } });
        await renderRadio(true);
      } else if (action === "samoradio-backend") {
        await api(base + "/settings", { method: "PATCH", body: { output: { backend: el.value } } });
        await renderRadio(true);
      } else if (action === "samoradio-default-station") {
        // Setting the station should also put it on air. Persisting it alone
        // leaves the speaker silent until the next daemon restart, which reads
        // as "I set the station and nothing happened" — the exact opposite of
        // what picking a station means. The one thing not to interrupt is an
        // ad-hoc queue somebody is listening to right now.
        const picked = parseStationValue(el.value);
        const target = samoRadioDevices.find((entry) => entry.id === el.dataset.id);
        const tuneNow = Boolean(picked) && (!target || !target.state || target.state.mode !== "queue");
        await api(base + "/settings", {
          method: "PATCH",
          body: {
            defaultStation: picked || { kind: "channel", id: "" },
            tuneNow: tuneNow,
          },
        });
        await renderRadio(true);
      } else if (action === "samoradio-tune") {
        const picked = parseStationValue(el.value);
        if (!picked) return;
        await api(base + "/play", { method: "POST", body: stationPlayBody(picked) });
        el.value = "";
        await renderRadio(true);
      }
    } catch (err) {
      alert(err.message || "samo-radio command failed");
    }
  });

  document.addEventListener("click", async (event) => {
    const el = event.target.closest("[data-action]");
    if (!el) return;
    const inMain = main.contains(el);
    const inIdentify = identifyModal.contains(el);
    const inScan = scanPanel && scanPanel.contains(el);
    const inActivity = activityPanel && activityPanel.contains(el);
    const inBar = el.closest && el.closest(".bar-util");
    if (!inMain && !inIdentify && !inScan && !inActivity && !inBar) return;
    const action = el.dataset.action;
    try {
      if (action === "music-mode") {
        musicMode = el.dataset.mode || "recent";
        await viewMusic(false);
      } else if (action === "music-load-more") {
        await viewMusic(true);
      } else if (action === "music-sort") {
        musicSort = el.dataset.sort || "recent";
        await viewMusic(false);
      } else if (action === "music-direction") {
        musicDirection = el.dataset.direction || "desc";
        await viewMusic(false);
      } else if (action === "audiobooks-mode") {
        audiobooksMode = el.dataset.mode || "titles";
        await viewAudiobooks();
      } else if (action === "podcasts-mode") {
        podcastsMode = el.dataset.mode || "shows";
        await viewPodcasts();
      } else if (action === "settings-mode") {
        settingsMode = el.dataset.mode || "libraries";
        await viewSettings();
      } else if (action === "album-detail") {
        event.preventDefault();
        navigateTo("music/album/" + encodeURIComponent(el.dataset.id));
      } else if (action === "audiobook-detail") {
        event.preventDefault();
        navigateTo("audiobooks/item/" + encodeURIComponent(el.dataset.id));
      } else if (action === "podcast-detail") {
        event.preventDefault();
        navigateTo("podcasts/show/" + encodeURIComponent(el.dataset.id));
      } else if (action === "open-artist") {
        event.preventDefault();
        navigateTo("music/artist/" + encodeURIComponent(el.dataset.id));
      } else if (action === "open-playlist") {
        event.preventDefault();
        navigateTo("music/playlist/" + encodeURIComponent(el.dataset.id));
      } else if (action === "open-author") {
        event.preventDefault();
        navigateTo("audiobooks/author/" + encodeURIComponent(el.dataset.id));
      } else if (action === "open-series") {
        event.preventDefault();
        navigateTo("audiobooks/series/" + encodeURIComponent(el.dataset.id));
      } else if (action === "back-music") {
        navigateTo("music");
      } else if (action === "back-tab") {
        navigateTo(el.dataset.tab || "home");
      } else if (action === "play-track") {
        event.preventDefault();
        await playTrack(el.dataset.id, el.dataset.title, el.dataset.sub, Number(el.dataset.duration || 0));
      } else if (action === "play-podcast-episode") {
        event.preventDefault();
        await playPodcastEpisode(el.dataset.id, el.dataset.title, el.dataset.sub, Number(el.dataset.duration || 0), Number(el.dataset.progress || 0));
      } else if (action === "play-audiobook") {
        event.preventDefault();
        await playAudiobook(el.dataset.id, el.dataset.title, el.dataset.sub, Number(el.dataset.duration || 0), Number(el.dataset.progress || 0));
      } else if (action === "play-url") {
        event.preventDefault();
        playURL(el.dataset.url, el.dataset.title, el.dataset.sub, null);
      } else if (action === "toggle-playback") {
        event.preventDefault();
        await withButton(el, "...", async () => {
          const patch = {};
          patch[el.dataset.field] = asBool(el.dataset.value);
          await patchPlayback(el.dataset.kind, el.dataset.id, patch);
          if (activeTab === "music") await viewMusic();
          if (activeTab === "search") await runSearch(searchQuery);
        });
      } else if (action === "go-tab") {
        setActiveTab(el.dataset.tab || "home");
      } else if (action === "go-settings") {
        settingsMode = el.dataset.mode || "libraries";
        setActiveTab("settings");
      } else if (action === "composer-toggle") {
        event.preventDefault();
        toggleComposer(el.dataset.composer);
      } else if (action === "composer-submit") {
        event.preventDefault();
        await withButton(el, "WORKING...", async () => {
          try {
            await composerSubmit(el.dataset.composer);
          } catch (err) {
            composerMessage(el.dataset.composer, err.message, true);
          }
        });
      } else if (action === "search-value") {
        searchQuery = el.dataset.query || "";
        setActiveTab("search");
      } else if (action === "refresh-scan" || action === "scan-quick-all") {
        event.preventDefault();
        await triggerGlobalScan("quick");
      } else if (action === "scan-panel-close") {
        event.preventDefault();
        closeScanPanel();
      } else if (action === "cancel-scan") {
        event.preventDefault();
        await withButton(el, "...", cancelActiveScan);
        if (scanPanel && !scanPanel.hidden) await openScanPanel();
      } else if (action === "activity-open") {
        event.preventDefault();
        await openActivityPanel();
      } else if (action === "activity-close") {
        event.preventDefault();
        closeActivityPanel();
      } else if (action === "now-playing") {
        event.preventDefault();
        if (playerDock) {
          playerDock.hidden = false;
          playerDock.scrollIntoView({ behavior: "smooth", block: "nearest" });
        }
      } else if (action === "scan-all") {
        event.preventDefault();
        await triggerGlobalScan("full");
      } else if (action === "repair-all") {
        event.preventDefault();
        await triggerGlobalScan("repair");
      } else if (action === "fetch-artist-images") {
        event.preventDefault();
        await withButton(el, "STARTING...", async () => {
          await api("/api/v1/music/artists/images/backfill", { method: "POST", body: { mode: "missing" } });
          if (activeTab !== "settings" || settingsMode !== "libraries") {
            settingsMode = "libraries";
            await viewSettings();
          } else {
            const data = await api("/api/v1/music/artists/images/backfill");
            updateArtistImageJobPanel(data && data.job);
          }
          watchArtistImageBackfill();
        });
      } else if (action === "cancel-artist-images") {
        event.preventDefault();
        if (!confirm("Cancel artist photo download? Images already downloaded stay in the library.")) return;
        await api("/api/v1/music/artists/images/backfill/cancel", { method: "POST" });
        updateArtistImageJobPanel(await api("/api/v1/music/artists/images/backfill").then((d) => d && d.job).catch(() => null));
      } else if (action === "scan-library") {
        event.preventDefault();
        await triggerLibraryScan(el.dataset.id, "quick");
      } else if (action === "scan-library-full") {
        event.preventDefault();
        await triggerLibraryScan(el.dataset.id, "full");
      } else if (action === "scan-library-kind") {
        event.preventDefault();
        const kind = el.dataset.kind || "";
        const mode = el.dataset.mode || "quick";
        const libs = await loadLibraries();
        const lib = libs.find((item) => item.kind === kind);
        if (!lib) {
          alert("No " + kind + " library is attached. Add one under Settings → Libraries.");
          return;
        }
        await triggerLibraryScan(lib.id, mode, lib.name);
      } else if (action === "repair-library") {
        event.preventDefault();
        await triggerLibraryRepair(el.dataset.id);
      } else if (action === "delete-album") {
        await deleteCatalogItem("/api/v1/music/albums/" + encodeURIComponent(el.dataset.id), el.dataset.name || "album", "album", () => navigateTo("music"));
      } else if (action === "delete-audiobook") {
        await deleteCatalogItem("/api/v1/audiobooks/" + encodeURIComponent(el.dataset.id), el.dataset.name || "audiobook", "audiobook", () => navigateTo("audiobooks"));
      } else if (action === "delete-podcast-show") {
        await deleteCatalogItem("/api/v1/podcasts/shows/" + encodeURIComponent(el.dataset.id), el.dataset.name || "podcast", "podcast show", () => navigateTo("podcasts"));
      } else if (action === "delete-library") {
        if (!confirm("Delete library " + (el.dataset.name || "") + "? Catalog rows for this library will be removed.")) return;
        await api("/api/v1/libraries/" + encodeURIComponent(el.dataset.id), { method: "DELETE" });
        await viewSettings();
      } else if (action === "remove-all-missing-files") {
        event.preventDefault();
        const total = Number(el.dataset.total || "0");
        const label = total === 1 ? "1 missing file entry" : (total + " missing file entries");
        if (!total || !confirm("Remove all " + label + " from the catalog? This cannot be undone.")) return;
        const result = await api("/api/v1/missing-files", { method: "DELETE" });
        const removed = result && typeof result.removed === "number" ? result.removed : total;
        alert("Removed " + removed + " missing file entr" + (removed === 1 ? "y" : "ies") + ".");
        await viewSettings();
      } else if (action === "remove-missing-file") {
        event.preventDefault();
        if (!confirm("Remove missing file entry for " + (el.dataset.label || "this track") + "? This deletes the catalog row for the missing file.")) return;
        await api("/api/v1/missing-files/" + encodeURIComponent(el.dataset.id), { method: "DELETE" });
        await viewSettings();
      } else if (action === "samoradio-configure") {
        samoRadioExpandedID = samoRadioExpandedID === el.dataset.id ? "" : (el.dataset.id || "");
        await viewRadio();
      } else if (action === "samoradio-pair") {
        await api("/api/v1/samo-radio/devices/" + encodeURIComponent(el.dataset.id) + "/pair", { method: "POST" });
        samoRadioDevices = [];
        await viewRadio();
      } else if (action === "samoradio-delete") {
        if (!confirm("Remove " + (el.dataset.name || "this device") + "? Its Samo token is revoked.")) return;
        await api("/api/v1/samo-radio/devices/" + encodeURIComponent(el.dataset.id), { method: "DELETE" });
        samoRadioDevices = [];
        if (samoRadioExpandedID === el.dataset.id) samoRadioExpandedID = "";
        await viewRadio();
      } else if (action === "samoradio-previous") {
        await api("/api/v1/samo-radio/devices/" + encodeURIComponent(el.dataset.id) + "/previous", { method: "POST" });
        setTimeout(() => { void renderRadio(true); }, 1200);
      } else if (action === "samoradio-skip") {
        // Sent to the DEVICE, not to the channel, even though the channel is
        // what decides what plays next. The device forwards the skip to the
        // channel and then throws away the several seconds of audio it has
        // already pulled down the pipe. Calling the channel endpoint from here
        // does only the first half, so the thing you skipped keeps playing for
        // a while afterwards and the button feels broken.
        await api(
          "/api/v1/samo-radio/devices/" + encodeURIComponent(el.dataset.id) +
            (el.dataset.scope === "kind" ? "/next-kind" : "/next"),
          { method: "POST" },
        );
        setTimeout(() => { void renderRadio(true); }, 1200);
      } else if (action === "samoradio-cmd") {
        await api("/api/v1/samo-radio/devices/" + encodeURIComponent(el.dataset.id) + "/" + el.dataset.cmd, { method: "POST" });
        await renderRadio(true);
      } else if (action === "samoradio-send") {
        const ids = (el.dataset.ids || "").split(",").filter(Boolean);
        const label = el.textContent;
        await sendToSamoRadio(el.dataset.id, el.dataset.type, ids, {});
        // The view does not re-render on send, so the button itself is the
        // only place feedback can land.
        el.textContent = "SENT →";
        setTimeout(() => { el.textContent = label; }, 1600);
      } else if (action === "channel-section") {
        channelSection = el.dataset.section || "mix";
        rankPickedID = "";
        rankDragID = "";
        await viewRadio();
        window.scrollTo({ top: 0 });
      } else if (action === "rank-card") {
        // Click to lift, click to place — the same two gestures as the drag,
        // for the touchscreen and the trackpad that HTML5 drag and drop does
        // not serve. A second click on the lifted card puts it back.
        const id = el.dataset.sourceId || "";
        if (!rankPickedID || rankPickedID === id) {
          rankPickedID = rankPickedID === id ? "" : id;
          renderRankSurface();
        } else {
          const row = el.closest(".tier-row");
          await setSourceTier(rankPickedID, row ? row.dataset.tier : "");
        }
      } else if (action === "rank-drop") {
        if (!rankPickedID) return;
        await setSourceTier(rankPickedID, el.dataset.tier || "");
      } else if (action === "plan-section") {
        const section = el.dataset.section;
        if (planCollapsed.has(section)) planCollapsed.delete(section);
        else planCollapsed.add(section);
        await viewRadio();
      } else if (action === "plan-auto-pools") {
        showAutoPools = !showAutoPools;
        await viewRadio();
      } else if (action === "picker-all" || action === "picker-none") {
        const listID = el.dataset.picker;
        const on = action === "picker-all";
        pickerVisibleItems(listID).forEach((row) => {
          const box = row.querySelector("input[type=checkbox]");
          if (box) box.checked = on;
        });
        updatePickerCount(listID);
      } else if (action === "radio-mode") {
        const mode = el.dataset.mode;
        radioMode = (mode === "internet" || mode === "samo-radio") ? mode : "channels";
        activeChannelID = "";
        await viewRadio();
      } else if (action === "channel-skip") {
        // "skip the track" vs "skip this show for a while" — not liking one
        // episode and not being in the mood for the podcast are different
        // things, and only the second should change what comes next.
        const scope = el.dataset.scope === "kind" ? "kind" : "item";
        const result = await api(
          "/api/v1/channels/" + encodeURIComponent(el.dataset.id) + "/skip?scope=" + scope,
          { method: "POST" },
        );
        if (result && result.skipped === false) {
          alert("Nothing is playing on this channel right now — it starts when something tunes in.");
          return;
        }
        // The streamer needs a moment to pick and start the next item.
        setTimeout(() => { void renderRadio(true); }, 1200);
      } else if (action === "channel-use-browser-zone") {
        await api("/api/v1/channels/" + encodeURIComponent(el.dataset.id), {
          method: "PATCH",
          body: { timezone: el.dataset.zone },
        });
        await viewRadio();
      } else if (action === "plan-block-new") {
        planEditIndex.block = -1;
        fillBlockForm(null);
        toggleComposer("plan-block");
      } else if (action === "plan-block-edit") {
        planEditIndex.block = Number(el.dataset.index);
        fillBlockForm(planBlocks()[planEditIndex.block]);
        const panel = document.getElementById("composer-plan-block");
        if (panel && panel.hidden) toggleComposer("plan-block");
      } else if (action === "plan-block-save") {
        const block = readBlockForm();
        if (!block) return;
        const blocks = planBlocks();
        // There can be only one default block: it is the thing every other
        // block ultimately falls back to, so two of them is not a preference,
        // it is an ambiguity the station cannot resolve at 3am.
        if (block.default) blocks.forEach((other) => { delete other.default; });
        if (planEditIndex.block >= 0) blocks[planEditIndex.block] = block;
        else blocks.push(block);
        activePlan.blocks = blocks;
        await savePlan();
      } else if (action === "plan-block-delete") {
        const blocks = planBlocks();
        const block = blocks[Number(el.dataset.index)];
        if (!block) return;
        if (!confirm("Remove the block " + (block.label || block.id) + "?")) return;
        blocks.splice(Number(el.dataset.index), 1);
        activePlan.blocks = blocks;
        await savePlan();
      } else if (action === "plan-block-move") {
        const blocks = planBlocks();
        const index = Number(el.dataset.index);
        const target = el.dataset.dir === "up" ? index - 1 : index + 1;
        if (target < 0 || target >= blocks.length) return;
        const moved = blocks[index];
        blocks[index] = blocks[target];
        blocks[target] = moved;
        activePlan.blocks = blocks;
        await savePlan();
      } else if (action === "plan-pool-new" || action === "plan-pool-edit") {
        planEditIndex.pool = action === "plan-pool-edit" ? Number(el.dataset.index) : -1;
        const pool = planEditIndex.pool >= 0 ? planPools()[planEditIndex.pool] : null;
        setPlanField("planPoolID", pool ? pool.id : "");
        setPlanField("planPoolLabel", pool ? pool.label : "");
        const members = {};
        ((pool && pool.sourceIds) || []).forEach((id) => { members[id] = true; });
        activePlanSources.forEach((src, index) => {
          setPlanChecked("planPoolSource" + index, Boolean(members[src.id]));
        });
        const panel = document.getElementById("composer-plan-pool");
        if (panel && panel.hidden) toggleComposer("plan-pool");
      } else if (action === "plan-pool-save") {
        const id = planField("planPoolID");
        if (!id) { alert("A pool needs an id."); return; }
        const sourceIds = [];
        activePlanSources.forEach((src, index) => {
          if (planChecked("planPoolSource" + index)) sourceIds.push(src.id);
        });
        const pool = { id: id, label: planField("planPoolLabel"), sourceIds: sourceIds };
        const pools = planPools();
        if (planEditIndex.pool >= 0) pools[planEditIndex.pool] = pool;
        else pools.push(pool);
        activePlan.pools = pools;
        await savePlan();
      } else if (action === "plan-pool-delete") {
        const pools = planPools();
        const pool = pools[Number(el.dataset.index)];
        if (!pool) return;
        if (!confirm("Remove the pool " + (pool.label || pool.id) + "? Any block using it will need editing too.")) return;
        pools.splice(Number(el.dataset.index), 1);
        activePlan.pools = pools;
        await savePlan();
      } else if (action === "plan-category-new" || action === "plan-category-edit") {
        planEditIndex.category = action === "plan-category-edit" ? Number(el.dataset.index) : -1;
        const category = planEditIndex.category >= 0 ? planCategories()[planEditIndex.category] : null;
        setPlanField("planCategoryID", category ? category.id : "");
        setPlanField("planCategoryLabel", category ? category.label : "");
        setPlanField("planCategoryTarget", category ? Math.round((Number(category.target) || 0) * 100) : "");
        const panel = document.getElementById("composer-plan-category");
        if (panel && panel.hidden) toggleComposer("plan-category");
      } else if (action === "plan-category-save") {
        const id = planField("planCategoryID");
        if (!id) { alert("A category needs an id."); return; }
        const target = planNumber("planCategoryTarget");
        const category = { id: id, label: planField("planCategoryLabel"), target: (target || 0) / 100 };
        const categories = planCategories();
        if (planEditIndex.category >= 0) categories[planEditIndex.category] = category;
        else categories.push(category);
        activePlan.categories = categories;
        await savePlan();
      } else if (action === "plan-category-delete") {
        const categories = planCategories();
        const category = categories[Number(el.dataset.index)];
        if (!category) return;
        if (!confirm("Remove the category " + (category.label || category.id) + "?")) return;
        categories.splice(Number(el.dataset.index), 1);
        activePlan.categories = categories;
        await savePlan();
      } else if (action === "plan-behaviour-edit") {
        // Four short answers rather than another form: these are numbers you
        // set once and rarely revisit.
        const separation = activePlan.separation || {};
        const ask = (label, current) => {
          const next = prompt(label, current || "");
          return next === null ? null : next.trim();
        };
        const item = ask("Minimum gap before the SAME ITEM may air again (e.g. 8h):", separation.item || "8h");
        if (item === null) return;
        const source = ask("Minimum gap before the same SOURCE may air again.\n" +
          "Only applies to sources that are one show — a playlist is a container of many artists, so this would make two songs in a row impossible.",
          separation.source || "45m");
        if (source === null) return;
        const creator = ask("Minimum gap before the same PERSON may air again.\n" +
          "A host, or a recording artist — two shows with the same host back to back is not variety.",
          separation.creator || "90m");
        if (creator === null) return;
        const epsilon = prompt("How much surprise? The top N% of scores compete for the pick.\n" +
          "0 always takes the best-scoring candidate, which sounds like a machine.",
          String(Math.round(((activePlan.selection || {}).epsilon != null ? activePlan.selection.epsilon : 0.15) * 100)));
        if (epsilon === null) return;
        activePlan.separation = Object.assign({}, separation, { item, source, creator });
        activePlan.selection = Object.assign({}, activePlan.selection || {}, {
          epsilon: Math.max(0, Math.min(99, Number(epsilon) || 0)) / 100,
        });
        await savePlan();
      } else if (action === "plan-source-edit") {
        const src = activePlanSources.find((entry) => entry.id === el.dataset.id);
        if (!src) return;
        planEditSourceID = src.id;
        const cfg = src.config || {};
        setPlanField("planSourceLabel", src.label || "");
        setPlanField("planSourceCategory", cfg.category || "");
        setPlanField("planSourceTier", (cfg.tier || "C").toUpperCase());
        setPlanField("planSourceCreator", cfg.creator || "");
        setPlanField("planSourceFamily", cfg.family || "");
        setPlanField("planSourceFresh", cfg.newWithinHours || "");
        setPlanField("planSourceWeight", src.weight || 1);
        const panel = document.getElementById("composer-plan-source");
        if (panel && panel.hidden) toggleComposer("plan-source");
      } else if (action === "plan-source-save") {
        const src = activePlanSources.find((entry) => entry.id === planEditSourceID);
        if (!src) return;
        // Merged, not replaced: the kind-specific bits (podcastId, paths,
        // playlistId) live in the same object and are not on this form.
        const config = Object.assign({}, src.config || {});
        const set = (key, value) => {
          if (value === "" || value == null) delete config[key];
          else config[key] = value;
        };
        set("category", planField("planSourceCategory"));
        set("tier", planField("planSourceTier"));
        set("creator", planField("planSourceCreator"));
        set("family", planField("planSourceFamily"));
        const fresh = planNumber("planSourceFresh");
        set("newWithinHours", fresh && fresh > 0 ? fresh : "");
        const weight = planNumber("planSourceWeight");
        await api("/api/v1/channels/" + encodeURIComponent(activeChannelID) +
          "/sources/" + encodeURIComponent(src.id), {
          method: "PATCH",
          body: {
            label: planField("planSourceLabel"),
            config: config,
            weight: weight && weight > 0 ? weight : 1,
          },
        });
        await viewRadio();
      } else if (action === "plan-pools-repair") {
        // Turn the frozen id lists into live rules. A rotation pool that is a
        // snapshot of the library loses everything added after it was saved,
        // which is not a mistake anybody can be expected to notice.
        const categories = planCategories();
        if (categories.length === 0) { alert("Add a category first."); return; }
        const pools = planPools();
        let repaired = 0;
        categories.forEach((category) => {
          const existing = pools.find((pool) => pool.id === category.id);
          if (existing) {
            delete existing.sourceIds;
            existing.match = { category: category.id };
            repaired++;
            return;
          }
          pools.push({ id: category.id, label: category.label || category.id, match: { category: category.id } });
          repaired++;
        });
        activePlan.pools = pools;
        // Any block that played a category pool keeps playing it; a block that
        // referenced nothing gets the rotation pools so it is not left empty.
        (activePlan.blocks || []).forEach((block) => {
          if (!block.pools || block.pools.length === 0) {
            block.pools = categories.map((category) => ({ pool: category.id, weight: 1 }));
          }
        });
        if (!confirm("Make " + repaired + " pool(s) match by category, so anything you add later joins automatically?")) return;
        await savePlan();
      } else if (action === "plan-json-toggle") {
        toggleComposer("plan-json");
      } else if (action === "plan-json-save") {
        const raw = document.getElementById("planJSON");
        if (!raw) return;
        let parsed;
        try {
          parsed = JSON.parse(raw.value);
        } catch (err) {
          alert("That is not valid JSON:\n\n" + (err.message || ""));
          return;
        }
        activePlan = parsed;
        await savePlan();
      } else if (action === "plan-reset") {
        if (!confirm("Throw away this channel's saved plan and go back to the one its sources and booked slots describe?")) return;
        await api("/api/v1/channels/" + encodeURIComponent(el.dataset.id) + "/plan", { method: "DELETE" });
        await viewRadio();
      } else if (action === "plan-why-refresh") {
        await viewRadio();
      } else if (action === "plan-why-more") {
        whyLimit = whyLimit >= 10 ? 1 : 10;
        await viewRadio();
      } else if (action === "channel-talk-share") {
        const current = Math.round((Number(el.dataset.share) || 0.75) * 100);
        const next = prompt(
          "What percentage of this channel should be spoken word?\n" +
            "The rest is music. 75 is a talk station with music threaded through it.",
          String(current),
        );
        if (next === null) return;
        const share = Number(next) / 100;
        if (!(share > 0 && share < 1)) {
          alert("Enter a number between 1 and 99.");
          return;
        }
        await api("/api/v1/channels/" + encodeURIComponent(el.dataset.id), {
          method: "PATCH",
          body: { talkShare: share },
        });
        await viewRadio();
      } else if (action === "channel-listening-day") {
        // The hours somebody is actually around. Everything the station
        // believes about "new" hangs off this: podcasts publish overnight, and
        // an episode aired to a dark room has not reached anyone, so airings
        // outside these hours do not spend it.
        const parse = (value) => {
          const match = /^\s*(\d{1,2}):?(\d{2})?\s*$/.exec(value || "");
          if (!match) return null;
          const hours = Number(match[1]);
          const minutes = Number(match[2] || 0);
          if (hours > 23 || minutes > 59) return null;
          return hours * 60 + minutes;
        };
        const start = parse(prompt(
          "When does your listening day start? (HH:MM)\n" +
            "New episodes are held until then rather than aired to an empty room.",
          minuteToHHMM(Number(el.dataset.start) || 480),
        ));
        if (start === null) return;
        const end = parse(prompt(
          "And when does it end? (HH:MM)",
          minuteToHHMM(Number(el.dataset.end) || 1380),
        ));
        if (end === null) return;
        await api("/api/v1/channels/" + encodeURIComponent(el.dataset.id), {
          method: "PATCH",
          body: { dayStartMinute: start, dayEndMinute: end },
        });
        await viewRadio();
      } else if (action === "channel-timezone") {
        // Schedule rules are a bare minute-of-day, so the zone is what gives
        // "16:00" a meaning. Worth being able to see and change it.
        let browserZone = "";
        try { browserZone = Intl.DateTimeFormat().resolvedOptions().timeZone || ""; } catch { browserZone = ""; }
        const current = el.dataset.tz || browserZone || "";
        const next = prompt(
          "Timezone for this channel's schedule (IANA name, e.g. America/Denver).\n" +
            "Leave blank to use the server default (" + (el.dataset.effective || "UTC") + ").",
          current,
        );
        if (next === null) return;
        await api("/api/v1/channels/" + encodeURIComponent(el.dataset.id), {
          method: "PATCH",
          body: { timezone: next.trim() },
        });
        await viewRadio();
      } else if (action === "channel-previous") {
        const back = await api(
          "/api/v1/channels/" + encodeURIComponent(el.dataset.id) + "/previous",
          { method: "POST" },
        );
        if (back && back.moved === false) {
          alert("Nothing to go back to yet on this channel.");
          return;
        }
        setTimeout(() => { void renderRadio(true); }, 1200);
      } else if (action === "channel-open") {
        activeChannelID = el.dataset.id || "";
        await viewRadio();
      } else if (action === "channel-back") {
        activeChannelID = "";
        await viewRadio();
      } else if (action === "channel-tune-in") {
        event.preventDefault();
        await ensureStreamToken();
        playURL(channelStreamURL(el.dataset.id), el.dataset.name || "Channel", "Personal radio · live", { kind: "channel", id: el.dataset.id });
      } else if (action === "channel-delete") {
        if (!confirm("Delete channel " + (el.dataset.name || "") + "? Sources, rules, and play history go with it.")) return;
        await api("/api/v1/channels/" + encodeURIComponent(el.dataset.id), { method: "DELETE" });
        activeChannelID = "";
        await viewRadio();
      } else if (action === "channel-source-toggle") {
        await api("/api/v1/channels/" + encodeURIComponent(activeChannelID) + "/sources/" + encodeURIComponent(el.dataset.id), {
          method: "PATCH",
          body: { enabled: asBool(el.dataset.enabled) },
        });
        await viewRadio();
      } else if (action === "channel-source-delete") {
        if (!confirm("Remove source " + (el.dataset.name || "") + "?")) return;
        await api("/api/v1/channels/" + encodeURIComponent(activeChannelID) + "/sources/" + encodeURIComponent(el.dataset.id), { method: "DELETE" });
        await viewRadio();
      } else if (action === "channel-schedule-delete") {
        if (!confirm("Remove rule " + (el.dataset.name || "") + "?")) return;
        await api("/api/v1/channels/" + encodeURIComponent(activeChannelID) + "/schedule/" + encodeURIComponent(el.dataset.id), { method: "DELETE" });
        await viewRadio();
      } else if (action === "station-dir-add") {
        await withButton(el, "ADDING...", async () => {
          await api("/api/v1/internet-radio/stations", {
            method: "POST",
            body: { name: el.dataset.name || "", streamUrl: el.dataset.url || "", enabled: true },
          });
          setStatus("STATION DIRECTORY · added " + (el.dataset.name || ""));
          if (activeTab === "settings") await viewSettings();
          else await viewRadio();
        });
      } else if (action === "probe-all-radio") {
        await withButton(el, "PROBING...", async () => {
          await api("/api/v1/internet-radio/stations/probe", { method: "POST" });
          if (activeTab === "settings") await viewSettings();
          else await viewRadio();
        });
      } else if (action === "probe-radio") {
        await withButton(el, "PROBING...", async () => {
          await api("/api/v1/internet-radio/stations/" + encodeURIComponent(el.dataset.id) + "/probe", { method: "POST" });
          if (activeTab === "radio") await viewRadio();
        });
      } else if (action === "toggle-radio") {
        await api("/api/v1/internet-radio/stations/" + encodeURIComponent(el.dataset.id), { method: "PATCH", body: { enabled: asBool(el.dataset.enabled) } });
        if (activeTab === "radio") await viewRadio();
      } else if (action === "delete-radio") {
        if (!confirm("Delete station " + (el.dataset.name || "") + "?")) return;
        await api("/api/v1/internet-radio/stations/" + encodeURIComponent(el.dataset.id), { method: "DELETE" });
        if (activeTab === "radio") await viewRadio();
      } else if (action === "clear-podcast-cache") {
        if (!confirm("Clear all cached podcast enclosure files on this server? Playback will re-fetch from the publisher URLs.")) return;
        await withButton(el, "CLEARING...", async () => {
          const result = await api("/api/v1/podcasts/cache", { method: "DELETE" });
          const count = result && result.episodesRemoved != null ? result.episodesRemoved : 0;
          setStatus("PODCAST CACHE · cleared " + count + " episode(s)");
          settingsMode = "podcasts";
          await viewSettings();
        });
      } else if (action === "poll-podcasts") {
        await withButton(el, "POLLING...", async () => {
          await api("/api/v1/podcasts/feeds/poll", { method: "POST" });
          await viewSettings();
        });
      } else if (action === "refresh-feed") {
        await withButton(el, "REFRESHING...", async () => {
          await api("/api/v1/podcasts/feeds/" + encodeURIComponent(el.dataset.id) + "/refresh", { method: "POST" });
          const showID = el.dataset.showId || "";
          if (showID) await openPodcast(showID, Date.now());
          else if (activeTab === "podcasts") await viewPodcasts();
          else await viewSettings();
        });
      } else if (action === "delete-feed") {
        if (!confirm("Delete feed " + (el.dataset.name || "") + "?")) return;
        await api("/api/v1/podcasts/feeds/" + encodeURIComponent(el.dataset.id), { method: "DELETE" });
        if (activeTab === "podcasts") await viewPodcasts();
        else await viewSettings();
      } else if (action === "toggle-feed-download") {
        event.preventDefault();
        const feedID = el.dataset.id || "";
        try {
          await api("/api/v1/podcasts/feeds/" + encodeURIComponent(feedID), {
            method: "PATCH",
            body: { autoDownloadEnabled: el.checked },
          });
        } catch (err) {
          el.checked = !el.checked;
          alert(err.message || "failed to update feed");
        }
      } else if (action === "download-podcast-episode") {
        event.preventDefault();
        await withButton(el, "DOWNLOADING...", async () => {
          await api("/api/v1/podcasts/episodes/" + encodeURIComponent(el.dataset.id) + "/cache", { method: "POST" });
          const showID = el.dataset.showId || "";
          if (showID) await openPodcast(showID);
          else await viewPodcasts();
        });
      } else if (action === "identify") {
        event.preventDefault();
        await openIdentifyModal(el.dataset.kind, el.dataset.id, el.dataset.title || "", el.dataset.author || "");
      } else if (action === "bulk-identify") {
        event.preventDefault();
        await runBulkIdentify(el, el.dataset.kind);
      } else if (action === "identify-apply") {
        event.preventDefault();
        await applyIdentifyCandidate(el.dataset.kind, el.dataset.id, identifyCandidates[Number(el.dataset.idx || -1)]);
      } else if (action === "identify-close") {
        event.preventDefault();
        closeIdentifyModal();
      } else if (action === "delete-playlist") {
        event.preventDefault();
        if (!confirm("Delete playlist " + (el.dataset.name || "") + "?")) return;
        await api("/api/v1/music/playlists/" + encodeURIComponent(el.dataset.id), { method: "DELETE" });
        musicMode = "playlists";
        navigateTo("music");
      } else if (action === "toggle-playlist-public") {
        event.preventDefault();
        const playlistID = el.dataset.id || "";
        await api("/api/v1/music/playlists/" + encodeURIComponent(playlistID), {
          method: "PATCH",
          body: { public: asBool(el.dataset.public) },
        });
        navigateTo("music/playlist/" + encodeURIComponent(playlistID));
      } else if (action === "remove-playlist-track") {
        event.preventDefault();
        const playlistID = el.dataset.playlistId || "";
        const trackID = el.dataset.trackId || "";
        const playlist = await api("/api/v1/music/playlists/" + encodeURIComponent(playlistID));
        const trackIDs = (playlist.trackIds || []).filter((id) => id !== trackID);
        await api("/api/v1/music/playlists/" + encodeURIComponent(playlistID), { method: "PATCH", body: { trackIds: trackIDs } });
        navigateTo("music/playlist/" + encodeURIComponent(playlistID));
      } else if (action === "playlist-tracks-edit-toggle") {
        event.preventDefault();
        const playlistID = el.dataset.id || "";
        if (playlistTracksBulkEditId === playlistID) {
          playlistTracksBulkEditId = "";
          playlistTracksBulkSelected.clear();
        } else {
          playlistTracksBulkEditId = playlistID;
          playlistTracksBulkSelected.clear();
        }
        await openPlaylist(playlistID);
      } else if (action === "playlist-tracks-edit-done") {
        event.preventDefault();
        playlistTracksBulkEditId = "";
        playlistTracksBulkSelected.clear();
        await openPlaylist(el.dataset.id || "");
      } else if (action === "remove-playlist-tracks-bulk") {
        event.preventDefault();
        const playlistID = el.dataset.id || "";
        if (!playlistID || playlistTracksBulkSelected.size === 0) return;
        const playlist = await api("/api/v1/music/playlists/" + encodeURIComponent(playlistID));
        const trackIDs = (playlist.trackIds || []).filter((id) => !playlistTracksBulkSelected.has(id));
        await api("/api/v1/music/playlists/" + encodeURIComponent(playlistID), { method: "PATCH", body: { trackIds: trackIDs } });
        playlistTracksBulkEditId = "";
        playlistTracksBulkSelected.clear();
        navigateTo("music/playlist/" + encodeURIComponent(playlistID));
      } else if (action === "revoke-token") {
        if (!confirm("Revoke this token?")) return;
        await api("/api/v1/users/me/tokens/" + encodeURIComponent(el.dataset.id), { method: "DELETE" });
        await viewSettings();
      } else if (action === "lastfm-clear-config") {
        if (!confirm("Clear Last.fm API keys?")) return;
        await api("/api/v1/lastfm/config", { method: "DELETE" });
        localStorage.removeItem(lastFMPendingStorageKey());
        localStorage.removeItem(legacyLastFMPendingKey);
        await viewSettings();
      } else if (action === "lastfm-begin") {
        await withButton(el, "OPENING...", async () => {
          const result = await api("/api/v1/lastfm/auth/begin", { method: "POST" });
          localStorage.setItem(lastFMPendingStorageKey(), result.token || "");
          if (result.authUrl) window.open(result.authUrl, "_blank", "noopener");
          setMessage("lastfmMessage", "approve in Last.fm, then click COMPLETE", false);
        });
      } else if (action === "lastfm-complete") {
        const pending = localStorage.getItem(lastFMPendingStorageKey()) || localStorage.getItem(legacyLastFMPendingKey) || "";
        if (!pending) { setMessage("lastfmMessage", "begin link first", true); return; }
        await api("/api/v1/lastfm/auth/complete", { method: "POST", body: { token: pending } });
        localStorage.removeItem(lastFMPendingStorageKey());
        localStorage.removeItem(legacyLastFMPendingKey);
        await viewSettings();
      } else if (action === "lastfm-disconnect") {
        if (!confirm("Disconnect Last.fm?")) return;
        await api("/api/v1/lastfm/auth/session", { method: "DELETE" });
        await viewSettings();
      } else if (action === "lastfm-flush") {
        await withButton(el, "FLUSHING...", async () => {
          await api("/api/v1/lastfm/queue/flush", { method: "POST" });
          await viewSettings();
        });
      }
    } catch (err) {
      alert(err.message || "action failed");
    }
  });

  /* -------- nav --------
   * Hash format:
   *   #home, #music, #audiobooks, #podcasts, #radio, #search, #settings
   *   #music/album/<id>, #music/artist/<id>
   *   #audiobooks/item/<id>, #audiobooks/author/<id>, #audiobooks/series/<id>
   *   #podcasts/item/<id>
   * navigateTo() pushes the hash; dispatchHash() decides what to render. */
  /* -------- EXPLO (weekly discovery silo) -------- */

  /* The EXPLO tab lists the auto-identified weekly drop: what got matched,
   * what's still awaiting an identify/cover retry, and what art it has.
   * Visible only when explo is configured (boot() unhides #exploTab from
   * /api/v1/explo/status); a deep-link to #explo on an unconfigured server
   * renders a pointer to the settings panel instead. */
  async function viewExplo() {
    renderLoading();
    let data;
    try {
      data = await api("/api/v1/explo/tracks");
    } catch (err) { renderError(err.message); return; }
    if (!data || !data.configured) {
      main.innerHTML = '<section class="view">' +
        '<div class="view-head"><h1>EXPLO</h1><span class="crumb">// weekly discovery silo</span></div>' +
        '<div class="empty-state">// explo is not configured — an admin can point Samo at the weekly drop folder under SETTINGS &rarr; EXPLO</div>' +
      '</section>';
      return;
    }
    const s = data.summary || {};
    let html = '<section class="view">' +
      '<div class="view-head"><h1>EXPLO</h1><span class="crumb">// weekly discovery silo &mdash; kept out of your library, gathered here</span></div>';
    if (!data.enabled) {
      html += '<div class="empty-state explo-warn">// pipeline paused — ' + escapeHTML(data.disabledReason || "missing prerequisite") + '</div>';
    }
    const chips = [
      (s.inFolder || 0) + ' IN FOLDER',
      (s.identified || 0) + ' IDENTIFIED',
      (s.awaitingRetry || 0) + ' AWAITING RETRY' + (s.nextRetryAt ? ' (next ' + escapeHTML(s.nextRetryAt) + ' UTC)' : ''),
      (s.retired || 0) + ' RETIRED',
      (s.coversDone || 0) + ' COVERS',
      (s.placeholders || 0) + ' PLACEHOLDERS',
      (s.coversPending || 0) + ' ART PENDING',
    ];
    html += '<div class="empty-state" style="margin-bottom:12px">// ' + chips.join(' &middot; ') + '</div>';

    const tracks = data.tracks || [];
    html += '<div class="section-row"><div class="section-label">// this drop, newest first</div>';
    if (tracks.length === 0) {
      html += '<div class="empty-state">// nothing in the explo folder yet — the next weekly drop lands here automatically</div>';
    } else {
      html += '<div class="list">';
      tracks.forEach((t, index) => { html += exploTrackRow(t, index + 1); });
      html += '</div>';
    }
    html += '</div></section>';
    main.innerHTML = html;
  }

  function exploTrackRow(t, num) {
    const title = t.title || t.matchedTitle || "(unidentified)";
    const artist = t.artist || t.matchedArtist || "";
    const albumBits = t.albumTitle ? escapeHTML(t.albumTitle) : "";
    const meta = [artist ? escapeHTML(artist) : null, albumBits || null].filter(Boolean).join(" &middot; ");
    let statusLabel;
    if (t.status === "matched") statusLabel = "IDENTIFIED";
    else if (t.status === "matched-fallback") statusLabel = "IDENTIFIED (TEXT)";
    else if (t.attempts >= 5) statusLabel = "RETIRED (" + t.attempts + " TRIES)";
    else if (t.status === "error") statusLabel = "ERROR &middot; RETRYING";
    else statusLabel = "AWAITING RETRY " + t.attempts + "/5";
    let coverLabel = "";
    if (t.status === "matched" || t.status === "matched-fallback") {
      if (t.coverStatus === "done") coverLabel = "ART OK";
      else if (t.coverStatus === "placeholder") coverLabel = "PLACEHOLDER ART";
      else coverLabel = "ART PENDING";
    }
    const spec = coverLabel ? statusLabel + " &middot; " + coverLabel : statusLabel;
    const inner = '<div class="num">' + num + '</div>' +
      '<div class="main"><div class="name">' + escapeHTML(title) + '</div>' +
      (meta ? '<div class="meta">' + meta + '</div>' : '') + '</div>' +
      '<div class="channel-spec">' + spec + '</div>';
    if (t.albumId) {
      return '<a class="list-row clickable" href="#music" data-action="album-detail" data-id="' + attr(t.albumId) + '">' + inner + '</a>';
    }
    return '<div class="list-row">' + inner + '</div>';
  }

  const views = {
    home: viewHome,
    music: viewMusic,
    audiobooks: viewAudiobooks,
    podcasts: viewPodcasts,
    radio: viewRadio,
    explo: viewExplo,
    search: viewSearch,
    settings: viewSettings,
  };
  const detailHandlers = {
    "music/album": openAlbum,
    "music/artist": openArtist,
    "music/playlist": openPlaylist,
    "audiobooks/item": openAudiobook,
    "audiobooks/author": openAuthor,
    "audiobooks/series": openSeries,
    "podcasts/show": openPodcast,
  };

  function navigateTo(path) {
    const target = "#" + path;
    if (location.hash === target) {
      dispatchHash();
    } else {
      location.hash = target;
    }
  }

  function setActiveTab(name) { navigateTo(name); }

  function highlightTab(name) {
    const previous = activeTab;
    activeTab = name;
    nav.querySelectorAll("[data-tab]").forEach((tab) => tab.classList.toggle("active", tab.dataset.tab === name));
    const cmdTitle = document.getElementById("cmdTitle");
    if (cmdTitle) cmdTitle.textContent = (name || "home").toUpperCase();
    if (previous === "radio" && name !== "radio") stopRadioPolling();
  }

  function dispatchHash() {
    const raw = (location.hash || "#home").slice(1);
    const parts = raw.split("/").filter(Boolean);
    const tab = parts[0] || "home";
    highlightTab(tab);
    if (parts.length >= 3) {
      const key = tab + "/" + parts[1];
      const handler = detailHandlers[key];
      if (handler) { handler(decodeURIComponent(parts[2])); return; }
    }
    if (views[tab]) views[tab]();
    else views.home();
  }

  nav.querySelectorAll("[data-tab]").forEach((tab) => {
    tab.addEventListener("click", () => navigateTo(tab.dataset.tab));
  });
  window.addEventListener("hashchange", dispatchHash);

  document.getElementById("signOut").addEventListener("click", () => {
    localStorage.removeItem(tokenKey);
    window.location.href = "/login";
  });

  // Live updates. Scan and backfill progress used to be polled every 1.5-2s
  // per open tab for as long as a job ran; it is now pushed.
  function startEventStream() {
    connectEvents({
      url: "/api/v1/events",
      token: () => token,
      onEvent: (event) => {
        if (event.type === "scan-job") {
          handleScanJobEvent(event.data);
        } else if (event.type === "artist-images") {
          updateArtistImageJobPanel(event.data && event.data.job);
        }
      },
      onStatus: (state) => {
        // Only downgrade the status line; "live" is the normal case and
        // should not stomp on whatever the current view is reporting.
        if (state === "down") setStatus("RECONNECTING…");
        else if (state === "live" && currentUser) setStatus("ONLINE · CATALOG READY");
      },
    });
  }

  /* -------- boot -------- */
  (async function boot() {
    try {
      setCurrentUser(await api("/api/v1/users/me"));
      document.getElementById("authUser").textContent = (currentUser.username || "-").toUpperCase();
      await ensureStreamToken();
      // Refresh the stream token well before its TTL so audio/img URLs
      // rendered later in the session keep working without flicker.
      setInterval(() => { refreshStreamToken().catch(() => {}); }, 20 * 60 * 1000);
      configureScanUI({
        refreshActiveView: async () => { if (activeTab && views[activeTab]) await views[activeTab](); },
        closeOtherPanels: closeActivityPanel,
      });
      setStatus("ONLINE · CATALOG READY");
      startEventStream();
      await resumeActiveScan();
      await resumeArtistImageBackfill();
      // Unhide the EXPLO tab when the feature is configured (even if
      // currently unhealthy — the tab itself explains why). Runs before
      // dispatchHash so a deep-link to #explo doesn't race the unhide.
      try {
        const exploStatus = await api("/api/v1/explo/status");
        if (exploStatus && exploStatus.configured) {
          const tab = document.getElementById("exploTab");
          if (tab) tab.hidden = false;
        }
      } catch { /* explo unavailable — tab stays hidden */ }
    } catch (err) {
      setStatus("ERROR · " + (err.message || "unknown"));
      return;
    }
    if (!location.hash) location.hash = "#home";
    dispatchHash();
  })();
})();
