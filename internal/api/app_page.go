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
      <button class="tab" data-tab="settings">SETTINGS</button>
    </nav>
    <div class="app-bar-right">
      <button class="scan-badge" id="scanBadge" type="button" title="Scanning…" hidden>
        <span class="scan-badge-ring"></span>
        <span class="scan-badge-text" id="scanBadgeText">0</span>
      </button>
      <span class="auth-chip"><span class="auth-label">SIGNED IN</span><span class="auth-user" id="authUser">—</span></span>
      <button class="btn ghost btn-small" id="signOut">SIGN OUT</button>
    </div>
  </header>
  <main class="app-main" id="appMain">
    <div class="boot-line">// booting samo client · please stand by</div>
  </main>
  <div class="scan-banner" id="scanBanner" hidden>
    <button class="scan-banner-body" id="scanBannerBody" type="button" title="View scan jobs">
      <span class="scan-banner-dot"></span>
      <span class="scan-banner-label" id="scanBannerLabel">SCAN</span>
      <span class="scan-banner-text" id="scanBannerText">starting…</span>
      <span class="scan-banner-more">DETAILS &rarr;</span>
    </button>
    <button class="scan-banner-close" id="scanBannerClose" type="button" aria-label="Dismiss">×</button>
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
    <div class="player-controls">
      <button class="btn ghost btn-mini" id="playerToggle">PAUSE</button>
      <audio id="audioPlayer" controls preload="none"></audio>
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
}
.app-nav .tab:hover { color: var(--text); }
.app-nav .tab.active {
  color: var(--accent);
  border-color: var(--accent);
}
.app-bar-right {
  display: flex;
  align-items: center;
  gap: 12px;
}
.auth-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 4px 10px;
  border: 1px solid var(--line);
  font-family: var(--mono);
  font-size: 0.66rem;
  letter-spacing: 0.16em;
  text-transform: uppercase;
}
.auth-chip .auth-label { color: var(--muted); }
.auth-chip .auth-user { color: var(--text); }
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
  font-size: clamp(1.6rem, 3vw, 2.4rem);
  font-weight: 900;
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
.album-card {
  border: 1px solid var(--line);
  background: var(--surface);
  padding: 14px;
  display: grid;
  gap: 6px;
  text-decoration: none;
  color: inherit;
  cursor: pointer;
}
.album-card:hover {
  border-color: var(--accent);
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
}
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
  padding: 18px 20px;
  align-items: center;
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
.radio-card .now-playing .dot {
  width: 6px; height: 6px; background: var(--accent); display: inline-block; box-shadow: 0 0 8px var(--accent);
}
.radio-card .now-playing.idle .dot { background: var(--muted); box-shadow: none; }
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
  grid-template-columns: minmax(0, 1fr) minmax(300px, 520px);
  gap: 18px;
  align-items: center;
  padding: 12px 28px;
  background: rgba(0, 0, 0, 0.94);
  border-top: 1px solid var(--line);
  backdrop-filter: blur(8px);
}
.player-dock[hidden] { display: none; }

/* Scan progress banner — sticky pill at the bottom-left, above the
 * player dock. Live file count updates while scanning, settles to a
 * dismissible "scan complete" / "scan failed" line when done. */
.scan-banner {
  position: fixed;
  left: 24px;
  bottom: 96px;
  z-index: 6;
  display: inline-flex;
  align-items: center;
  gap: 12px;
  padding: 10px 14px;
  background: rgba(0, 0, 0, 0.94);
  border: 1px solid var(--accent);
  font-family: var(--mono);
  font-size: 0.74rem;
  letter-spacing: 0.12em;
  text-transform: uppercase;
  color: var(--text);
  box-shadow: 0 0 20px -6px var(--accent);
}
.scan-banner[hidden] { display: none; }
.scan-banner.ok    { border-color: var(--accent); }
.scan-banner.error { border-color: var(--danger); box-shadow: 0 0 20px -6px var(--danger); }
.scan-banner-body {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  background: transparent;
  border: 0;
  padding: 0;
  margin: 0;
  font: inherit;
  color: inherit;
  text-transform: inherit;
  letter-spacing: inherit;
  cursor: pointer;
}
.scan-banner-body:hover .scan-banner-more { color: var(--accent); }
.scan-banner-more {
  color: var(--muted);
  font-family: var(--mono);
  font-size: 0.68rem;
  letter-spacing: 0.2em;
  margin-left: 8px;
  padding-left: 8px;
  border-left: 1px solid var(--line);
}
.scan-banner-dot {
  width: 8px;
  height: 8px;
  background: var(--accent);
  display: inline-block;
  box-shadow: 0 0 10px var(--accent);
  animation: samoPulse 1.2s ease-in-out infinite;
}
.scan-banner.ok .scan-banner-dot { animation: none; }
.scan-banner.error .scan-banner-dot { background: var(--danger); box-shadow: 0 0 10px var(--danger); animation: none; }
.scan-banner-label { color: var(--accent); letter-spacing: 0.22em; }
.scan-banner.error .scan-banner-label { color: var(--danger); }
.scan-banner-text { color: var(--text-dim); font-family: var(--mono); }
.scan-banner-close {
  background: transparent;
  border: 1px solid var(--ghost);
  color: var(--muted);
  width: 22px; height: 22px;
  padding: 0;
  cursor: pointer;
  font-family: var(--mono);
  font-size: 0.85rem;
  line-height: 1;
}
.scan-banner-close:hover { color: var(--text); border-color: var(--text); }

