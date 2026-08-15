// The tier list: every show the station can owe you, ranked in one place.
//
// Tier was already the strongest dial the scheduler has — one step up outranks
// the entire recency range, so an S-tier episode from this morning goes to air
// before a B-tier one published ten minutes ago — and the only way to set it
// was a dropdown inside a per-source composer. Ranking a dozen podcasts meant
// twelve round trips through a form, each one showing you a single letter with
// nothing to compare it against. Ranking is a comparison; it needs the whole
// field on screen at once.
//
// So: the tier list. Bands top to bottom, shows as cards you drag between
// them. Nothing here is a new concept — a card's row IS config.tier on that
// source, which is what the queue has always sorted on.

import { attr, escapeHTML } from "./html.js";
import { podcastTitle } from "./labels.js";
import { TIER_CHOICES } from "./plan.js";
import { podcastCoverURL } from "./stream.js";

// What each band means, in the terms the queue actually uses. Keyed off
// TIER_CHOICES so the two lists cannot drift apart.
const TIER_NOTE = {
  S: "goes first, whatever else is owed",
  A: "ahead of most things",
  B: "above the middle",
  C: "where everything starts",
  D: "below the middle",
  E: "behind most things",
  F: "last — only if nothing else is owed",
};

export const RANK_TIERS = TIER_CHOICES.map(([value]) => value);

// rankableSource: does a tier do anything for this source?
//
// Tier orders OBLIGATIONS, and only a source whose items carry a publication
// date can owe you one (refreshObligations skips the rest). A playlist in a
// tier list would be a control wired to nothing, so the panel says which
// sources it left out rather than showing dials that do not turn.
export function rankableSource(src) {
  if (!src) return false;
  const traits = ((src.config || {}).traits) || {};
  if (typeof traits.supportsFreshness === "boolean") return traits.supportsFreshness;
  return src.kind === "podcast-subscription";
}

export function sourceTier(src) {
  const raw = String(((src || {}).config || {}).tier || "").trim().toUpperCase();
  return RANK_TIERS.includes(raw) ? raw : "";
}

// rankPanel renders the whole surface. `ui.picked` is the card lifted by a
// click (the tap-to-move path that drag and drop cannot serve on a touch
// screen); `ui.surfacings` is the plan's per-tier airing count, shown on the
// band it belongs to because "S" meaning "aired twice" is a fact about the
// tier, not about the plan document it happens to be stored in.
export function rankPanel(sources, names, ui) {
  ui = ui || {};
  const all = sources || [];
  const rankable = all.filter(rankableSource);
  const skipped = all.length - rankable.length;

  const byTier = {};
  RANK_TIERS.forEach((tier) => { byTier[tier] = []; });
  const unplaced = [];
  rankable.forEach((src) => {
    const tier = sourceTier(src);
    if (tier) byTier[tier].push(src);
    else unplaced.push(src);
  });

  // Alphabetical inside a band, and it has to be said out loud: within a tier
  // the queue takes the newest episode, so where a card sits in its row is not
  // a finer-grained ranking. Sorting by name at least keeps a card from
  // jumping somewhere else the moment you drop it.
  const byName = (a, b) =>
    String(names[a.id] || "").localeCompare(String(names[b.id] || ""), undefined, { sensitivity: "base" });
  RANK_TIERS.forEach((tier) => { byTier[tier].sort(byName); });
  unplaced.sort(byName);

  if (rankable.length === 0) {
    return '<div class="panel panel-wide">' +
      rankHead(0) +
      '<div class="empty-state">// nothing to rank — tier orders what the station owes you, ' +
        'and only podcast subscriptions publish episodes it can owe. Add one from THE MIX.</div>' +
    '</div>';
  }

  const rows = RANK_TIERS.map((tier) =>
    tierRow(tier, byTier[tier], names, ui, (ui.surfacings || {})[tier] || 0)).join("");

  return '<div class="panel panel-wide">' +
    rankHead(rankable.length) +
    '<div class="panel-sub">Drag a show into a band — it saves as you drop it. ' +
      'Tier is the strongest dial the station has: one step up outranks the whole recency range, ' +
      'so an S-tier episode from this morning airs before a B-tier one published ten minutes ago. ' +
      'Inside a band the newest episode goes first, so a card\'s position in its row means nothing.</div>' +
    (ui.picked ? '<div class="rank-armed">// ' + escapeHTML(names[ui.picked] || "that show") +
      ' is lifted — click a band to drop it, or click it again to put it back</div>' : "") +
    '<div class="tier-list' + (ui.picked ? " armed" : "") + '">' +
      rows +
      unplacedRow(unplaced, names, ui) +
    '</div>' +
    (skipped > 0
      ? '<div class="rank-foot">// ' + skipped + ' other source' + (skipped === 1 ? " is" : "s are") +
        ' not here. Tier orders new episodes, and only a source that publishes them — a podcast — can be owed one. ' +
        'A playlist or a folder is reached through the plan, not through this queue.</div>'
      : "") +
  '</div>';
}

