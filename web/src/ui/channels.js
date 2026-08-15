// Radio channel rendering: the card, its schedule strip, and what is on now.

import { formatDate, minuteToHHMM } from "./format.js";
import { attr, escapeHTML } from "./html.js";

export function channelCard(ch) {
  const codec = (ch.codec || "mp3").toUpperCase() + " · " + (ch.bitrateKbps || 192) + "K";
  return '<div class="channel-card">' +
    '<div class="channel-card-meta">' +
      '<div class="channel-eyebrow">// CHANNEL</div>' +
      '<h3 class="name">' + escapeHTML(ch.name) + '</h3>' +
      (ch.description ? '<p class="desc">' + escapeHTML(ch.description) + '</p>' : '') +
      '<div class="channel-spec">' + codec + ' · ' + (ch.enabled ? 'ENABLED' : 'DISABLED') + '</div>' +
    '</div>' +
    '<div class="channel-actions">' +
      '<button class="btn primary btn-small" data-action="channel-tune-in" data-id="' + attr(ch.id) + '" data-name="' + attr(ch.name) + '">TUNE IN</button>' +
      '<button class="btn ghost btn-small" data-action="channel-open" data-id="' + attr(ch.id) + '">PROGRAM &rarr;</button>' +
    '</div>' +
  '</div>';
}

// channelScheduleTimeline renders a per-weekday 24-hour strip with
// colored bands for each scheduled rule. Same source → same color so
// patterns across days are immediately visible. The "now" indicator
// is a thin vertical line tracking current wall clock; idle slots
// show through as dim gridlines so the user sees where rotation
// takes over.
export function channelScheduleTimeline(rules, sourceLookup) {
  const weekdays = ["SUN", "MON", "TUE", "WED", "THU", "FRI", "SAT"];
  const palette = ["#f59e0b", "#22d3ee", "#a78bfa", "#34d399", "#fb7185", "#fbbf24", "#60a5fa", "#f472b6"];
  const sourceColorMap = {};
  let palIdx = 0;
  function colorFor(sourceID) {
    if (!sourceID) return palette[0];
    if (sourceColorMap[sourceID] != null) return sourceColorMap[sourceID];
    sourceColorMap[sourceID] = palette[palIdx % palette.length];
    palIdx++;
    return sourceColorMap[sourceID];
  }
  const now = new Date();
  const nowDay = now.getDay();
  const nowMin = now.getHours() * 60 + now.getMinutes();
  const nowPct = (nowMin / 1440) * 100;

  let html = '<div class="sched-timeline">' +
    '<div class="sched-hour-labels">';
  for (let h = 0; h <= 24; h += 3) {
    html += '<span style="left:' + ((h / 24) * 100) + '%">' + String(h).padStart(2, "0") + ':00</span>';
  }
  html += '</div>';

  for (let day = 0; day < 7; day++) {
    const dayRules = rules.filter((r) => r.enabled && (r.weekdayMask & (1 << day)));
    html += '<div class="sched-row' + (day === nowDay ? ' today' : '') + '">' +
      '<div class="sched-row-label">' + weekdays[day] + '</div>' +
      '<div class="sched-row-track">';
    dayRules.forEach((r) => {
      const left = (r.startMinute / 1440) * 100;
      const width = ((r.endMinute - r.startMinute) / 1440) * 100;
      const src = sourceLookup[r.sourceId];
      const color = colorFor(r.sourceId);
      const label = r.label || (src ? src.label || src.kind : "rule");
      const title = label + " · " + minuteToHHMM(r.startMinute) + "–" + minuteToHHMM(r.endMinute);
      html += '<div class="sched-band" style="left:' + left + '%;width:' + width + '%;background:' + color + '" title="' + attr(title) + '">' + escapeHTML(label) + '</div>';
    });
    if (day === nowDay) {
      html += '<div class="sched-now" style="left:' + nowPct + '%"></div>';
    }
    html += '</div></div>';
  }
  html += '</div>';
  return html;
}

