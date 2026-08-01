// The page's fixed furniture, looked up once.
//
// These are exported as plain consts rather than accessor functions on
// purpose: an ES module import is a live binding, so every `main.innerHTML`
// in the app keeps working unchanged. That is what made pulling them out of
// app.js a zero-churn move — and they were the single biggest thing tying it
// together, with 37 functions closing over `main` alone.
//
// Safe to resolve at module load because the bundle is a <script type="module">
// at the end of <body>: module scripts are deferred, so the document is fully
// parsed before this runs. Every element here is part of the static shell in
// internal/api/web/app.html and is never replaced, only written into.

// The shell.
export const main = document.getElementById("appMain");
export const nav = document.getElementById("appNav");

// The player dock.
export const playerDock = document.getElementById("playerDock");
export const audio = document.getElementById("audioPlayer");
export const playerTitle = document.getElementById("playerTitle");
export const playerSub = document.getElementById("playerSub");
export const playerToggle = document.getElementById("playerToggle");
export const playerGlyph = document.getElementById("playerGlyph");
export const playerStop = document.getElementById("playerStop");
export const playerSeek = document.getElementById("playerSeek");
export const playerSeekBar = document.getElementById("playerSeekBar");
export const playerSeekHead = document.getElementById("playerSeekHead");
export const playerTimeEl = document.getElementById("playerTime");
export const playerDurationEl = document.getElementById("playerDuration");

// The command bar: refresh/activity/scan readouts and their panels.
export const refreshBtn = document.getElementById("refreshBtn");
export const refreshSub = document.getElementById("refreshSub");
export const refreshRing = document.getElementById("refreshRing");
export const nowPlayingBtn = document.getElementById("nowPlayingBtn");
export const nowPlayingSub = document.getElementById("nowPlayingSub");
export const activityPanel = document.getElementById("activityPanel");
export const activityBody = document.getElementById("activityBody");
export const scanPanel = document.getElementById("scanPanel");
export const scanPanelCurrent = document.getElementById("scanPanelCurrent");
export const scanPanelHistory = document.getElementById("scanPanelHistory");
export const scanCancelBtn = document.getElementById("scanCancelBtn");

// The identify modal.
export const identifyModal = document.getElementById("identifyModal");
export const identifyTitle = document.getElementById("identifyTitle");
export const identifyQuery = document.getElementById("identifyQuery");
export const identifyResults = document.getElementById("identifyResults");
export const identifyForm = document.getElementById("identifyForm");
