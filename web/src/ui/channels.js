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
