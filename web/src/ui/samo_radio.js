// samo-radio: the server's own audio output, rendered as a device panel.
//
// A device is a headless player wired into a physical socket on some machine —
// normally this one, feeding its line-out. This file only draws it; every
// command goes through /api/v1/samo-radio/*, which forwards to the daemon.

import { composerHTML, fieldHTML } from "./composer.js";
import { attr, escapeHTML } from "./html.js";

const STATUS_LABEL = {
  playing: "PLAYING",
  paused: "PAUSED",
  buffering: "BUFFERING",
  idle: "IDLE",
  error: "ERROR",
};

function clock(seconds) {
  const total = Math.max(0, Math.floor(Number(seconds) || 0));
  const hours = Math.floor(total / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  const secs = total % 60;
  const pad = (n) => String(n).padStart(2, "0");
  return hours > 0 ? hours + ":" + pad(minutes) + ":" + pad(secs) : minutes + ":" + pad(secs);
}

// deviceStatusChip is the single line that answers "is the aux port alive".
// Reachability comes first: a device Samo cannot talk to has no state at all,
// and showing a stale "PLAYING" there would be a lie.
function deviceStatusChip(device) {
  if (!device.enabled) {
    return '<span class="samoradio-chip off">DISABLED</span>';
  }
  if (!device.state) {
    return '<span class="samoradio-chip off">UNREACHABLE</span>';
  }
  const status = device.state.status || "idle";
  const cls = status === "error" ? "bad" : (status === "playing" ? "live" : "");
  return '<span class="samoradio-chip ' + cls + '">' + (STATUS_LABEL[status] || status.toUpperCase()) + '</span>';
}

function nowPlayingBody(device) {
  const state = device.state;
  if (!state) {
    const reason = device.lastError ? device.lastError : "no answer from the device";
    return '<div class="empty-state">// ' + escapeHTML(reason) + '</div>';
  }
  if (!state.item) {
    return '<div class="empty-state">// standby — the sink is open and silent</div>';
  }

  // On a channel the item is the station; what is actually airing comes from
  // the channel's own now-playing, which the daemon polls.
  const channel = state.channel || null;
  const onChannel = state.mode === "channel" && channel;
  const title = onChannel ? (channel.title || channel.name || state.item.title) : state.item.title;
  const subtitle = onChannel ? (channel.artist || channel.sourceLabel || "") : (state.item.subtitle || "");
  const eyebrow = onChannel
    ? "// ON AIR · " + escapeHTML(channel.name || "CHANNEL")
    : "// NOW PLAYING";

  let position = clock(state.positionSeconds);
  if (state.durationSeconds > 0) {
    position += " / " + clock(state.durationSeconds);
  } else if (onChannel) {
    position += " TUNED";
  }

  let html = '<div class="samoradio-now">' +
    '<div class="channel-eyebrow">' + eyebrow + '</div>' +
    '<div class="name">' + escapeHTML(title || "Untitled") + '</div>' +
    (subtitle ? '<div class="sub">' + escapeHTML(subtitle) + '</div>' : "") +
    '<div class="sub mono">' + escapeHTML(position) +
      (state.queue && state.queue.length > 1
        ? " · " + (state.queueIndex + 1) + " OF " + state.queue.length
        : "") +
    '</div>' +
  '</div>';

  if (state.durationSeconds > 0) {
    const pct = Math.min(100, (state.positionSeconds / state.durationSeconds) * 100);
    html += '<div class="samoradio-bar"><span style="width:' + pct.toFixed(1) + '%"></span></div>';
  }
  return html;
}

function transportRow(device) {
  const state = device.state;
  if (!state) return "";
  const id = attr(device.id);
  const paused = state.status === "paused";
  const inQueue = state.mode === "queue";

  return '<div class="samoradio-transport">' +
    (inQueue
      ? '<button class="btn ghost btn-mini" data-action="samoradio-cmd" data-id="' + id + '" data-cmd="previous">&#8592; PREV</button>'
      : "") +
    '<button class="btn ghost btn-mini" data-action="samoradio-cmd" data-id="' + id + '" data-cmd="' + (paused ? "resume" : "pause") + '">' +
      (paused ? "RESUME" : "PAUSE") + '</button>' +
    (inQueue
      ? '<button class="btn ghost btn-mini" data-action="samoradio-cmd" data-id="' + id + '" data-cmd="next">NEXT &#8594;</button>'
      : "") +
    // STOP returns to the fallback channel; STANDBY is the real off switch.
    // Both exist because "stop the podcast" and "silence the room" are
    // different intentions on a device whose job is to always be on air.
    // On a channel, NEXT means "move the station on" rather than "next item in
    // my queue" — the device has no queue to advance, the channel does.
    (state.mode === "channel" && state.channel && state.channel.kind !== "station"
      ? '<button class="btn ghost btn-mini" data-action="samoradio-previous" data-id="' + id +
          '" data-channel="' + attr(state.channel.id) + '">&#8592; BACK</button>' +
        '<button class="btn ghost btn-mini" data-action="samoradio-skip" data-id="' + id +
          '" data-channel="' + attr(state.channel.id) + '" data-scope="item">SKIP &#8594;</button>' +
        '<button class="btn ghost btn-mini" data-action="samoradio-skip" data-id="' + id +
          '" data-channel="' + attr(state.channel.id) + '" data-scope="kind">NEXT MEDIA TYPE</button>'
      : "") +
    '<button class="btn ghost btn-mini" data-action="samoradio-cmd" data-id="' + id + '" data-cmd="stop">&#8635; STATION</button>' +
    '<button class="btn ghost btn-mini" data-action="samoradio-cmd" data-id="' + id + '" data-cmd="standby">STANDBY</button>' +
    '<label class="samoradio-volume">' +
      '<span>VOL</span>' +
      '<input type="range" min="0" max="100" step="1" value="' + Math.round((state.volume || 0) * 100) + '"' +
        ' data-action="samoradio-volume" data-id="' + id + '">' +
      '<span class="samoradio-volume-value">' + Math.round((state.volume || 0) * 100) + '</span>' +
    '</label>' +
  '</div>';
}

function outputRow(device, outputs) {
  const state = device.state;
  const output = state ? state.output : null;
  const id = attr(device.id);

  let deviceOptions = '<option value="">DEFAULT</option>';
  const list = (outputs && outputs.devices) || [];
  list.forEach((entry) => {
    const selected = output && output.device === entry.id ? " selected" : "";
    const label = entry.recommended ? "* " + entry.id : "  " + entry.id;
    deviceOptions += '<option value="' + attr(entry.id) + '"' + selected + '>' +
      escapeHTML(label) + (entry.name && entry.name !== entry.id ? " — " + escapeHTML(entry.name) : "") +
      '</option>';
  });

  const backends = (outputs && outputs.backends) || ["auto", "alsa", "pulse"];
  let backendOptions = "";
  backends.forEach((name) => {
    const selected = output && output.backend === name ? " selected" : "";
    backendOptions += '<option value="' + attr(name) + '"' + selected + '>' + escapeHTML(String(name).toUpperCase()) + '</option>';
  });

  return '<div class="composer-row">' +
    '<label class="field"><span class="field-label">Output device</span>' +
      '<select data-action="samoradio-output" data-id="' + id + '">' + deviceOptions + '</select>' +
    '</label>' +
    '<label class="field"><span class="field-label">Backend</span>' +
      '<select data-action="samoradio-backend" data-id="' + id + '">' + backendOptions + '</select>' +
    '</label>' +
  '</div>' +
  (outputs && outputs.error
    ? '<div class="composer-hint">// ' + escapeHTML(outputs.error) + '</div>'
    : '<div class="composer-hint">// * = recommended. On ALSA prefer plughw: over hw: — the plug layer converts rate and format, so a 44.1kHz-only card still takes the stream.</div>');
}

// stationOptions renders channels and internet stations as one list.
//
// Values are "<kind>:<id>" because the two id spaces are separate and the
// device has to be told which endpoint to pull — a bare id would be ambiguous.
function stationOptions(channels, stations, selectedKind, selectedID) {
  let html = "";
  if ((channels || []).length > 0) {
    html += '<optgroup label="CHANNELS">';
    channels.forEach((channel) => {
      const value = "channel:" + channel.id;
      const selected = selectedKind === "channel" && selectedID === channel.id ? " selected" : "";
      html += '<option value="' + attr(value) + '"' + selected + '>' + escapeHTML(channel.name) + '</option>';
    });
    html += '</optgroup>';
  }
  if ((stations || []).length > 0) {
    html += '<optgroup label="INTERNET RADIO">';
    stations.forEach((station) => {
      const value = "station:" + station.id;
      const selected = selectedKind === "station" && selectedID === station.id ? " selected" : "";
      html += '<option value="' + attr(value) + '"' + selected + '>' + escapeHTML(station.name) + '</option>';
    });
    html += '</optgroup>';
  }
  return html;
}

function stationRow(device, channels, stations) {
  const state = device.state;
  const current = (state && state.defaultStation) || null;
  const id = attr(device.id);
  const options = stationOptions(channels, stations, current ? current.kind : "", current ? current.id : "");
  const hasAny = ((channels || []).length + (stations || []).length) > 0;

  if (!hasAny) {
    return '<div class="composer-hint">// no channels or internet stations yet — create one under RADIO to give this device something to fall back to</div>';
  }

  return '<div class="composer-row">' +
    '<label class="field"><span class="field-label">Default station</span>' +
      '<select data-action="samoradio-default-station" data-id="' + id + '">' +
        '<option value="">— none —</option>' + options +
      '</select>' +
    '</label>' +
    '<label class="field"><span class="field-label">Tune now</span>' +
      '<select data-action="samoradio-tune" data-id="' + id + '">' +
        '<option value="">— pick to tune —</option>' + stationOptions(channels, stations, "", "") +
      '</select>' +
    '</label>' +
  '</div>' +
  '<div class="composer-hint">// the default station is what plays at boot and whenever a sent queue runs out</div>';
}

// deviceCard is the whole panel for one device.
export function samoRadioDeviceCard(device, extras) {
  extras = extras || {};
  const state = device.state;
  const output = state ? state.output : null;
  const id = attr(device.id);

  let spec = escapeHTML(device.baseUrl);
  if (output) {
    spec += " · " + escapeHTML(String(output.backend || "").toUpperCase());
    spec += " " + escapeHTML(output.device || "default");
    spec += " · " + (output.sampleRate || 0) / 1000 + "kHz";
    if (!output.open) spec += " · SINK CLOSED";
  }

  let html = '<div class="panel panel-wide samoradio-card">' +
    '<div class="panel-head"><span>// ' + escapeHTML(device.name) + '</span><span>' + deviceStatusChip(device) + '</span></div>' +
    '<div class="samoradio-spec">' + spec + '</div>';

  if (!device.paired) {
    html += '<div class="empty-state">// not paired — Samo has not given this device a token yet</div>';
  }
  if (state && state.error) {
    html += '<div class="composer-hint bad">// ' + escapeHTML(state.error) + '</div>';
  }
  if (output && output.lastError) {
    html += '<div class="composer-hint bad">// ' + escapeHTML(output.lastError) + '</div>';
  }

  html += nowPlayingBody(device);
  html += transportRow(device);

  if (extras.expanded) {
    html += '<div class="samoradio-settings">' +
      stationRow(device, extras.channels, extras.stations) +
      outputRow(device, extras.outputs) +
    '</div>';
  }

  html += '<div class="samoradio-actions">' +
    '<button class="btn ghost btn-mini" data-action="samoradio-configure" data-id="' + id + '">' +
      (extras.expanded ? "HIDE SETTINGS" : "SETTINGS") + '</button>' +
    '<button class="btn ghost btn-mini" data-action="samoradio-pair" data-id="' + id + '">' +
      (device.paired ? "RE-PAIR" : "PAIR") + '</button>' +
    '<button class="btn danger btn-mini" data-action="samoradio-delete" data-id="' + id + '" data-name="' + attr(device.name) + '">REMOVE</button>' +
  '</div>' +
  '</div>';

  return html;
}

// composerSamoRadioDevice is the "add a device" form. The control token is what
// the installer printed; it is how Samo proves itself to the daemon.
//
// Built through composerHTML rather than hand-rolled, because the composer
// contract is carried by ids, not classes: toggleComposer does
// getElementById("composer-<id>") and composerMessage writes to
// "composer-<id>-message". A hand-written panel with the right classes and the
// right data-composer attribute still looks correct and does absolutely
// nothing when the button is clicked — the lookup misses and the function
// returns silently.
export function composerSamoRadioDevice() {
  const body =
    '<div class="composer-row">' +
      fieldHTML("composerRadioDeviceName", "Name", "Living room", "text", "") +
      fieldHTML("composerRadioDeviceURL", "Control URL", "http://127.0.0.1:7970", "text", "http://127.0.0.1:7970") +
    '</div>' +
    '<div class="composer-row">' +
      fieldHTML("composerRadioDeviceToken", "Control token", "printed by install.sh", "text", "", "full") +
    '</div>' +
    '<div class="composer-actions">' +
      '<button class="btn primary" data-action="composer-submit" data-composer="samo-radio-device">ADD DEVICE</button>' +
      '<button class="btn ghost" data-action="composer-toggle" data-composer="samo-radio-device">CANCEL</button>' +
    '</div>';
  return composerHTML("samo-radio-device", "ADD SAMO-RADIO DEVICE",body,
    "// run packaging/install.sh on the machine with the sound card — it prints the control token and the URL");
}

// samoRadioSendBar is the "play this here instead" control that rides along
// with a detail view — the web equivalent of AirPlay's speaker picker.
export function samoRadioSendBar(devices, payload) {
  const list = (devices || []).filter((device) => device.enabled && device.paired);
  if (list.length === 0) return "";
  let buttons = "";
  list.forEach((device) => {
    buttons += '<button class="btn ghost btn-mini" data-action="samoradio-send"' +
      ' data-id="' + attr(device.id) + '"' +
      ' data-type="' + attr(payload.type) + '"' +
      ' data-ids="' + attr((payload.ids || []).join(",")) + '">' +
      '&#9654; ' + escapeHTML(device.name.toUpperCase()) + '</button>';
  });
  return '<div class="samoradio-send-bar"><span class="samoradio-send-label">// PLAY TO</span>' + buttons + '</div>';
}
