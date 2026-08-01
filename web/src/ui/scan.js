// Scanning: triggering one, tracking the job, and the command-bar readout.
//
// scanWatchJobID is which job this tab is following. Snapshots for any other
// job are ignored — a second dashboard can start a scan this one never asked
// about, and the event stream carries every job to everyone.
//
// The scanLast* values exist because the readout is rebuilt from scratch on
// each update but only some updates carry every field; they are the last
// known good label, text and state to fall back on.

import { api } from "./auth.js";
import { refreshBtn, refreshRing, refreshSub, scanCancelBtn, scanPanel, scanPanelCurrent, scanPanelHistory } from "./elements.js";
import { formatDate } from "./format.js";
import { escapeHTML } from "./html.js";
import { scanPruneSummary } from "./labels.js";

// The two things scanning needs from the shell it lives in. Injected rather
// than imported: app.js imports this module, so importing back would be a
// cycle — and naming them here makes the coupling one short, visible list
// instead of a reach into whatever app.js happens to have in scope.
let refreshActiveView = async () => {};
let closeOtherPanels = () => {};

export function configureScanUI(hooks) {
  if (hooks.refreshActiveView) refreshActiveView = hooks.refreshActiveView;
  if (hooks.closeOtherPanels) closeOtherPanels = hooks.closeOtherPanels;
}

/* ---- Header scan + activity -------------------------------------------
 * REFRESH in the utility bar shows live scan progress and opens the
 * detail panel on click. Resumes in-flight jobs on page load. */
export let scanWatchJobID = "";

export let scanLastFilesSeen = 0;

export let scanLastLabel = "SCAN";

export let scanLastText = "starting…";

export let scanLastState = "idle";

export let libraryNameById = {};

export function rememberLibraries(libs) {
  (libs || []).forEach((lib) => {
    if (lib && lib.id) libraryNameById[lib.id] = lib.name || lib.id;
  });
}

export function scanJobScopeLabel(job) {
  if (!job) return "scan";
  const mode = job.scanMode ? String(job.scanMode).toUpperCase() : "";
  if (job.scope === "subpaths") {
    const name = libraryNameById[job.libraryId] || job.libraryId || "library";
    return (mode ? mode + " · " : "") + "incremental · " + name;
  }
  if (job.scope === "library") {
    const name = libraryNameById[job.libraryId] || job.libraryId || "library";
    return (mode ? mode + " · " : "") + name;
  }
  return (mode ? mode + " · " : "") + "all libraries";
}

export async function ensureScanAvailable() {
  if (scanLastState === "running") {
    await openScanPanel();
    return false;
  }
  return true;
}

export async function triggerLibraryScan(libraryID, mode, libraryName) {
  libraryID = String(libraryID || "").trim();
  mode = String(mode || "quick").trim() || "quick";
  if (!libraryID) {
    updateRefreshUI("error", "FAILED", "library not found");
    return;
  }
  if (!(await ensureScanAvailable())) return;
  const label = libraryName || libraryNameById[libraryID] || "library";
  await triggerScan(async () => {
    const result = await api("/api/v1/libraries/" + encodeURIComponent(libraryID) + "/scan", {
      method: "POST",
      body: { mode: mode },
    });
    const job = result && result.job;
    if (job && job.scope === "library" && job.libraryId && job.libraryId !== libraryID) {
      throw new Error("server attached scan to a different library");
    }
    scanLastLabel = label.toUpperCase();
    return result;
  });
}

export async function triggerLibraryRepair(libraryID, libraryName) {
  libraryID = String(libraryID || "").trim();
  if (!libraryID) {
    updateRefreshUI("error", "FAILED", "library not found");
    return;
  }
  if (!(await ensureScanAvailable())) return;
  const label = libraryName || libraryNameById[libraryID] || "library";
  await triggerScan(async () => {
    scanLastLabel = label.toUpperCase();
    return api("/api/v1/libraries/" + encodeURIComponent(libraryID) + "/scan", { method: "POST", body: { mode: "repair" } });
  });
}

