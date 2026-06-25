package api

import (
	"html/template"
	"log"
	"net/http"
)

// loginPage serves the sign-in screen. If the server is still in setup mode
// the page redirects to the wizard so users see one onboarding flow at a
// time.
func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	status, err := s.computeSetupStatus(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if status.NeedsSetup {
		http.Redirect(w, r, "/setup", http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := loginTemplate.Execute(w, nil); err != nil {
		log.Printf("failed to render login page: %v", err)
	}
}

var loginTemplate = template.Must(template.New("login").Parse(loginHTML))

const loginHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>SAMO SERVER · SIGN IN</title>
  <link href="https://fonts.googleapis.com/css2?family=Young+Serif&display=swap" rel="stylesheet">
  <link rel="icon" type="image/png" href="/favicon-dark.png">
  <link rel="icon" type="image/png" href="/favicon-light.png" media="(prefers-color-scheme: light)">
  <link rel="icon" type="image/png" href="/favicon-dark.png" media="(prefers-color-scheme: dark)">
  <style>` + samoBaseCSS + `</style>
  <style>
    body {
      display: grid;
      align-items: center;
    }
    main.login-main {
      display: grid;
      grid-template-columns: 1fr 400px;
      gap: 60px;
      align-items: center;
      width: 100%;
      max-width: 1080px;
      margin: 0 auto;
    }
    @media (max-width: 720px) {
      main.login-main { grid-template-columns: 1fr; gap: 40px; padding: 32px 24px; }
    }
    .login-hero { display: grid; gap: 16px; }
    .login-shell {
      display: grid;
      gap: 6px;
    }
    .login-shell h1 {
      margin: 0 0 4px;
      font-family: var(--serif);
      font-size: 2.2rem;
      font-weight: 400;
      letter-spacing: -0.01em;
    }
    .login-shell p.lede {
      margin: 0 0 24px;
      color: var(--muted);
      font-family: var(--sans);
      line-height: 1.5;
    }
    .footer-note {
      margin-top: 22px;
      font-family: var(--mono);
      font-size: 0.72rem;
      letter-spacing: 0.06em;
      color: var(--muted);
      line-height: 1.6;
    }
    .footer-note a { color: var(--accent); }
  </style>
</head>
<body>
  <div class="grid-bg"></div>
  <main class="login-main">
    <section class="login-hero">
      <div class="samo-wm hero"><span class="word">SAMO</span><span class="word dim">SERVER</span></div>
      <div class="samo-status pulse"><span class="dot"></span><span>ONLINE · AWAITING SIGN IN</span></div>
    </section>
    <section class="card login-shell">
      <div class="card-head"><span class="caret">&gt;</span> SIGN IN</div>
      <h1>Welcome back.</h1>
      <p class="lede">Use the username and password you created during setup.</p>
      <label class="field">
        <span class="field-label">USERNAME</span>
        <input type="text" id="username" autocomplete="username" autofocus>
      </label>
      <label class="field">
        <span class="field-label">PASSWORD</span>
        <input type="password" id="password" autocomplete="current-password">
      </label>
      <div class="actions">
        <button class="btn primary" id="submit">SIGN IN &rarr;</button>
      </div>
      <div class="footer-note">// forgot your password? sign back in as another admin and reset from settings,<br>// or re-run setup with SAMO_BOOTSTRAP_PASSWORD set to override</div>
    </section>
  </main>
  <script>
  (function () {
    const tokenKey = "samo-token";

    // Resolve where to land after sign-in. The /app shell appends
    // ?next=<encoded path+hash> when it bounces a logged-out user here, so
    // deep links like /app#audiobooks survive the round-trip.
    function nextDestination() {
      try {
        const params = new URLSearchParams(window.location.search);
        const next = params.get("next");
        if (next && next.startsWith("/")) return next;
      } catch (err) { /* ignore */ }
      return "/app";
    }
    const destination = nextDestination();

    if (localStorage.getItem(tokenKey)) {
      // Confirm the stored token still works; if it does, skip the form.
      fetch("/api/v1/users/me", { headers: { "Authorization": "Bearer " + localStorage.getItem(tokenKey) } })
        .then((res) => { if (res.ok) window.location.href = destination; })
        .catch(() => {});
    }

    function setError(message) {
      const card = document.querySelector(".login-shell");
      const existing = card.querySelector(".error-line");
      if (existing) existing.remove();
      if (!message) return;
      const div = document.createElement("div");
      div.className = "error-line";
      div.textContent = "× " + message;
      card.appendChild(div);
    }

    async function submit() {
      const username = document.getElementById("username").value.trim();
      const password = document.getElementById("password").value;
      setError("");
      if (!username || !password) return setError("username and password required");
      const button = document.getElementById("submit");
      button.disabled = true;
      const original = button.textContent;
      button.textContent = "SIGNING IN…";
      try {
        const res = await fetch("/api/v1/auth/login", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ username, password }),
        });
        const body = await res.json().catch(() => ({}));
        if (!res.ok) throw new Error(body.error || "sign in failed");
        localStorage.setItem(tokenKey, body.token);
        window.location.href = destination;
      } catch (err) {
        setError(err.message);
        button.disabled = false;
        button.textContent = original;
      }
    }

    document.getElementById("submit").addEventListener("click", submit);
    document.getElementById("password").addEventListener("keydown", (e) => { if (e.key === "Enter") submit(); });
    document.getElementById("username").addEventListener("keydown", (e) => { if (e.key === "Enter") submit(); });
  })();
  </script>
</body>
</html>`
