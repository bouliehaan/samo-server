// The player dock: what is loaded, where it is, and reporting that back.
//
// playerTarget is the catalog item currently loaded — kind, id, duration and
// the offset the stream itself started at. That last field is why this is not
// just audio.currentTime: an audiobook or episode is streamed FROM a resume
// point, so the element's clock starts at zero partway through the book.
// globalBase is what converts back to a position in the whole item, and
// getting it wrong silently reports everyone's progress as near-zero.
//
// lastProgressSync throttles the PATCH: playback reports every ~20s, not on
// every timeupdate, which fires several times a second.

import { api } from "./auth.js";
import { setStatus } from "./html.js";
import { audio, nowPlayingBtn, nowPlayingSub, playerDock, playerDurationEl, playerGlyph, playerSeek, playerSeekBar, playerSeekHead, playerSub, playerTimeEl, playerTitle, playerToggle } from "./elements.js";
import { formatClock } from "./format.js";

export let playerTarget = null;

export let lastProgressSync = 0;

export function patchPlayback(kind, id, patch) {
  return api("/api/v1/playback/" + encodeURIComponent(kind) + "/" + encodeURIComponent(id), {
    method: "PATCH",
    body: patch,
  });
}

export function playbackGlobalSeconds(target) {
  if (!target) return 0;
  if (target.kind === "audiobook" || target.kind === "podcast-episode") {
    return (target.globalBase || 0) + Math.floor(audio.currentTime || 0);
  }
  return Math.floor(audio.currentTime || 0);
}

export function flushPlaybackProgress() {
  if (!playerTarget) return;
  const now = playbackGlobalSeconds(playerTarget);
  if (now <= 0) return;
  if (playerTarget.kind === "audiobook") {
    patchPlayback("audiobook", playerTarget.id, { progressSeconds: now, touchLastPositionAt: true }).catch(() => {});
  } else if (playerTarget.kind === "music-track" || playerTarget.kind === "podcast-episode") {
    patchPlayback(playerTarget.kind, playerTarget.id, { progressSeconds: now, touchLastPositionAt: true }).catch(() => {});
  }
}

export function playURL(url, title, subtitle, target) {
  flushPlaybackProgress();
  playerTarget = target || null;
  lastProgressSync = playerTarget ? (playerTarget.globalBase || 0) : 0;
  playerTitle.textContent = title || "UNKNOWN";
  playerSub.textContent = subtitle || "";
  if (nowPlayingBtn && nowPlayingSub) {
    nowPlayingBtn.hidden = false;
    nowPlayingSub.textContent = (title || "PLAYING").toUpperCase();
  }
  playerDock.hidden = false;
  setPlayerGlyph(false);
  audio.src = url;
  refreshSeekUI();
  audio.play().catch((err) => {
    setPlayerGlyph(true);
    setStatus("PLAYER · " + (err.message || "blocked"));
  });
}

export function setPlayerGlyph(paused) {
  if (!playerGlyph) return;
  playerGlyph.textContent = paused ? GLYPH_PLAY : GLYPH_PAUSE;
  if (playerToggle) playerToggle.setAttribute("aria-label", paused ? "Play" : "Pause");
}

export function applyStreamResumeSeek() {
  if (!playerTarget) return;
  const base = playerTarget.globalBase || 0;
  if (base <= 0) return;
  const fileDur = isFinite(audio.duration) ? audio.duration : 0;
  if (fileDur <= 0) return;
  const total = playerTarget.duration || 0;
  // Tail-only partial response: currentTime 0 already matches globalBase audibly.
  if (total > 0 && fileDur < total * 0.85) return;
  if (audio.currentTime >= 0.75) return;
  const cap = total > 0 ? Math.min(base, total - 0.25) : base;
  const target = Math.min(cap, fileDur - 0.05);
  if (target > 0) {
    audio.currentTime = target;
    // Full file in the browser — position lives in currentTime only. Keeping
    // globalBase would double-count on save (globalBase + currentTime).
    playerTarget.globalBase = 0;
    lastProgressSync = Math.floor(target);
  }
}