/* Scan badge — minimized state of the scan banner. Lives in the
 * header bar (right side) so you can dismiss the banner but still see
 * progress at a glance, navidrome-style. Click it to re-show the
 * banner. */
.scan-badge {
  position: relative;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  padding: 0;
  background: transparent;
  border: 1px solid var(--ghost);
  border-radius: 50%;
  cursor: pointer;
  font-family: var(--mono);
  color: var(--accent);
}
.scan-badge[hidden] { display: none; }
.scan-badge:hover { border-color: var(--accent); }
.scan-badge-ring {
  position: absolute;
  inset: -1px;
  border-radius: 50%;
  border: 1px solid transparent;
  border-top-color: var(--accent);
  border-right-color: var(--accent);
  animation: scanBadgeSpin 1.1s linear infinite;
}
.scan-badge.ok .scan-badge-ring,
.scan-badge.error .scan-badge-ring { animation: none; }
.scan-badge.ok .scan-badge-ring    { border-color: var(--accent); }
.scan-badge.error .scan-badge-ring { border-color: var(--danger); }
.scan-badge.error { color: var(--danger); }
.scan-badge-text {
  position: relative;
  z-index: 1;
  font-size: 0.6rem;
  letter-spacing: 0.04em;
  color: inherit;
}
@keyframes scanBadgeSpin {
  to { transform: rotate(360deg); }
}

