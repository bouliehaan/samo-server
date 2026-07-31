// HTML building blocks.
//
// escapeHTML and attr are the only thing between a track title and an
// injected <script>, so everything interpolated into innerHTML goes through
// one of them. Keeping them in one small module is what makes that
// reviewable.

export function escapeHTML(value) {
  return String(value == null ? "" : value).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", "\"": "&quot;" }[c]));
}

export function attr(value) {
  return escapeHTML(value).replace(/'/g, "&#39;");
}

export function tagsLine(tags) {
  if (!tags || tags.length === 0) return "";
  return '<div class="tag-line">' + tags.slice(0, 8).map((tag) => '<span class="meta-chip">' + escapeHTML(tag) + '</span>').join("") + '</div>';
}

export function splitTags(raw) {
  return String(raw || "").split(",").map((tag) => tag.trim()).filter(Boolean);
}

export function progressBar(state, duration) {
  const progress = state && state.progressSeconds ? state.progressSeconds : 0;
  const pct = duration > 0 ? Math.min(100, Math.round((progress / duration) * 100)) : 0;
  return '<div class="progress-track"><div class="bar" style="width:' + pct + '%"></div></div>';
}

export function setStatus(text) {
  const el = document.getElementById("barStatusText");
  if (el) el.textContent = text;
}

export function setMessage(id, message, bad) {
  const el = document.getElementById(id);
  if (!el) return;
  el.className = "status-line " + (bad ? "bad" : "good");
  el.textContent = "// " + message;
  el.hidden = false;
}