function rankHead(count) {
  return '<div class="panel-head"><span>// RANK</span><span>' +
    (count ? count + ' SHOW' + (count === 1 ? "" : "S") : "") + '</span></div>';
}

function tierRow(tier, sources, names, ui, surfacings) {
  const note = TIER_NOTE[tier] || "";
  const airings = surfacings > 1 ? surfacings + "× before it counts as heard" : "";
  return '<div class="tier-row tier-' + attr(tier) + '" data-action="rank-drop" data-tier="' + attr(tier) + '">' +
    '<div class="tier-key"><span class="tier-letter">' + escapeHTML(tier) + '</span>' +
      (airings ? '<span class="tier-airings">' + escapeHTML(airings) + '</span>' : '') +
    '</div>' +
    '<div class="tier-slots">' +
      (sources.length === 0
        ? '<span class="tier-empty">' + escapeHTML(note) + '</span>'
        : sources.map((src) => cardHTML(src, names, ui)).join("")) +
    '</div>' +
  '</div>';
}

// The bottom tray. Everything starts here rather than pre-filled into C:
// "nobody has said" and "somebody said C" are the same to the scheduler but
// not to the person doing the ranking, and a tray that empties as you work is
// the only thing on this screen that says how much is left to do.
function unplacedRow(sources, names, ui) {
  return '<div class="tier-row tier-unplaced" data-action="rank-drop" data-tier="">' +
    '<div class="tier-key"><span class="tier-letter">—</span>' +
      '<span class="tier-airings">unranked · treated as C</span>' +
    '</div>' +
    '<div class="tier-slots">' +
      (sources.length === 0
        ? '<span class="tier-empty">everything has been placed</span>'
        : sources.map((src) => cardHTML(src, names, ui)).join("")) +
    '</div>' +
  '</div>';
}

function cardHTML(src, names, ui) {
  const name = names[src.id] || "Untitled";
  const cfg = src.config || {};
  const podcast = (ui.podcasts || {})[cfg.podcastId || ""];
  const cover = cfg.podcastId ? podcastCoverURL(cfg.podcastId) : "";
  const sub = podcast ? podcastTitle(podcast) : "";
  const classes = ["tier-card"];
  if (ui.picked === src.id) classes.push("picked");
  if (!src.enabled) classes.push("off");
  // The glyph is always in the markup and the cover is layered over it, so a
  // show whose artwork 404s — which is most of them until a feed has been
  // read — falls back to a mark rather than to an empty grey square.
  return '<div class="' + classes.join(" ") + '" draggable="true"' +
    ' data-action="rank-card" data-source-id="' + attr(src.id) + '"' +
    ' title="' + attr(name + (sub && sub !== name ? " · " + sub : "")) + '">' +
    '<div class="tier-card-art">' +
      '<span class="tier-card-glyph">&#9835;</span>' +
      (cover ? '<span class="tier-card-cover" style="background-image:url(&quot;' + attr(cover) + '&quot;)"></span>' : '') +
      (src.enabled ? '' : '<span class="tier-card-off">OFF</span>') +
    '</div>' +
    '<span class="tier-card-name">' + escapeHTML(name) + '</span>' +
  '</div>';
}