// channelOnAirHeader is the one band that answers "what is happening right
// now", and it is pinned above everything else.
//
// Those facts used to be split across two panels fifteen hundred pixels apart:
// what is playing sat at the top, while the clock, the block it is playing
// under, and what is booked next were buried in the middle of the PROGRAM
// panel behind four lines of balance arithmetic. Reading the station meant
// scrolling between them and holding one half in your head.
export function channelOnAirHeader(channelID, now, status) {
  const cur = (now && now.current) || null;
  const listeners = (now && now.listenerCount) || 0;
  const programming = (status && status.programming) || null;

  const nowCell = cur
    ? '<div class="onair-now">' +
        '<div class="channel-eyebrow">' + (cur.live ? '// LIVE CUT-IN' : '// ON AIR') + '</div>' +
        '<div class="onair-title">' + escapeHTML(cur.title || 'Untitled') + '</div>' +
        (cur.artist ? '<div class="onair-sub">' + escapeHTML(cur.artist) + '</div>' : '') +
        '<div class="onair-sub mono">' + escapeHTML(cur.sourceLabel || cur.kind || '') +
          (now.startedAt ? ' · SINCE ' + escapeHTML(formatDate(now.startedAt)) : '') + '</div>' +
      '</div>'
    : '<div class="onair-now">' +
        '<div class="channel-eyebrow">// OFF AIR</div>' +
        '<div class="onair-title dim">nothing is playing</div>' +
        '<div class="onair-sub mono">STARTS WHEN SOMETHING TUNES IN</div>' +
      '</div>';

  // The right column is the station's clock and its immediate future: what
  // block it is in, and what it is about to be interrupted by.
  const clockCell = status
    ? '<div class="onair-clock">' +
        '<div class="onair-time">' + escapeHTML(status.localTime || '??:??') + '</div>' +
        '<div class="onair-sub mono">' + escapeHTML(status.weekday || '') + ' · ' +
          escapeHTML(status.timezone || 'UTC') + '</div>' +
        (programming
          ? '<div class="onair-sub">' + escapeHTML(programming.blockLabel || programming.blockId || '—') + '</div>'
          : '') +
        (programming && programming.nextAnchor
          ? '<div class="onair-sub mono">NEXT ' + escapeHTML(programming.nextAnchor.label) + ' ' +
              escapeHTML(programming.nextAnchor.at) + ' · IN ' + escapeHTML(programming.nextAnchor.in) + '</div>'
          : '<div class="onair-sub mono">NOTHING BOOKED AHEAD</div>') +
      '</div>'
    : '';

  const id = attr(channelID);
  return '<div class="onair' + (cur ? ' live' : '') + '">' +
    '<div class="onair-grid">' + nowCell + clockCell + '</div>' +
    '<div class="onair-foot">' +
      '<div class="onair-transport">' +
        '<button class="btn ghost btn-mini" data-action="channel-previous" data-id="' + id + '">&#8592; BACK</button>' +
        '<button class="btn ghost btn-mini" data-action="channel-skip" data-id="' + id + '" data-scope="item">SKIP &#8594;</button>' +
        '<button class="btn ghost btn-mini" data-action="channel-skip" data-id="' + id + '" data-scope="kind">NEXT MEDIA TYPE</button>' +
      '</div>' +
      '<span class="channel-listeners">' + listeners + ' LISTENER' + (listeners === 1 ? '' : 'S') + '</span>' +
    '</div>' +
  '</div>';
}

// channelScheduleStatusBody explains, in the panel itself, why the thing you
// booked is or is not on air.
//
// "My slot did not fire" has several causes that look identical from the
// listening end — the clock is in the wrong zone, nothing is booked today, a
// slot matched but its source could not produce audio — and the only one that
// used to be visible was none of them.
// channelBalanceBody shows the mix the rotation is actually working from.
//
// "Why is it playing this" has as many answers as the algorithm has rules, and
// none of them were visible: the mix could be nine hours of talk against a 75%
// target and the panel would say nothing at all. These are the exact numbers
// the next decision is made from, so a station that sounds wrong can be argued
// with instead of guessed at.
//
// It lives beside CATEGORIES rather than in the schedule panel, because these
// numbers are the categories — a target you set next to the share it is
// actually getting. Split across two screens they were two facts; together
// they are one, and the gap between them is the thing worth looking at.
export function channelBalanceBody(programming) {
  if (!programming) return "";
  const categories = programming.categories || [];
  if (categories.length === 0) return "";
  const off = categories.some((c) => Math.abs((c.actualPercent || 0) - (c.targetPercent || 0)) > 15);

  const bars = categories.map((c) => {
    const actual = c.actualPercent || 0;
    const target = c.targetPercent || 0;
    const drift = actual - target;
    const state = Math.abs(drift) > 15 ? " bad" : (Math.abs(drift) > 7 ? " warn" : "");
    return '<div class="balance-row' + state + '">' +
      '<span class="balance-name">' + escapeHTML(c.category) + '</span>' +
      '<span class="balance-track">' +
        '<span class="balance-fill" style="width:' + Math.min(100, actual) + '%"></span>' +
        '<span class="balance-target" style="left:' + Math.min(100, target) + '%"></span>' +
      '</span>' +
      '<span class="balance-value">' + actual + '% <span class="dim">/ ' + target + '%</span></span>' +
    '</div>';
  }).join("");

  const limits = (programming.limits || []).map((limit) =>
    '<div class="sched-status-detail' + (limit.exceeded ? " bad" : "") + '">' +
      escapeHTML(limit.category) + " has run " + limit.runMinutes + "m of its " + limit.maxMinutes + "m limit" +
      (limit.exceeded ? " — over, so something else goes next" : "") +
    '</div>').join("");

  return '<div class="balance' + (off ? " bad" : "") + '">' +
    '<div class="balance-head">actual share of the last ' +
      escapeHTML(String(programming.windowHours || 0)) + 'h, against the target &#9662;</div>' +
    bars +
    limits +
  '</div>';
}