/* Identify modal — picks a metadata candidate (OpenLibrary, Google Books,
 * Apple Podcasts) and applies it to the audiobook/podcast. */
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
.player-controls {
  display: grid;
  grid-template-columns: auto 1fr;
  gap: 10px;
  align-items: center;
}
.player-controls audio {
  width: 100%;
  height: 34px;
  filter: grayscale(1);
}

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
  .radio-card .actions {
    align-items: flex-start;
    flex-direction: row;
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

  let currentUser = null;
  let activeTab = "";
  let musicMode = "albums";
  let musicSort = "recent";
  let musicDirection = "desc";
  let audiobooksMode = "titles";
  let podcastsMode = "shows";
  let settingsMode = "libraries";
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

  function audiobookStreamURL(id) {
    return "/api/v1/audiobooks/" + encodeURIComponent(id) + "/stream" + streamQuery();
  }

  function audiobookCoverURL(id) {
    return "/api/v1/audiobooks/" + encodeURIComponent(id) + "/cover" + streamQuery();
  }

  function podcastCoverURL(id) {
    return "/api/v1/podcasts/shows/" + encodeURIComponent(id) + "/cover" + streamQuery();
  }

  function podcastEpisodeStreamURL(id) {
    return "/api/v1/podcasts/episodes/" + encodeURIComponent(id) + "/stream" + streamQuery();
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

  /* ---- Scan progress banner ----------------------------------------------
   * Async scans return a job ID immediately; this controller polls the job
   * row, surfaces a live file count, and dismisses on completion. Repeated
   * scan triggers latch onto the same job (server side returns the active
   * job rather than queuing). Dismissing the banner does not stop polling
   * — instead a small spinning badge stays in the header (navidrome-style)
   * and clicking it re-opens the banner. */
  const scanBanner = document.getElementById("scanBanner");
  const scanBannerLabel = document.getElementById("scanBannerLabel");
  const scanBannerText = document.getElementById("scanBannerText");
  const scanBannerClose = document.getElementById("scanBannerClose");
  const scanBadge = document.getElementById("scanBadge");
  const scanBadgeText = document.getElementById("scanBadgeText");
  let scanPollHandle = null;
  let scanWatchJobID = "";
  let scanLastFilesSeen = 0;
  let scanLastLabel = "SCAN";
  let scanLastText = "starting…";
  let scanLastState = "running";

  scanBannerClose.addEventListener("click", (event) => {
    event.stopPropagation();
    scanBanner.hidden = true;
    // Keep the badge visible so the user remembers a scan is still going.
    if (scanLastState === "running") scanBadge.hidden = false;
  });
  // Clicking the banner body navigates to the scan jobs panel where the
  // full history (status, files seen, errors) is rendered. Previously the
  // banner was a passive label and there was no obvious way to drill in.
  const scanBannerBody = document.getElementById("scanBannerBody");
  scanBannerBody.addEventListener("click", () => {
    navigateTo("settings");
  });
  scanBadge.addEventListener("click", () => {
    setScanBanner(scanLastState, scanLastLabel, scanLastText);
  });

  function updateScanBadge(state) {
    scanBadge.classList.remove("ok", "error");
    if (state === "ok") scanBadge.classList.add("ok");
    if (state === "error") scanBadge.classList.add("error");
    scanBadgeText.textContent = formatScanBadgeCount(scanLastFilesSeen);
  }

  function formatScanBadgeCount(count) {
    if (!count) return "·";
    if (count >= 1000) return Math.floor(count / 1000) + "k";
    return String(count);
  }

  function setScanBanner(state, label, text) {
    scanLastState = state;
    scanLastLabel = label;
    scanLastText = text;
    scanBanner.classList.remove("ok", "error");
    if (state === "ok") scanBanner.classList.add("ok");
    if (state === "error") scanBanner.classList.add("error");
    scanBannerLabel.textContent = label;
    scanBannerText.textContent = text;
    scanBanner.hidden = false;
    updateScanBadge(state);
    // Terminal states hide the badge after a short delay so the header
    // doesn't accumulate stale spinners.
    if (state === "ok" || state === "error") {
      setTimeout(() => { scanBadge.hidden = true; }, 4000);
    } else {
      scanBadge.hidden = false;
    }
  }

  function stopScanPolling() {
    if (scanPollHandle) {
      clearInterval(scanPollHandle);
      scanPollHandle = null;
    }
    scanWatchJobID = "";
  }

  async function watchScanJob(jobID) {
    if (!jobID) return;
    stopScanPolling();
    scanWatchJobID = jobID;
    scanLastFilesSeen = 0;
    setScanBanner("running", "SCAN", "starting...");
    const tick = async () => {
      if (scanWatchJobID !== jobID) return;
      try {
        const job = await api("/api/v1/scan/jobs/" + encodeURIComponent(jobID));
        scanLastFilesSeen = job.filesSeen || 0;
        const total = job.filesTotal || 0;
        if (job.status === "running" || job.status === "pending") {
          const text = total > 0
            ? scanLastFilesSeen + " of " + total + " files"
            : scanLastFilesSeen + " files indexed";
          setScanBanner("running", "SCAN", text);
          return;
        }
        stopScanPolling();
        if (job.status === "completed") {
          const parts = [scanLastFilesSeen + " files"];
          if (job.itemsPruned) parts.push(job.itemsPruned + " pruned");
          setScanBanner("ok", "DONE", parts.join(" · "));
        } else {
          setScanBanner("error", "FAILED", job.error || "scan failed");
        }
        if (activeTab && views[activeTab]) await views[activeTab]();
      } catch (err) {
        stopScanPolling();
        setScanBanner("error", "FAILED", err.message || "polling failed");
      }
    };
    tick();
    scanPollHandle = setInterval(tick, 1500);
  }

  async function triggerScan(kickoff) {
    setScanBanner("running", "SCAN", "starting...");
    try {
      const result = await kickoff();
      const jobID = result && result.job && result.job.id;
      if (!jobID) {
        setScanBanner("error", "FAILED", "no job id returned");
        return;
      }
      watchScanJob(jobID);
    } catch (err) {
      setScanBanner("error", "FAILED", err.message || "scan failed");
    }
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
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && !identifyModal.hidden) closeIdentifyModal();
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
    const metaParts = [candidate.provider || "", authors, candidate.publishedYear || candidate.publishedDate || ""].filter(Boolean);
    return '<div class="identify-result">' +
      '<div class="cover" ' + coverStyle + '></div>' +
      '<div><div class="title">' + escapeHTML(candidate.title || "Untitled") + '</div>' +
        '<div class="meta">' + escapeHTML(metaParts.join(" · ")) + '</div></div>' +
      '<button class="btn primary btn-mini" data-action="identify-apply" data-kind="' + attr(identifyContext ? identifyContext.kind : "audiobook") + '" data-id="' + attr(identifyContext ? identifyContext.id : "") + '" data-idx="' + idx + '">APPLY</button>' +
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
    setScanBanner("running", "METADATA", "fetching " + niceKind + "...");
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
          setScanBanner("running", "METADATA", processed + " of " + total + " · " + (title || "untitled"));
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
      setScanBanner(failed > 0 ? "error" : "ok", "METADATA", summary);
      // Reload the view so the user sees the new metadata.
      if (activeTab && views[activeTab]) await views[activeTab]();
    } catch (err) {
      setScanBanner("error", "METADATA", err.message || "scan failed");
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
    try {
      await api("/api/v1/metadata/apply", {
        method: "POST",
        body: { targetKind: targetKind, targetId: id, candidate: candidate, fields: fields },
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

  function playURL(url, title, subtitle, target) {
    playerTarget = target || null;
    lastProgressSync = 0;
    playerTitle.textContent = title || "UNKNOWN";
    playerSub.textContent = subtitle || "";
    playerDock.hidden = false;
    audio.src = url;
    audio.play().catch((err) => {
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

  async function playPodcastEpisode(id, title, subtitle, duration) {
    playURL(podcastEpisodeStreamURL(id), title || "Episode", subtitle || "Podcast", { kind: "podcast-episode", id: id, duration: duration || 0 });
    try {
      await patchPlayback("podcast-episode", id, { incrementPlayCount: true, touchLastPlayedAt: true });
    } catch (err) {
      setStatus("PLAYBACK · " + err.message);
    }
  }

  playerToggle.addEventListener("click", () => {
    if (audio.paused) {
      audio.play();
      playerToggle.textContent = "PAUSE";
    } else {
      audio.pause();
      playerToggle.textContent = "PLAY";
    }
  });

  audio.addEventListener("play", () => { playerToggle.textContent = "PAUSE"; });
  audio.addEventListener("pause", () => { playerToggle.textContent = "PLAY"; });
  audio.addEventListener("timeupdate", () => {
    if (!playerTarget || (playerTarget.kind !== "music-track" && playerTarget.kind !== "podcast-episode")) return;
    const now = Math.floor(audio.currentTime || 0);
    if (now <= 0 || now - lastProgressSync < 20) return;
    lastProgressSync = now;
    patchPlayback(playerTarget.kind, playerTarget.id, { progressSeconds: now, touchLastPositionAt: true }).catch(() => {});
  });
  audio.addEventListener("ended", () => {
    if (!playerTarget || (playerTarget.kind !== "music-track" && playerTarget.kind !== "podcast-episode")) return;
    patchPlayback(playerTarget.kind, playerTarget.id, { completed: true, touchLastPlayedAt: true }).catch(() => {});
  });

  /* -------- HOME -------- */
  async function viewHome() {
    renderLoading();
    try {
      const [overview, recentlyAdded, libraries, stations] = await Promise.all([
        api("/api/v1/catalog/overview"),
        api("/api/v1/music/browse/recently-added?limit=8"),
        api("/api/v1/libraries").catch(() => ({ items: [], total: 0 })),
        api("/api/v1/radio/stations").catch(() => []),
      ]);
      const musicCounts = overview.music || {};
      const audiobookCounts = overview.audiobook || {};
      const podcastCounts = overview.podcast || {};
      const libCount = (libraries && libraries.total) || 0;
      const stationCount = Array.isArray(stations) ? stations.length : 0;

      let html = '<section class="view">';
      html += '<div class="view-head"><h1>HOME</h1><div class="view-actions">' +
        '<button class="btn primary btn-small" data-action="scan-all">SCAN ALL</button>' +
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

      const albums = browseAlbums(recentlyAdded).slice(0, 8);
      html += '<div class="section-row">';
      html += '<div class="section-label">// recently added</div>';
      if (albums.length > 0) {
        html += '<div class="album-grid">' + albums.map(albumCard).join("") + '</div>';
      } else {
        html += '<div class="empty-state">// no albums yet - add a music library and run a scan</div>';
      }
      html += '</div>';

      const libs = (libraries && libraries.items) || [];
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
            '<div class="actions"><button class="btn ghost btn-mini" data-action="scan-library" data-id="' + attr(lib.id) + '">SCAN</button></div>' +
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

  /* -------- MUSIC -------- */
  async function viewMusic() {
    renderLoading();
    try {
      const pills = '<div class="pill-bar">' +
        '<button class="pill ' + (musicMode === "albums" ? "active" : "") + '" data-action="music-mode" data-mode="albums">ALBUMS</button>' +
        '<button class="pill ' + (musicMode === "tracks" ? "active" : "") + '" data-action="music-mode" data-mode="tracks">TRACKS</button>' +
        '<button class="pill ' + (musicMode === "artists" ? "active" : "") + '" data-action="music-mode" data-mode="artists">ARTISTS</button>' +
        '<button class="pill ' + (musicMode === "playlists" ? "active" : "") + '" data-action="music-mode" data-mode="playlists">PLAYLISTS</button>' +
        '<button class="pill ' + (musicMode === "recent" ? "active" : "") + '" data-action="music-mode" data-mode="recent">RECENT</button>' +
        '<button class="pill ' + (musicMode === "starred" ? "active" : "") + '" data-action="music-mode" data-mode="starred">STARRED</button>' +
        '<button class="pill ' + (musicMode === "favorites" ? "active" : "") + '" data-action="music-mode" data-mode="favorites">FAVORITES</button>' +
      '</div>';

      let body = "";
      const sortQuery = musicSortQuery();
      if (musicMode === "albums") {
        const data = await api("/api/v1/music/albums?limit=80&" + sortQuery);
        body = albumGridFromList((data && data.items) || []);
      } else if (musicMode === "tracks") {
        const data = await api("/api/v1/music/tracks?limit=120&" + sortQuery);
        body = trackList((data && data.items) || []);
      } else if (musicMode === "artists") {
        const data = await api("/api/v1/music/artists?limit=80&" + sortQuery);
        body = artistList((data && data.items) || []);
      } else if (musicMode === "playlists") {
        const data = await api("/api/v1/music/playlists?limit=80");
        body = playlistList((data && data.items) || []);
      } else if (musicMode === "recent") {
        const data = await api("/api/v1/music/browse/recently-played?limit=80");
        body = musicMixedResults(data, "recent plays");
      } else if (musicMode === "starred") {
        const data = await api("/api/v1/music/browse/starred?limit=80");
        body = musicMixedResults(data, "starred music");
      } else {
        const data = await api("/api/v1/music/browse/favorites?limit=80");
        body = musicMixedResults(data, "favorite music");
      }
      const sortControls = (musicMode === "albums" || musicMode === "tracks" || musicMode === "artists") ? musicSortToolbar() : "";
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
          '<button class="btn ghost btn-mini" data-action="toggle-playback" data-kind="music-track" data-id="' + attr(track.id) + '" data-field="starred" data-value="' + (!playback.starred) + '">' + (playback.starred ? "UNSTAR" : "STAR") + '</button>' +
          '<button class="btn ghost btn-mini" data-action="toggle-playback" data-kind="music-track" data-id="' + attr(track.id) + '" data-field="favorite" data-value="' + (!playback.favorite) + '">' + (playback.favorite ? "UNFAV" : "FAV") + '</button>' +
        '</div>' +
      '</div>';
    }).join("") + '</div>';
  }

  function playlistTrackList(playlistID, items, canEdit) {
    if (!items || items.length === 0) return '<div class="empty-state">// no tracks in this playlist yet</div>';
    return '<div class="list">' + items.map((track, idx) => {
      const artist = track.displayArtist || (track.artistNames || []).join(", ");
      const meta = [artist, track.albumTitle, formatDuration(track.durationSeconds)].filter(Boolean).join(" · ");
      const removeButton = canEdit ? '<button class="btn danger btn-mini" data-action="remove-playlist-track" data-playlist-id="' + attr(playlistID) + '" data-track-id="' + attr(track.id) + '">REMOVE</button>' : "";
      return '<div class="list-row">' +
        '<div class="num">' + String(idx + 1).padStart(2, "0") + '</div>' +
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
    return '<div class="list">' + items.map((playlist, idx) => (
      '<div class="list-row clickable" data-action="open-playlist" data-id="' + attr(playlist.id) + '">' +
        '<div class="num">' + String(idx + 1).padStart(2, "0") + '</div>' +
        '<div class="main">' +
          '<div class="name">' + escapeHTML(playlist.name || "Untitled Playlist") + '</div>' +
          '<div class="meta">' + (playlist.trackCount || 0) + ' TRACKS · ' + formatDuration(playlist.durationSeconds || 0) + ' · ' + (playlist.public ? "PUBLIC" : "PRIVATE") + '</div>' +
        '</div>' +
        '<div class="actions">' +
          '<button class="btn ghost btn-mini" data-action="open-playlist" data-id="' + attr(playlist.id) + '">OPEN &rarr;</button>' +
          (playlistOwnedByCurrentUser(playlist) ? '<button class="btn danger btn-mini" data-action="delete-playlist" data-id="' + attr(playlist.id) + '" data-name="' + attr(playlist.name || "playlist") + '">DELETE</button>' : "") +
        '</div>' +
      '</div>'
    )).join("") + '</div>';
  }

  function playlistOwnedByCurrentUser(playlist) {
    if (!playlist) return false;
    if (!playlist.ownerId) return true;
    return currentUser && playlist.ownerId === currentUser.id;
  }

  function musicMixedResults(data, label) {
    const albums = (data && data.albums) || [];
    const tracks = (data && data.tracks) || [];
    const artists = (data && data.artists) || [];
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
        api("/api/v1/music/tracks?limit=500"),
      ]);
      const tracks = ((tracksPage && tracksPage.items) || []).filter((track) => track.albumId === id);
      tracks.sort((a, b) => (a.discNumber || 0) - (b.discNumber || 0) || (a.trackNumber || 0) - (b.trackNumber || 0) || String(a.title || "").localeCompare(String(b.title || "")));
      const cover = musicCoverURL(album.id);
      let html = '<section class="view">' +
        '<div class="view-head"><h1>MUSIC</h1><div class="view-actions"><button class="btn ghost btn-small" data-action="back-music">BACK</button>' +
        (tracks[0] ? '<button class="btn primary btn-small" data-action="play-track" data-id="' + attr(tracks[0].id) + '" data-title="' + attr(tracks[0].title || album.title) + '" data-sub="' + attr(album.displayArtist || "") + '" data-duration="' + (tracks[0].durationSeconds || 0) + '">PLAY FIRST</button>' : "") +
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
      const [playlist, tracksPage] = await Promise.all([
        api("/api/v1/music/playlists/" + encodeURIComponent(id)),
        api("/api/v1/music/playlists/" + encodeURIComponent(id) + "/tracks").catch(() => ({ items: [] })),
      ]);
      const tracks = (tracksPage && tracksPage.items) || [];
      const canEdit = playlistOwnedByCurrentUser(playlist);
      const ownerActions = canEdit ?
        '<button class="btn ghost btn-small" data-action="toggle-playlist-public" data-id="' + attr(playlist.id) + '" data-public="' + (!playlist.public) + '">' + (playlist.public ? "MAKE PRIVATE" : "MAKE PUBLIC") + '</button>' +
        '<button class="btn danger btn-small" data-action="delete-playlist" data-id="' + attr(playlist.id) + '" data-name="' + attr(playlist.name || "playlist") + '">DELETE</button>' :
        "";
      let html = '<section class="view">' +
        '<div class="view-head"><h1>MUSIC</h1><div class="view-actions">' +
          '<button class="btn ghost btn-small" data-action="back-music">BACK</button>' +
          ownerActions +
        '</div></div>' +
        '<div class="detail-shell">' +
          '<div class="detail-cover" style="background-color: #0a0a0a"></div>' +
          '<div class="detail-meta">' +
            '<div class="card-head"><span class="caret">&gt;</span> PLAYLIST</div>' +
            '<h2>' + escapeHTML(playlist.name || "Untitled Playlist") + '</h2>' +
            (playlist.description ? '<div class="artist">' + escapeHTML(playlist.description) + '</div>' : "") +
            '<div class="stats"><span>' + (playlist.trackCount || tracks.length || 0) + ' TRACKS</span><span>' + formatDuration(playlist.durationSeconds || 0) + '</span><span>' + (playlist.public ? "PUBLIC" : "PRIVATE") + '</span></div>' +
          '</div>' +
        '</div>' +
        '<div class="section-row"><div class="section-label">// tracks</div>' + playlistTrackList(playlist.id, tracks, canEdit) + '</div>' +
      '</section>';
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
        const data = await api("/api/v1/audiobooks?limit=80");
        body = audiobookGrid((data && data.items) || []);
      }
      main.innerHTML = '<section class="view">' +
        '<div class="view-head"><h1>AUDIOBOOKS</h1><div class="view-actions">' +
          '<button class="btn ghost btn-small" data-action="bulk-identify" data-kind="audiobook">SCAN FOR METADATA</button>' +
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
      '</div>';

      let body = "";
      if (podcastsMode === "episodes") {
        const data = await api("/api/v1/podcasts/episodes?limit=80");
        body = episodeList((data && data.items) || []);
      } else {
        const data = await api("/api/v1/podcasts?limit=80");
        body = podcastGrid((data && data.items) || []);
      }
      main.innerHTML = '<section class="view">' +
        '<div class="view-head"><h1>PODCASTS</h1><div class="view-actions">' +
          '<button class="btn ghost btn-small" data-action="bulk-identify" data-kind="podcast">SCAN FOR METADATA</button>' +
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

  function episodeList(items) {
    if (!items || items.length === 0) return '<div class="empty-state">// no podcast episodes yet</div>';
    return '<div class="list">' + items.map((item, idx) => {
      const meta = [formatDate(item.publishedAt || item.addedAt), formatDuration(item.durationSeconds || 0)].filter(Boolean).join(" · ");
      const title = item.title || "Untitled";
      return '<div class="list-row">' +
        '<div class="num">' + String(idx + 1).padStart(2, "0") + '</div>' +
        '<div class="main">' +
          '<div class="name">' + escapeHTML(title) + '</div>' +
          '<div class="meta">' + escapeHTML(meta) + '</div>' +
          progressBar(item.progress || {}, item.durationSeconds || 0) +
        '</div>' +
        '<div class="actions">' +
          '<button class="btn primary btn-mini" data-action="play-podcast-episode" data-id="' + attr(item.id) + '" data-title="' + attr(title) + '" data-sub="Podcast episode" data-duration="' + attr(item.durationSeconds || 0) + '">PLAY</button>' +
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
        '<a class="btn primary btn-small" href="' + attr(audiobookStreamURL(id)) + '" target="_blank">OPEN STREAM</a></div></div>' +
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

  async function openPodcast(id) {
    renderLoading();
    try {
      const [item, episodes] = await Promise.all([
        api("/api/v1/podcasts/shows/" + encodeURIComponent(id)),
        api("/api/v1/podcasts/shows/" + encodeURIComponent(id) + "/episodes?limit=200").catch(() => ({ items: [] })),
      ]);
      const title = podcastTitle(item);
      const sub = podcastSub(item);
      const cover = podcastCoverURL(id);
      const items = (episodes && episodes.items) || [];
      let html = '<section class="view">' +
        '<div class="view-head"><h1>PODCASTS</h1><div class="view-actions">' +
          '<button class="btn ghost btn-small" data-action="back-tab" data-tab="podcasts">BACK</button>' +
          '<button class="btn ghost btn-small" data-action="identify" data-kind="podcast" data-id="' + attr(id) + '" data-title="' + attr(title) + '" data-author="' + attr(sub) + '">FIND MATCH</button>' +
        '</div></div>' +
        '<div class="detail-shell">' +
          '<div class="detail-cover" style="background-image:url(&quot;' + attr(cover) + '&quot;)"></div>' +
          '<div class="detail-meta">' +
            '<h2>' + escapeHTML(title) + '</h2>' +
            '<div class="artist">' + escapeHTML(sub) + '</div>' +
            '<div class="stats"><span>PODCAST</span><span>' + items.length + ' EPISODES</span></div>' +
            (item.podcast && item.podcast.description ? '<p class="lede" style="margin-top:14px; color: var(--text-dim)">' + escapeHTML(item.podcast.description) + '</p>' : "") +
            tagsLine((item.podcast && item.podcast.categories) || item.genres || []) +
          '</div>' +
        '</div>' +
        '<div class="section-row"><div class="section-label">// episodes</div>' + episodeList(items) + '</div>' +
      '</section>';
      main.innerHTML = html;
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
      await renderRadio(true);
    }, 8000);
  }

  async function viewRadio() { return renderRadio(false); }

  async function renderRadio(isRefresh) {
    if (!isRefresh) renderLoading();
    try {
      const [stations, internet] = await Promise.all([
        api("/api/v1/radio/stations").catch(() => []),
        api("/api/v1/internet-radio/stations").catch(() => ({ items: [] })),
      ]);
      let html = '<section class="view">' +
        '<div class="view-head"><h1>RADIO</h1><div class="view-actions">' +
        '<button class="btn primary btn-small" data-action="composer-toggle" data-composer="radio-station">+ NEW STATION</button>' +
        '<button class="btn ghost btn-small" data-action="probe-all-radio">PROBE ALL</button>' +
        '</div></div>' +
        composerRadioStation();

      html += '<div class="section-row"><div class="section-label">// programmed stations</div>';
      if (!stations || stations.length === 0) {
        html += '<div class="empty-state">// no programmed stations yet</div>';
      } else {
        stations.forEach((station) => { html += radioCard(station, station.streamUrl, station.playlistUrl, false); });
      }
      html += '</div>';

      const inet = (internet && internet.items) || [];
      html += '<div class="section-row"><div class="section-label">// internet radio</div>';
      if (inet.length === 0) {
        html += '<div class="empty-state">// add an internet station from settings</div>';
      } else {
        inet.forEach((station) => { html += radioCard(station, station.publicStreamUrl, station.playlistUrl, true); });
      }
      html += '</div>';

      html += '</section>';
      main.innerHTML = html;
      if (activeTab === "radio") scheduleRadioPoll();
    } catch (err) { renderError(err.message); }
  }

  function radioCard(station, streamURL, playlistURL, internet) {
    const now = internet ? station.nowPlaying : station.now;
    const nowText = now ? (now.raw || now.title || "") : (internet ? "WAITING FOR METADATA" : "IDLE");
    const sub = station.description || station.homepageUrl || station.contentType || "";
    const image = station.imageUrl || "";
    const coverStyle = image ? 'style="background-image:url(&quot;' + attr(image) + '&quot;)"' : "";
    const coverEmpty = image ? "" : "empty";
    return '<div class="radio-card">' +
      '<div class="radio-cover ' + coverEmpty + '" ' + coverStyle + '></div>' +
      '<div class="radio-meta"><h3 class="name">' + escapeHTML(station.name) + '</h3>' +
        (sub ? '<p class="desc">' + escapeHTML(sub) + '</p>' : "") +
        '<div class="now-playing ' + (now ? "" : "idle") + '"><span class="dot"></span><span>' + (now ? "NOW" : (internet ? "WAITING" : "IDLE")) + '</span><span>' + escapeHTML(nowText) + '</span></div>' +
      '</div>' +
      '<div class="actions">' +
        '<button class="btn primary btn-mini" data-action="play-url" data-url="' + attr(streamURL || "") + '" data-title="' + attr(station.name || "Radio") + '" data-sub="' + attr(nowText || "Radio stream") + '">PLAY</button>' +
        '<a class="btn ghost btn-mini" href="' + attr(playlistURL || streamURL || "#") + '" target="_blank">M3U</a>' +
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
    const [libraries, jobs] = await Promise.all([
      api("/api/v1/libraries"),
      api("/api/v1/scan/jobs?limit=8").catch(() => ({ items: [] })),
    ]);
    const libs = (libraries && libraries.items) || [];
    let html = '<div class="panel-grid">';
    html += '<form class="panel panel-wide settings-form" id="libraryForm">' +
      '<div class="panel-head"><span>// add library</span></div>' +
      '<div class="form-grid">' +
        fieldHTML("libraryName", "Name", "Music", "text", "") +
        fieldHTML("libraryPath", "Path", "/srv/media/music", "text", "") +
        '<label class="field"><span class="field-label">Kind</span><select id="libraryKind"><option value="mixed">Mixed (auto-detect)</option><option value="music">Music only</option><option value="audiobook">Audiobooks</option><option value="podcast">Podcasts</option></select></label>' +
        fieldHTML("libraryDescription", "Description", "optional", "text", "", "full") +
      '</div>' +
      '<div class="actions"><button class="btn primary" type="submit">ADD LIBRARY</button><button class="btn ghost" type="button" data-action="scan-all">SCAN ALL</button></div>' +
      '<div class="status-line" id="libraryMessage" hidden></div>' +
    '</form>';

    html += '<div class="panel panel-wide"><div class="panel-head"><span>// attached libraries</span><span>' + libs.length + '</span></div>';
    if (libs.length === 0) {
      html += '<div class="empty-state">// no libraries attached yet</div>';
    } else {
      html += '<div class="list">';
      libs.forEach((lib) => {
        html += '<div class="list-row">' +
          '<div class="num">·</div>' +
          '<div class="main"><div class="name">' + escapeHTML(lib.name) + '</div>' +
          '<div class="meta">' + escapeHTML(lib.path) + ' · ' + libraryKindLabel(lib) + ' · ' + (lib.itemCount || 0) + ' ITEMS · LAST SCAN ' + formatDate(lib.lastScanAt) + '</div></div>' +
          '<div class="actions">' +
            '<button class="btn ghost btn-mini" data-action="scan-library" data-id="' + attr(lib.id) + '">SCAN</button>' +
            '<button class="btn danger btn-mini" data-action="delete-library" data-id="' + attr(lib.id) + '" data-name="' + attr(lib.name) + '">DELETE</button>' +
          '</div>' +
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
          '<div class="meta">' + filesText + ' · ' + (job.itemsPruned || 0) + ' ITEMS PRUNED · STARTED ' + formatDate(job.startedAt) + (job.error ? ' · ' + escapeHTML(job.error) : '') + '</div></div></div>';
      });
      html += '</div>';
    }
    html += '</div></div>';
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

  async function settingsPodcasts() {
    const data = await api("/api/v1/podcasts/feeds?limit=80").catch(() => ({ items: [] }));
    const feeds = (data && data.items) || [];
    let html = '<div class="panel-grid">';
    html += '<form class="panel panel-wide settings-form" id="podcastFeedForm">' +
      '<div class="panel-head"><span>// add podcast feed</span></div>' +
      '<div class="form-grid">' +
        fieldHTML("podcastTitle", "Title", "optional", "text", "") +
        fieldHTML("podcastURL", "Feed URL", "https://example.com/feed.xml", "url", "", "full") +
      '</div>' +
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
    let html = '<div class="panel-grid">';
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
    '</form>';

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
    '</div></div></div>';

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
    html += '</div>';

    if (users && Array.isArray(users.items)) {
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
      html += '</div></div>';
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
        fieldHTML("composerRadioImage", "Thumbnail URL", "https://example.com/logo.png", "url", "") +
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
      '<div class="composer-actions">' +
        '<button class="btn primary" data-action="composer-submit" data-composer="podcast-feed">ADD FEED</button>' +
        '<button class="btn ghost" data-action="composer-toggle" data-composer="podcast-feed">CANCEL</button>' +
      '</div>';
    return composerHTML("podcast-feed", "NEW PODCAST FEED", body,
      "// the title field is optional — Samo will read it from the RSS feed");
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
      await api("/api/v1/internet-radio/stations", {
        method: "POST",
        body: {
          name: stationName,
          streamUrl: stream,
          homepageUrl: document.getElementById("composerRadioHomepage").value.trim(),
          imageUrl: document.getElementById("composerRadioImage").value.trim(),
          tags: splitTags(document.getElementById("composerRadioTags").value),
          enabled: true,
        },
      });
      composerClose(name);
      await viewRadio();
    } else if (name === "podcast-feed") {
      const url = document.getElementById("composerPodcastURL").value.trim();
      if (!url) return composerMessage(name, "feed URL is required", true);
      await api("/api/v1/podcasts/feeds", {
        method: "POST",
        body: {
          url: url,
          title: document.getElementById("composerPodcastTitle").value.trim(),
        },
      });
      composerClose(name);
      await viewPodcasts();
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
          sharedSecret: document.getElementById("lastfmSharedSecret").value,
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

  document.addEventListener("click", async (event) => {
    const el = event.target.closest("[data-action]");
    if (!el || (!main.contains(el) && !identifyModal.contains(el))) return;
    const action = el.dataset.action;
    try {
      if (action === "music-mode") {
        musicMode = el.dataset.mode || "albums";
        await viewMusic();
      } else if (action === "music-sort") {
        musicSort = el.dataset.sort || "recent";
        await viewMusic();
      } else if (action === "music-direction") {
        musicDirection = el.dataset.direction || "desc";
        await viewMusic();
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
        await playPodcastEpisode(el.dataset.id, el.dataset.title, el.dataset.sub, Number(el.dataset.duration || 0));
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
      } else if (action === "scan-all") {
        // Kick off the async scan; the banner polls and refreshes views
        // when the job finishes.
        triggerScan(() => api("/api/v1/scan", { method: "POST" }));
      } else if (action === "scan-library") {
        const libID = el.dataset.id;
        triggerScan(() => api("/api/v1/libraries/" + encodeURIComponent(libID) + "/scan", { method: "POST" }));
      } else if (action === "delete-library") {
        if (!confirm("Delete library " + (el.dataset.name || "") + "? Catalog rows for this library will be removed.")) return;
        await api("/api/v1/libraries/" + encodeURIComponent(el.dataset.id), { method: "DELETE" });
        await viewSettings();
      } else if (action === "probe-all-radio") {
        await withButton(el, "PROBING...", async () => {
          await api("/api/v1/internet-radio/stations/probe", { method: "POST" });
          if (activeTab === "settings") await viewSettings();
          else await viewRadio();
        });
      } else if (action === "probe-radio") {
        await withButton(el, "PROBING...", async () => {
          await api("/api/v1/internet-radio/stations/" + encodeURIComponent(el.dataset.id) + "/probe", { method: "POST" });
          await viewSettings();
        });
      } else if (action === "toggle-radio") {
        await api("/api/v1/internet-radio/stations/" + encodeURIComponent(el.dataset.id), { method: "PATCH", body: { enabled: asBool(el.dataset.enabled) } });
        await viewSettings();
      } else if (action === "save-radio-image") {
        const input = document.getElementById(el.dataset.input || "");
        await withButton(el, "SAVING...", async () => {
          await api("/api/v1/internet-radio/stations/" + encodeURIComponent(el.dataset.id), {
            method: "PATCH",
            body: { imageUrl: input ? input.value.trim() : "" },
          });
          await viewSettings();
        });
      } else if (action === "delete-radio") {
        if (!confirm("Delete station " + (el.dataset.name || "") + "?")) return;
        await api("/api/v1/internet-radio/stations/" + encodeURIComponent(el.dataset.id), { method: "DELETE" });
        await viewSettings();
      } else if (action === "poll-podcasts") {
        await withButton(el, "POLLING...", async () => {
          await api("/api/v1/podcasts/feeds/poll", { method: "POST" });
          await viewSettings();
        });
      } else if (action === "refresh-feed") {
        await withButton(el, "REFRESHING...", async () => {
          await api("/api/v1/podcasts/feeds/" + encodeURIComponent(el.dataset.id) + "/refresh", { method: "POST" });
          await viewSettings();
        });
      } else if (action === "delete-feed") {
        if (!confirm("Delete feed " + (el.dataset.name || "") + "?")) return;
        await api("/api/v1/podcasts/feeds/" + encodeURIComponent(el.dataset.id), { method: "DELETE" });
        await viewSettings();
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
    } catch (err) {
      setStatus("ERROR · " + (err.message || "unknown"));
      return;
    }
    if (!location.hash) location.hash = "#home";
    dispatchHash();
  })();
})();
`
