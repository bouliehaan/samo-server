package api

import (
	"html/template"
	"log"
	"net/http"
)

// appPage serves the authenticated dashboard shell. Setup-pending visitors
// get redirected to the wizard; everyone else gets the same HTML and the JS
// decides whether to render content or punt to /login.
func (s *Server) appPage(w http.ResponseWriter, r *http.Request) {
	if status, err := s.computeSetupStatus(r.Context()); err == nil && status.NeedsSetup {
		http.Redirect(w, r, "/setup", http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := appTemplate.Execute(w, nil); err != nil {
		log.Printf("failed to render app page: %v", err)
	}
}

var appTemplate = template.Must(template.New("app").Parse(appHTML))

const appHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>SAMO SERVER</title>
  <link href="https://fonts.googleapis.com/css2?family=Young+Serif&display=swap" rel="stylesheet">
  <link rel="icon" type="image/png" href="/favicon-dark.png">
  <link rel="icon" type="image/png" href="/favicon-light.png" media="(prefers-color-scheme: light)">
  <link rel="icon" type="image/png" href="/favicon-dark.png" media="(prefers-color-scheme: dark)">
  <style>` + samoBaseCSS + `</style>
  <style>` + appCSS + `</style>
</head>
<body>
  <div class="grid-bg"></div>
  <header class="app-bar">
    <div class="app-bar-left">
      <a class="app-bar-brand" href="/app#home">
        <span class="samo-wm bar"><span class="word">SAMO</span><span class="word dim">SERVER</span></span>
        <span class="samo-status bar"><span class="dot"></span><span id="barStatusText">CONNECTING</span></span>
      </a>
    </div>
    <nav class="app-nav" id="appNav">
      <button class="tab" data-tab="home">HOME</button>
      <button class="tab" data-tab="music">MUSIC</button>
      <button class="tab" data-tab="audiobooks">AUDIOBOOKS</button>
      <button class="tab" data-tab="podcasts">PODCASTS</button>
      <button class="tab" data-tab="radio">RADIO</button>
      <button class="tab" data-tab="search">SEARCH</button>
    </nav>
    <div class="app-bar-right">
      <div class="bar-util" role="toolbar" aria-label="Session utilities">
        <button class="bar-util-btn" id="refreshBtn" type="button" data-action="refresh-scan" title="Quick rescan of every attached library — new and changed files only">
          <span class="util-ring" id="refreshRing" hidden></span>
          <span class="util-label">REFRESH</span>
          <span class="util-sub" id="refreshSub">ALL LIBS</span>
        </button>
        <button class="bar-util-btn" id="nowPlayingBtn" type="button" data-action="now-playing" title="Now playing" hidden>
          <span class="util-label">NOW PLAYING</span>
          <span class="util-sub" id="nowPlayingSub">IDLE</span>
        </button>
        <button class="bar-util-btn" id="activityBtn" type="button" data-action="activity-open" title="Server activity">
          <span class="util-label">ACTIVITY</span>
          <span class="util-sub" id="activitySub">SERVER</span>
        </button>
        <button class="bar-util-btn" type="button" data-action="go-settings" data-mode="libraries" title="Settings">
          <span class="util-label">SETTINGS</span>
          <span class="util-sub">CONTROL</span>
        </button>
        <button class="bar-util-btn" id="signOut" type="button" title="Sign out">
          <span class="util-label">SIGN OUT</span>
          <span class="util-sub" id="authUser">—</span>
        </button>
      </div>
    </div>
  </header>
  <main class="app-main" id="appMain">
    <div class="boot-line">// booting samo client · please stand by</div>
  </main>
  <div class="scan-panel" id="scanPanel" hidden>
    <div class="scan-shell">
      <header class="scan-head">
        <div>
          <div class="scan-eyebrow">// SYNC PROGRESS</div>
          <h3>Scan jobs</h3>
        </div>
        <button class="btn danger btn-mini" id="scanCancelBtn" data-action="cancel-scan" hidden>CANCEL</button>
        <button class="btn ghost btn-mini" data-action="scan-panel-close">CLOSE</button>
      </header>
      <div class="scan-current" id="scanPanelCurrent"></div>
      <div class="scan-history" id="scanPanelHistory"></div>
    </div>
  </div>
  <div class="activity-panel" id="activityPanel" hidden>
    <div class="scan-shell">
      <header class="scan-head">
        <div>
          <div class="scan-eyebrow">// SERVER ACTIVITY</div>
          <h3>Activity</h3>
        </div>
        <button class="btn ghost btn-mini" data-action="activity-close">CLOSE</button>
      </header>
      <div class="activity-body" id="activityBody"></div>
    </div>
  </div>
  <div class="identify-modal" id="identifyModal" hidden>
    <div class="identify-shell">
      <header class="identify-head">
        <div>
          <div class="identify-eyebrow">// FIND MATCH</div>
          <h3 id="identifyTitle">Identify</h3>
        </div>
        <button class="btn ghost btn-mini" data-action="identify-close">CLOSE</button>
      </header>
      <form class="identify-form" id="identifyForm">
        <label class="field"><span class="field-label">QUERY</span><input type="text" id="identifyQuery"></label>
        <button class="btn primary btn-small" type="submit">SEARCH PROVIDERS</button>
      </form>
      <div class="identify-results" id="identifyResults"></div>
    </div>
  </div>
  <div class="player-dock" id="playerDock" hidden>
    <div class="player-meta">
      <span class="player-label">// now playing</span>
      <span class="player-title" id="playerTitle">IDLE</span>
      <span class="player-sub" id="playerSub"></span>
    </div>
    <div class="player-transport">
      <button class="player-btn" id="playerToggle" type="button" aria-label="Play/Pause" title="Space">
        <span class="player-glyph" id="playerGlyph">&#9612;&#9612;</span>
      </button>
      <div class="player-track">
        <div class="player-seek" id="playerSeek" role="slider" aria-label="Seek" tabindex="0">
          <div class="player-seek-bar" id="playerSeekBar"></div>
          <div class="player-seek-head" id="playerSeekHead"></div>
        </div>
        <div class="player-times">
          <span id="playerTime">0:00</span>
          <span class="player-times-sep">/</span>
          <span id="playerDuration">0:00</span>
        </div>
      </div>
      <button class="player-btn ghost" id="playerStop" type="button" aria-label="Stop" title="Stop">
        <span class="player-glyph">&#9632;</span>
      </button>
      <audio id="audioPlayer" preload="none"></audio>
    </div>
  </div>
  <script>` + appJS + `</script>
</body>
</html>`

const appCSS = `
main.app-main {
  position: relative;
  z-index: 1;
  max-width: 1200px;
  margin: 0 auto;
  padding: 36px 28px 96px;
  min-height: calc(100vh - 96px);
}
.app-bar {
  position: sticky;
  top: 0;
  z-index: 5;
  display: grid;
  grid-template-columns: auto 1fr auto;
  align-items: center;
  gap: 24px;
  padding: 14px 28px;
  background: rgba(0, 0, 0, 0.92);
  backdrop-filter: blur(8px);
  border-bottom: 1px solid var(--line);
}
.app-bar-brand {
  display: inline-flex;
  align-items: baseline;
  gap: 14px;
  text-decoration: none;
  color: inherit;
}
.app-bar-brand:hover { text-decoration: none; }
.app-nav {
  display: flex;
  gap: 4px;
  justify-content: center;
}
.app-nav .tab {
  font-family: var(--mono);
  font-size: 0.7rem;
  letter-spacing: 0.22em;
  text-transform: uppercase;
  background: transparent;
  border: 1px solid transparent;
  color: var(--text-dim);
  padding: 8px 14px;
  cursor: pointer;
  transition: color 120ms ease-out, border-color 120ms ease-out, background 120ms ease-out;
}
.app-nav .tab:hover { color: var(--text); border-color: var(--ghost); }
.app-nav .tab.active {
  color: var(--accent);
  border-color: var(--accent);
  background: color-mix(in srgb, var(--accent) 6%, transparent);
}
.app-bar-right {
  display: flex;
  align-items: center;
  gap: 12px;
}
.bar-util {
  display: flex;
  align-items: stretch;
  border: 1px solid var(--line);
  background: #000;
}
.bar-util-btn {
  display: grid;
  gap: 1px;
  padding: 5px 10px;
  min-width: 72px;
  background: transparent;
  border: 0;
  border-right: 1px solid var(--line);
  font-family: var(--mono);
  cursor: pointer;
  color: var(--text-dim);
  text-align: left;
  position: relative;
}
.bar-util-btn:last-child { border-right: 0; }
.bar-util-btn:hover { color: var(--text); background: var(--surface); }
.bar-util-btn.running { color: var(--accent); }
.bar-util-btn.ok { color: var(--accent); }
.bar-util-btn.error { color: var(--danger); }
.bar-util-btn .util-label {
  font-size: 0.56rem;
  letter-spacing: 0.18em;
  text-transform: uppercase;
}
.bar-util-btn .util-sub {
  font-size: 0.62rem;
  letter-spacing: 0.06em;
  color: var(--muted);
  max-width: 96px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.bar-util-btn.running .util-sub { color: var(--accent); }
.util-ring {
  position: absolute;
  top: 5px;
  right: 5px;
  width: 10px;
  height: 10px;
  border: 1px solid transparent;
  border-top-color: var(--accent);
  border-radius: 50%;
  animation: scanBadgeSpin 1.1s linear infinite;
  pointer-events: none;
}
.account-layout { display: grid; gap: 14px; }
.account-row { display: grid; gap: 14px; }
.account-row-2 { grid-template-columns: repeat(2, minmax(0, 1fr)); }
@media (max-width: 780px) { .account-row-2 { grid-template-columns: 1fr; } }
.btn-small {
  padding: 6px 12px;
  font-size: 0.66rem;
}
.boot-line {
  font-family: var(--mono);
  font-size: 0.78rem;
  color: var(--text-dim);
  letter-spacing: 0.08em;
  padding: 64px 0;
  text-align: center;
}
.boot-line::after {
  content: "_";
  display: inline-block;
  margin-left: 4px;
  color: var(--accent);
  animation: bootBlink 1.1s steps(1) infinite;
}
@keyframes bootBlink { 50% { opacity: 0; } }

.view {
  display: grid;
  gap: 28px;
}
.view-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
}
.view-head h1 {
  margin: 0;
  font-family: var(--serif);
  font-size: clamp(2.4rem, 4.5vw, 3.6rem);
  font-weight: 400;
  letter-spacing: -0.025em;
}
.view-head .crumb {
  font-family: var(--mono);
  font-size: 0.72rem;
  letter-spacing: 0.22em;
  text-transform: uppercase;
  color: var(--muted);
}

.stat-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(160px, 1fr));
  gap: 12px;
}
.stat-card {
  border: 1px solid var(--line);
  background: var(--surface);
  padding: 18px;
  display: grid;
  gap: 6px;
}
.stat-card .label {
  font-family: var(--mono);
  font-size: 0.65rem;
  letter-spacing: 0.22em;
  color: var(--muted);
  text-transform: uppercase;
}
.stat-card .value {
  font-family: var(--sans);
  font-size: 2.2rem;
  font-weight: 900;
  color: var(--text);
  line-height: 1;
  letter-spacing: -0.02em;
}
.stat-card .value.accent { color: var(--accent); }

.section-row {
  display: grid;
  gap: 14px;
}
.section-row .section-label {
  font-family: var(--mono);
  font-size: 0.72rem;
  letter-spacing: 0.22em;
  text-transform: uppercase;
  color: var(--text-dim);
}

