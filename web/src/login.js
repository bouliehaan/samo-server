// Entry point for the login page. Vite bundles this into
// internal/api/web/build/, which go:embed compiles into the binary.
//
// The stylesheet is imported rather than linked so the bundler owns it too:
// it emits one hashed .css per entry alongside the .js. base.css comes in
// via an @import at the top of that stylesheet, which keeps it first in the
// cascade — as a separate shared chunk its order would not be guaranteed.
import "./login.css";
import { loginDestination } from "./ui/destination.js";

  (function () {
    const tokenKey = "samo-token";
    // With single sign-on configured the server sends /login straight to the
    // provider, so this page is drawn only for ?local=1 (the way in when the
    // provider is down) and for a sign-in coming back: ?sso=done with the
    // hand-off in the fragment, or ?sso_error= with the reason it failed.
    const query = new URLSearchParams(window.location.search);
    const fragment = new URLSearchParams(window.location.hash.slice(1));

    function nextDestination(user, next) {
      return loginDestination(user, next === undefined ? query.get("next") : next, window.location.origin);
    }

    if (fragment.has("sso") || fragment.has("sso_error")) {
      // Out of the address bar and history before anything else happens.
      history.replaceState(null, "", window.location.pathname + window.location.search);
    }

    if (fragment.has("sso")) {
      completeSSO(fragment.get("sso"));
    } else if (localStorage.getItem(tokenKey)) {
      // Confirm the stored token still works; if it does, skip the form.
      fetch("/api/v1/users/me", { headers: { "Authorization": "Bearer " + localStorage.getItem(tokenKey) } })
        .then(async (res) => { if (res.ok) window.location.href = nextDestination(await res.json()); })
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

    // The server kept where the sign-in was headed (it can be /pair#code=…,
    // a TV's pairing code) and hands it back with the bearer.
    async function completeSSO(code) {
      try {
        const res = await fetch("/api/v1/auth/oidc/complete", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ code }),
        });
        const body = await res.json().catch(() => ({}));
        if (!res.ok) throw new Error(body.error || "sign in failed");
        localStorage.setItem(tokenKey, body.token);
        window.location.href = nextDestination(body.user, body.next || null);
      } catch (err) {
        setError(err.message);
      }
    }

    // Offer the identity provider's button when the server has one.
    fetch("/api/v1/auth/providers")
      .then((res) => (res.ok ? res.json() : null))
      .then((providers) => {
        if (!providers || !providers.oidc) return;
        const button = document.getElementById("ssoButton");
        button.textContent = "SIGN IN WITH " + String(providers.oidc.label || "SSO").toUpperCase() + " \u2192";
        const start = providers.oidc.startUrl || "/auth/oidc/start";
        button.href = query.get("next") ? start + "?next=" + encodeURIComponent(query.get("next")) : start;
        document.getElementById("ssoBlock").hidden = false;
      })
      .catch(() => {});

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
        window.location.href = nextDestination(body.user);
      } catch (err) {
        setError(err.message);
        button.disabled = false;
        button.textContent = original;
      }
    }

    if (query.has("sso_error") || fragment.has("sso_error")) setError(query.get("sso_error") || fragment.get("sso_error"));

    document.getElementById("submit").addEventListener("click", submit);
    document.getElementById("password").addEventListener("keydown", (e) => { if (e.key === "Enter") submit(); });
    document.getElementById("username").addEventListener("keydown", (e) => { if (e.key === "Enter") submit(); });
  })();
  