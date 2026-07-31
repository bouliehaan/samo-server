// The scan/repair button clusters, which appear per library, per kind and
// globally with the same actions behind them.

import { attr } from "./html.js";
import { librarySupportsRepair } from "./labels.js";

export async function withButton(button, busyText, fn) {
  const oldText = button ? button.textContent : "";
  if (button) {
    button.disabled = true;
    if (busyText) button.textContent = busyText;
  }
  try {
    return await fn();
  } finally {
    if (button) {
      button.disabled = false;
      button.textContent = oldText;
    }
  }
}

export function globalScanActionsHTML(options) {
  options = options || {};
  const btnClass = options.btnClass || "btn ghost btn-small";
  const primaryClass = options.primaryClass || "btn primary btn-small";
  let html =
    '<button class="' + btnClass + '" data-action="scan-quick-all" title="Quick rescan of every attached library — new and changed files only">SCAN ALL</button>' +
    '<button class="' + primaryClass + '" data-action="scan-all" title="Full scan — re-probe every file in every library">FULL SCAN</button>' +
    '<button class="' + btnClass + '" data-action="repair-all" title="Re-index music metadata and covers without re-reading every file">REPAIR INDEX</button>';
  if (options.includeArtistPhotos !== false) {
    html += '<button class="' + btnClass + '" data-action="fetch-artist-images" title="Download missing artist photos from Deezer into the local cover cache">FETCH ARTIST PHOTOS</button>';
  }
  return html;
}

export function libraryScanActionsHTML(lib, btnClass) {
  btnClass = btnClass || "btn ghost btn-mini";
  if (!lib || !lib.id) return "";
  let html =
    '<button class="' + btnClass + '" data-action="scan-library" data-id="' + attr(lib.id) + '" title="Quick scan — new and changed files only">SCAN</button>' +
    '<button class="' + btnClass + '" data-action="scan-library-full" data-id="' + attr(lib.id) + '" title="Full scan — re-probe every file in this library">FULL</button>';
  if (librarySupportsRepair(lib)) {
    html += '<button class="' + btnClass + '" data-action="repair-library" data-id="' + attr(lib.id) + '" title="Re-index metadata and covers (music only)">REPAIR</button>';
  }
  return html;
}

export function libraryKindScanActionsHTML(kind) {
  const btnClass = "btn ghost btn-small";
  const folder = kind === "audiobook" ? "Audiobooks" : "Podcasts";
  return '<button class="' + btnClass + '" data-action="scan-library-kind" data-kind="' + attr(kind) + '" data-mode="quick" title="Quick scan of the ' + folder + ' folder — new/changed files only">SCAN</button>' +
    '<button class="' + btnClass + '" data-action="scan-library-kind" data-kind="' + attr(kind) + '" data-mode="full" title="Full scan of the ' + folder + ' folder — re-probe every file">FULL</button>';
}
