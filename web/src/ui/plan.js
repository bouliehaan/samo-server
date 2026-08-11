// The station-building surface: pools, blocks, categories and separation,
// plus the panel that answers "why the hell did it play that".
//
// This edits the SAME generic concepts the scheduler runs on. There is nothing
// in here about talk, music, mornings or podcasts — a station is whatever
// categories, pools and blocks its owner writes down, and the editor has to
// speak that language or the backend model is a lie.

import { attr, escapeHTML } from "./html.js";
import { composerHTML, fieldHTML } from "./composer.js";

const WEEKDAY_CHOICES = [
  ["*", "EVERY DAY"],
  ["mon-fri", "WEEKDAYS (MON–FRI)"],
  ["sat,sun", "WEEKENDS"],
  ["mon", "MONDAY"],
  ["tue", "TUESDAY"],
  ["wed", "WEDNESDAY"],
  ["thu", "THURSDAY"],
  ["fri", "FRIDAY"],
  ["sat", "SATURDAY"],
  ["sun", "SUNDAY"],
];

const START_CHOICES = [
  ["makeNext", "MAKE NEXT — let the current item finish"],
  ["startImmediately", "START IMMEDIATELY — cut in on the minute"],
  ["waitUpTo", "WAIT UP TO — finish the item, or cut it off past the grace"],
];

const WANT_CHOICES = [
  ["fill", "ORDINARY PROGRAMMING"],
  ["obligation", "SOMETHING NEW — from what the station owes"],
  ["break", "A BREAK"],
];

// Exposure is offered as a few named positions rather than a free number: it is
// a judgement about whether a slot reaches anybody, and 0.37 does not mean
// anything a person can defend.
const EXPOSURE_CHOICES = [
  ["", "FOLLOW THE LISTENING DAY"],
  ["1", "COUNTS FULLY — airing here reaches you"],
  ["0.5", "COUNTS HALF"],
  ["0", "COUNTS FOR NOTHING — overnight, empty room"],
];

export const TIER_CHOICES = [
  ["S", "S — surface the moment it lands"],
  ["A", "A"],
  ["B", "B"],
  ["C", "C — the default"],
  ["D", "D"],
  ["E", "E"],
  ["F", "F — only if nothing else is owed"],
];

function selectHTML(id, choices, selected, label) {
  const options = choices.map(([value, text]) =>
    '<option value="' + attr(value) + '"' + (String(value) === String(selected || "") ? " selected" : "") + '>' +
      escapeHTML(text) + '</option>').join("");
  return '<label class="field"><span class="field-label">' + escapeHTML(label) + '</span>' +
    '<select id="' + attr(id) + '">' + options + '</select></label>';
}

function checkboxHTML(id, label, checked) {
  return '<label class="field field-check"><span class="field-label">' + escapeHTML(label) + '</span>' +
    '<input id="' + attr(id) + '" type="checkbox"' + (checked ? " checked" : "") + '></label>';
}

// ---- the plan panel ----------------------------------------------------

export function planPanel(view, sources, channelID, unreachable) {
  const plan = (view && view.plan) || {};
  const custom = Boolean(view && view.custom);
  const categories = plan.categories || [];
  const pools = plan.pools || [];
  const blocks = plan.blocks || [];

  const origin = custom
    ? 'This channel runs a plan you wrote.'
    : 'This channel has no saved plan, so it is running the one its sources and booked slots already describe. Any edit below saves it as your own.';

  // Content the station cannot reach. This has to shout: from every other
  // screen the source reads ENABLED and its episodes read as owed, and it will
  // never play a second of it.
  const orphans = (unreachable || []).length === 0 ? "" :
    '<div class="sched-status-warn">' +
      '// no pool can reach ' + escapeHTML((unreachable || []).join(", ")) +
      ' — the station will never play ' +
      ((unreachable || []).length === 1 ? 'it' : 'them') + '. ' +
      '<button class="btn primary btn-mini" data-action="plan-pools-repair" data-id="' + attr(channelID) + '">' +
        'FIX: MATCH POOLS BY CATEGORY</button>' +
    '</div>';

  return '<div class="panel panel-wide">' +
    orphans +
    '<div class="panel-head"><span>// STATION PLAN</span><span>' +
      '<button class="btn ghost btn-mini" data-action="plan-json-toggle">JSON</button> ' +
      (custom ? '<button class="btn danger btn-mini" data-action="plan-reset" data-id="' + attr(channelID) + '">REVERT TO DERIVED</button>' : '') +
    '</span></div>' +
    '<div class="panel-sub">' + escapeHTML(origin) + '</div>' +
    categoriesSection(categories) +
    poolsSection(pools, sources) +
    blocksSection(blocks, plan) +
    behaviourSection(plan) +
    blockComposer(plan) +
    poolComposer(plan, sources) +
    categoryComposer() +
    jsonComposer(plan) +
  '</div>';
}