export function refreshSeekUI() {
  if (!playerSeekBar || !playerSeekHead) return;
  const fileDur = isFinite(audio.duration) ? audio.duration : 0;
  const globalNow =
    playerTarget && (playerTarget.kind === "audiobook" || playerTarget.kind === "podcast-episode")
      ? playbackGlobalSeconds(playerTarget)
      : Math.floor(audio.currentTime || 0);
  const totalDur =
    playerTarget &&
    (playerTarget.kind === "audiobook" || playerTarget.kind === "podcast-episode") &&
    playerTarget.duration > 0
      ? playerTarget.duration
      : fileDur;
  const pct = totalDur > 0 ? Math.min(100, (globalNow / totalDur) * 100) : 0;
  playerSeekBar.style.width = pct + "%";
  playerSeekHead.style.left = pct + "%";
  if (playerTimeEl) playerTimeEl.textContent = formatClock(globalNow);
  if (playerDurationEl) playerDurationEl.textContent = formatClock(totalDur);
  if (playerSeek) playerSeek.setAttribute("aria-valuenow", String(Math.round(pct)));
}

export function seekFromPointer(event) {
  if (!playerSeek) return;
  const rect = playerSeek.getBoundingClientRect();
  const x = Math.max(0, Math.min(rect.width, event.clientX - rect.left));
  const fileDur = isFinite(audio.duration) ? audio.duration : 0;
  if (fileDur <= 0) return;
  if (playerTarget && (playerTarget.kind === "audiobook" || playerTarget.kind === "podcast-episode")) {
    const total = playerTarget.duration || fileDur;
    const globalTarget = (x / rect.width) * total;
    const base = playerTarget.globalBase || 0;
    audio.currentTime = Math.max(0, Math.min(fileDur - 0.05, globalTarget - base));
  } else {
    audio.currentTime = (x / rect.width) * fileDur;
  }
  refreshSeekUI();
}

// The glyphs the dock's toggle shows. Here rather than in elements.js because
// they are player vocabulary, not page furniture.
const GLYPH_PAUSE = "▌▌";
const GLYPH_PLAY = "▶";

// reportPlaybackProgress is the throttled half of the timeupdate handler.
//
// timeupdate fires several times a second; playback positions are worth
// persisting about every 20s. Keeping the throttle here rather than in the
// listener is what lets lastProgressSync stay private to this module.
//
// Audiobooks and episodes report a GLOBAL position — the stream may have
// started partway through the item, so audio.currentTime alone would report
// everyone as near the beginning forever.
export function reportPlaybackProgress() {
  if (!playerTarget) return;
  if (playerTarget.kind === "audiobook" || playerTarget.kind === "podcast-episode") {
    const globalNow = playbackGlobalSeconds(playerTarget);
    if (globalNow <= 0 || globalNow - lastProgressSync < 20) return;
    lastProgressSync = globalNow;
    patchPlayback(playerTarget.kind, playerTarget.id, { progressSeconds: globalNow, touchLastPositionAt: true }).catch(() => {});
    return;
  }
  if (playerTarget.kind !== "music-track") return;
  const now = Math.floor(audio.currentTime || 0);
  if (now <= 0 || now - lastProgressSync < 20) return;
  lastProgressSync = now;
  patchPlayback(playerTarget.kind, playerTarget.id, { progressSeconds: now, touchLastPositionAt: true }).catch(() => {});
}

// Clears the throttle so the next position is reported immediately. Used when
// an audiobook rolls onto its next file, where the new position is a jump
// rather than a tick.
export function resetProgressThrottle() {
  lastProgressSync = 0;
}

// clearPlayerTarget forgets what was loaded, on stop. A setter rather than an
// exported mutable binding because an import is read-only at the import site —
// assigning to one throws in a module, which is what caught this.
export function clearPlayerTarget() {
  playerTarget = null;
}