.album-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
  gap: 12px;
}
.album-row {
  display: grid;
  grid-template-columns: repeat(10, minmax(0, 1fr));
  gap: 12px;
}
@media (max-width: 1200px) {
  .album-row {
    grid-template-columns: repeat(auto-fill, minmax(140px, 1fr));
  }
}
.album-card {
  border: 1px solid var(--line);
  background: var(--surface);
  padding: 14px;
  display: grid;
  gap: 6px;
  text-decoration: none;
  color: inherit;
  cursor: pointer;
  transition: border-color 120ms ease-out, transform 120ms ease-out, box-shadow 160ms ease-out;
}
.album-card:hover {
  border-color: var(--accent);
  transform: translateY(-1px);
  box-shadow: 0 0 24px -12px var(--accent);
}
.album-card .cover {
  aspect-ratio: 1 / 1;
  background: #000;
  border: 1px solid var(--line);
  background-size: cover;
  background-position: center;
  position: relative;
  display: flex;
  align-items: flex-end;
  justify-content: flex-start;
  padding: 8px;
  font-family: var(--mono);
  font-size: 0.65rem;
  letter-spacing: 0.16em;
  text-transform: uppercase;
  color: var(--muted);
  transition: border-color 120ms ease-out;
}
.album-card:hover .cover { border-color: var(--accent); }
.album-card .cover.empty::after {
  content: "// no art";
}
.album-card .title {
  font-family: var(--sans);
  font-weight: 700;
  font-size: 0.95rem;
  letter-spacing: -0.01em;
  color: var(--text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.album-card .sub {
  font-family: var(--mono);
  font-size: 0.72rem;
  color: var(--text-dim);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.list {
  display: grid;
  border: 1px solid var(--line);
  background: var(--surface);
}
.list-row {
  display: grid;
  grid-template-columns: auto 1fr auto;
  gap: 14px;
  align-items: center;
  padding: 14px 16px;
  border-bottom: 1px solid color-mix(in srgb, var(--line) 50%, transparent);
}
.list-row:last-child { border-bottom: 0; }
.list-row .num {
  font-family: var(--mono);
  font-size: 0.78rem;
  color: var(--muted);
  min-width: 30px;
  text-align: right;
}
.list-row .main { min-width: 0; }
.list-row .main .name {
  font-family: var(--sans);
  font-weight: 700;
  letter-spacing: -0.01em;
}
.list-row .main .meta {
  font-family: var(--mono);
  font-size: 0.72rem;
  color: var(--text-dim);
}
.list-row .actions {
  display: flex;
  gap: 8px;
}
.list-thumb {
  width: 40px;
  height: 40px;
  background: #000;
  border: 1px solid var(--line);
  background-size: cover;
  background-position: center;
  flex-shrink: 0;
}
.list-row:has(.list-thumb) {
  grid-template-columns: 40px 1fr auto;
}
.list-thumb.empty::after {
  content: "//";
  font-family: var(--mono);
  font-size: 0.55rem;
  color: var(--muted);
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
}
.btn-mini {
  padding: 5px 10px;
  font-size: 0.62rem;
  letter-spacing: 0.14em;
}
.empty-state {
  border: 1px dashed var(--line);
  background: var(--surface);
  padding: 28px;
  font-family: var(--mono);
  font-size: 0.85rem;
  color: var(--text-dim);
  text-align: center;
}

.search-shell {
  display: grid;
  gap: 18px;
}
.search-form { display: flex; gap: 12px; }
.search-form input {
  flex: 1;
  padding: 14px 16px;
  background: #000;
  border: 1px solid var(--line);
  color: var(--text);
  font-family: var(--mono);
  font-size: 1rem;
  border-radius: 0;
}
.search-form input:focus {
  outline: none;
  border-color: var(--accent);
  box-shadow: 0 0 0 1px var(--accent), 0 0 18px -6px var(--accent);
}
.search-group { display: grid; gap: 10px; }
.search-group-head {
  font-family: var(--mono);
  font-size: 0.7rem;
  letter-spacing: 0.22em;
  color: var(--accent);
  text-transform: uppercase;
}

.radio-card {
  display: grid;
  grid-template-columns: 72px 1fr auto;
  align-items: center;
  gap: 16px;
  border: 1px solid var(--line);
  background: var(--surface);
  padding: 14px 16px;
}
.radio-cover {
  width: 72px;
  height: 72px;
  background-size: cover;
  background-position: center;
  background-color: #0a0a0a;
  border: 1px solid var(--line);
}
.radio-cover.empty {
  background-image:
    repeating-linear-gradient(45deg, rgba(255,255,255,0.04) 0 6px, transparent 6px 12px);
}
.radio-card-admin {
  grid-template-columns: 72px 1fr auto;
}
.radio-cover-upload {
  position: relative;
  display: block;
  width: 72px;
  height: 72px;
  background-size: cover;
  background-position: center;
  background-color: #0a0a0a;
  border: 1px solid var(--line);
  cursor: pointer;
  overflow: hidden;
}
.radio-cover-upload.empty {
  background-image:
    repeating-linear-gradient(45deg, rgba(255,255,255,0.04) 0 6px, transparent 6px 12px);
}
.radio-cover-upload .radio-cover-input {
  position: absolute;
  inset: 0;
  opacity: 0;
  cursor: pointer;
}
.radio-cover-upload .radio-cover-hint {
  position: absolute;
  inset: auto 0 0 0;
  padding: 4px 2px;
  background: rgba(0, 0, 0, 0.72);
  font-family: var(--mono);
  font-size: 0.52rem;
  letter-spacing: 0.14em;
  text-transform: uppercase;
  text-align: center;
  color: var(--text-dim);
  pointer-events: none;
}
.radio-cover-upload:hover .radio-cover-hint { color: var(--accent); }
.radio-admin-meta { min-width: 0; }
.radio-admin-actions {
  display: flex;
  flex-direction: column;
  gap: 8px;
  align-items: flex-end;
}
.radio-card .name {
  font-family: var(--sans);
  font-weight: 800;
  font-size: 1.15rem;
  letter-spacing: -0.01em;
  margin: 0 0 4px;
}
.radio-card .desc { color: var(--text-dim); font-size: 0.92rem; margin: 0 0 10px; max-width: 56ch; }
.radio-card .now-playing {
  font-family: var(--mono);
  font-size: 0.78rem;
  letter-spacing: 0.12em;
  text-transform: uppercase;
  color: var(--accent);
  display: flex; align-items: center; gap: 8px;
}
.radio-card .now-playing.idle { color: var(--muted); }
.radio-card .now-playing .np-label { color: var(--accent); }
.radio-card .now-playing.idle .np-label { color: var(--muted); }
.radio-card .now-playing .np-text { color: var(--text); }
.radio-card .now-playing .dot {
  width: 6px; height: 6px; background: var(--accent); display: inline-block; box-shadow: 0 0 8px var(--accent);
}
.radio-card .now-playing.idle .dot { background: var(--muted); box-shadow: none; }
.radio-admin-status {
  font-family: var(--mono);
  font-size: 0.7rem;
  letter-spacing: 0.08em;
  color: var(--text-dim);
  margin-top: 4px;
}
.radio-card .actions { display: flex; flex-direction: column; gap: 8px; align-items: flex-end; }
.radio-image-edit {
  display: flex;
  gap: 8px;
  margin-top: 8px;
  max-width: 680px;
}
.radio-image-edit input {
  flex: 1;
  min-width: 0;
  padding: 7px 9px;
  background: #000;
  border: 1px solid var(--line);
  color: var(--text);
  font-family: var(--mono);
  font-size: 0.72rem;
}
.radio-image-edit input:focus {
  outline: none;
  border-color: var(--accent);
}

/* ---- Channels (24/7 programmed radio) ----
 * Distinct card style from radio stations so the user can feel that
 * channels are the "live, programmed" thing. Slightly wider, accent-
 * tinted hover, and a NOW PLAYING block that reads like a control
 * surface, not a list row. */
.channel-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
  gap: 14px;
}
.channel-card {
  border: 1px solid var(--line);
  background: var(--surface);
  padding: 18px;
  display: grid;
  gap: 16px;
  position: relative;
  transition: border-color 120ms ease-out, box-shadow 160ms ease-out;
}
.channel-card:hover {
  border-color: var(--accent);
  box-shadow: 0 0 24px -12px var(--accent);
}
.channel-card::before {
  content: "";
  position: absolute;
  top: -1px;
  left: -1px;
  width: 14px;
  height: 14px;
  border-top: 1px solid var(--accent);
  border-left: 1px solid var(--accent);
  pointer-events: none;
}
.channel-eyebrow {
  font-family: var(--mono);
  font-size: 0.65rem;
  letter-spacing: 0.22em;
  text-transform: uppercase;
  color: var(--accent);
}
.channel-card .name {
  margin: 4px 0 0;
  font-family: var(--sans);
  font-size: 1.2rem;
  font-weight: 800;
  letter-spacing: -0.01em;
}
.channel-card .desc {
  margin: 0;
  color: var(--text-dim);
  font-size: 0.85rem;
}
.channel-card .channel-spec {
  font-family: var(--mono);
  font-size: 0.7rem;
  letter-spacing: 0.14em;
  color: var(--muted);
  text-transform: uppercase;
}
.channel-card .channel-actions {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}
.channel-now {
  position: relative;
}
.channel-now .channel-now-body {
  display: grid;
  gap: 8px;
}
.channel-now .channel-now-current .name {
  font-family: var(--sans);
  font-size: 1.4rem;
  font-weight: 800;
  letter-spacing: -0.015em;
  margin: 4px 0 2px;
}
.channel-now .channel-now-current .sub {
  font-family: var(--sans);
  font-size: 0.95rem;
  color: var(--text-dim);
}
.channel-now .channel-now-current .sub.mono {
  font-family: var(--mono);
  font-size: 0.72rem;
  letter-spacing: 0.1em;
  color: var(--muted);
  text-transform: uppercase;
}
.channel-now-stats {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  margin-top: 4px;
}
.channel-listeners {
  font-family: var(--mono);
  font-size: 0.66rem;
  letter-spacing: 0.18em;
  text-transform: uppercase;
  padding: 4px 10px;
  border: 1px solid var(--accent);
  color: var(--accent);
  background: color-mix(in srgb, var(--accent) 6%, transparent);
}

/* ---- Schedule timeline ----
 * 24-hour horizontal strip per weekday. Hour labels float above the
 * first row; each row is a track with absolutely-positioned rule
 * "bands" anchored by start-minute and width-by-duration. A single
 * vertical "now" indicator overlays today's row so the user sees
 * where current playback sits relative to upcoming rules. */
.sched-timeline {
  position: relative;
  display: grid;
  gap: 4px;
  padding: 8px 0 4px;
  margin-bottom: 12px;
}
.sched-hour-labels {
  position: relative;
  height: 14px;
  margin-left: 48px;
}
.sched-hour-labels span {
  position: absolute;
  transform: translateX(-50%);
  top: 0;
  font-family: var(--mono);
  font-size: 0.6rem;
  letter-spacing: 0.1em;
  color: var(--muted);
  text-transform: uppercase;
}
.sched-row {
  display: grid;
  grid-template-columns: 48px 1fr;
  align-items: center;
  gap: 0;
  height: 22px;
}
.sched-row-label {
  font-family: var(--mono);
  font-size: 0.66rem;
  letter-spacing: 0.16em;
  color: var(--text-dim);
  text-align: right;
  padding-right: 10px;
}
.sched-row.today .sched-row-label { color: var(--accent); }
.sched-row-track {
  position: relative;
  height: 100%;
  background: #000;
  border: 1px solid var(--line);
  background-image: repeating-linear-gradient(to right,
    transparent 0, transparent calc((100% / 24) - 1px),
    rgba(255,255,255,0.06) calc((100% / 24) - 1px), rgba(255,255,255,0.06) calc(100% / 24));
}
.sched-row.today .sched-row-track {
  border-color: color-mix(in srgb, var(--accent) 40%, var(--line));
}
.sched-band {
  position: absolute;
  top: 1px;
  bottom: 1px;
  font-family: var(--mono);
  font-size: 0.6rem;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: #000;
  padding: 2px 5px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  border-right: 1px solid rgba(0, 0, 0, 0.25);
  cursor: default;
}
.sched-now {
  position: absolute;
  top: -2px;
  bottom: -2px;
  width: 2px;
  background: var(--accent);
  box-shadow: 0 0 8px var(--accent);
  pointer-events: none;
}

.detail-shell {
  display: grid;
  grid-template-columns: 220px 1fr;
  gap: 28px;
  align-items: start;
}
@media (max-width: 720px) { .detail-shell { grid-template-columns: 1fr; } }
.detail-cover {
  aspect-ratio: 1 / 1;
  background: #000;
  border: 1px solid var(--line);
  background-size: cover;
  background-position: center;
}
.detail-cover.radio-cover-upload {
  width: auto;
  height: auto;
  display: block;
  position: relative;
  cursor: pointer;
  overflow: hidden;
}
.detail-meta h2 {
  margin: 0 0 4px;
  font-size: 1.8rem;
  font-weight: 900;
  letter-spacing: -0.025em;
}
.detail-meta .artist {
  font-family: var(--mono);
  font-size: 0.85rem;
  color: var(--text-dim);
  letter-spacing: 0.08em;
}
.detail-meta .stats {
  display: flex; gap: 14px; margin-top: 14px;
  font-family: var(--mono); font-size: 0.7rem; letter-spacing: 0.18em;
  color: var(--muted); text-transform: uppercase;
}

.pill-bar {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
  margin-bottom: 8px;
}
/* .pill is the base chip style — used inside .pill-bar, .sort-toolbar,
 * and anywhere else we need a tiny mono-cased toggle. Keeping the base
 * styles unscoped means new containers don't need to copy them in. */
.pill {
  font-family: var(--mono);
  font-size: 0.68rem;
  letter-spacing: 0.2em;
  text-transform: uppercase;
  background: transparent;
  border: 1px solid var(--line);
  color: var(--text-dim);
  padding: 6px 12px;
  cursor: pointer;
}
.pill:hover { color: var(--text); }
.pill.active { color: var(--accent); border-color: var(--accent); }

.sort-toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  border: 1px solid var(--line);
  background: var(--surface);
  padding: 10px 12px;
}
.sort-toolbar .sort-group {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
  align-items: center;
}
.sort-toolbar .sort-label {
  font-family: var(--mono);
  font-size: 0.65rem;
  letter-spacing: 0.18em;
  color: var(--muted);
  text-transform: uppercase;
  margin-right: 4px;
}
.sort-toolbar .pill {
  margin: 0;
}

.view-actions,
.quick-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  align-items: center;
}
.view-head .view-actions {
  justify-content: flex-end;
}