// Categories are the station's own vocabulary for what kind of programming
// something is. The engine never compares one to a literal, so a station can
// run comedy, audiobook and old-time-radio as easily as talk and music.
function categoriesSection(categories) {
  const rows = categories.length === 0
    ? '<div class="empty-state">// no categories — a plan needs at least one</div>'
    : '<div class="list">' + categories.map((category, index) => {
        const target = Math.round((Number(category.target) || 0) * 100);
        return '<div class="list-row">' +
          '<div class="num">' + target + '%</div>' +
          '<div class="main"><div class="name">' + escapeHTML(category.label || category.id) + '</div>' +
          '<div class="meta">' + escapeHTML(category.id) + ' · target ' + target + '% of airtime</div></div>' +
          '<div class="actions">' +
            '<button class="btn ghost btn-mini" data-action="plan-category-edit" data-index="' + index + '">EDIT</button>' +
            '<button class="btn danger btn-mini" data-action="plan-category-delete" data-index="' + index + '">REMOVE</button>' +
          '</div>' +
        '</div>';
      }).join("") + '</div>';

  return '<div class="plan-section">' +
    '<div class="panel-head"><span>// CATEGORIES</span>' +
      '<button class="btn ghost btn-mini" data-action="plan-category-new">+ CATEGORY</button>' +
    '</div>' +
    '<div class="panel-sub">What kinds of programming this station has, and what share of airtime each should get. ' +
      'Targets are relative — they do not have to add up to 100.</div>' +
    rows +
  '</div>';
}

// Pools are reusable groupings of content. Blocks reference pools, never
// sources directly, which is what stops a daypart being welded to one podcast.
function poolsSection(pools, sources) {
  const lookup = {};
  (sources || []).forEach((src) => { lookup[src.id] = src; });
  const rows = pools.length === 0
    ? '<div class="empty-state">// no pools — add one and put some of your sources in it</div>'
    : '<div class="list">' + pools.map((pool, index) => {
        // A matched pool is a live rule, so it says what it selects rather than
        // listing a snapshot that goes stale the moment you add anything.
        if (pool.match) {
          const rule = [
            pool.match.category ? "category " + pool.match.category : "",
            pool.match.role ? "role " + pool.match.role : "",
            pool.match.kind ? "kind " + pool.match.kind : "",
          ].filter(Boolean).join(" · ");
          const matched = (sources || []).filter((src) =>
            (!pool.match.category || (src.config || {}).category === pool.match.category ||
              (!(src.config || {}).category && defaultCategoryForRole(src.role) === pool.match.category)) &&
            (!pool.match.role || src.role === pool.match.role) &&
            (!pool.match.kind || src.kind === pool.match.kind)).length;
          return '<div class="list-row">' +
            '<div class="num">' + matched + '</div>' +
            '<div class="main"><div class="name">' + escapeHTML(pool.label || pool.id) + '</div>' +
            '<div class="meta">' + escapeHTML(pool.id) + ' · everything matching ' + escapeHTML(rule) +
              ' — new content joins automatically</div></div>' +
            '<div class="actions">' +
              '<button class="btn ghost btn-mini" data-action="plan-pool-edit" data-index="' + index + '">EDIT</button>' +
              '<button class="btn danger btn-mini" data-action="plan-pool-delete" data-index="' + index + '">REMOVE</button>' +
            '</div>' +
          '</div>';
        }
        const members = (pool.sourceIds || []).map((id) => {
          const src = lookup[id];
          return src ? (src.label || src.kind) : id;
        });
        const summary = members.length === 0 ? "empty" : members.join(", ");
        return '<div class="list-row">' +
          '<div class="num">' + (pool.sourceIds || []).length + '</div>' +
          '<div class="main"><div class="name">' + escapeHTML(pool.label || pool.id) + '</div>' +
          '<div class="meta">' + escapeHTML(pool.id) + ' · ' + escapeHTML(summary) + '</div></div>' +
          '<div class="actions">' +
            '<button class="btn ghost btn-mini" data-action="plan-pool-edit" data-index="' + index + '">EDIT</button>' +
            '<button class="btn danger btn-mini" data-action="plan-pool-delete" data-index="' + index + '">REMOVE</button>' +
          '</div>' +
        '</div>';
      }).join("") + '</div>';

  return '<div class="plan-section">' +
    '<div class="panel-head"><span>// CONTENT POOLS</span>' +
      '<button class="btn ghost btn-mini" data-action="plan-pool-new">+ POOL</button>' +
    '</div>' +
    '<div class="panel-sub">Reusable sets of content. Pools may overlap freely — "everything" and "just the music" ' +
      'are both useful groupings of the same sources.</div>' +
    rows +
  '</div>';
}

