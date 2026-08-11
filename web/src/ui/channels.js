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

export function channelNowPlayingBody(now) {
  const listeners = (now && now.listenerCount) || 0;
  const listenersChip = '<span class="channel-listeners">' + listeners + ' LISTENER' + (listeners === 1 ? '' : 'S') + '</span>';
  if (!now || !now.current) {
    return '<div class="channel-now-body"><div class="empty-state">// no listeners — tune in to start the stream</div>' +
      '<div class="channel-now-stats">' + listenersChip + '</div></div>';
  }
  const cur = now.current;
  const sub = cur.sourceLabel || cur.kind || "";
  const startedAt = now.startedAt ? formatDate(now.startedAt) : "";
  return '<div class="channel-now-body">' +
    '<div class="channel-now-current">' +
      '<div class="channel-eyebrow">' + (cur.live ? 'LIVE CUT-IN' : 'NOW') + '</div>' +
      '<div class="name">' + escapeHTML(cur.title || 'Untitled') + '</div>' +
      (cur.artist ? '<div class="sub">' + escapeHTML(cur.artist) + '</div>' : '') +
      '<div class="sub mono">' + escapeHTML(sub) + (startedAt ? ' · STARTED ' + escapeHTML(startedAt) : '') + '</div>' +
    '</div>' +
    '<div class="channel-now-stats">' + listenersChip + '</div>' +
  '</div>';
}

// channelScheduleStatusBody explains, in the panel itself, why the thing you
// booked is or is not on air.
//
// "My slot did not fire" has several causes that look identical from the
// listening end — the clock is in the wrong zone, nothing is booked today, a
// slot matched but its source could not produce audio — and the only one that
// used to be visible was none of them.
// programmingBody shows the balance the rotation is actually working from.
//
// "Why is it playing this" has as many answers as the algorithm has rules, and
// none of them were visible: the mix could be nine hours of talk against a 75%
// target and the panel would say nothing at all. These are the exact numbers
// the next decision is made from, so a station that sounds wrong can be argued
// with instead of guessed at.
function programmingBody(programming) {
  if (!programming) return "";
  const categories = programming.categories || [];
  const off = categories.some((c) => Math.abs((c.actualPercent || 0) - (c.targetPercent || 0)) > 15);

  const mix = categories.length === 0
    ? "no categories configured"
    : categories
        .map((c) => escapeHTML(c.category) + " " + (c.actualPercent || 0) + "%/" + (c.targetPercent || 0) + "%")
        .join(" · ");

  const limits = (programming.limits || []).map((limit) =>
    escapeHTML(limit.category) + " run " + limit.runMinutes + "m of " + limit.maxMinutes + "m" +
    (limit.exceeded ? " — over, so something else goes next" : "")).join(" · ");

  const room = programming.nextAnchor
    ? "next booked: " + escapeHTML(programming.nextAnchor.label) + " at " +
      escapeHTML(programming.nextAnchor.at) + " (in " + escapeHTML(programming.nextAnchor.in) + ") — " +
      (programming.roomMinutes || 0) + "m of room"
    : "nothing booked ahead";

  return '<div class="sched-programming' + (off ? " bad" : "") + '">' +
    '<div class="sched-status-detail">' +
      "on now: " + escapeHTML(programming.blockLabel || programming.blockId || "—") +
      (programming.entryReason ? " — " + escapeHTML(programming.entryReason) : "") +
      (programming.exitReason ? " · ends " + escapeHTML(programming.exitReason) : "") +
    '</div>' +
    '<div class="sched-status-detail">' +
      "mix over the last " + escapeHTML(String(programming.windowHours || 0)) + "h: " + mix +
      " (actual/target)" +
    '</div>' +
    (limits ? '<div class="sched-status-detail">' + limits + '</div>' : "") +
    '<div class="sched-status-detail">' + room + '</div>' +
    '<div class="sched-status-detail">' +
      "listening day " + escapeHTML(String(programming.listeningDay || "")) +
      " — episodes aired outside it stay new · plan: " +
      escapeHTML(String(programming.planSource || "derived")) +
    '</div>' +
  '</div>';
}

export function channelScheduleStatusBody(status, channelID) {
  if (!status) return "";
  const bad = Boolean(status.ruleError || status.playbackError);

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
  const clock = escapeHTML(status.localTime || "??:??") + " " +
    escapeHTML(status.weekday || "") + " · " + escapeHTML(status.timezone || "UTC");

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

  return '<div class="sched-status' + (bad || mismatched ? " bad" : "") + '">' +
    warning +
    '<div class="sched-status-clock">' + clock + '</div>' +
    '<div class="sched-status-line">' + escapeHTML(status.onAir || "") + '</div>' +
    '<div class="sched-status-detail">' + detail + '</div>' +
    programmingBody(status.programming) +
    (bad ? '<div class="sched-status-error">// ' + escapeHTML(status.ruleError) + '</div>' : "") +
    (status.playbackError
      ? '<div class="sched-status-error">// audio failed' +
          (status.playbackErrorItem ? " on " + escapeHTML(status.playbackErrorItem) : "") +
          ': ' + escapeHTML(status.playbackError) + '</div>'
      : "") +
  '</div>';
}
