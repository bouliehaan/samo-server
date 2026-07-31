// Value formatting: a value in, a display string out. No DOM, no state.

export function formatDuration(seconds) {
  seconds = Math.max(0, Math.floor(seconds || 0));
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = seconds % 60;
  if (h > 0) return h + "H " + m + "M";
  return m + ":" + String(s).padStart(2, "0");
}

export function formatDate(value) {
  if (!value) return "NEVER";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "UNKNOWN";
  if (date.getFullYear() < 2000) return "NEVER";
  return date.toLocaleString([], { year: "numeric", month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" }).toUpperCase();
}

export function formatUptime(seconds) {
  const s = Math.max(0, Number(seconds) || 0);
  const days = Math.floor(s / 86400);
  const hrs = Math.floor((s % 86400) / 3600);
  const mins = Math.floor((s % 3600) / 60);
  if (days > 0) return days + "d " + hrs + "h";
  if (hrs > 0) return hrs + "h " + mins + "m";
  return mins + "m";
}

export function formatClock(seconds) {
  seconds = Math.max(0, Math.floor(seconds || 0));
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = seconds % 60;
  const mm = h > 0 ? String(m).padStart(2, "0") : String(m);
  const ss = String(s).padStart(2, "0");
  return h > 0 ? h + ":" + mm + ":" + ss : mm + ":" + ss;
}

export function formatDataSize(bytes) {
  bytes = Number(bytes) || 0;
  if (bytes < 1024) return bytes + " B";
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + " KB";
  if (bytes < 1024 * 1024 * 1024) return (bytes / (1024 * 1024)).toFixed(1) + " MB";
  return (bytes / (1024 * 1024 * 1024)).toFixed(2) + " GB";
}

export function minuteToHHMM(min) {
  const h = Math.floor(min / 60);
  const m = min % 60;
  return String(h).padStart(2, "0") + ":" + String(m).padStart(2, "0");
}

export function parseHHMM(text) {
  const trimmed = String(text || "").trim();
  if (!trimmed) return -1;
  const parts = trimmed.split(":");
  if (parts.length !== 2) return -1;
  const h = parseInt(parts[0], 10);
  const m = parseInt(parts[1], 10);
  if (Number.isNaN(h) || Number.isNaN(m) || h < 0 || h > 24 || m < 0 || m > 59) return -1;
  return Math.min(1440, h * 60 + m);
}

export function weekdayMaskToLabel(mask) {
  const names = ["SUN", "MON", "TUE", "WED", "THU", "FRI", "SAT"];
  if (mask === 127) return "EVERY DAY";
  if (mask === 62) return "WEEKDAYS"; // Mon-Fri
  if (mask === 65) return "WEEKENDS"; // Sun+Sat
  const out = [];
  for (let i = 0; i < 7; i++) {
    if (mask & (1 << i)) out.push(names[i]);
  }
  return out.join(", ");
}