// A block is what the station IS for a stretch. Entry can be a clock time, a
// handover from another block, a condition, or a combination.
function blocksSection(blocks, plan) {
  const rows = blocks.length === 0
    ? '<div class="empty-state">// no blocks — a plan needs at least a default one</div>'
    : '<div class="list">' + blocks.map((block, index) => {
        const tag = block.default ? "DEF" : (block.enter && block.enter.hard ? "HARD" : String(index + 1).padStart(2, "0"));
        return '<div class="list-row">' +
          '<div class="num">' + escapeHTML(tag) + '</div>' +
          '<div class="main"><div class="name">' + escapeHTML(block.label || block.id) + '</div>' +
          '<div class="meta">' + escapeHTML(blockSummary(block, plan)) + '</div></div>' +
          '<div class="actions">' +
            '<button class="btn ghost btn-mini" data-action="plan-block-move" data-index="' + index + '" data-dir="up">↑</button>' +
            '<button class="btn ghost btn-mini" data-action="plan-block-move" data-index="' + index + '" data-dir="down">↓</button>' +
            '<button class="btn ghost btn-mini" data-action="plan-block-edit" data-index="' + index + '">EDIT</button>' +
            '<button class="btn danger btn-mini" data-action="plan-block-delete" data-index="' + index + '">REMOVE</button>' +
          '</div>' +
        '</div>';
      }).join("") + '</div>';

  return '<div class="plan-section">' +
    '<div class="panel-head"><span>// PROGRAMMING BLOCKS</span>' +
      '<button class="btn ghost btn-mini" data-action="plan-block-new">+ BLOCK</button>' +
    '</div>' +
    '<div class="panel-sub">A block says what the station is for a stretch of the day. Anchor one to a clock time, ' +
      'or to <strong>after</strong> another block — anchor the follow-ons and moving a show moves everything after it.</div>' +
    rows +
  '</div>';
}

export function blockSummary(block, plan) {
  const parts = [];
  const enter = block.enter || {};
  if (block.default) parts.push("default — where everything falls back to");
  if (enter.at) {
    parts.push((enter.hard ? "booked " : "from ") + enter.at + (enter.days && enter.days !== "*" ? " " + enter.days : ""));
    if (enter.hard) parts.push(enter.start || "makeNext");
  }
  if (enter.after) parts.push("after " + blockLabelFor(plan, enter.after));
  if (enter.when) parts.push("when " + enter.when);

  const exit = block.exit || {};
  if (exit.at) parts.push("until " + exit.at);
  if (exit.duration) parts.push("for " + exit.duration);
  if (exit.count) parts.push(exit.count + " items");
  if (exit.atNextAnchor) parts.push("until the next booked slot");
  if (exit.when) parts.push("until " + exit.when);
  if (block.next) parts.push("→ " + blockLabelFor(plan, block.next));

  const pools = (block.pools || []).map((ref) => ref.pool + (ref.weight && ref.weight !== 1 ? "×" + ref.weight : ""));
  if (pools.length > 0) parts.push("plays " + pools.join(" + "));

  const limits = block.limits || {};
  (limits.maxUnbroken || []).forEach((limit) => {
    parts.push("max " + limit.max + " unbroken " + limit.category);
  });
  (limits.minUnbroken || []).forEach((run) => {
    parts.push("min " + run.min + " of " + run.category + " once started");
  });
  if ((block.pattern || []).length > 0) {
    parts.push("cycle: " + block.pattern.map((step) => step.want).join(" → "));
  }
  if (block.breaks) {
    const target = block.breaks.target || {};
    parts.push("breaks ≈ " + (target.duration || (target.items || "?") + " items"));
  }
  if (block.exposure != null) {
    parts.push(block.exposure === 0
      ? "airing something new here counts for nothing"
      : "new content counts " + Math.round(block.exposure * 100) + "%");
  }
  return parts.join(" · ");
}

