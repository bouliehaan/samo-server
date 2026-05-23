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
      <div class="bar-wordmark">
        <span class="bar-word">SAMO</span><span class="bar-word dim">SERVER</span>
        <span class="bar-status"><span class="dot"></span><span id="barStatusText">CONNECTING</span></span>
      </div>
    </div>
    <nav class="app-nav" id="appNav">
      <button class="tab" data-tab="home">HOME</button>
      <button class="tab" data-tab="music">MUSIC</button>
      <button class="tab" data-tab="shelf">SHELF</button>
      <button class="tab" data-tab="radio">RADIO</button>
      <button class="tab" data-tab="search">SEARCH</button>
    </nav>
    <div class="app-bar-right">
      <span class="auth-chip"><span class="auth-label">SIGNED IN</span><span class="auth-user" id="authUser">—</span></span>
      <button class="btn ghost btn-small" id="signOut">SIGN OUT</button>
    </div>
  </header>
  <main class="app-main" id="appMain">
    <div class="boot-line">// booting samo client · please stand by</div>
  </main>
  <script>` + appJS + `</script>
</body>
</html>`

const appCSS = `
body { background: #000; }
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
.app-bar-left .bar-wordmark {
  display: flex;
  align-items: baseline;
  gap: 8px;
}
.bar-wordmark .bar-word {
  font-family: var(--sans);
  font-weight: 900;
  font-size: 1.2rem;
  letter-spacing: -0.02em;
  color: var(--text);
}
.bar-wordmark .bar-word.dim { color: var(--text-dim); }
.bar-wordmark .bar-status {
  margin-left: 14px;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-family: var(--mono);
  font-size: 0.66rem;
  letter-spacing: 0.18em;
  text-transform: uppercase;
  color: var(--muted);
}
.bar-wordmark .bar-status .dot {
  width: 6px; height: 6px; background: var(--accent);
  box-shadow: 0 0 8px var(--accent);
  display: inline-block;
}
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
  grid-template-columns: 1fr auto;
  gap: 16px;
  border: 1px solid var(--line);
  background: var(--surface);
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
.pill-bar .pill {
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
.pill-bar .pill:hover { color: var(--text); }
.pill-bar .pill.active { color: var(--accent); border-color: var(--accent); }
`

const appJS = `
(function () {
  const tokenKey = "samo-token";
  let token = localStorage.getItem(tokenKey) || "";
  if (!token) { window.location.href = "/login"; return; }

  const main = document.getElementById("appMain");
  const nav = document.getElementById("appNav");

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
      window.location.href = "/login";
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

  function formatDuration(seconds) {
    seconds = Math.max(0, Math.floor(seconds || 0));
    const h = Math.floor(seconds / 3600);
    const m = Math.floor((seconds % 3600) / 60);
    const s = seconds % 60;
    if (h > 0) return h + "H " + m + "M";
    return m + ":" + String(s).padStart(2, "0");
  }

  function setStatus(text) {
    const el = document.getElementById("barStatusText");
    if (el) el.textContent = text;
  }

  function renderLoading() {
    main.innerHTML = "<div class=\"boot-line\">// loading…</div>";
  }

  function renderError(message) {
    main.innerHTML = "<div class=\"empty-state\">// " + escapeHTML(message) + "</div>";
  }

  /* -------- HOME -------- */
  async function viewHome() {
    renderLoading();
    try {
      const [overview, recentlyAdded, libraries, stations] = await Promise.all([
        api("/api/v1/catalog/overview"),
        api("/api/v1/music/browse/recently-added?limit=8"),
        api("/api/v1/libraries"),
        api("/api/v1/radio/stations"),
      ]);
      const musicCounts = overview.music || {};
      const shelfCounts = overview.shelf || {};
      const libCount = (libraries && libraries.total) || 0;
      const stationCount = Array.isArray(stations) ? stations.length : 0;

      let html = '<section class="view">';
      html += '<div class="view-head"><h1>HOME</h1><span class="crumb">// dashboard</span></div>';
      html += '<div class="stat-grid">';
      html += statCard("ARTISTS", musicCounts.artists || 0);
      html += statCard("ALBUMS", musicCounts.albums || 0, true);
      html += statCard("TRACKS", musicCounts.tracks || 0);
      html += statCard("AUDIOBOOKS", shelfCounts.audiobooks || 0);
      html += statCard("PODCASTS", shelfCounts.podcasts || 0);
      html += statCard("RADIO", stationCount);
      html += statCard("LIBRARIES", libCount);
      html += '</div>';

      const albums = (recentlyAdded && recentlyAdded.items) || [];
      if (albums.length > 0) {
        html += '<div class="section-row">';
        html += '<div class="section-label">// recently added</div>';
        html += '<div class="album-grid">';
        albums.forEach((album) => { html += albumCard(album); });
        html += '</div></div>';
      } else {
        html += '<div class="section-row">';
        html += '<div class="section-label">// recently added</div>';
        html += '<div class="empty-state">// no albums yet — add a music library and run a scan</div>';
        html += '</div>';
      }

      const libs = (libraries && libraries.items) || [];
      if (libs.length > 0) {
        html += '<div class="section-row">';
        html += '<div class="section-label">// attached libraries</div>';
        html += '<div class="list">';
        libs.forEach((lib) => {
          const kind = lib.kind === "mixed" ? "MIXED"
            : lib.kind === "music" ? "MUSIC"
            : lib.kind === "shelf" && lib.mediaType === "book" ? "AUDIOBOOKS"
            : lib.kind === "shelf" && lib.mediaType === "podcast" ? "PODCASTS"
            : lib.kind.toUpperCase();
          html += '<div class="list-row">' +
            '<div class="num">·</div>' +
            '<div class="main">' +
              '<div class="name">' + escapeHTML(lib.name) + '</div>' +
              '<div class="meta">' + escapeHTML(lib.path) + ' · ' + kind + ' · ' + (lib.itemCount || 0) + ' ITEMS</div>' +
            '</div>' +
          '</div>';
        });
        html += '</div></div>';
      }
      html += '</section>';
      main.innerHTML = html;
    } catch (err) { renderError(err.message); }
  }

  function statCard(label, value, accent) {
    return '<div class="stat-card"><span class="label">' + label + '</span>' +
      '<span class="value' + (accent ? " accent" : "") + '">' + (value || 0) + '</span></div>';
  }

  function albumCard(album) {
    const cover = album.coverUrl || (album.id ? "/api/v1/music/albums/" + encodeURIComponent(album.id) + "/cover" : "");
    const style = cover ? 'style="background-image:url(\\'' + cover + '\\')"' : "";
    const empty = cover ? "" : "empty";
    return '<a class="album-card" data-album="' + escapeHTML(album.id) + '">' +
      '<div class="cover ' + empty + '" ' + style + '></div>' +
      '<div class="title">' + escapeHTML(album.title || album.name || "Untitled") + '</div>' +
      '<div class="sub">' + escapeHTML(album.displayArtist || album.artist || "Various") + '</div>' +
    '</a>';
  }

  /* -------- MUSIC -------- */
  let musicMode = "albums";
  async function viewMusic() {
    renderLoading();
    try {
      const pills = '<div class="pill-bar">' +
        '<button class="pill ' + (musicMode === "albums" ? "active" : "") + '" data-mode="albums">ALBUMS</button>' +
        '<button class="pill ' + (musicMode === "artists" ? "active" : "") + '" data-mode="artists">ARTISTS</button>' +
        '<button class="pill ' + (musicMode === "recent" ? "active" : "") + '" data-mode="recent">RECENTLY PLAYED</button>' +
      '</div>';

      let body = "";
      if (musicMode === "albums") {
        const data = await api("/api/v1/music/albums?limit=60");
        body = albumGridFromList((data && data.items) || []);
      } else if (musicMode === "artists") {
        const data = await api("/api/v1/music/artists?limit=60");
        body = artistList((data && data.items) || []);
      } else {
        const data = await api("/api/v1/music/browse/recently-played?limit=40");
        body = albumGridFromList((data && data.items) || []);
      }
      main.innerHTML = '<section class="view">' +
        '<div class="view-head"><h1>MUSIC</h1><span class="crumb">// library</span></div>' +
        pills + body +
      '</section>';
      main.querySelectorAll(".pill[data-mode]").forEach((pill) => {
        pill.addEventListener("click", () => {
          musicMode = pill.dataset.mode;
          viewMusic();
        });
      });
    } catch (err) { renderError(err.message); }
  }
  function albumGridFromList(items) {
    if (items.length === 0) return '<div class="empty-state">// no albums to show yet</div>';
    return '<div class="album-grid">' + items.map(albumCard).join("") + '</div>';
  }
  function artistList(items) {
    if (items.length === 0) return '<div class="empty-state">// no artists yet</div>';
    return '<div class="list">' + items.map((artist, idx) => (
      '<div class="list-row">' +
        '<div class="num">' + String(idx + 1).padStart(2, "0") + '</div>' +
        '<div class="main">' +
          '<div class="name">' + escapeHTML(artist.name || artist.displayName) + '</div>' +
          '<div class="meta">' + (artist.albumCount || 0) + ' ALBUMS · ' + (artist.trackCount || 0) + ' TRACKS</div>' +
        '</div>' +
      '</div>'
    )).join("") + '</div>';
  }

  /* -------- SHELF -------- */
  let shelfMode = "audiobooks";
  async function viewShelf() {
    renderLoading();
    try {
      const pills = '<div class="pill-bar">' +
        '<button class="pill ' + (shelfMode === "audiobooks" ? "active" : "") + '" data-mode="audiobooks">AUDIOBOOKS</button>' +
        '<button class="pill ' + (shelfMode === "podcasts" ? "active" : "") + '" data-mode="podcasts">PODCASTS</button>' +
        '<button class="pill ' + (shelfMode === "authors" ? "active" : "") + '" data-mode="authors">AUTHORS</button>' +
      '</div>';

      let body = "";
      if (shelfMode === "audiobooks") {
        const data = await api("/api/v1/shelf/audiobooks?limit=60");
        body = shelfGrid((data && data.items) || [], "book");
      } else if (shelfMode === "podcasts") {
        const data = await api("/api/v1/shelf/podcasts?limit=60");
        body = shelfGrid((data && data.items) || [], "podcast");
      } else {
        const data = await api("/api/v1/shelf/authors?limit=60");
        body = authorList((data && data.items) || []);
      }
      main.innerHTML = '<section class="view">' +
        '<div class="view-head"><h1>SHELF</h1><span class="crumb">// audiobooks &amp; podcasts</span></div>' +
        pills + body +
      '</section>';
      main.querySelectorAll(".pill[data-mode]").forEach((pill) => {
        pill.addEventListener("click", () => { shelfMode = pill.dataset.mode; viewShelf(); });
      });
    } catch (err) { renderError(err.message); }
  }
  function shelfGrid(items, kind) {
    if (items.length === 0) return '<div class="empty-state">// nothing on the ' + (kind === "podcast" ? "podcast feed" : "shelf") + ' yet</div>';
    return '<div class="album-grid">' + items.map((item) => {
      const cover = item.coverUrl || "/api/v1/shelf/items/" + encodeURIComponent(item.id) + "/cover";
      const author = (item.book && item.book.authors && item.book.authors.length > 0) ? item.book.authors[0].name
        : (item.podcast && item.podcast.author) || "";
      return '<a class="album-card" data-shelf="' + escapeHTML(item.id) + '">' +
        '<div class="cover" style="background-image:url(\\'' + cover + '\\')"></div>' +
        '<div class="title">' + escapeHTML(item.title) + '</div>' +
        '<div class="sub">' + escapeHTML(author) + '</div>' +
      '</a>';
    }).join("") + '</div>';
  }
  function authorList(items) {
    if (items.length === 0) return '<div class="empty-state">// no authors yet</div>';
    return '<div class="list">' + items.map((author, idx) => (
      '<div class="list-row">' +
        '<div class="num">' + String(idx + 1).padStart(2, "0") + '</div>' +
        '<div class="main">' +
          '<div class="name">' + escapeHTML(author.name) + '</div>' +
          '<div class="meta">' + (author.bookCount || 0) + ' TITLES</div>' +
        '</div>' +
      '</div>'
    )).join("") + '</div>';
  }

  /* -------- RADIO -------- */
  async function viewRadio() {
    renderLoading();
    try {
      const [stations, internet] = await Promise.all([
        api("/api/v1/radio/stations"),
        api("/api/v1/internet-radio/stations"),
      ]);
      let html = '<section class="view">' +
        '<div class="view-head"><h1>RADIO</h1><span class="crumb">// always on</span></div>';

      html += '<div class="section-row"><div class="section-label">// programmed stations</div>';
      if (!stations || stations.length === 0) {
        html += '<div class="empty-state">// no programmed stations yet — POST to /api/v1/radio/admin/stations</div>';
      } else {
        stations.forEach((station) => {
          const now = station.now;
          const nowText = now ? (escapeHTML(now.title || "") + (now.artist ? ' <span style="color:var(--text-dim)">/ ' + escapeHTML(now.artist) + '</span>' : "")) : "IDLE";
          html += '<div class="radio-card">' +
            '<div><h3 class="name">' + escapeHTML(station.name) + '</h3>' +
              (station.description ? '<p class="desc">' + escapeHTML(station.description) + '</p>' : "") +
              '<div class="now-playing ' + (now ? "" : "idle") + '"><span class="dot"></span><span>' + (now ? "NOW" : "IDLE") + '</span><span>' + nowText + '</span></div>' +
            '</div>' +
            '<div class="actions">' +
              '<a class="btn primary btn-mini" href="' + station.streamUrl + '" target="_blank">STREAM &rarr;</a>' +
              '<a class="btn ghost btn-mini" href="' + station.playlistUrl + '" target="_blank">M3U</a>' +
            '</div>' +
          '</div>';
        });
      }
      html += '</div>';

      const inet = (internet && internet.items) || [];
      html += '<div class="section-row"><div class="section-label">// internet radio</div>';
      if (inet.length === 0) {
        html += '<div class="empty-state">// add a station via POST /api/v1/internet-radio/stations</div>';
      } else {
        inet.forEach((station) => {
          const np = station.nowPlaying || null;
          const npText = np && (np.raw || np.title) ? escapeHTML(np.raw || np.title) : "WAITING FOR METADATA";
          html += '<div class="radio-card">' +
            '<div><h3 class="name">' + escapeHTML(station.name) + '</h3>' +
              (station.description ? '<p class="desc">' + escapeHTML(station.description) + '</p>' : "") +
              '<div class="now-playing ' + (np ? "" : "idle") + '"><span class="dot"></span><span>' + (np ? "NOW" : "WAITING") + '</span><span>' + npText + '</span></div>' +
            '</div>' +
            '<div class="actions">' +
              '<a class="btn primary btn-mini" href="' + station.publicStreamUrl + '" target="_blank">STREAM &rarr;</a>' +
              '<a class="btn ghost btn-mini" href="' + station.playlistUrl + '" target="_blank">M3U</a>' +
            '</div>' +
          '</div>';
        });
      }
      html += '</div>';

      html += '</section>';
      main.innerHTML = html;
    } catch (err) { renderError(err.message); }
  }

  /* -------- SEARCH -------- */
  let searchQuery = "";
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
    out.innerHTML = '<div class="boot-line">// querying…</div>';
    try {
      const [music, shelf] = await Promise.all([
        api("/api/v1/music/search?q=" + encodeURIComponent(query) + "&limit=15"),
        api("/api/v1/shelf/search?q=" + encodeURIComponent(query) + "&limit=15"),
      ]);
      let html = "";
      const musicItems = (music && (music.albums || []).concat(music.artists || []).concat(music.tracks || [])) || [];
      if (musicItems.length > 0) {
        html += '<div class="search-group"><div class="search-group-head">// MUSIC</div><div class="list">';
        musicItems.slice(0, 20).forEach((item, idx) => {
          html += '<div class="list-row"><div class="num">' + String(idx + 1).padStart(2, "0") + '</div>' +
            '<div class="main"><div class="name">' + escapeHTML(item.title || item.name || "") + '</div>' +
            '<div class="meta">' + escapeHTML(item.displayArtist || item.artist || "") + '</div></div></div>';
        });
        html += '</div></div>';
      }
      const shelfItems = ((shelf && shelf.items) || []);
      if (shelfItems.length > 0) {
        html += '<div class="search-group"><div class="search-group-head">// SHELF</div><div class="list">';
        shelfItems.slice(0, 20).forEach((item, idx) => {
          html += '<div class="list-row"><div class="num">' + String(idx + 1).padStart(2, "0") + '</div>' +
            '<div class="main"><div class="name">' + escapeHTML(item.title) + '</div>' +
            '<div class="meta">' + escapeHTML(item.mediaType || "") + '</div></div></div>';
        });
        html += '</div></div>';
      }
      if (!html) html = '<div class="empty-state">// no matches for &ldquo;' + escapeHTML(query) + '&rdquo;</div>';
      out.innerHTML = html;
    } catch (err) { out.innerHTML = '<div class="empty-state">// ' + escapeHTML(err.message) + '</div>'; }
  }

  /* -------- nav -------- */
  const views = { home: viewHome, music: viewMusic, shelf: viewShelf, radio: viewRadio, search: viewSearch };
  function setActiveTab(name) {
    Array.from(nav.children).forEach((tab) => tab.classList.toggle("active", tab.dataset.tab === name));
    location.hash = "#" + name;
    if (views[name]) views[name]();
  }
  Array.from(nav.children).forEach((tab) => {
    tab.addEventListener("click", () => setActiveTab(tab.dataset.tab));
  });
  window.addEventListener("hashchange", () => {
    const name = (location.hash || "#home").slice(1);
    if (views[name]) setActiveTab(name);
  });

  document.getElementById("signOut").addEventListener("click", () => {
    localStorage.removeItem(tokenKey);
    window.location.href = "/login";
  });

  /* -------- boot -------- */
  (async function boot() {
    try {
      const me = await api("/api/v1/users/me");
      document.getElementById("authUser").textContent = (me.username || "—").toUpperCase();
      setStatus("ONLINE · CATALOG READY");
    } catch (err) {
      // api() already redirected on 401; any other error just shows boot text.
      setStatus("ERROR · " + (err.message || "unknown"));
      return;
    }
    const initial = (location.hash || "#home").slice(1);
    setActiveTab(views[initial] ? initial : "home");
  })();
})();
`