/* ---- Inline composer ----
 * Add-flows expand in-context instead of navigating to settings. The amber
 * hairline signals an active edit zone; close with CANCEL or the X. */
.composer {
  border: 1px solid var(--accent);
  background: color-mix(in srgb, var(--accent) 4%, var(--surface));
  padding: 18px 20px;
  display: grid;
  gap: 14px;
  position: relative;
}
.composer[hidden] { display: none; }
.composer-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-family: var(--mono);
  font-size: 0.7rem;
  letter-spacing: 0.22em;
  text-transform: uppercase;
  color: var(--accent);
}
.composer-close {
  background: transparent;
  border: 1px solid var(--ghost);
  color: var(--muted);
  width: 24px;
  height: 24px;
  padding: 0;
  cursor: pointer;
  font-family: var(--mono);
  font-size: 0.85rem;
  line-height: 1;
  transition: color 90ms, border-color 90ms;
}
.composer-close:hover { color: var(--accent); border-color: var(--accent); }
.composer-row {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 12px;
}
.composer-row .field { margin-bottom: 0; }
.composer-row .field.full { grid-column: 1 / -1; }
.composer-actions {
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
  align-items: center;
}
.composer-hint {
  font-family: var(--mono);
  font-size: 0.68rem;
  letter-spacing: 0.08em;
  color: var(--muted);
  line-height: 1.5;
}
.tag-preview {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  margin-top: 8px;
  min-height: 22px;
}
.tag-preview .tag-preview-empty {
  font-family: var(--mono);
  font-size: 0.66rem;
  color: var(--ghost);
  letter-spacing: 0.12em;
  text-transform: uppercase;
}
.panel-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
  gap: 14px;
}
.panel {
  border: 1px solid var(--line);
  background: var(--surface);
  padding: 18px;
  display: grid;
  gap: 14px;
  align-content: start;
}
.panel-wide { grid-column: 1 / -1; }
.settings-form {
  grid-column: 1 / -1;
}
.settings-form .form-grid {
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
}
.panel-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
  font-family: var(--mono);
  font-size: 0.7rem;
  letter-spacing: 0.22em;
  text-transform: uppercase;
  color: var(--accent);
}
.panel-sub {
  font-family: var(--mono);
  font-size: 0.72rem;
  color: var(--text-dim);
  line-height: 1.5;
}
.form-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(190px, 1fr));
  gap: 12px;
}
.form-grid .field { margin-bottom: 0; }
.form-grid .full { grid-column: 1 / -1; }
.field.checkbox {
  display: flex;
  align-items: center;
  gap: 10px;
  margin: 0;
  font-family: var(--mono);
  font-size: 0.72rem;
  letter-spacing: 0.14em;
  color: var(--text-dim);
  text-transform: uppercase;
}
.field.checkbox input {
  width: 16px;
  height: 16px;
  accent-color: var(--accent);
}
.status-line,
.secret-line {
  border: 1px solid var(--line);
  background: #000;
  padding: 10px 12px;
  font-family: var(--mono);
  font-size: 0.76rem;
  color: var(--text-dim);
  line-height: 1.45;
  overflow-wrap: anywhere;
}
.status-line.good { color: var(--accent); border-color: color-mix(in srgb, var(--accent) 70%, var(--line)); }
.status-line.bad { color: var(--danger); border-color: var(--danger); }
.secret-line { color: var(--text); }
.tag-line {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.meta-chip {
  border: 1px solid var(--line);
  color: var(--text-dim);
  font-family: var(--mono);
  font-size: 0.62rem;
  letter-spacing: 0.16em;
  text-transform: uppercase;
  padding: 4px 7px;
}
.lastfm-utility-grid {
  display: grid;
  grid-template-columns: minmax(0, 1.15fr) minmax(260px, 0.85fr);
  gap: 18px 24px;
  align-items: start;
}
.lastfm-utility-section {
  display: grid;
  gap: 12px;
  min-width: 0;
}
.lastfm-utility-label {
  font-family: var(--mono);
  font-size: 0.68rem;
  letter-spacing: 0.18em;
  text-transform: uppercase;
  color: var(--text-dim);
}
.lastfm-utility .actions {
  margin: 0;
}
@media (max-width: 780px) {
  .lastfm-utility-grid { grid-template-columns: 1fr; }
}
.list-row.clickable { cursor: pointer; }
.list-row.clickable:hover { background: color-mix(in srgb, var(--accent) 8%, transparent); }
.list-row .actions { justify-content: flex-end; margin: 0; }
.progress-track {
  width: 100%;
  height: 4px;
  background: var(--ghost);
  margin-top: 8px;
}
.progress-track .bar {
  height: 100%;
  background: var(--accent);
}
.player-dock {
  position: fixed;
  left: 0;
  right: 0;
  bottom: 0;
  z-index: 8;
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(360px, 620px);
  gap: 24px;
  align-items: center;
  padding: 14px 28px;
  background: rgba(0, 0, 0, 0.94);
  border-top: 1px solid var(--line);
  backdrop-filter: blur(10px);
  box-shadow: 0 -8px 32px -16px rgba(0, 0, 0, 0.9);
}
.player-dock[hidden] { display: none; }
.player-dock::before {
  content: "";
  position: absolute;
  top: -1px;
  left: 0;
  right: 0;
  height: 1px;
  background: linear-gradient(90deg, transparent 0%, var(--accent) 50%, transparent 100%);
  opacity: 0.4;
}


.scan-panel,
.activity-panel {
  position: fixed;
  inset: 0;
  z-index: 20;
  display: grid;
  place-items: center;
  background: rgba(0, 0, 0, 0.82);
  backdrop-filter: blur(4px);
}
.scan-panel[hidden],
.activity-panel[hidden] { display: none; }
.scan-shell {
  width: min(560px, calc(100vw - 32px));
  max-height: min(80vh, 640px);
  overflow: auto;
  border: 1px solid var(--line);
  background: var(--surface);
  padding: 18px;
  display: grid;
  gap: 14px;
}
.scan-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}
.scan-head h3 { margin: 4px 0 0; font-size: 1.2rem; font-weight: 800; }
.scan-eyebrow {
  font-family: var(--mono);
  font-size: 0.66rem;
  letter-spacing: 0.22em;
  text-transform: uppercase;
  color: var(--accent);
}
.scan-current, .scan-history, .activity-body { display: grid; gap: 8px; }
.scan-history-head {
  font-family: var(--mono);
  font-size: 0.66rem;
  letter-spacing: 0.18em;
  text-transform: uppercase;
  color: var(--muted);
  margin-top: 4px;
}
.scan-job-row {
  border: 1px solid var(--line);
  background: #000;
  padding: 10px 12px;
  display: grid;
  gap: 4px;
}
.scan-job-row.active { border-color: color-mix(in srgb, var(--accent) 60%, var(--line)); }
.scan-job-row .name {
  font-family: var(--mono);
  font-size: 0.68rem;
  letter-spacing: 0.14em;
  text-transform: uppercase;
  color: var(--text);
}
.scan-job-row .meta {
  font-family: var(--mono);
  font-size: 0.72rem;
  color: var(--text-dim);
}
.activity-stat-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px;
}
.activity-stat {
  border: 1px solid var(--line);
  background: #000;
  padding: 10px 12px;
  display: grid;
  gap: 4px;
}
.activity-stat .label {
  font-family: var(--mono);
  font-size: 0.62rem;
  letter-spacing: 0.16em;
  text-transform: uppercase;
  color: var(--muted);
}
.activity-stat .value {
  font-family: var(--mono);
  font-size: 0.82rem;
  color: var(--text);
}
@keyframes scanBadgeSpin {
  to { transform: rotate(360deg); }
}