function blockLabelFor(plan, id) {
  const match = (plan.blocks || []).find((block) => block.id === id);
  return match ? (match.label || match.id) : id;
}

function behaviourSection(plan) {
  const sep = plan.separation || {};
  const sel = plan.selection || {};
  const horizons = plan.horizons || {};
  return '<div class="plan-section">' +
    '<div class="panel-head"><span>// BEHAVIOUR</span>' +
      '<button class="btn ghost btn-mini" data-action="plan-behaviour-edit">EDIT</button>' +
    '</div>' +
    '<div class="panel-sub">How far apart the same thing may be repeated, how far back the balance is measured, ' +
      'and how much the station is allowed to surprise you.</div>' +
    '<div class="list">' +
      behaviourRow("SEPARATION", "same item " + (sep.item || "8h") +
        " · same source " + (sep.source || "45m") +
        " · same person " + (sep.creator || "90m") +
        " · same family " + (sep.family || "45m"),
        "Two shows with the same host, back to back, is not variety — so the person is a separate rule from the source.") +
      behaviourRow("MEMORY", "balance over " + (horizons.balance || "6h") + " · repeats looked up over " + (horizons.rerun || "720h"),
        "Airtime is measured by overlap, so a long item that started before the window still counts for the part inside it.") +
      behaviourRow("SURPRISE", "top " + Math.round((sel.epsilon != null ? sel.epsilon : 0.15) * 100) + "% of scores compete · search depth " + (sel.searchDepth || 200),
        "Always taking the highest score makes a station predictable. Randomness happens after the rules, never instead of them.") +
    '</div>' +
  '</div>';
}

function behaviourRow(name, value, hint) {
  return '<div class="list-row">' +
    '<div class="num">' + escapeHTML(name.slice(0, 3)) + '</div>' +
    '<div class="main"><div class="name">' + escapeHTML(value) + '</div>' +
    '<div class="meta">' + escapeHTML(hint) + '</div></div>' +
  '</div>';
}

// ---- the editors -------------------------------------------------------

