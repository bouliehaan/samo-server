// Who we are and how we talk to the server.
//
// Everything below api() in the dependency order — every view, every action —
// goes through it, which is why it is worth having in one small file: the
// bearer header, the 401 bounce, and the JSON conventions are decided once.

export const tokenKey = "samo-token";

// The Last.fm callback used to leave its pending token under a single global
// key. It is read only to migrate a value left by an older build; nothing
// writes it any more.
export const legacyLastFMPendingKey = "samo-lastfm-token";

// Read once at load. The login page is what writes it, and it navigates, so
// there is no in-session change to observe here.
export const token = localStorage.getItem(tokenKey) || "";

// The signed-in user, or null before /users/me answers.
//
// Exported as a live binding so readers just use `currentUser`; writers go
// through setCurrentUser, because an imported binding is read-only at the
// import site.
export let currentUser = null;

export function setCurrentUser(user) {
  currentUser = user;
}

export function isAdmin() {
  return currentUser && currentUser.role === "admin";
}

export function lastFMPendingStorageKey() {
  const userID = currentUser && currentUser.id ? currentUser.id : "anonymous";
  return legacyLastFMPendingKey + ":" + userID;
}

export function loginRedirect() {
  // Preserve the deep-link the user was trying to reach so login can
  // bounce them back to /app#audiobooks or wherever they came from.
  const next = encodeURIComponent(window.location.pathname + window.location.hash);
  window.location.href = "/login?next=" + next;
}

// api is the single fetch wrapper: bearer header, JSON in and out, and a 401
// that clears the stored token and bounces to /login rather than surfacing as
// a thousand different errors at a thousand call sites.
export async function api(path, options) {
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
