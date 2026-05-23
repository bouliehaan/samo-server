package api

import (
	"html/template"
	"log"
	"net/http"
)

// setupPage serves the first-run wizard. Once setup is complete, visitors are
// redirected back to the dashboard so the wizard doesn't become a permanent
// /setup entry point.
func (s *Server) setupPage(w http.ResponseWriter, r *http.Request) {
	status, err := s.computeSetupStatus(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !status.NeedsSetup {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := setupTemplate.Execute(w, nil); err != nil {
		log.Printf("failed to render setup page: %v", err)
	}
}

var setupTemplate = template.Must(template.New("setup").Parse(setupHTML))

const setupHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>SAMO SERVER · SETUP</title>
  <style>` + samoBaseCSS + `</style>
  <style>
    .wizard {
      display: grid;
      gap: 28px;
    }
    .step-meta {
      display: flex;
      align-items: center;
      gap: 12px;
      flex-wrap: wrap;
      font-family: var(--mono);
      font-size: 0.72rem;
      letter-spacing: 0.18em;
      text-transform: uppercase;
      color: var(--muted);
    }
    .step-meta .step-pill {
      padding: 4px 10px;
      border: 1px solid var(--line);
      color: var(--text);
    }
    .step-meta .step-pill.active {
      border-color: var(--accent);
      color: var(--accent);
    }
    .step-meta .step-pill.done {
      color: var(--text-dim);
    }
    .step-card h2 {
      font-size: 1.5rem;
      margin: 0 0 6px;
      letter-spacing: -0.01em;
    }
    .step-card p.lede {
      color: var(--muted);
      margin: 0 0 20px;
      max-width: 60ch;
      line-height: 1.5;
    }
    .form-row-split {
      display: grid;
      grid-template-columns: 1fr 1fr;
      gap: 14px;
    }
    @media (max-width: 540px) {
      .form-row-split { grid-template-columns: 1fr; }
    }

    /* Libraries step */
    .libs-shell {
      display: grid;
      gap: 24px;
    }
    .libs-attached {
      border: 1px solid var(--line);
      background: var(--surface);
    }
    .libs-attached-head {
      display: flex;
      justify-content: space-between;
      align-items: baseline;
      padding: 14px 16px;
      border-bottom: 1px solid var(--line);
      font-family: var(--mono);
      font-size: 0.7rem;
      letter-spacing: 0.2em;
      text-transform: uppercase;
    }
    .libs-attached-head .label { color: var(--text-dim); }
    .libs-attached-head .count { color: var(--accent); }
    .libs-empty {
      padding: 22px 16px;
      font-family: var(--mono);
      font-size: 0.8rem;
      color: var(--text-dim);
    }
    .lib-row {
      display: grid;
      grid-template-columns: 1fr auto;
      gap: 12px;
      align-items: center;
      padding: 14px 16px;
      border-bottom: 1px solid color-mix(in srgb, var(--line) 50%, transparent);
    }
    .lib-row:last-child { border-bottom: 0; }
    .lib-row .lib-main { min-width: 0; }
    .lib-row .lib-name {
      font-family: var(--sans);
      font-size: 1rem;
      font-weight: 700;
      letter-spacing: -0.01em;
      color: var(--text);
      display: flex;
      align-items: center;
      gap: 10px;
      flex-wrap: wrap;
    }
    .lib-row .kind-chip {
      font-family: var(--mono);
      font-size: 0.65rem;
      letter-spacing: 0.18em;
      padding: 2px 8px;
      border: 1px solid var(--accent);
      color: var(--accent);
      text-transform: uppercase;
    }
    .lib-row .lib-path {
      font-family: var(--mono);
      font-size: 0.78rem;
      color: var(--text-dim);
      margin-top: 4px;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
    .lib-row .btn-remove {
      background: transparent;
      color: var(--muted);
      border: 1px solid var(--line);
      width: 32px;
      height: 32px;
      padding: 0;
      cursor: pointer;
      font-family: var(--mono);
      font-size: 1rem;
      line-height: 1;
      transition: color 90ms, border-color 90ms;
    }
    .lib-row .btn-remove:hover { color: var(--danger); border-color: var(--danger); }

    /* Add-folder form */
    .libs-add {
      border: 1px solid var(--line);
      background: var(--surface);
    }
    .libs-add-head {
      padding: 14px 16px;
      border-bottom: 1px solid var(--line);
      font-family: var(--mono);
      font-size: 0.7rem;
      letter-spacing: 0.2em;
      text-transform: uppercase;
      color: var(--accent);
    }
    .libs-add-body { padding: 16px; }

    .browser-shell {
      border: 1px solid var(--line);
      background: #000;
      margin-bottom: 16px;
    }
    .browser-head {
      padding: 10px 14px;
      border-bottom: 1px solid var(--line);
      font-family: var(--mono);
      font-size: 0.75rem;
      letter-spacing: 0.06em;
      color: var(--muted);
      word-break: break-all;
    }
    .browser-head .label { color: var(--text-dim); margin-right: 6px; }
    .browser-list {
      max-height: 240px;
      overflow: auto;
      padding: 4px 0;
    }
    .browser-row {
      display: flex;
      align-items: center;
      justify-content: space-between;
      padding: 10px 14px;
      font-family: var(--mono);
      font-size: 0.85rem;
      cursor: pointer;
      border-bottom: 1px solid color-mix(in srgb, var(--line) 50%, transparent);
    }
    .browser-row:hover {
      background: color-mix(in srgb, var(--accent) 6%, transparent);
      color: var(--accent);
    }
    .browser-row:last-child { border-bottom: 0; }
    .browser-row .meta { color: var(--text-dim); font-size: 0.78rem; }
    .browser-row.is-parent { color: var(--accent); }

    .kind-hint {
      font-family: var(--mono);
      font-size: 0.7rem;
      letter-spacing: 0.1em;
      color: var(--muted);
      margin-top: -8px;
      margin-bottom: 14px;
    }

    /* Scan output */
    .scan-output {
      border: 1px solid var(--line);
      background: var(--surface);
      padding: 14px;
      font-family: var(--mono);
      font-size: 0.78rem;
      color: var(--text-dim);
      white-space: pre-wrap;
      word-break: break-all;
      max-height: 240px;
      overflow: auto;
    }
    .scan-output.success { border-color: var(--accent); color: var(--accent); }

    .continue-row {
      display: flex;
      justify-content: flex-end;
      gap: 12px;
      padding-top: 12px;
    }
    .help-text {
      font-family: var(--mono);
      font-size: 0.72rem;
      letter-spacing: 0.06em;
      color: var(--muted);
      margin-top: -10px;
      margin-bottom: 14px;
      line-height: 1.5;
    }
  </style>
</head>
<body>
  <div class="grid-bg"></div>
  <main>
    <header class="samo-head">
      <div class="wordmark">
        <div class="word">SAMO</div>
        <div class="word dim">SERVER</div>
        <div class="status" id="hostStatus">
          <span class="dot"></span><span class="status-text">SETUP · STEP 1 OF 3</span>
        </div>
      </div>
      <div class="ledger">
        <div><span class="label">PROTOCOL</span><span class="value">SAMO-NATIVE V1</span></div>
        <div><span class="label">SESSION</span><span class="value" id="hostSession">SIGNED OUT</span></div>
      </div>
    </header>

    <section class="wizard">
      <div class="step-meta" id="progress">
        <span class="step-pill" data-step="admin">[ 01 / ACCOUNT ]</span>
        <span class="step-pill" data-step="libraries">[ 02 / LIBRARIES ]</span>
        <span class="step-pill" data-step="scan">[ 03 / SCAN ]</span>
      </div>
      <section class="card step-card" id="step-card">
        <p class="lede">Loading setup state…</p>
      </section>
    </section>
  </main>
  <script>
  (function () {
    const tokenKey = "samo-token";
    let token = localStorage.getItem(tokenKey) || "";
    let state = null;
    const card = document.getElementById("step-card");
    const progress = document.getElementById("progress");

    function setStep(step) {
      const order = ["admin", "libraries", "scan", "done"];
      Array.from(progress.children).forEach((pill) => {
        pill.classList.remove("active", "done");
        const idx = order.indexOf(pill.dataset.step);
        const currentIdx = order.indexOf(step);
        if (idx < currentIdx) pill.classList.add("done");
        else if (idx === currentIdx) pill.classList.add("active");
      });
    }

    async function fetchStatus() {
      const res = await fetch("/api/v1/setup/status");
      if (!res.ok) throw new Error("status " + res.status);
      state = await res.json();
      setStep(state.currentStep);
      const stepNum = { admin: "1 OF 3", libraries: "2 OF 3", scan: "3 OF 3", done: "COMPLETE" }[state.currentStep] || "—";
      document.getElementById("hostStatus").querySelector(".status-text").textContent = "SETUP · STEP " + stepNum;
      render();
    }

    async function withToken(path, options) {
      options = options || {};
      options.headers = options.headers || {};
      if (options.body) options.headers["Content-Type"] = "application/json";
      if (token) options.headers["Authorization"] = "Bearer " + token;
      const res = await fetch(path, options);
      if (res.status === 204) return null;
      const body = await res.json().catch(() => ({}));
      if (!res.ok) {
        throw new Error(body.error || ("request failed: " + res.status));
      }
      return body;
    }

    function setError(message) {
      const existing = card.querySelector(".error-line");
      if (existing) existing.remove();
      if (!message) return;
      const div = document.createElement("div");
      div.className = "error-line";
      div.textContent = "× " + message;
      card.appendChild(div);
    }

    function escapeHTML(value) {
      return String(value || "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", "\"": "&quot;" }[c]));
    }

    /* ---------------- STEP 1 : account ---------------- */
    function renderAdminStep() {
      card.innerHTML = ` + "`" + `
        <div class="card-head"><span class="caret">&gt;</span> CREATE YOUR ACCOUNT</div>
        <h2>Pick a username and password.</h2>
        <p class="lede">This is how you'll sign in. Nothing leaves your machine — Samo stores credentials locally.</p>
        <div class="form-row-split">
          <label class="field">
            <span class="field-label">USERNAME</span>
            <input type="text" id="adminUsername" autocomplete="username" placeholder="jake">
          </label>
          <label class="field">
            <span class="field-label">PASSWORD</span>
            <input type="password" id="adminPassword" autocomplete="new-password" placeholder="8+ characters">
          </label>
        </div>
        <div class="actions">
          <button class="btn primary" id="adminSubmit">CREATE ACCOUNT &rarr;</button>
        </div>
      ` + "`" + `;
      const submit = () => {
        const username = document.getElementById("adminUsername").value.trim();
        const password = document.getElementById("adminPassword").value;
        setError("");
        if (!username) return setError("username is required");
        if (password.length < 8) return setError("password must be at least 8 characters");
        const button = document.getElementById("adminSubmit");
        button.disabled = true;
        button.textContent = "CREATING…";
        withToken("/api/v1/setup/admin", {
          method: "POST",
          body: JSON.stringify({ username, password }),
        }).then((result) => {
          token = result.token;
          localStorage.setItem(tokenKey, token);
          document.getElementById("hostSession").textContent = "SIGNED IN · " + result.user.username.toUpperCase();
          return fetchStatus();
        }).catch((err) => {
          button.disabled = false;
          button.textContent = "CREATE ACCOUNT →";
          setError(err.message);
        });
      };
      document.getElementById("adminSubmit").addEventListener("click", submit);
      document.getElementById("adminPassword").addEventListener("keydown", (e) => { if (e.key === "Enter") submit(); });
    }

    /* ---------------- STEP 2 : libraries ---------------- */
    let currentBrowsePath = "";
    let attachedLibraries = [];

    async function loadDirectories(path) {
      currentBrowsePath = path || "";
      const url = "/api/v1/setup/directories" + (path ? "?path=" + encodeURIComponent(path) : "");
      const data = await withToken(url, { method: "GET" });
      renderDirectoryListing(data);
    }

    function renderDirectoryListing(data) {
      const list = document.querySelector(".browser-list");
      const head = document.querySelector(".browser-head .value");
      if (!list || !head) return;
      head.textContent = data.path || "SUGGESTED LOCATIONS";
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
        row.addEventListener("click", () => loadDirectories(entry.path));
        list.appendChild(row);
      });
      const pathInput = document.getElementById("libraryPath");
      if (pathInput && data.path) {
        pathInput.value = data.path;
        // Auto-suggest a name from the folder if the user hasn't typed one.
        const nameInput = document.getElementById("libraryName");
        if (nameInput && !nameInput.dataset.touched) {
          const parts = data.path.split("/").filter(Boolean);
          nameInput.placeholder = parts.length ? parts[parts.length - 1] : "autodetect";
        }
      }
    }

    async function refreshLibraryList() {
      try {
        const data = await withToken("/api/v1/libraries", { method: "GET" });
        attachedLibraries = (data && data.items) || [];
      } catch (err) {
        attachedLibraries = [];
        setError(err.message);
      }
      renderAttachedList();
      const continueBtn = document.getElementById("librariesContinue");
      if (continueBtn) continueBtn.disabled = attachedLibraries.length === 0;
      const countEl = document.querySelector(".libs-attached-head .count");
      if (countEl) countEl.textContent = attachedLibraries.length + " ATTACHED";
    }

    function renderAttachedList() {
      const wrap = document.querySelector(".libs-attached-body");
      if (!wrap) return;
      if (attachedLibraries.length === 0) {
        wrap.innerHTML = "<div class=\"libs-empty\">// no folders attached yet · pick one below and Samo will index it on the next step</div>";
        return;
      }
      wrap.innerHTML = "";
      attachedLibraries.forEach((item) => {
        const row = document.createElement("div");
        row.className = "lib-row";
        const kind = item.kind === "mixed" ? "MIXED"
          : item.kind === "music" ? "MUSIC"
          : item.kind === "shelf" && item.mediaType === "book" ? "AUDIOBOOKS"
          : item.kind === "shelf" && item.mediaType === "podcast" ? "PODCASTS"
          : item.kind.toUpperCase();
        row.innerHTML =
          "<div class=\"lib-main\">" +
            "<div class=\"lib-name\">" + escapeHTML(item.name) + "<span class=\"kind-chip\">" + kind + "</span></div>" +
            "<div class=\"lib-path\">" + escapeHTML(item.path) + "</div>" +
          "</div>" +
          "<button class=\"btn-remove\" title=\"Remove\" data-id=\"" + escapeHTML(item.id) + "\">×</button>";
        wrap.appendChild(row);
      });
      wrap.querySelectorAll(".btn-remove").forEach((button) => {
        button.addEventListener("click", async (e) => {
          const id = e.currentTarget.getAttribute("data-id");
          try {
            await withToken("/api/v1/libraries/" + encodeURIComponent(id), { method: "DELETE" });
            await refreshLibraryList();
            await fetchStatus();
          } catch (err) { setError(err.message); }
        });
      });
    }

    function renderLibrariesStep() {
      card.innerHTML = ` + "`" + `
        <div class="card-head"><span class="caret">&gt;</span> ATTACH YOUR MEDIA</div>
        <h2>Where is your stuff?</h2>
        <p class="lede">Add one or more folders. <strong>Mixed</strong> auto-detects music vs. audiobooks per subfolder — pick that if you have a single folder with everything, or you're not sure.</p>

        <div class="libs-shell">
          <div class="libs-attached">
            <div class="libs-attached-head">
              <span class="label">ATTACHED LIBRARIES</span>
              <span class="count">0 ATTACHED</span>
            </div>
            <div class="libs-attached-body"></div>
          </div>

          <div class="libs-add">
            <div class="libs-add-head">+ ADD A FOLDER</div>
            <div class="libs-add-body">
              <div class="browser-shell">
                <div class="browser-head"><span class="label">PATH</span><span class="value">SUGGESTED LOCATIONS</span></div>
                <div class="browser-list"></div>
              </div>
              <label class="field">
                <span class="field-label">PATH</span>
                <input type="text" id="libraryPath" placeholder="/srv/media">
              </label>
              <div class="form-row-split">
                <label class="field">
                  <span class="field-label">KIND</span>
                  <select id="libraryKind">
                    <option value="mixed">MIXED (AUTO-DETECT)</option>
                    <option value="music">MUSIC ONLY</option>
                    <option value="shelf-book">AUDIOBOOKS</option>
                    <option value="shelf-podcast">PODCASTS</option>
                  </select>
                </label>
                <label class="field">
                  <span class="field-label">NAME (OPTIONAL)</span>
                  <input type="text" id="libraryName" placeholder="autodetect">
                </label>
              </div>
              <div class="actions">
                <button class="btn primary" id="libraryAdd">+ ATTACH THIS FOLDER</button>
              </div>
            </div>
          </div>

          <div class="continue-row">
            <button class="btn ghost" id="librariesContinue" disabled>CONTINUE TO SCAN &rarr;</button>
          </div>
        </div>
      ` + "`" + `;
      loadDirectories("").catch((e) => setError(e.message));
      refreshLibraryList();

      document.getElementById("libraryName").addEventListener("input", (e) => { e.target.dataset.touched = "1"; });

      document.getElementById("libraryAdd").addEventListener("click", async () => {
        const path = document.getElementById("libraryPath").value.trim();
        const name = document.getElementById("libraryName").value.trim();
        const kindSelection = document.getElementById("libraryKind").value;
        setError("");
        if (!path) return setError("pick a folder first — browse above or paste a path");
        let kind = kindSelection; let mediaType = "";
        if (kindSelection === "shelf-book") { kind = "shelf"; mediaType = "book"; }
        else if (kindSelection === "shelf-podcast") { kind = "shelf"; mediaType = "podcast"; }
        const button = document.getElementById("libraryAdd");
        button.disabled = true;
        const original = button.textContent;
        button.textContent = "ATTACHING…";
        try {
          await withToken("/api/v1/setup/libraries", {
            method: "POST",
            body: JSON.stringify({ path, name, kind, mediaType }),
          });
          // Reset the form for the next folder.
          document.getElementById("libraryPath").value = "";
          const nameField = document.getElementById("libraryName");
          nameField.value = "";
          delete nameField.dataset.touched;
          await refreshLibraryList();
          await fetchStatus();
        } catch (err) {
          setError(err.message);
        } finally {
          button.disabled = false;
          button.textContent = original;
        }
      });

      document.getElementById("librariesContinue").addEventListener("click", async () => {
        if (attachedLibraries.length === 0) {
          setError("attach at least one folder first");
          return;
        }
        await fetchStatus();
      });
    }

    /* ---------------- STEP 3 : scan ---------------- */
    function renderScanStep() {
      card.innerHTML = ` + "`" + `
        <div class="card-head"><span class="caret">&gt;</span> INDEX YOUR MEDIA</div>
        <h2>One last thing — let's build the catalog.</h2>
        <p class="lede">Samo reads each file once to learn what it has. Large libraries take a few minutes. You can skip and come back to this later from settings.</p>
        <div class="actions">
          <button class="btn primary" id="scanRun">RUN INITIAL SCAN</button>
          <button class="btn ghost" id="finishLater">SKIP FOR NOW</button>
        </div>
        <div id="scanOutput" style="margin-top: 18px;"></div>
      ` + "`" + `;
      document.getElementById("scanRun").addEventListener("click", async () => {
        const button = document.getElementById("scanRun");
        button.disabled = true;
        const original = button.textContent;
        button.textContent = "SCANNING…";
        const out = document.getElementById("scanOutput");
        out.innerHTML = "<div class=\"scan-output\">// scanning libraries…</div>";
        setError("");
        try {
          const result = await withToken("/api/v1/setup/scan", { method: "POST" });
          out.innerHTML = "<div class=\"scan-output success\">// scan complete\n" + escapeHTML(JSON.stringify(result, null, 2)) + "</div>";
          await fetchStatus();
        } catch (err) {
          out.innerHTML = "";
          setError(err.message);
        } finally {
          button.disabled = false;
          button.textContent = original;
        }
      });
      document.getElementById("finishLater").addEventListener("click", async () => {
        try {
          await withToken("/api/v1/setup/complete", { method: "POST" });
        } catch (err) { setError(err.message); return; }
        window.location.href = "/";
      });
    }

    function renderDoneStep() {
      card.innerHTML = ` + "`" + `
        <div class="card-head"><span class="caret">&gt;</span> READY</div>
        <h2>Samo is live.</h2>
        <p class="lede">Catalog seeded. Your token is stored in this browser — clear site data to sign out. Open the dashboard to start listening.</p>
        <div class="actions">
          <a class="btn primary" href="/">OPEN DASHBOARD &rarr;</a>
        </div>
      ` + "`" + `;
    }

    function render() {
      if (!state) return;
      switch (state.currentStep) {
        case "admin": renderAdminStep(); break;
        case "libraries": renderLibrariesStep(); break;
        case "scan": renderScanStep(); break;
        default: renderDoneStep();
      }
    }

    fetchStatus().catch((err) => {
      card.innerHTML = "<div class=\"card-head\"><span class=\"caret\">&gt;</span> ERROR</div><h2>Setup unavailable</h2><p class=\"lede\">" + escapeHTML(err.message) + "</p>";
    });
  })();
  </script>
</body>
</html>`