function blockComposer(plan) {
  const categories = plan.categories || [];
  const pools = plan.pools || [];
  const blockChoices = [["", "— none —"]].concat((plan.blocks || []).map((block) => [block.id, block.label || block.id]));

  const poolRows = pools.length === 0
    ? '<div class="empty-state">// add a pool first — a block plays pools, not sources</div>'
    : '<div class="plan-grid">' + pools.map((pool, index) =>
        '<div class="plan-grid-row">' +
          checkboxHTML("planBlockPool" + index, pool.label || pool.id, false) +
          fieldHTML("planBlockPoolWeight" + index, "weight", "1", "number", "1") +
        '</div>').join("") + '</div>';

  const categoryRows = categories.length === 0
    ? ""
    : '<div class="plan-grid">' +
        '<div class="plan-grid-head">Per category, only while this block is on air</div>' +
        categories.map((category, index) =>
        '<div class="plan-grid-row">' +
          '<span class="plan-grid-label">' + escapeHTML(category.label || category.id) + '</span>' +
          fieldHTML("planBlockBalance" + index, "share %", "", "number", "") +
          fieldHTML("planBlockMinRun" + index, "min run", "20m", "text", "") +
          fieldHTML("planBlockMaxRun" + index, "max unbroken", "90m", "text", "") +
          fieldHTML("planBlockResetAfter" + index, "reset after", "15m", "text", "") +
          fieldHTML("planBlockMinItem" + index, "min item", "20m", "text", "") +
        '</div>').join("") + '</div>';

  const breakRows =
    '<div class="composer-sub">// breaks — what goes between the programming</div>' +
    '<div class="composer-row">' +
      fieldHTML("planBlockBreakTargetDuration", "Break should run about", "8m", "text", "") +
      fieldHTML("planBlockBreakAcceptMin", "but no shorter than", "3m", "text", "") +
      fieldHTML("planBlockBreakAcceptMax", "and no longer than", "14m", "text", "") +
      fieldHTML("planBlockBreakMinGap", "Not more often than", "20m", "text", "") +
    '</div>' +
    '<div class="composer-row">' +
      fieldHTML("planBlockBreakBetween", "Between (categories, blank = all)", "talk", "text", "") +
    '</div>' +
    (pools.length === 0
      ? '<div class="empty-state">// add a pool to build breaks out of</div>'
      : '<div class="plan-grid">' +
          '<div class="plan-grid-head">What a break is made of. Leave every count at zero for no breaks at all.</div>' +
          pools.map((pool, index) =>
            '<div class="plan-grid-row">' +
              '<span class="plan-grid-label">' + escapeHTML(pool.label || pool.id) + '</span>' +
              fieldHTML("planBlockBreakMin" + index, "min", "0", "number", "0") +
              fieldHTML("planBlockBreakMax" + index, "max", "0", "number", "0") +
              checkboxHTML("planBlockBreakFill" + index, "absorbs the time", false) +
            '</div>').join("") +
        '</div>');

  const patternRows =
    '<div class="composer-sub">// cycle — leave empty for ordinary rotation</div>' +
    '<div class="composer-row">' +
      selectHTML("planBlockPattern0", [["", "— no cycle —"]].concat(WANT_CHOICES), "", "Then") +
      selectHTML("planBlockPattern1", [["", "— nothing —"]].concat(WANT_CHOICES), "", "Then") +
      selectHTML("planBlockPattern2", [["", "— nothing —"]].concat(WANT_CHOICES), "", "Then") +
      selectHTML("planBlockPattern3", [["", "— nothing —"]].concat(WANT_CHOICES), "", "Then") +
    '</div>';

  const body =
    '<div class="composer-row">' +
      fieldHTML("planBlockID", "ID", "morning-news", "text", "") +
      fieldHTML("planBlockLabel", "Label", "Morning News", "text", "") +
      checkboxHTML("planBlockDefault", "Default block", false) +
    '</div>' +
    '<div class="composer-row">' +
      selectHTML("planBlockExposure", EXPOSURE_CHOICES, "", "Airing something new here") +
    '</div>' +
    '<div class="composer-row">' +
      fieldHTML("planBlockAt", "Starts at (HH:MM)", "07:00", "text", "") +
      selectHTML("planBlockDays", WEEKDAY_CHOICES, "*", "Days") +
      checkboxHTML("planBlockHard", "Booked (hard anchor)", false) +
    '</div>' +
    '<div class="composer-row">' +
      selectHTML("planBlockStart", START_CHOICES, "makeNext", "When it is due and something is on") +
      fieldHTML("planBlockGrace", "Grace (waitUpTo)", "5m", "text", "") +
    '</div>' +
    '<div class="composer-row">' +
      selectHTML("planBlockAfter", blockChoices, "", "Or: starts after") +
      fieldHTML("planBlockWhen", "Only when", "window >= 45m", "text", "") +
    '</div>' +
    '<div class="composer-row">' +
      fieldHTML("planBlockExitAt", "Ends at (HH:MM)", "08:00", "text", "") +
      fieldHTML("planBlockExitDuration", "Or runs for", "12m", "text", "") +
      fieldHTML("planBlockExitTolerance", "± tolerance", "6m", "text", "") +
    '</div>' +
    '<div class="composer-row">' +
      fieldHTML("planBlockExitCount", "Or after N items", "3", "number", "") +
      fieldHTML("planBlockExitWhen", "Or ends when", "window < 10m", "text", "") +
      checkboxHTML("planBlockExitAnchor", "Ends at the next booked slot", false) +
    '</div>' +
    '<div class="composer-row">' +
      selectHTML("planBlockNext", blockChoices, "", "Then hands over to") +
    '</div>' +
    '<div class="composer-sub">// what it plays</div>' +
    poolRows +
    categoryRows +
    patternRows +
    breakRows +
    '<div class="composer-actions">' +
      '<button class="btn primary" data-action="plan-block-save">SAVE BLOCK</button>' +
      '<button class="btn ghost" data-action="composer-toggle" data-composer="plan-block">CANCEL</button>' +
    '</div>';

  return composerHTML("plan-block", "PROGRAMMING BLOCK", body,
    "// leave the clock fields empty and use 'starts after' to chain a block to whatever came before it. " +
    "Conditions understand: always · window &gt;= 45m · window unbounded · pool.&lt;id&gt;.available · " +
    "obligations.pending &gt; 0");
}