export function updateRefreshUI(state, label, text) {
  scanLastState = state;
  scanLastLabel = label || "SCAN";
  scanLastText = text || "";
  if (!refreshBtn || !refreshSub) return;
  refreshBtn.classList.remove("running", "ok", "error");
  if (state === "running") refreshBtn.classList.add("running");
  if (state === "ok") refreshBtn.classList.add("ok");
  if (state === "error") refreshBtn.classList.add("error");
  if (refreshRing) refreshRing.hidden = state !== "running";
  if (scanCancelBtn) scanCancelBtn.hidden = state !== "running" || !scanWatchJobID || scanLastLabel === "CANCEL";
  if (state === "running") refreshSub.textContent = text || "SCANNING";
  else if (state === "ok") refreshSub.textContent = text || "DONE";
  else if (state === "error") refreshSub.textContent = text || "FAILED";
  else refreshSub.textContent = "READY";
  refreshScanPanelCurrent();
}

export function closeScanPanel() { if (scanPanel) scanPanel.hidden = true; }

export function renderScanJobRow(job, highlight) {
  const seen = job.filesSeen || 0;
  const total = job.filesTotal || 0;
  let filesText = total > 0 ? (seen + " / " + total + " files") : (seen + " files indexed");
  if (job.currentPath && (job.status === "running" || job.status === "pending")) {
    filesText += " · " + job.currentPath;
  } else if (seen === 0 && (job.status === "running" || job.status === "pending")) {
    filesText = "enumerating library…";
  }
  const scope = scanJobScopeLabel(job);
  return '<div class="scan-job-row' + (highlight ? " active" : "") + '">' +
    '<div class="name">' + escapeHTML(String(job.status || "unknown").toUpperCase()) + " · " + escapeHTML(scope.toUpperCase()) + '</div>' +
    '<div class="meta">' + escapeHTML(filesText) +
      " · started " + formatDate(job.startedAt) +
      escapeHTML(scanPruneSummary(job)) +
      (job.error ? " · " + escapeHTML(job.error) : "") +
    '</div></div>';
}

export function refreshScanPanelCurrent() {
  if (!scanPanel || scanPanel.hidden || !scanPanelCurrent) return;
  if (scanWatchJobID && scanLastState === "running") {
    const label = scanLastLabel === "CANCEL" ? "CANCELLING" : "RUNNING";
    scanPanelCurrent.innerHTML = '<div class="scan-job-row active"><div class="name">' + label + ' · ' + escapeHTML(scanLastLabel === "CANCEL" ? "SCAN" : scanLastLabel) + '</div><div class="meta">' + escapeHTML(scanLastText) + '</div></div>';
  }
}

export async function openScanPanel() {
  if (!scanPanel) return;
  closeOtherPanels();
  scanPanel.hidden = false;
  if (scanPanelCurrent) scanPanelCurrent.innerHTML = '<div class="boot-line">// loading...</div>';
  if (scanPanelHistory) scanPanelHistory.innerHTML = "";
  try {
    const [jobs, libraries] = await Promise.all([
      api("/api/v1/scan/jobs?limit=12"),
      api("/api/v1/libraries").catch(() => ({ items: [] })),
    ]);
    rememberLibraries((libraries && libraries.items) || []);
    const items = (jobs && jobs.items) || [];
    let active = scanWatchJobID ? items.find((job) => job.id === scanWatchJobID) : null;
    if (!active) active = items.find((job) => job.status === "running" || job.status === "pending") || null;
    if (scanPanelCurrent) {
      if (active) scanPanelCurrent.innerHTML = renderScanJobRow(active, true);
      else if (scanLastState === "running") {
        scanPanelCurrent.innerHTML = '<div class="scan-job-row active"><div class="name">RUNNING</div><div class="meta">' + escapeHTML(scanLastText) + '</div></div>';
      } else {
        scanPanelCurrent.innerHTML = '<div class="empty-state">// no scan running</div>';
      }
    }
    if (scanCancelBtn) {
      scanCancelBtn.hidden = !(active && (active.status === "running" || active.status === "pending"));
    }
    if (scanPanelHistory) {
      if (items.length) {
        scanPanelHistory.innerHTML = '<div class="scan-history-head">// recent jobs</div>' + items.map((job) => renderScanJobRow(job, active && job.id === active.id)).join("");
      } else {
        scanPanelHistory.innerHTML = '<div class="empty-state">// no scan history yet</div>';
      }
    }
  } catch (err) {
    if (scanPanelCurrent) scanPanelCurrent.innerHTML = '<div class="empty-state">// ' + escapeHTML(err.message) + '</div>';
  }
}

