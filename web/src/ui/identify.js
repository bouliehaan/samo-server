// The identify modal: search a metadata provider for a better match to an
// audiobook or podcast, then apply the chosen candidate.
//
// identifyContext is what the modal is currently about (kind + id); the
// candidates are the last search's results, held so the apply step can look
// one up by index without re-querying the provider.

import { api } from "./auth.js";
import { identifyModal, identifyQuery, identifyResults, identifyTitle } from "./elements.js";
import { attr, escapeHTML } from "./html.js";
import { candidateFeedURL } from "./labels.js";

/* ---- Identify modal ---------------------------------------------------
 * Fronts /api/v1/metadata/search (OpenLibrary, Google Books, Apple
 * Podcasts) and /api/v1/metadata/apply. The same modal serves both
 * audiobooks (kind=audiobook → ApplyTargetAudiobook) and podcast shows
 * (kind=podcast → ApplyTargetPodcast). Music tracks/albums could be
 * wired in by adding a third button, but the user's complaint was
 * specifically about audiobooks/podcasts. */
export let identifyContext = null;

export let identifyCandidates = [];

export async function openIdentifyModal(kind, id, title, author) {
  identifyContext = { kind, id };
  identifyCandidates = [];
  identifyTitle.textContent = title || "Identify";
  identifyQuery.value = [title, author].filter(Boolean).join(" ");
  identifyResults.innerHTML = '<div class="boot-line">// type a query and search</div>';
  identifyModal.hidden = false;
  identifyQuery.focus();
  if (identifyQuery.value.trim()) await runIdentifySearch();
}

export function closeIdentifyModal() {
  identifyModal.hidden = true;
  identifyContext = null;
  identifyCandidates = [];
  identifyResults.innerHTML = "";
}

export async function runIdentifySearch() {
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

export function identifyResultRow(candidate, idx) {
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