function poolComposer(plan, sources) {
  const rows = (sources || []).length === 0
    ? '<div class="empty-state">// no sources on this channel yet</div>'
    : '<div class="plan-grid">' + (sources || []).map((src, index) =>
        '<div class="plan-grid-row">' +
          checkboxHTML("planPoolSource" + index, (src.label || src.kind) + " · " + src.kind, false) +
        '</div>').join("") + '</div>';
  const body =
    '<div class="composer-row">' +
      fieldHTML("planPoolID", "ID", "fresh-podcasts", "text", "") +
      fieldHTML("planPoolLabel", "Label", "Fresh podcasts", "text", "") +
    '</div>' +
    rows +
    '<div class="composer-actions">' +
      '<button class="btn primary" data-action="plan-pool-save">SAVE POOL</button>' +
      '<button class="btn ghost" data-action="composer-toggle" data-composer="plan-pool">CANCEL</button>' +
    '</div>';
  return composerHTML("plan-pool", "CONTENT POOL", body,
    "// a pool is just a named set of sources. The same source can be in as many pools as you like.");
}

function categoryComposer() {
  const body =
    '<div class="composer-row">' +
      fieldHTML("planCategoryID", "ID", "comedy", "text", "") +
      fieldHTML("planCategoryLabel", "Label", "Comedy", "text", "") +
      fieldHTML("planCategoryTarget", "Target share %", "25", "number", "") +
    '</div>' +
    '<div class="composer-actions">' +
      '<button class="btn primary" data-action="plan-category-save">SAVE CATEGORY</button>' +
      '<button class="btn ghost" data-action="composer-toggle" data-composer="plan-category">CANCEL</button>' +
    '</div>';
  return composerHTML("plan-category", "CATEGORY", body,
    "// set a source's category from the mix panel. Anything without one falls back to talk or music by its role.");
}

function jsonComposer(plan) {
  return composerHTML("plan-json", "PLAN AS JSON",
    '<label class="field"><span class="field-label">Plan document</span>' +
      '<textarea id="planJSON" rows="24" spellcheck="false">' + escapeHTML(JSON.stringify(plan, null, 2)) + '</textarea>' +
    '</label>' +
    '<div class="composer-actions">' +
      '<button class="btn primary" data-action="plan-json-save">SAVE JSON</button>' +
      '<button class="btn ghost" data-action="composer-toggle" data-composer="plan-json">CANCEL</button>' +
    '</div>',
    "// the escape hatch. Everything the editor above does ends up here, and anything the editor cannot express yet can be written by hand.");
}

// ---- what the station owes ---------------------------------------------

// A new episode is not "a candidate with a good score" — it is something the
// station owes you. This panel is the queue, which is also the answer to "is it
// even aware the episode exists".
export function owedPanel(items, pending) {
  const rows = (items || []).length === 0
    ? '<div class="empty-state">// nothing owed — every recent episode has reached you, or there are none</div>'
    : '<div class="list">' + items.map((obligation) => {
        const settled = obligation.state !== "pending";
        const credit = Math.round((obligation.credit || 0) * 100);
        const age = obligation.publishedAt ? sinceLabel(obligation.publishedAt) : "";
        const meta = [
          obligation.sourceLabel || obligation.sourceId,
          age ? "published " + age + " ago" : "",
          settled ? "reached you" : "credit " + credit + "%",
        ].filter(Boolean).join(" · ");
        return '<div class="list-row' + (settled ? " done" : "") + '">' +
          '<div class="num">' + escapeHTML(obligation.tier || "C") + '</div>' +
          '<div class="main"><div class="name">' + escapeHTML(obligation.title || obligation.itemRef) + '</div>' +
          '<div class="meta">' + escapeHTML(meta) + '</div></div>' +
        '</div>';
      }).join("") + '</div>';

  return '<div class="panel panel-wide">' +
    '<div class="panel-head"><span>// OWED TO YOU</span><span>' + (pending || 0) + ' PENDING</span></div>' +
    '<div class="panel-sub">New episodes are an obligation, not just a good score. One works its way off this list ' +
      'by actually reaching you — airing it somewhere that counts for nothing, or being cut off after five minutes, ' +
      'does not settle it. Tier decides the order; within a tier, newest first.</div>' +
    rows +
  '</div>';
}