export function applyScanJobStatus(job, jobID) {
  scanLastFilesSeen = job.filesSeen || 0;
  const total = job.filesTotal || 0;
  if (job.status === "running" || job.status === "pending") {
    if (scanLastLabel !== "CANCEL") {
      let progress = total > 0 ? scanLastFilesSeen + " of " + total + " files" : scanLastFilesSeen + " files";
      if (job.currentPath) {
        progress += " · " + job.currentPath;
      } else if (scanLastFilesSeen === 0) {
        progress = "enumerating library…";
      }
      updateRefreshUI("running", "SCAN", scanJobScopeLabel(job) + " · " + progress);
    }
    return false;
  }
  if (scanWatchJobID === jobID) {
    scanWatchJobID = "";
  }
  if (job.status === "completed") {
    const parts = [scanLastFilesSeen + " files"];
    if (job.filesPruned) parts.push(job.filesPruned + " stale files");
    if (job.itemsPruned) parts.push(job.itemsPruned + " orphan items");
    updateRefreshUI("ok", "DONE", parts.join(" · "));
    setTimeout(() => updateRefreshUI("idle", "SCAN", "READY"), 5000);
  } else if (job.status === "cancelled") {
    const parts = [(scanLastFilesSeen || job.filesSeen || 0) + " files indexed"];
    updateRefreshUI("idle", "CANCELLED", parts.join(" · "));
    setTimeout(() => updateRefreshUI("idle", "SCAN", "READY"), 5000);
  } else {
    updateRefreshUI("error", "FAILED", job.error || "scan failed");
  }
  return true;
}

// Watching a scan is now just remembering which job we care about: progress
// arrives on the event stream. The one fetch here covers the gap between the
// job existing and its first published snapshot.
export async function watchScanJob(jobID) {
  if (!jobID) return;
  scanWatchJobID = jobID;
  scanLastFilesSeen = 0;
  updateRefreshUI("running", "SCAN", "starting...");
  try {
    await handleScanJobEvent(await api("/api/v1/scan/jobs/" + encodeURIComponent(jobID)));
  } catch (err) {
    scanWatchJobID = "";
    updateRefreshUI("error", "FAILED", err.message || "could not read scan job");
  }
}

// Applies one scan-job snapshot, from the stream or from the initial fetch.
// Snapshots for a job we are not watching are ignored — a second dashboard
// can start a scan we never asked about.
export async function handleScanJobEvent(job) {
  if (!job || !job.id || job.id !== scanWatchJobID) return;
  if (applyScanJobStatus(job, job.id)) {
    await refreshActiveView();
  }
}

export async function cancelActiveScan() {
  const jobID = scanWatchJobID;
  if (!jobID) return;
  if (!confirm("Cancel the running scan? Files already indexed stay in your library.")) return;
  updateRefreshUI("running", "CANCEL", "cancelling…");
  if (scanCancelBtn) scanCancelBtn.hidden = true;
  try {
    await api("/api/v1/scan/jobs/" + encodeURIComponent(jobID) + "/cancel", { method: "POST" });
  } catch (err) {
    updateRefreshUI("error", "FAILED", err.message || "cancel failed");
    if (scanCancelBtn) scanCancelBtn.hidden = false;
    return;
  }
  const deadline = Date.now() + 60000;
  while (Date.now() < deadline && scanWatchJobID === jobID) {
    try {
      const job = await api("/api/v1/scan/jobs/" + encodeURIComponent(jobID));
      if (applyScanJobStatus(job, jobID)) {
        await refreshActiveView();
        if (scanPanel && !scanPanel.hidden) await openScanPanel();
        return;
      }
    } catch (err) {
      scanWatchJobID = "";
      updateRefreshUI("error", "FAILED", err.message || "could not read scan job");
      return;
    }
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
}

export async function triggerScan(kickoff) {
  updateRefreshUI("running", "SCAN", "starting...");
  try {
    const result = await kickoff();
    const jobID = result && result.job && result.job.id;
    if (!jobID) { updateRefreshUI("error", "FAILED", "no job id returned"); return; }
    watchScanJob(jobID);
  } catch (err) {
    const message = err.message || "scan failed";
    if (message.toLowerCase().includes("already in progress")) {
      updateRefreshUI("error", "BUSY", "another scan running");
      await openScanPanel();
      return;
    }
    updateRefreshUI("error", "FAILED", message);
  }
}
