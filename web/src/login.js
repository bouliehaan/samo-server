// Entry point for the login page. Vite bundles this into
// internal/api/web/build/, which go:embed compiles into the binary.
//
// The stylesheet is imported rather than linked so the bundler owns it too:
// it emits one hashed .css per entry alongside the .js. base.css comes in
// via an @import at the top of that stylesheet, which keeps it first in the
// cascade — as a separate shared chunk its order would not be guaranteed.
import "./login.css";

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
  