// defaultCategoryForRole mirrors the server's fallback when a source has no
// category of its own, so the pool count shown here matches what the scheduler
// will actually select.
function defaultCategoryForRole(role) {
  return role === "music" ? "music" : "talk";
}

function sinceLabel(iso) {
  const then = new Date(iso);
  if (isNaN(then.getTime())) return "";
  const minutes = Math.max(0, Math.round((Date.now() - then.getTime()) / 60000));
  if (minutes < 60) return minutes + "m";
  if (minutes < 60 * 24) return Math.round(minutes / 60) + "h";
  return Math.round(minutes / (60 * 24)) + "d";
}

// ---- source metadata ---------------------------------------------------

// What a piece of content IS: which category, how much you care, and who is
// behind it. Everything here feeds a rule — the category feeds the balance, the
// tier orders what is owed, the creator keeps two shows with the same host
// apart.
export function sourceComposer(categories) {
  const categoryChoices = [["", "— use the role default —"]]
    .concat((categories || []).map((category) => [category.id, category.label || category.id]));
  const body =
    '<div class="composer-row">' +
      fieldHTML("planSourceLabel", "Label", "MSSP", "text", "") +
      selectHTML("planSourceCategory", categoryChoices, "", "Category") +
      selectHTML("planSourceTier", TIER_CHOICES, "C", "Tier") +
    '</div>' +
    '<div class="composer-row">' +
      fieldHTML("planSourceCreator", "Creator / host", "leave blank to use the label", "text", "") +
      fieldHTML("planSourceFamily", "Family / network", "optional", "text", "") +
      fieldHTML("planSourceFresh", "New for (hours)", "72", "number", "") +
    '</div>' +
    '<div class="composer-row">' +
      fieldHTML("planSourceWeight", "Weight within its category", "1", "number", "") +
    '</div>' +
    '<div class="composer-actions">' +
      '<button class="btn primary" data-action="plan-source-save">SAVE</button>' +
      '<button class="btn ghost" data-action="composer-toggle" data-composer="plan-source">CANCEL</button>' +
    '</div>';
  return composerHTML("plan-source", "CONTENT SETTINGS", body,
    "// tier orders what the station owes you; weight splits a category's airtime. They are different dials: " +
    "a show can be S-tier and low-weight — surface every new episode promptly, but do not fill the afternoon with its back catalogue.");
}

// ---- why did it play that ----------------------------------------------

export function whyPanel(decisions) {
  const items = decisions || [];
  if (items.length === 0) {
    return '<div class="panel panel-wide">' +
      '<div class="panel-head"><span>// WHY THIS PLAYED</span>' +
        '<button class="btn ghost btn-mini" data-action="plan-why-refresh">REFRESH</button></div>' +
      '<div class="empty-state">// nothing decided yet — tune in and the reasoning for each choice shows up here</div>' +
    '</div>';
  }
  return '<div class="panel panel-wide">' +
    '<div class="panel-head"><span>// WHY THIS PLAYED</span><span>' +
      '<button class="btn ghost btn-mini" data-action="plan-why-more">MORE</button> ' +
      '<button class="btn ghost btn-mini" data-action="plan-why-refresh">REFRESH</button>' +
    '</span></div>' +
    items.map(decisionBody).join("") +
  '</div>';
}