// channelScheduleStatusBody explains what the clock is doing to the schedule.
// The clock itself, the block on air and the next anchor moved to the on-air
// header; what is left here is what belongs beside the booked slots.
export function channelScheduleStatusBody(status, channelID) {
  if (!status) return "";
  const bad = Boolean(status.ruleError || status.playbackError);
  const programming = status.programming || null;

  // The browser is the only party that knows what clock the operator programs
  // against. The server is UTC on purpose and the host is too, so neither can
  // supply this — but a schedule read in UTC silently shifts every slot by the
  // operator's offset, which looks exactly like the feature being broken.
  const browserZone = (() => {
    try {
      return Intl.DateTimeFormat().resolvedOptions().timeZone || "";
    } catch {
      return "";
    }
  })();
  const mismatched = status.usingFallbackZone && browserZone && browserZone !== status.timezone;

  let detail = "";
  if (status.activeRule) {
    const window = minuteToHHMM(status.activeRule.startMinute) + "–" + minuteToHHMM(status.activeRule.endMinute);
    detail = "slot " + escapeHTML(status.activeRule.label || "(unnamed)") + " " + escapeHTML(window);
    if (status.activeSource) {
      detail += " → " + escapeHTML(status.activeSource.label || status.activeSource.kind);
    }
  } else if (status.nextRule) {
    detail = "next slot " + escapeHTML(status.nextRule.label || "(unnamed)") +
      " at " + escapeHTML(status.nextRuleAt || "") +
      (status.nextRuleIn ? " (in " + escapeHTML(status.nextRuleIn) + ")" : "");
  } else {
    detail = status.rulesToday + " of " + status.totalRules + " slots apply today";
  }

  const warning = mismatched
    ? '<div class="sched-status-warn">' +
        '// this schedule is being read in ' + escapeHTML(status.timezone) +
        ', but you are in ' + escapeHTML(browserZone) + ' — every slot is off by the difference' +
        ' <button class="btn primary btn-mini" data-action="channel-use-browser-zone"' +
          ' data-id="' + attr(channelID) + '" data-zone="' + attr(browserZone) + '">' +
          'USE ' + escapeHTML(browserZone.toUpperCase()) + '</button>' +
      '</div>'
    : "";

  const room = programming && programming.nextAnchor
    ? '<div class="sched-status-detail">' + (programming.roomMinutes || 0) +
        'm of room before ' + escapeHTML(programming.nextAnchor.label) + '</div>'
    : "";

  return '<div class="sched-status' + (bad || mismatched ? " bad" : "") + '">' +
    warning +
    '<div class="sched-status-line">' + escapeHTML(status.onAir || "") + '</div>' +
    '<div class="sched-status-detail">' + detail + '</div>' +
    room +
    (bad ? '<div class="sched-status-error">// ' + escapeHTML(status.ruleError) + '</div>' : "") +
    (status.playbackError
      ? '<div class="sched-status-error">// audio failed' +
          (status.playbackErrorItem ? " on " + escapeHTML(status.playbackErrorItem) : "") +
          ': ' + escapeHTML(status.playbackError) + '</div>'
      : "") +
  '</div>';
}