.identify-modal {
  position: fixed;
  inset: 0;
  z-index: 20;
  background: rgba(0, 0, 0, 0.78);
  display: flex;
  align-items: flex-start;
  justify-content: center;
  padding: 8vh 24px;
}
.identify-modal[hidden] { display: none; }
.identify-shell {
  width: min(820px, 100%);
  max-height: 80vh;
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding: 22px;
  background: var(--surface);
  border: 1px solid var(--accent);
  box-shadow: 0 0 30px -8px var(--accent);
  overflow: hidden;
}
.identify-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}
.identify-head h3 {
  margin: 4px 0 0;
  font-family: var(--sans);
  font-size: 1.2rem;
  letter-spacing: -0.01em;
}
.identify-eyebrow {
  font-family: var(--mono);
  font-size: 0.7rem;
  letter-spacing: 0.18em;
  color: var(--accent);
  text-transform: uppercase;
}
.identify-form {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: end;
  gap: 12px;
}
.identify-form .field { margin: 0; }
.identify-results {
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.identify-result {
  display: grid;
  grid-template-columns: 56px 1fr auto;
  gap: 12px;
  align-items: center;
  padding: 10px 12px;
  border: 1px solid var(--line);
  background: rgba(255,255,255,0.02);
}
.identify-result .cover {
  width: 56px;
  height: 56px;
  background-size: cover;
  background-position: center;
  background-color: #0a0a0a;
  border: 1px solid var(--line);
}
.identify-result .title { font-weight: 700; }
.identify-result .meta {
  color: var(--text-dim);
  font-family: var(--mono);
  font-size: 0.74rem;
  letter-spacing: 0.08em;
  text-transform: uppercase;
}

.player-meta {
  min-width: 0;
  display: grid;
  gap: 3px;
}
.player-label {
  font-family: var(--mono);
  font-size: 0.62rem;
  letter-spacing: 0.22em;
  color: var(--accent);
  text-transform: uppercase;
}
.player-title {
  font-weight: 800;
  letter-spacing: -0.01em;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.player-sub {
  font-family: var(--mono);
  font-size: 0.7rem;
  color: var(--text-dim);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.player-transport {
  display: grid;
  grid-template-columns: auto 1fr auto;
  gap: 14px;
  align-items: center;
}
.player-transport audio { display: none; }
.player-btn {
  appearance: none;
  background: transparent;
  border: 1px solid var(--line);
  color: var(--text);
  width: 42px;
  height: 42px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  padding: 0;
  transition: border-color 90ms ease-out, color 90ms ease-out, background 90ms ease-out, box-shadow 120ms ease-out;
}
.player-btn:hover {
  border-color: var(--accent);
  color: var(--accent);
  box-shadow: 0 0 18px -8px var(--accent);
}
.player-btn.ghost { color: var(--text-dim); }
.player-btn.ghost:hover { color: var(--accent); }
.player-glyph {
  font-family: var(--mono);
  font-size: 0.95rem;
  letter-spacing: -0.05em;
  line-height: 1;
}
.player-track {
  display: grid;
  gap: 6px;
  align-items: center;
}
.player-seek {
  position: relative;
  height: 18px;
  cursor: pointer;
  display: flex;
  align-items: center;
  outline: none;
}
.player-seek::before {
  content: "";
  position: absolute;
  left: 0;
  right: 0;
  top: 50%;
  height: 2px;
  margin-top: -1px;
  background: var(--ghost);
  pointer-events: none;
}
.player-seek-bar {
  position: absolute;
  left: 0;
  top: 50%;
  height: 2px;
  margin-top: -1px;
  width: 0%;
  background: var(--accent);
  box-shadow: 0 0 8px -1px var(--accent);
  pointer-events: none;
  transition: width 90ms linear;
}
.player-seek-head {
  position: absolute;
  top: 50%;
  width: 8px;
  height: 8px;
  margin-top: -4px;
  margin-left: -4px;
  left: 0%;
  background: var(--accent);
  box-shadow: 0 0 10px var(--accent);
  pointer-events: none;
  opacity: 0;
  transition: opacity 120ms ease-out, left 90ms linear;
}
.player-seek:hover .player-seek-head,
.player-seek:focus-visible .player-seek-head { opacity: 1; }
.player-seek:focus-visible::before {
  background: color-mix(in srgb, var(--accent) 30%, var(--ghost));
}
.player-times {
  display: flex;
  gap: 6px;
  font-family: var(--mono);
  font-size: 0.68rem;
  letter-spacing: 0.1em;
  color: var(--text-dim);
}
.player-times-sep { color: var(--muted); }

@media (max-width: 880px) {
  .app-bar {
    grid-template-columns: 1fr;
    align-items: stretch;
  }
  .app-nav {
    justify-content: flex-start;
    overflow-x: auto;
    padding-bottom: 2px;
  }
  .app-bar-right {
    justify-content: space-between;
  }
  .view-head {
    display: grid;
    gap: 12px;
  }
  .view-head .view-actions {
    justify-content: flex-start;
  }
  .radio-card {
    grid-template-columns: 72px 1fr;
  }
  .radio-card .actions,
  .radio-admin-actions {
    align-items: flex-start;
    flex-direction: row;
    flex-wrap: wrap;
  }
  .player-dock {
    grid-template-columns: 1fr;
    padding: 12px 16px;
  }
}
`

const appJS = `
(function () {
  const tokenKey = "samo-token";
  const legacyLastFMPendingKey = "samo-lastfm-token";

  function loginRedirect() {
    // Preserve the deep-link the user was trying to reach so login can
    // bounce them back to /app#audiobooks or wherever they came from.
    const next = encodeURIComponent(window.location.pathname + window.location.hash);
    window.location.href = "/login?next=" + next;
  }

  let token = localStorage.getItem(tokenKey) || "";
  if (!token) { loginRedirect(); return; }

  const main = document.getElementById("appMain");
  const nav = document.getElementById("appNav");
  const playerDock = document.getElementById("playerDock");
  const audio = document.getElementById("audioPlayer");
  const playerTitle = document.getElementById("playerTitle");
  const playerSub = document.getElementById("playerSub");
  const playerToggle = document.getElementById("playerToggle");
  const playerGlyph = document.getElementById("playerGlyph");
  const playerStop = document.getElementById("playerStop");
  const playerSeek = document.getElementById("playerSeek");
  const playerSeekBar = document.getElementById("playerSeekBar");
  const playerSeekHead = document.getElementById("playerSeekHead");
  const playerTimeEl = document.getElementById("playerTime");
  const playerDurationEl = document.getElementById("playerDuration");

  const GLYPH_PAUSE = "▌▌";
  const GLYPH_PLAY = "▶";

  let currentUser = null;

  function isAdmin() {
    return currentUser && currentUser.role === "admin";
  }

  function isLibraryFolderPodcast(item) {
    const path = String((item && item.path) || "");
    return Boolean(path && !path.startsWith("samo://"));
  }

  function podcastHasLinkedFeed(item) {
    return Boolean(item && item.rssFeed && item.rssFeed.id);
  }

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
  // Default RADIO sub-mode is CHANNELS — personal programmed streams.
  // INTERNET is for live stream bookmarks.
  let radioMode = "channels";
  let activeChannelID = "";
  let searchQuery = "";
  let playerTarget = null;
  let lastProgressSync = 0;

  function lastFMPendingStorageKey() {
    const userID = currentUser && currentUser.id ? currentUser.id : "anonymous";
    return legacyLastFMPendingKey + ":" + userID;
  }

  async function api(path, options) {
    options = options || {};
    options.headers = options.headers || {};
    if (token) options.headers["Authorization"] = "Bearer " + token;
    if (options.body && typeof options.body !== "string") {
      options.headers["Content-Type"] = "application/json";
      options.body = JSON.stringify(options.body);
    }
    const res = await fetch(path, options);
    if (res.status === 401) {
      localStorage.removeItem(tokenKey);
      loginRedirect();
      throw new Error("unauthorized");
    }
    if (res.status === 204) return null;
    const body = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(body.error || ("request failed: " + res.status));
    return body;
  }

  function escapeHTML(value) {
    return String(value == null ? "" : value).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", "\"": "&quot;" }[c]));
  }

  function attr(value) {
    return escapeHTML(value).replace(/'/g, "&#39;");
  }

  function formatDuration(seconds) {
    seconds = Math.max(0, Math.floor(seconds || 0));
    const h = Math.floor(seconds / 3600);
    const m = Math.floor((seconds % 3600) / 60);
    const s = seconds % 60;
    if (h > 0) return h + "H " + m + "M";
    return m + ":" + String(s).padStart(2, "0");
  }

  function formatDate(value) {
    if (!value) return "NEVER";
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return "UNKNOWN";
    if (date.getFullYear() < 2000) return "NEVER";
    return date.toLocaleString([], { year: "numeric", month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" }).toUpperCase();
  }

  function setStatus(text) {
    const el = document.getElementById("barStatusText");
    if (el) el.textContent = text;
  }

  function renderLoading() {
    main.innerHTML = "<div class=\"boot-line\">// loading...</div>";
  }

  function renderError(message) {
    main.innerHTML = "<div class=\"empty-state\">// " + escapeHTML(message) + "</div>";
  }

  function setMessage(id, message, bad) {
    const el = document.getElementById(id);
    if (!el) return;
    el.className = "status-line " + (bad ? "bad" : "good");
    el.textContent = "// " + message;
    el.hidden = false;
  }

  function statCard(label, value, accent) {
    return '<div class="stat-card"><span class="label">' + label + '</span>' +
      '<span class="value' + (accent ? " accent" : "") + '">' + (value || 0) + '</span></div>';
  }

  /* Stream tokens are short-lived credentials minted by the server. They
   * keep the bearer out of <audio src> / <img src> URLs (which can't carry
   * custom headers and would otherwise leak via Referer / server log). */
  let streamToken = "";
  let streamTokenExpiresAt = 0;
  let streamTokenPromise = null;

  async function refreshStreamToken() {
    const result = await api("/api/v1/auth/stream-token", { method: "POST" });
    streamToken = result.token || "";
    streamTokenExpiresAt = new Date(result.expiresAt || 0).getTime();
    return streamToken;
  }

  async function ensureStreamToken() {
    // 60s safety margin so requests in flight don't race expiry.
    if (streamToken && Date.now() < streamTokenExpiresAt - 60000) return streamToken;
    if (!streamTokenPromise) {
      streamTokenPromise = refreshStreamToken().finally(() => { streamTokenPromise = null; });
    }
    return streamTokenPromise;
  }

  function streamQuery() {
    return streamToken ? "?stream_token=" + encodeURIComponent(streamToken) : "";
  }

  function musicStreamURL(id) {
    return "/api/v1/music/tracks/" + encodeURIComponent(id) + "/stream" + streamQuery();
  }

  function musicCoverURL(id) {
    return "/api/v1/music/albums/" + encodeURIComponent(id) + "/cover" + streamQuery();
  }

  function musicPlaylistCoverURL(id, bust) {
    let url = "/api/v1/music/playlists/" + encodeURIComponent(id) + "/cover" + streamQuery();
    if (bust) url += (url.includes("?") ? "&" : "?") + "_=" + bust;
    return url;
  }

  function musicPaginationFooter(loaded, total) {
    if (!total || loaded >= total) return "";
    return '<div class="section-row"><button class="btn ghost btn-small" data-action="music-load-more">LOAD MORE (' + loaded + " / " + total + ')</button></div>';
  }

  function audiobookStreamURL(id) {
    return "/api/v1/audiobooks/" + encodeURIComponent(id) + "/stream" + streamQuery();
  }

  function audiobookStreamURLAt(id, atSeconds) {
    const base = audiobookStreamURL(id);
    const at = Math.max(0, Math.floor(atSeconds || 0));
    if (at <= 0) return base;
    return base + (base.includes("?") ? "&" : "?") + "at=" + at;
  }

  function audiobookCoverURL(id) {
    return "/api/v1/audiobooks/" + encodeURIComponent(id) + "/cover" + streamQuery();
  }

  function podcastCoverURL(id, bust) {
    let url = "/api/v1/podcasts/shows/" + encodeURIComponent(id) + "/cover" + streamQuery();
    if (bust) url += (url.includes("?") ? "&" : "?") + "_=" + bust;
    return url;
  }

  function radioCoverURL(station) {
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

  function podcastEpisodeStreamURL(id) {
    return "/api/v1/podcasts/episodes/" + encodeURIComponent(id) + "/stream" + streamQuery();
  }

  function podcastEpisodeStreamURLAt(id, atSeconds) {
    const base = podcastEpisodeStreamURL(id);
    const at = Math.max(0, Math.floor(atSeconds || 0));
    if (at <= 0) return base;
    return base + (base.includes("?") ? "&" : "?") + "offsetSeconds=" + at;
  }

  function libraryKindLabel(lib) {
    if (!lib) return "UNKNOWN";
    switch (lib.kind) {
      case "mixed":     return "MIXED";
      case "music":     return "MUSIC";
      case "audiobook": return "AUDIOBOOKS";
      case "podcast":   return "PODCASTS";
    }
    return String(lib.kind || "unknown").toUpperCase();
  }

  function librarySupportsRepair(lib) {
    return lib && (lib.kind === "music" || lib.kind === "mixed");
  }

  function libraryScanActionsHTML(lib, btnClass) {
    btnClass = btnClass || "btn ghost btn-mini";
    if (!lib || !lib.id) return "";
    let html =
      '<button class="' + btnClass + '" data-action="scan-library" data-id="' + attr(lib.id) + '" title="Quick scan — new and changed files only">SCAN</button>' +
      '<button class="' + btnClass + '" data-action="scan-library-full" data-id="' + attr(lib.id) + '" title="Full scan — re-probe every file in this library">FULL</button>';
    if (librarySupportsRepair(lib)) {
      html += '<button class="' + btnClass + '" data-action="repair-library" data-id="' + attr(lib.id) + '" title="Re-index metadata and covers (music only)">REPAIR</button>';
    }
    return html;
  }

  function globalScanActionsHTML(options) {
    options = options || {};
    const btnClass = options.btnClass || "btn ghost btn-small";
    const primaryClass = options.primaryClass || "btn primary btn-small";
    let html =
      '<button class="' + btnClass + '" data-action="scan-quick-all" title="Quick rescan of every attached library — new and changed files only">SCAN ALL</button>' +
      '<button class="' + primaryClass + '" data-action="scan-all" title="Full scan — re-probe every file in every library">FULL SCAN</button>' +
      '<button class="' + btnClass + '" data-action="repair-all" title="Re-index music metadata and covers without re-reading every file">REPAIR INDEX</button>';
    if (options.includeArtistPhotos !== false) {
      html += '<button class="' + btnClass + '" data-action="fetch-artist-images" title="Download missing artist photos from Deezer into the local cover cache">FETCH ARTIST PHOTOS</button>';
    }
    return html;
  }

  function libraryKindScanActionsHTML(kind) {
    const btnClass = "btn ghost btn-small";
    const folder = kind === "audiobook" ? "Audiobooks" : "Podcasts";
    return '<button class="' + btnClass + '" data-action="scan-library-kind" data-kind="' + attr(kind) + '" data-mode="quick" title="Quick scan of the ' + folder + ' folder — new/changed files only">SCAN</button>' +
      '<button class="' + btnClass + '" data-action="scan-library-kind" data-kind="' + attr(kind) + '" data-mode="full" title="Full scan of the ' + folder + ' folder — re-probe every file">FULL</button>';
  }

  function audiobookTitle(item) {
    if (!item) return "Untitled";
    if (item.book && item.book.title) return item.book.title;
    return item.title || "Untitled";
  }

  function audiobookSub(item) {
    if (!item) return "";
    if (item.book && item.book.authors && item.book.authors.length > 0) {
      return item.book.authors.map((author) => author.name).join(", ");
    }
    return "AUDIOBOOK";
  }

  function podcastTitle(item) {
    if (!item) return "Untitled";
    if (item.podcast && item.podcast.title) return item.podcast.title;
    return item.title || "Untitled";
  }

  function podcastSub(item) {
    if (!item) return "";
    if (item.podcast && item.podcast.author) return item.podcast.author;
    return "PODCAST";
  }

  function progressBar(state, duration) {
    const progress = state && state.progressSeconds ? state.progressSeconds : 0;
    const pct = duration > 0 ? Math.min(100, Math.round((progress / duration) * 100)) : 0;
    return '<div class="progress-track"><div class="bar" style="width:' + pct + '%"></div></div>';
  }

  function browseAlbums(data) {
    if (!data) return [];
    if (Array.isArray(data.items)) return data.items;
    return data.albums || [];
  }

  function browseTracks(data) {
    if (!data) return [];
    if (Array.isArray(data.items)) return data.items;
    return data.tracks || [];
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

  function tagsLine(tags) {
    if (!tags || tags.length === 0) return "";
    return '<div class="tag-line">' + tags.slice(0, 8).map((tag) => '<span class="meta-chip">' + escapeHTML(tag) + '</span>').join("") + '</div>';
  }

  function splitTags(raw) {
    return String(raw || "").split(",").map((tag) => tag.trim()).filter(Boolean);
  }

  function asBool(raw) {
    return raw === "true";
  }

  /* ---- Header scan + activity -------------------------------------------
   * REFRESH in the utility bar shows live scan progress and opens the
   * detail panel on click. Resumes in-flight jobs on page load. */
  const refreshBtn = document.getElementById("refreshBtn");
  const refreshSub = document.getElementById("refreshSub");
  const refreshRing = document.getElementById("refreshRing");
  const nowPlayingBtn = document.getElementById("nowPlayingBtn");
  const nowPlayingSub = document.getElementById("nowPlayingSub");
  const activityPanel = document.getElementById("activityPanel");
  const activityBody = document.getElementById("activityBody");
  const scanPanel = document.getElementById("scanPanel");
  const scanPanelCurrent = document.getElementById("scanPanelCurrent");
  const scanPanelHistory = document.getElementById("scanPanelHistory");
  const scanCancelBtn = document.getElementById("scanCancelBtn");
  let scanPollHandle = null;
  let scanWatchJobID = "";
  let artistImagePollHandle = null;
  let scanLastFilesSeen = 0;
  let scanLastLabel = "SCAN";
  let scanLastText = "starting…";
  let scanLastState = "idle";
  let libraryNameById = {};

  function rememberLibraries(libs) {
    (libs || []).forEach((lib) => {
      if (lib && lib.id) libraryNameById[lib.id] = lib.name || lib.id;
    });
  }

  function scanPruneSummary(job) {
    if (!job) return "";
    const parts = [];
    if (job.filesMarked) parts.push(job.filesMarked + " missing files");
    if (job.filesPruned) parts.push(job.filesPruned + " stale files");
    if (job.itemsPruned) parts.push(job.itemsPruned + " orphan items");
    return parts.length ? " · " + parts.join(" · ") : "";
  }

  function scanJobScopeLabel(job) {
    if (!job) return "scan";
    const mode = job.scanMode ? String(job.scanMode).toUpperCase() : "";
    if (job.scope === "subpaths") {
      const name = libraryNameById[job.libraryId] || job.libraryId || "library";
      return (mode ? mode + " · " : "") + "incremental · " + name;
    }
    if (job.scope === "library") {
      const name = libraryNameById[job.libraryId] || job.libraryId || "library";
      return (mode ? mode + " · " : "") + name;
    }
    return (mode ? mode + " · " : "") + "all libraries";
  }

  async function loadLibraries() {
    const data = await api("/api/v1/libraries").catch(() => ({ items: [] }));
    rememberLibraries((data && data.items) || []);
    return (data && data.items) || [];
  }

  async function ensureScanAvailable() {
    if (scanLastState === "running") {
      await openScanPanel();
      return false;
    }
    return true;
  }

  async function triggerGlobalScan(mode) {
    if (!(await ensureScanAvailable())) return;
    await triggerScan(() => api("/api/v1/scan", { method: "POST", body: { mode: mode } }));
  }

  async function triggerLibraryScan(libraryID, mode, libraryName) {
    libraryID = String(libraryID || "").trim();
    mode = String(mode || "quick").trim() || "quick";
    if (!libraryID) {
      updateRefreshUI("error", "FAILED", "library not found");
      return;
    }
    if (!(await ensureScanAvailable())) return;
    const label = libraryName || libraryNameById[libraryID] || "library";
    await triggerScan(async () => {
      const result = await api("/api/v1/libraries/" + encodeURIComponent(libraryID) + "/scan", {
        method: "POST",
        body: { mode: mode },
      });
      const job = result && result.job;
      if (job && job.scope === "library" && job.libraryId && job.libraryId !== libraryID) {
        throw new Error("server attached scan to a different library");
      }
      scanLastLabel = label.toUpperCase();
      return result;
    });
  }

  async function triggerLibraryRepair(libraryID, libraryName) {
    libraryID = String(libraryID || "").trim();
    if (!libraryID) {
      updateRefreshUI("error", "FAILED", "library not found");
      return;
    }
    if (!(await ensureScanAvailable())) return;
    const label = libraryName || libraryNameById[libraryID] || "library";
    await triggerScan(async () => {
      scanLastLabel = label.toUpperCase();
      return api("/api/v1/libraries/" + encodeURIComponent(libraryID) + "/scan", { method: "POST", body: { mode: "repair" } });
    });
  }

  function updateRefreshUI(state, label, text) {
    scanLastState = state;
    scanLastLabel = label || "SCAN";
    scanLastText = text || "";
    if (!refreshBtn || !refreshSub) return;
    refreshBtn.classList.remove("running", "ok", "error");
    if (state === "running") refreshBtn.classList.add("running");
    if (state === "ok") refreshBtn.classList.add("ok");
    if (state === "error") refreshBtn.classList.add("error");
    if (refreshRing) refreshRing.hidden = state !== "running";
    if (scanCancelBtn) scanCancelBtn.hidden = state !== "running" || !scanWatchJobID || scanLastLabel === "CANCEL";
    if (state === "running") refreshSub.textContent = text || "SCANNING";
    else if (state === "ok") refreshSub.textContent = text || "DONE";
    else if (state === "error") refreshSub.textContent = text || "FAILED";
    else refreshSub.textContent = "READY";
    refreshScanPanelCurrent();
  }

  function closeScanPanel() { if (scanPanel) scanPanel.hidden = true; }
  function closeActivityPanel() { if (activityPanel) activityPanel.hidden = true; }

  function renderScanJobRow(job, highlight) {
    const seen = job.filesSeen || 0;
    const total = job.filesTotal || 0;
    let filesText = total > 0 ? (seen + " / " + total + " files") : (seen + " files indexed");
    if (job.currentPath && (job.status === "running" || job.status === "pending")) {
      filesText += " · " + job.currentPath;
    } else if (seen === 0 && (job.status === "running" || job.status === "pending")) {
      filesText = "enumerating library…";
    }
    const scope = scanJobScopeLabel(job);
    return '<div class="scan-job-row' + (highlight ? " active" : "") + '">' +
      '<div class="name">' + escapeHTML(String(job.status || "unknown").toUpperCase()) + " · " + escapeHTML(scope.toUpperCase()) + '</div>' +
      '<div class="meta">' + escapeHTML(filesText) +
        " · started " + formatDate(job.startedAt) +
        escapeHTML(scanPruneSummary(job)) +
        (job.error ? " · " + escapeHTML(job.error) : "") +
      '</div></div>';
  }

  function refreshScanPanelCurrent() {
    if (!scanPanel || scanPanel.hidden || !scanPanelCurrent) return;
    if (scanWatchJobID && scanLastState === "running") {
      const label = scanLastLabel === "CANCEL" ? "CANCELLING" : "RUNNING";
      scanPanelCurrent.innerHTML = '<div class="scan-job-row active"><div class="name">' + label + ' · ' + escapeHTML(scanLastLabel === "CANCEL" ? "SCAN" : scanLastLabel) + '</div><div class="meta">' + escapeHTML(scanLastText) + '</div></div>';
    }
  }

  async function openScanPanel() {
    if (!scanPanel) return;
    closeActivityPanel();
    scanPanel.hidden = false;
    if (scanPanelCurrent) scanPanelCurrent.innerHTML = '<div class="boot-line">// loading...</div>';
    if (scanPanelHistory) scanPanelHistory.innerHTML = "";
    try {
      const [jobs, libraries] = await Promise.all([
        api("/api/v1/scan/jobs?limit=12"),
        api("/api/v1/libraries").catch(() => ({ items: [] })),
      ]);
      rememberLibraries((libraries && libraries.items) || []);
      const items = (jobs && jobs.items) || [];
      let active = scanWatchJobID ? items.find((job) => job.id === scanWatchJobID) : null;
      if (!active) active = items.find((job) => job.status === "running" || job.status === "pending") || null;
      if (scanPanelCurrent) {
        if (active) scanPanelCurrent.innerHTML = renderScanJobRow(active, true);
        else if (scanLastState === "running") {
          scanPanelCurrent.innerHTML = '<div class="scan-job-row active"><div class="name">RUNNING</div><div class="meta">' + escapeHTML(scanLastText) + '</div></div>';
        } else {
          scanPanelCurrent.innerHTML = '<div class="empty-state">// no scan running</div>';
        }
      }
      if (scanCancelBtn) {
        scanCancelBtn.hidden = !(active && (active.status === "running" || active.status === "pending"));
      }
      if (scanPanelHistory) {
        if (items.length) {
          scanPanelHistory.innerHTML = '<div class="scan-history-head">// recent jobs</div>' + items.map((job) => renderScanJobRow(job, active && job.id === active.id)).join("");
        } else {
          scanPanelHistory.innerHTML = '<div class="empty-state">// no scan history yet</div>';
        }
      }
    } catch (err) {
      if (scanPanelCurrent) scanPanelCurrent.innerHTML = '<div class="empty-state">// ' + escapeHTML(err.message) + '</div>';
    }
  }

  function formatUptime(seconds) {
    const s = Math.max(0, Number(seconds) || 0);
    const days = Math.floor(s / 86400);
    const hrs = Math.floor((s % 86400) / 3600);
    const mins = Math.floor((s % 3600) / 60);
    if (days > 0) return days + "d " + hrs + "h";
    if (hrs > 0) return hrs + "h " + mins + "m";
    return mins + "m";
  }

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

  function stopScanPolling() {
    if (scanPollHandle) { clearInterval(scanPollHandle); scanPollHandle = null; }
  }

  function applyScanJobStatus(job, jobID) {
    scanLastFilesSeen = job.filesSeen || 0;
    const total = job.filesTotal || 0;
    if (job.status === "running" || job.status === "pending") {
      if (scanLastLabel !== "CANCEL") {
        let progress = total > 0 ? scanLastFilesSeen + " of " + total + " files" : scanLastFilesSeen + " files";
        if (job.currentPath) {
          progress += " · " + job.currentPath;
        } else if (scanLastFilesSeen === 0) {
          progress = "enumerating library…";
        }
        updateRefreshUI("running", "SCAN", scanJobScopeLabel(job) + " · " + progress);
      }
      return false;
    }
    if (scanWatchJobID === jobID) {
      scanWatchJobID = "";
    }
    stopScanPolling();
    if (job.status === "completed") {
      const parts = [scanLastFilesSeen + " files"];
      if (job.filesPruned) parts.push(job.filesPruned + " stale files");
      if (job.itemsPruned) parts.push(job.itemsPruned + " orphan items");
      updateRefreshUI("ok", "DONE", parts.join(" · "));
      setTimeout(() => updateRefreshUI("idle", "SCAN", "READY"), 5000);
    } else if (job.status === "cancelled") {
      const parts = [(scanLastFilesSeen || job.filesSeen || 0) + " files indexed"];
      updateRefreshUI("idle", "CANCELLED", parts.join(" · "));
      setTimeout(() => updateRefreshUI("idle", "SCAN", "READY"), 5000);
    } else {
      updateRefreshUI("error", "FAILED", job.error || "scan failed");
    }
    return true;
  }

  async function watchScanJob(jobID) {
    if (!jobID) return;
    stopScanPolling();
    scanWatchJobID = jobID;
    scanLastFilesSeen = 0;
    updateRefreshUI("running", "SCAN", "starting...");
    const tick = async () => {
      if (scanWatchJobID !== jobID) return;
      try {
        const job = await api("/api/v1/scan/jobs/" + encodeURIComponent(jobID));
        if (applyScanJobStatus(job, jobID) && activeTab && views[activeTab]) {
          await views[activeTab]();
        }
      } catch (err) {
        scanWatchJobID = "";
        stopScanPolling();
        updateRefreshUI("error", "FAILED", err.message || "polling failed");
      }
    };
    tick();
    scanPollHandle = setInterval(tick, 1500);
  }

  async function resumeActiveScan() {
    try {
      const jobs = await api("/api/v1/scan/jobs?limit=6");
      const items = (jobs && jobs.items) || [];
      const active = items.find((job) => job.status === "running" || job.status === "pending");
      if (active && active.id) watchScanJob(active.id);
    } catch (_) {}
  }

  function stopArtistImagePolling() {
    if (artistImagePollHandle) { clearInterval(artistImagePollHandle); artistImagePollHandle = null; }
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

  async function watchArtistImageBackfill() {
    if (artistImagePollHandle) return;
    const tick = async () => {
      try {
        const data = await api("/api/v1/music/artists/images/backfill");
        const job = data && data.job;
        updateArtistImageJobPanel(job);
        if (!job || (job.status !== "running" && job.status !== "pending")) {
          stopArtistImagePolling();
        }
      } catch (_) {
        stopArtistImagePolling();
      }
    };
    await tick();
    artistImagePollHandle = setInterval(tick, 2000);
  }

  async function resumeArtistImageBackfill() {
    try {
      const data = await api("/api/v1/music/artists/images/backfill");
      const job = data && data.job;
      if (job && (job.status === "running" || job.status === "pending")) watchArtistImageBackfill();
    } catch (_) {}
  }

  async function cancelActiveScan() {
    const jobID = scanWatchJobID;
    if (!jobID) return;
    if (!confirm("Cancel the running scan? Files already indexed stay in your library.")) return;
    updateRefreshUI("running", "CANCEL", "cancelling…");
    if (scanCancelBtn) scanCancelBtn.hidden = true;
    try {
      await api("/api/v1/scan/jobs/" + encodeURIComponent(jobID) + "/cancel", { method: "POST" });
    } catch (err) {
      updateRefreshUI("error", "FAILED", err.message || "cancel failed");
      if (scanCancelBtn) scanCancelBtn.hidden = false;
      return;
    }
    const deadline = Date.now() + 60000;
    while (Date.now() < deadline && scanWatchJobID === jobID) {
      try {
        const job = await api("/api/v1/scan/jobs/" + encodeURIComponent(jobID));
        if (applyScanJobStatus(job, jobID)) {
          if (activeTab && views[activeTab]) await views[activeTab]();
          if (scanPanel && !scanPanel.hidden) await openScanPanel();
          return;
        }
      } catch (err) {
        scanWatchJobID = "";
        stopScanPolling();
        updateRefreshUI("error", "FAILED", err.message || "polling failed");
        return;
      }
      await new Promise((resolve) => setTimeout(resolve, 200));
    }
  }

  async function triggerScan(kickoff) {
    updateRefreshUI("running", "SCAN", "starting...");
    try {
      const result = await kickoff();
      const jobID = result && result.job && result.job.id;
      if (!jobID) { updateRefreshUI("error", "FAILED", "no job id returned"); return; }
      watchScanJob(jobID);
    } catch (err) {
      const message = err.message || "scan failed";
      if (message.toLowerCase().includes("already in progress")) {
        updateRefreshUI("error", "BUSY", "another scan running");
        await openScanPanel();
        return;
      }
      updateRefreshUI("error", "FAILED", message);
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

  function playlistCoverBlock(id, coverURL, canEdit) {
    const style = coverURL ? 'style="background-image:url(&quot;' + attr(coverURL) + '&quot;)"' : 'style="background-color:#0a0a0a"';
    if (!canEdit) {
      return '<div class="detail-cover" ' + style + '></div>';
    }
    return '<label class="detail-cover radio-cover-upload" ' + style + ' title="Upload custom artwork">' +
      '<input type="file" class="radio-cover-input" accept="image/*" data-playlist-id="' + attr(id) + '">' +
      '<span class="radio-cover-hint">UPLOAD</span>' +
    '</label>';
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

  /* ---- Identify modal ---------------------------------------------------
   * Fronts /api/v1/metadata/search (OpenLibrary, Google Books, Apple
   * Podcasts) and /api/v1/metadata/apply. The same modal serves both
   * audiobooks (kind=audiobook → ApplyTargetAudiobook) and podcast shows
   * (kind=podcast → ApplyTargetPodcast). Music tracks/albums could be
   * wired in by adding a third button, but the user's complaint was
   * specifically about audiobooks/podcasts. */
  const identifyModal = document.getElementById("identifyModal");
  const identifyTitle = document.getElementById("identifyTitle");
  const identifyQuery = document.getElementById("identifyQuery");
  const identifyResults = document.getElementById("identifyResults");
  const identifyForm = document.getElementById("identifyForm");
  let identifyContext = null;
  let identifyCandidates = [];

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

  async function openIdentifyModal(kind, id, title, author) {
    identifyContext = { kind, id };
    identifyCandidates = [];
    identifyTitle.textContent = title || "Identify";
    identifyQuery.value = [title, author].filter(Boolean).join(" ");
    identifyResults.innerHTML = '<div class="boot-line">// type a query and search</div>';
    identifyModal.hidden = false;
    identifyQuery.focus();
    if (identifyQuery.value.trim()) await runIdentifySearch();
  }

  function closeIdentifyModal() {
    identifyModal.hidden = true;
    identifyContext = null;
    identifyCandidates = [];
    identifyResults.innerHTML = "";
  }

  async function runIdentifySearch() {
    if (!identifyContext) return;
    const kind = identifyContext.kind === "podcast" ? "podcast" : "audiobook";
    const q = identifyQuery.value.trim();
    if (!q) return;
    identifyResults.innerHTML = '<div class="boot-line">// searching providers...</div>';
    try {
      const response = await api("/api/v1/metadata/search?kind=" + encodeURIComponent(kind) + "&q=" + encodeURIComponent(q) + "&limit=10");
      const candidates = (response && response.results) || [];
      const providers = (response && response.providers) || [];
      const providerErrors = (response && response.providerErrors) || [];
      if (providers.length === 0) {
        identifyCandidates = [];
        identifyResults.innerHTML = '<div class="empty-state">// metadata providers are disabled · enable SAMO_METADATA_PROVIDERS=openlibrary,googlebooks,itunes,musicbrainz or use the default server config</div>';
        return;
      }
      if (candidates.length === 0) {
        identifyCandidates = [];
        if (providerErrors.length > 0) {
          identifyResults.innerHTML = '<div class="empty-state">// provider errors: ' + escapeHTML(providerErrors.map((item) => (item.provider || "provider") + ": " + (item.error || "failed")).join(" · ")) + '</div>';
        } else {
          identifyResults.innerHTML = '<div class="empty-state">// no matches across providers</div>';
        }
        return;
      }
      identifyCandidates = candidates;
      identifyResults.innerHTML = candidates.map((candidate, idx) => identifyResultRow(candidate, idx)).join("");
    } catch (err) {
      identifyCandidates = [];
      identifyResults.innerHTML = '<div class="empty-state">// ' + escapeHTML(err.message || "search failed") + '</div>';
    }
  }

  function identifyResultRow(candidate, idx) {
    const cover = (candidate.cover && candidate.cover.url) || "";
    const coverStyle = cover ? 'style="background-image:url(&quot;' + attr(cover) + '&quot;)"' : "";
    const authors = (candidate.authors || []).map((person) => person.name).join(", ");
    const feedURL = identifyContext && identifyContext.kind === "podcast" ? candidateFeedURL(candidate) : "";
    const metaParts = [candidate.provider || "", authors, candidate.publishedYear || candidate.publishedDate || ""].filter(Boolean);
    if (feedURL) metaParts.push("RSS");
    return '<div class="identify-result">' +
      '<div class="cover" ' + coverStyle + '></div>' +
      '<div><div class="title">' + escapeHTML(candidate.title || "Untitled") + '</div>' +
        '<div class="meta">' + escapeHTML(metaParts.join(" · ")) + '</div>' +
        (feedURL ? '<div class="meta">' + escapeHTML(feedURL) + '</div>' : "") +
        '</div>' +
      '<button class="btn primary btn-mini" data-action="identify-apply" data-kind="' + attr(identifyContext ? identifyContext.kind : "audiobook") + '" data-id="' + attr(identifyContext ? identifyContext.id : "") + '" data-idx="' + idx + '">' +
        (feedURL && identifyContext && identifyContext.kind === "podcast" ? "APPLY + LINK RSS" : "APPLY") +
      '</button>' +
    '</div>';
  }

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
          } catch (err) {
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

  function candidateFeedURL(candidate) {
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

  async function withButton(button, busyText, fn) {
    const oldText = button ? button.textContent : "";
    if (button) {
      button.disabled = true;
      if (busyText) button.textContent = busyText;
    }
    try {
      return await fn();
    } finally {
      if (button) {
        button.disabled = false;
        button.textContent = oldText;
      }
    }
  }

  function patchPlayback(kind, id, patch) {
    return api("/api/v1/playback/" + encodeURIComponent(kind) + "/" + encodeURIComponent(id), {
      method: "PATCH",
      body: patch,
    });
  }

  function playbackGlobalSeconds(target) {
    if (!target) return 0;
    if (target.kind === "audiobook" || target.kind === "podcast-episode") {
      return (target.globalBase || 0) + Math.floor(audio.currentTime || 0);
    }
    return Math.floor(audio.currentTime || 0);
  }

  function flushPlaybackProgress() {
    if (!playerTarget) return;
    const now = playbackGlobalSeconds(playerTarget);
    if (now <= 0) return;
    if (playerTarget.kind === "audiobook") {
      patchPlayback("audiobook", playerTarget.id, { progressSeconds: now, touchLastPositionAt: true }).catch(() => {});
    } else if (playerTarget.kind === "music-track" || playerTarget.kind === "podcast-episode") {
      patchPlayback(playerTarget.kind, playerTarget.id, { progressSeconds: now, touchLastPositionAt: true }).catch(() => {});
    }
  }

  function playURL(url, title, subtitle, target) {
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
    } catch (_) {}
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

  function formatClock(seconds) {
    seconds = Math.max(0, Math.floor(seconds || 0));
    const h = Math.floor(seconds / 3600);
    const m = Math.floor((seconds % 3600) / 60);
    const s = seconds % 60;
    const mm = h > 0 ? String(m).padStart(2, "0") : String(m);
    const ss = String(s).padStart(2, "0");
    return h > 0 ? h + ":" + mm + ":" + ss : mm + ":" + ss;
  }

  function setPlayerGlyph(paused) {
    if (!playerGlyph) return;
    playerGlyph.textContent = paused ? GLYPH_PLAY : GLYPH_PAUSE;
    if (playerToggle) playerToggle.setAttribute("aria-label", paused ? "Play" : "Pause");
  }

  function applyStreamResumeSeek() {
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

  function refreshSeekUI() {
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

  function seekFromPointer(event) {
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
      playerTarget = null;
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
      try { playerSeek.releasePointerCapture(event.pointerId); } catch (_) {}
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
        lastProgressSync = 0;
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

  function recentlyAddedKindLabel(kind) {
    switch (kind) {
      case "music-album": return "ALBUM";
      case "audiobook": return "AUDIOBOOK";
      case "podcast": return "PODCAST";
      default: return "";
    }
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
          (playlistOwnedByCurrentUser(playlist) ? '<button class="btn danger btn-mini" data-action="delete-playlist" data-id="' + attr(playlist.id) + '" data-name="' + attr(playlist.name || "playlist") + '">DELETE</button>' : "") +
        '</div>' +
      '</div>';
    }).join("") + '</div>';
  }

  function playlistOwnedByCurrentUser(playlist) {
    if (!playlist) return false;
    if (!playlist.ownerId) return true;
    return currentUser && playlist.ownerId === currentUser.id;
  }

  function browseResultCount(data) {
    if (!data) return 0;
    return ((data.albums && data.albums.length) || 0) +
      ((data.tracks && data.tracks.length) || 0) +
      ((data.artists && data.artists.length) || 0) +
      ((data.playlists && data.playlists.length) || 0);
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
      const ownerActions = canEdit ?
        '<button class="btn primary btn-small" data-action="composer-toggle" data-composer="playlist-edit">EDIT PLAYLIST</button>' +
        '<button class="btn ghost btn-small" data-action="playlist-tracks-edit-toggle" data-id="' + attr(playlist.id) + '">' + (bulkMode ? "DONE EDITING TRACKS" : "EDIT TRACKS") + '</button>' +
        '<button class="btn ghost btn-small" data-action="toggle-playlist-public" data-id="' + attr(playlist.id) + '" data-public="' + (!playlist.public) + '">' + (playlist.public ? "MAKE PRIVATE" : "MAKE PUBLIC") + '</button>' +
        '<button class="btn danger btn-small" data-action="delete-playlist" data-id="' + attr(playlist.id) + '" data-name="' + attr(playlist.name || "playlist") + '">DELETE</button>' :
        "";
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
      // Don't blow away an open composer / form the user is filling in.
      // Reschedule so we try again on the next tick.
      if (hasOpenComposerOrModal()) { scheduleRadioPoll(); return; }
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
    '</div>';
  }

  async function renderRadio(isRefresh) {
    if (!isRefresh) renderLoading();
    try {
      if (activeChannelID) {
        await renderChannelDetail(activeChannelID, isRefresh);
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

  function channelCard(ch) {
    const codec = (ch.codec || "mp3").toUpperCase() + " · " + (ch.bitrateKbps || 192) + "K";
    return '<div class="channel-card">' +
      '<div class="channel-card-meta">' +
        '<div class="channel-eyebrow">// CHANNEL</div>' +
        '<h3 class="name">' + escapeHTML(ch.name) + '</h3>' +
        (ch.description ? '<p class="desc">' + escapeHTML(ch.description) + '</p>' : '') +
        '<div class="channel-spec">' + codec + ' · ' + (ch.enabled ? 'ENABLED' : 'DISABLED') + '</div>' +
      '</div>' +
      '<div class="channel-actions">' +
        '<button class="btn primary btn-small" data-action="channel-tune-in" data-id="' + attr(ch.id) + '" data-name="' + attr(ch.name) + '">TUNE IN</button>' +
        '<button class="btn ghost btn-small" data-action="channel-open" data-id="' + attr(ch.id) + '">PROGRAM &rarr;</button>' +
      '</div>' +
    '</div>';
  }

  async function renderChannelDetail(channelID, isRefresh) {
    const [ch, sources, schedule, now, recent, podcasts, internet] = await Promise.all([
      api("/api/v1/channels/" + encodeURIComponent(channelID)).catch(() => null),
      api("/api/v1/channels/" + encodeURIComponent(channelID) + "/sources").catch(() => ({ items: [] })),
      api("/api/v1/channels/" + encodeURIComponent(channelID) + "/schedule").catch(() => ({ items: [] })),
      api("/api/v1/channels/" + encodeURIComponent(channelID) + "/now").catch(() => null),
      api("/api/v1/channels/" + encodeURIComponent(channelID) + "/recent?limit=8").catch(() => ({ items: [] })),
      api("/api/v1/podcasts?limit=200").catch(() => ({ items: [] })),
      api("/api/v1/internet-radio/stations?limit=200").catch(() => ({ items: [] })),
    ]);
    if (!ch) { renderError("channel not found"); return; }
    const sourceItems = (sources && sources.items) || [];
    const ruleItems = (schedule && schedule.items) || [];
    const recentItems = (recent && recent.items) || [];
    const podcastItems = (podcasts && podcasts.items) || [];
    const internetItems = (internet && internet.items) || [];
    const sourceLookup = {};
    sourceItems.forEach((s) => { sourceLookup[s.id] = s; });

    let html = '<section class="view">' +
      '<div class="view-head"><h1>' + escapeHTML(ch.name) + '</h1><div class="view-actions">' +
        '<button class="btn primary btn-small" data-action="channel-tune-in" data-id="' + attr(ch.id) + '" data-name="' + attr(ch.name) + '">TUNE IN</button>' +
        '<button class="btn ghost btn-small" data-action="channel-back">BACK</button>' +
        '<button class="btn danger btn-small" data-action="channel-delete" data-id="' + attr(ch.id) + '" data-name="' + attr(ch.name) + '">DELETE</button>' +
      '</div></div>';

    // Now playing
    html += '<div class="panel panel-wide channel-now">' +
      '<div class="panel-head"><span>// NOW PLAYING</span><span>' + escapeHTML(ch.id) + '</span></div>' +
      channelNowPlayingBody(now) +
    '</div>';

    // Sources
    html += '<div class="panel panel-wide">' +
      '<div class="panel-head"><span>// SOURCES</span>' +
      '<span>' +
        '<button class="btn ghost btn-mini" data-action="composer-toggle" data-composer="channel-source-file">+ FILE POOL</button> ' +
        '<button class="btn ghost btn-mini" data-action="composer-toggle" data-composer="channel-source-podcast">+ PODCAST</button> ' +
        '<button class="btn ghost btn-mini" data-action="composer-toggle" data-composer="channel-source-internet">+ INTERNET STATION</button> ' +
        '<button class="btn ghost btn-mini" data-action="composer-toggle" data-composer="channel-source-live">+ LIVE URL</button>' +
      '</span></div>' +
      composerChannelSourceFile(channelID) +
      composerChannelSourcePodcast(channelID, podcastItems) +
      composerChannelSourceInternet(channelID, internetItems) +
      composerChannelSourceLive(channelID) +
      channelSourcesBody(sourceItems, podcastItems, internetItems) +
    '</div>';

    // Schedule rules
    html += '<div class="panel panel-wide">' +
      '<div class="panel-head"><span>// SCHEDULE</span>' +
      '<button class="btn ghost btn-mini" data-action="composer-toggle" data-composer="channel-schedule">+ NEW RULE</button>' +
      '</div>' +
      composerChannelSchedule(channelID, sourceItems) +
      channelScheduleTimeline(ruleItems, sourceLookup) +
      channelScheduleBody(ruleItems, sourceLookup) +
    '</div>';

    // Recent
    html += '<div class="panel panel-wide">' +
      '<div class="panel-head"><span>// RECENT</span><span>' + recentItems.length + '</span></div>' +
      channelRecentBody(recentItems) +
    '</div>';

    html += '</section>';
    main.innerHTML = html;
    if (activeTab === "radio") scheduleRadioPoll();
  }

  function channelNowPlayingBody(now) {
    const listeners = (now && now.listenerCount) || 0;
    const listenersChip = '<span class="channel-listeners">' + listeners + ' LISTENER' + (listeners === 1 ? '' : 'S') + '</span>';
    if (!now || !now.current) {
      return '<div class="channel-now-body"><div class="empty-state">// no listeners — tune in to start the stream</div>' +
        '<div class="channel-now-stats">' + listenersChip + '</div></div>';
    }
    const cur = now.current;
    const sub = cur.sourceLabel || cur.kind || "";
    const startedAt = now.startedAt ? formatDate(now.startedAt) : "";
    return '<div class="channel-now-body">' +
      '<div class="channel-now-current">' +
        '<div class="channel-eyebrow">' + (cur.live ? 'LIVE CUT-IN' : 'NOW') + '</div>' +
        '<div class="name">' + escapeHTML(cur.title || 'Untitled') + '</div>' +
        (cur.artist ? '<div class="sub">' + escapeHTML(cur.artist) + '</div>' : '') +
        '<div class="sub mono">' + escapeHTML(sub) + (startedAt ? ' · STARTED ' + escapeHTML(startedAt) : '') + '</div>' +
      '</div>' +
      '<div class="channel-now-stats">' + listenersChip + '</div>' +
    '</div>';
  }

  function channelSourcesBody(items, podcasts, internetStations) {
    if (!items || items.length === 0) {
      return '<div class="empty-state">// no sources — add a file pool, podcast subscription, or internet station above</div>';
    }
    const podLookup = {};
    (podcasts || []).forEach((p) => { podLookup[p.id] = p; });
    const inetLookup = {};
    (internetStations || []).forEach((s) => { inetLookup[s.id] = s; });
    return '<div class="list">' + items.map((src) => {
      const kindLabel = (src.kind || "").toUpperCase();
      const cfg = src.config || {};
      let detail = "";
      if (src.kind === "podcast-subscription") {
        const podID = cfg.podcastId || "";
        const pod = podLookup[podID];
        detail = pod ? podcastTitle(pod) : ("podcast: " + (podID || "?"));
      } else if (src.kind === "internet-station") {
        const stID = cfg.stationId || "";
        const st = inetLookup[stID];
        detail = st ? st.name : ("station: " + (stID || "?"));
      } else if (src.kind === "live-stream") {
        detail = cfg.url || '';
      } else if (src.kind === "file-pool" || src.kind === "scheduled-show") {
        const paths = cfg.paths || (cfg.path ? [cfg.path] : []);
        detail = (paths.length > 0 ? paths[0] : '') + (paths.length > 1 ? " · +" + (paths.length - 1) + " more" : "");
      }
      return '<div class="list-row">' +
        '<div class="num">' + (src.defaultRotation ? "ROT" : "PIN") + '</div>' +
        '<div class="main"><div class="name">' + escapeHTML(src.label || kindLabel) + '</div>' +
        '<div class="meta">' + escapeHTML(kindLabel) + (detail ? ' · ' + escapeHTML(detail) : '') + ' · WEIGHT ' + (src.weight || 1) + ' · ' + (src.enabled ? 'ENABLED' : 'DISABLED') + '</div></div>' +
        '<div class="actions">' +
          '<button class="btn ghost btn-mini" data-action="channel-source-toggle" data-id="' + attr(src.id) + '" data-enabled="' + (!src.enabled) + '">' + (src.enabled ? 'DISABLE' : 'ENABLE') + '</button>' +
          '<button class="btn danger btn-mini" data-action="channel-source-delete" data-id="' + attr(src.id) + '" data-name="' + attr(src.label || kindLabel) + '">DELETE</button>' +
        '</div>' +
      '</div>';
    }).join("") + '</div>';
  }

  // channelScheduleTimeline renders a per-weekday 24-hour strip with
  // colored bands for each scheduled rule. Same source → same color so
  // patterns across days are immediately visible. The "now" indicator
  // is a thin vertical line tracking current wall clock; idle slots
  // show through as dim gridlines so the user sees where rotation
  // takes over.
  function channelScheduleTimeline(rules, sourceLookup) {
    const weekdays = ["SUN", "MON", "TUE", "WED", "THU", "FRI", "SAT"];
    const palette = ["#f59e0b", "#22d3ee", "#a78bfa", "#34d399", "#fb7185", "#fbbf24", "#60a5fa", "#f472b6"];
    const sourceColorMap = {};
    let palIdx = 0;
    function colorFor(sourceID) {
      if (!sourceID) return palette[0];
      if (sourceColorMap[sourceID] != null) return sourceColorMap[sourceID];
      sourceColorMap[sourceID] = palette[palIdx % palette.length];
      palIdx++;
      return sourceColorMap[sourceID];
    }
    const now = new Date();
    const nowDay = now.getDay();
    const nowMin = now.getHours() * 60 + now.getMinutes();
    const nowPct = (nowMin / 1440) * 100;

    let html = '<div class="sched-timeline">' +
      '<div class="sched-hour-labels">';
    for (let h = 0; h <= 24; h += 3) {
      html += '<span style="left:' + ((h / 24) * 100) + '%">' + String(h).padStart(2, "0") + ':00</span>';
    }
    html += '</div>';

    for (let day = 0; day < 7; day++) {
      const dayRules = rules.filter((r) => r.enabled && (r.weekdayMask & (1 << day)));
      html += '<div class="sched-row' + (day === nowDay ? ' today' : '') + '">' +
        '<div class="sched-row-label">' + weekdays[day] + '</div>' +
        '<div class="sched-row-track">';
      dayRules.forEach((r) => {
        const left = (r.startMinute / 1440) * 100;
        const width = ((r.endMinute - r.startMinute) / 1440) * 100;
        const src = sourceLookup[r.sourceId];
        const color = colorFor(r.sourceId);
        const label = r.label || (src ? src.label || src.kind : "rule");
        const title = label + " · " + minuteToHHMM(r.startMinute) + "–" + minuteToHHMM(r.endMinute);
        html += '<div class="sched-band" style="left:' + left + '%;width:' + width + '%;background:' + color + '" title="' + attr(title) + '">' + escapeHTML(label) + '</div>';
      });
      if (day === nowDay) {
        html += '<div class="sched-now" style="left:' + nowPct + '%"></div>';
      }
      html += '</div></div>';
    }
    html += '</div>';
    return html;
  }

  function channelScheduleBody(rules, sourceLookup) {
    if (!rules || rules.length === 0) {
      return '<div class="empty-state">// no schedule rules — without rules the channel runs pure rotation</div>';
    }
    return '<div class="list">' + rules.map((rule) => {
      const src = sourceLookup[rule.sourceId];
      const days = weekdayMaskToLabel(rule.weekdayMask);
      const window = minuteToHHMM(rule.startMinute) + " → " + minuteToHHMM(rule.endMinute);
      const label = rule.label || (src ? src.label : "Rule");
      return '<div class="list-row">' +
        '<div class="num">P' + (rule.priority || 100) + '</div>' +
        '<div class="main"><div class="name">' + escapeHTML(label) + '</div>' +
        '<div class="meta">' + escapeHTML(days) + ' · ' + escapeHTML(window) + ' · ' + escapeHTML(src ? src.label || src.kind : 'unknown source') + ' · ' + (rule.enabled ? 'ENABLED' : 'DISABLED') + '</div></div>' +
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

  function weekdayMaskToLabel(mask) {
    const names = ["SUN", "MON", "TUE", "WED", "THU", "FRI", "SAT"];
    if (mask === 127) return "EVERY DAY";
    if (mask === 62) return "WEEKDAYS"; // Mon-Fri
    if (mask === 65) return "WEEKENDS"; // Sun+Sat
    const out = [];
    for (let i = 0; i < 7; i++) {
      if (mask & (1 << i)) out.push(names[i]);
    }
    return out.join(", ");
  }

  function minuteToHHMM(min) {
    const h = Math.floor(min / 60);
    const m = min % 60;
    return String(h).padStart(2, "0") + ":" + String(m).padStart(2, "0");
  }

  function parseHHMM(text) {
    const trimmed = String(text || "").trim();
    if (!trimmed) return -1;
    const parts = trimmed.split(":");
    if (parts.length !== 2) return -1;
    const h = parseInt(parts[0], 10);
    const m = parseInt(parts[1], 10);
    if (Number.isNaN(h) || Number.isNaN(m) || h < 0 || h > 24 || m < 0 || m > 59) return -1;
    return Math.min(1440, h * 60 + m);
  }

  function composerChannel() {
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

  function composerChannelSourceFile(channelID) {
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

  function composerChannelSourcePodcast(channelID, podcasts) {
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

  function composerChannelSourceInternet(channelID, stations) {
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

  function composerChannelSourceLive(channelID) {
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

  function composerChannelSchedule(channelID, sources) {
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

  function channelStreamURL(channelID) {
    return "/channels/" + encodeURIComponent(channelID) + "/stream" + streamQuery();
  }

  function nowPlayingLine(now, liveText, idleLabel) {
    if (now && liveText) {
      return '<div class="now-playing"><span class="dot"></span><span class="np-label">NOW</span><span class="np-text">' + escapeHTML(liveText) + '</span></div>';
    }
    return '<div class="now-playing idle"><span class="dot"></span><span class="np-label">' + escapeHTML(idleLabel) + '</span></div>';
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
      } else {
        body = await settingsAccount();
      }
      const pills = '<div class="pill-bar">' +
        '<button class="pill ' + (settingsMode === "libraries" ? "active" : "") + '" data-action="settings-mode" data-mode="libraries">LIBRARIES</button>' +
        '<button class="pill ' + (settingsMode === "radio" ? "active" : "") + '" data-action="settings-mode" data-mode="radio">RADIO</button>' +
        '<button class="pill ' + (settingsMode === "podcasts" ? "active" : "") + '" data-action="settings-mode" data-mode="podcasts">PODCASTS</button>' +
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

  async function settingsRadio() {
    const data = await api("/api/v1/internet-radio/stations").catch(() => ({ items: [] }));
    const stations = (data && data.items) || [];
    let html = '<div class="panel-grid">';
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

  function formatDataSize(bytes) {
    bytes = Number(bytes) || 0;
    if (bytes < 1024) return bytes + " B";
    if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + " KB";
    if (bytes < 1024 * 1024 * 1024) return (bytes / (1024 * 1024)).toFixed(1) + " MB";
    return (bytes / (1024 * 1024 * 1024)).toFixed(2) + " GB";
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

  function fieldHTML(id, label, placeholder, type, value, cls) {
    return '<label class="field ' + (cls || "") + '"><span class="field-label">' + escapeHTML(label) + '</span>' +
      '<input id="' + attr(id) + '" type="' + attr(type || "text") + '" placeholder="' + attr(placeholder || "") + '" value="' + attr(value || "") + '">' +
    '</label>';
  }

  function textAreaHTML(id, label, placeholder, value, cls) {
    return '<label class="field ' + (cls || "") + '"><span class="field-label">' + escapeHTML(label) + '</span>' +
      '<textarea id="' + attr(id) + '" rows="8" placeholder="' + attr(placeholder || "") + '">' + escapeHTML(value || "") + '</textarea>' +
    '</label>';
  }

  /* ---- Inline composer helpers ----
   * Add-flows live in the same view as the list they extend. composerHTML
   * renders a hidden panel; toggleComposer(id) reveals/hides it. The button
   * that triggers the composer carries data-composer="<id>" so the global
   * click handler can wire it up generically. */
  function composerHTML(id, head, body, hint) {
    return '<div class="composer" id="composer-' + attr(id) + '" hidden>' +
      '<div class="composer-head"><span>// ' + head + '</span>' +
        '<button type="button" class="composer-close" data-action="composer-toggle" data-composer="' + attr(id) + '" aria-label="Close">×</button>' +
      '</div>' +
      body +
      (hint ? '<div class="composer-hint">' + hint + '</div>' : "") +
      '<div class="status-line" id="composer-' + attr(id) + '-message" hidden></div>' +
    '</div>';
  }

  function toggleComposer(id) {
    const el = document.getElementById("composer-" + id);
    if (!el) return;
    const opening = el.hidden;
    el.hidden = !el.hidden;
    if (opening) {
      const first = el.querySelector("input, select, textarea");
      if (first) first.focus();
    }
  }

  function composerMessage(id, message, bad) {
    setMessage("composer-" + id + "-message", message, bad);
  }

  function composerClose(id) {
    const el = document.getElementById("composer-" + id);
    if (el) el.hidden = true;
  }

  /* Composer markup factories. Each returns the HTML for a self-contained
   * panel. The shared submit handler reads field values, posts to the API,
   * then closes the composer and refreshes the view. */
  function composerRadioStation() {
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

  function composerPodcastFeed() {
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

  function composerPodcastAttachFeed(podcastID, suggestedURL) {
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

  function composerPlaylist() {
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

  function composerPlaylistEdit(playlist) {
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

  function composerPlaylistImport() {
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

  function composerLibrary() {
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

  async function composerSubmit(name) {
    if (name === "radio-station") {
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
    } else if (name === "channel-source-file") {
      const channelID = document.querySelector('[data-composer="channel-source-file"][data-action="composer-submit"]').dataset.channelId;
      const label = document.getElementById("composerSrcFileLabel").value.trim();
      const pathsRaw = document.getElementById("composerSrcFilePaths").value;
      const paths = pathsRaw.split("\n").map((s) => s.trim()).filter(Boolean);
      if (paths.length === 0) return composerMessage(name, "add at least one path or folder", true);
      const weight = parseInt(document.getElementById("composerSrcFileWeight").value || "1", 10) || 1;
      await api("/api/v1/channels/" + encodeURIComponent(channelID) + "/sources", {
        method: "POST",
        body: { kind: "file-pool", label: label, config: { paths: paths }, weight: weight, defaultRotation: true, enabled: true },
      });
      composerClose(name);
      await viewRadio();
    } else if (name === "channel-source-podcast") {
      const channelID = document.querySelector('[data-composer="channel-source-podcast"][data-action="composer-submit"]').dataset.channelId;
      const podcastSelect = document.getElementById("composerSrcPodID");
      const podcastId = podcastSelect ? podcastSelect.value : "";
      if (!podcastId) return composerMessage(name, "pick a podcast", true);
      const label = document.getElementById("composerSrcPodLabel").value.trim();
      const maxAge = parseInt(document.getElementById("composerSrcPodMaxAge").value || "30", 10) || 30;
      const weight = parseInt(document.getElementById("composerSrcPodWeight").value || "1", 10) || 1;
      await api("/api/v1/channels/" + encodeURIComponent(channelID) + "/sources", {
        method: "POST",
        body: { kind: "podcast-subscription", label: label, config: { podcastId: podcastId, maxAgeDays: maxAge }, weight: weight, defaultRotation: true, enabled: true },
      });
      composerClose(name);
      await viewRadio();
    } else if (name === "channel-source-internet") {
      const channelID = document.querySelector('[data-composer="channel-source-internet"][data-action="composer-submit"]').dataset.channelId;
      const stationID = document.getElementById("composerSrcInetID").value;
      if (!stationID) return composerMessage(name, "pick a station", true);
      const label = document.getElementById("composerSrcInetLabel").value.trim();
      const rotation = document.getElementById("composerSrcInetRotation").checked;
      await api("/api/v1/channels/" + encodeURIComponent(channelID) + "/sources", {
        method: "POST",
        body: { kind: "internet-station", label: label, config: { stationId: stationID }, defaultRotation: rotation, enabled: true },
      });
      composerClose(name);
      await viewRadio();
    } else if (name === "channel-source-live") {
      const channelID = document.querySelector('[data-composer="channel-source-live"][data-action="composer-submit"]').dataset.channelId;
      const label = document.getElementById("composerSrcLiveLabel").value.trim();
      const url = document.getElementById("composerSrcLiveURL").value.trim();
      if (!url) return composerMessage(name, "stream URL is required", true);
      const rotation = document.getElementById("composerSrcLiveRotation").checked;
      await api("/api/v1/channels/" + encodeURIComponent(channelID) + "/sources", {
        method: "POST",
        body: { kind: "live-stream", label: label, config: { url: url }, defaultRotation: rotation, enabled: true },
      });
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

    const profileForm = document.getElementById("profileForm");
    if (profileForm) {
      profileForm.addEventListener("submit", async (event) => {
        event.preventDefault();
        const body = { displayName: document.getElementById("displayName").value.trim() };
        const password = document.getElementById("newPassword").value;
        if (password) body.password = password;
        try {
          currentUser = await api("/api/v1/users/me", { method: "PATCH", body: body });
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
        stopArtistImagePolling();
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
      } else if (action === "radio-mode") {
        radioMode = el.dataset.mode === "internet" ? "internet" : "channels";
        activeChannelID = "";
        await viewRadio();
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
  const views = {
    home: viewHome,
    music: viewMusic,
    audiobooks: viewAudiobooks,
    podcasts: viewPodcasts,
    radio: viewRadio,
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
    Array.from(nav.children).forEach((tab) => tab.classList.toggle("active", tab.dataset.tab === name));
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

  Array.from(nav.children).forEach((tab) => {
    tab.addEventListener("click", () => navigateTo(tab.dataset.tab));
  });
  window.addEventListener("hashchange", dispatchHash);

  document.getElementById("signOut").addEventListener("click", () => {
    localStorage.removeItem(tokenKey);
    window.location.href = "/login";
  });

  /* -------- boot -------- */
  (async function boot() {
    try {
      currentUser = await api("/api/v1/users/me");
      document.getElementById("authUser").textContent = (currentUser.username || "-").toUpperCase();
      await ensureStreamToken();
      // Refresh the stream token well before its TTL so audio/img URLs
      // rendered later in the session keep working without flicker.
      setInterval(() => { refreshStreamToken().catch(() => {}); }, 20 * 60 * 1000);
      setStatus("ONLINE · CATALOG READY");
      await resumeActiveScan();
      await resumeArtistImageBackfill();
    } catch (err) {
      setStatus("ERROR · " + (err.message || "unknown"));
      return;
    }
    if (!location.hash) location.hash = "#home";
    dispatchHash();
  })();
})();
`