function decisionBody(decision) {
  const selected = decision.selected;
  const when = decision.at ? new Date(decision.at) : null;
  const stamp = when && !isNaN(when.getTime())
    ? when.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })
    : "";

  let html = '<div class="why-decision">';
  html += '<div class="why-head">' +
    '<span class="why-time">' + escapeHTML(stamp) + '</span>' +
    '<span class="why-title">' + escapeHTML(selected ? selected.title : (decision.error || "nothing could be played")) + '</span>' +
  '</div>';

  html += '<div class="why-line"><span class="why-key">programming</span>' +
    escapeHTML(decision.blockLabel || decision.blockId || "—") +
    (decision.entryReason ? " — " + escapeHTML(decision.entryReason) : "") + '</div>';
  if (decision.exitReason) {
    html += '<div class="why-line"><span class="why-key">ends</span>' + escapeHTML(decision.exitReason) + '</div>';
  }
  if (decision.nextAnchor) {
    html += '<div class="why-line"><span class="why-key">next slot</span>' +
      escapeHTML(decision.nextAnchor.label) + ' at ' + escapeHTML(decision.nextAnchor.at) +
      ' (in ' + escapeHTML(decision.nextAnchor.in) + ', ' + escapeHTML(decision.nextAnchor.policy) + ')' +
    '</div>';
  }
  if (decision.windowSeconds) {
    html += '<div class="why-line"><span class="why-key">room</span>' +
      Math.round(decision.windowSeconds / 60) + ' minutes before the next booked slot</div>';
  }
  (decision.targets || []).forEach((target) => {
    html += '<div class="why-line"><span class="why-key">' + escapeHTML(target.category) + '</span>' +
      target.actualPercent + '% of the last window vs ' + target.targetPercent + '% target (' + target.airedMinutes + 'm)</div>';
  });
  (decision.limits || []).forEach((limit) => {
    html += '<div class="why-line' + (limit.exceeded ? ' bad' : '') + '"><span class="why-key">limit</span>' +
      escapeHTML(limit.category) + ' run ' + limit.runMinutes + 'm of ' + limit.maxMinutes + 'm' +
      (limit.exceeded ? ' — over, so another category goes next' : '') + '</div>';
  });
  if (decision.want) {
    html += '<div class="why-line"><span class="why-key">wanted</span>' +
      escapeHTML(decision.want) + ' — this position in the block\'s cycle</div>';
  }
  if (decision.break) {
    const brk = decision.break;
    html += '<div class="why-line' + (brk.inRange === false ? ' bad' : '') + '"><span class="why-key">break</span>' +
      escapeHTML((brk.items || []).join(" → ")) +
      (brk.minutes ? ' · ' + brk.minutes + 'm' : '') +
      (brk.targetMinutes ? ' against a ' + brk.targetMinutes + 'm target' : '') +
      (brk.of ? ' · part ' + brk.position + ' of ' + brk.of : '') +
      (brk.reason ? ' · ' + escapeHTML(brk.reason) : '') +
    '</div>';
    if (brk.note) {
      html += '<div class="why-line bad"><span class="why-key"></span>' + escapeHTML(brk.note) + '</div>';
    }
  }
  (decision.owed || []).forEach((obligation, index) => {
    html += '<div class="why-line"><span class="why-key">' + (index === 0 ? "owed" : "") + '</span>' +
      escapeHTML(obligation.tier) + '  ' + escapeHTML(obligation.title) +
      ' · ' + Math.round(obligation.ageMinutes / 60) + 'h old' +
      ' · credit ' + Math.round((obligation.credit || 0) * 100) + '%' +
      (obligation.expiresIn ? ' · expires in ' + escapeHTML(obligation.expiresIn) : '') +
    '</div>';
  });
  if (decision.note) {
    html += '<div class="why-line"><span class="why-key">note</span>' + escapeHTML(decision.note) + '</div>';
  }

  if ((decision.candidates || []).length > 0) {
    html += '<div class="why-sub">// considered ' + decision.considered + '</div><div class="why-table">';
    decision.candidates.forEach((candidate) => {
      const terms = (candidate.terms || [])
        .map((term) => term.name + " " + (term.value * term.weight).toFixed(2))
        .join(" · ");
      html += '<div class="why-row' + (candidate.contender ? " contender" : "") + '">' +
        '<span class="why-score">' + candidate.score.toFixed(2) + '</span>' +
        '<span class="why-name">' + escapeHTML(candidate.title) + '</span>' +
        '<span class="why-terms">' + escapeHTML(terms) + '</span>' +
      '</div>';
    });
    html += '</div>';
  }

  if ((decision.rejected || []).length > 0) {
    html += '<div class="why-sub">// ruled out</div><div class="why-table">';
    decision.rejected.forEach((rejection) => {
      html += '<div class="why-row rejected">' +
        '<span class="why-score">✕</span>' +
        '<span class="why-name">' + escapeHTML(rejection.title || rejection.ref) + '</span>' +
        '<span class="why-terms">' + escapeHTML(rejection.rule + ": " + rejection.reason) + '</span>' +
      '</div>';
    });
    html += '</div>';
  }

  if ((decision.relaxed || []).length > 0) {
    html += '<div class="why-line bad"><span class="why-key">relaxed</span>' +
      escapeHTML(decision.relaxed.join(", ")) +
      ' — nothing qualified, so these rules were given up in order</div>';
  }
  if (selected && selected.reason) {
    html += '<div class="why-line"><span class="why-key">chosen</span>' + escapeHTML(selected.reason) + '</div>';
  }
  html += '</div>';
  return html;
}
