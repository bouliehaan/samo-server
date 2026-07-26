package api

// samoBaseCSS is the shared design language across the setup wizard, login
// screen, and dashboard: the SAMO SERVER console. One monospace face (Office
// Code Pro), a strictly NEUTRAL black → grey → white ladder (no blue/steel
// tint), hard 90-degree corners, and white used only as a hallmark (active
// state, focus, the online dot). It is the terminal of the Samo family — it
// shares the clients' structure and restraint, not their cool-grey palette.
//
// The font is embedded in the binary and served from /assets/fonts (see
// fonts.go), so the UI is fully styled offline with no CDN round-trip.
const samoBaseCSS = `
@font-face {
  font-family: "OfficeCodePro";
  font-style: normal;
  font-weight: 400;
  src: url("/assets/fonts/officecodepro-regular.otf") format("opentype");
  font-display: swap;
}
@font-face {
  font-family: "OfficeCodePro";
  font-style: normal;
  font-weight: 700;
  src: url("/assets/fonts/officecodepro-bold.otf") format("opentype");
  font-display: swap;
}

:root {
  /* Neutral ladder — pure black up through grey, no hue. Depth reads by
   * value alone. */
  --bg: #000000;
  --bg-elevated: #0d0d0d;
  --surface: #151515;      /* cards, list rows, tiles */
  --surface-2: #1f1f1f;    /* raised chrome — hover, active fills */
  --surface-high: #2b2b2b; /* highest — popovers, menus */

  /* Hairlines — plain white at low alpha, no tint. */
  --line: rgba(255, 255, 255, 0.08);
  --line-strong: rgba(255, 255, 255, 0.15);

  /* Ink — neutral greys. Primary text sits just under pure white so white
   * can act as the accent. */
  --text: #f2f2f2;
  --text-dim: #9e9e9e;
  --muted: #6a6a6a;
  --ghost: #3a3a3a;

  /* Hallmark = pure white. Used sparingly: active nav, focus ring, the online
   * dot, one highlighted stat, the primary button. Never as decoration. */
  --accent: #ffffff;
  --accent-strong: #ffffff;
  --accent-soft: rgba(255, 255, 255, 0.10);
  --accent-line: rgba(255, 255, 255, 0.30);

  /* Destructive — the only non-grey ink, kept desaturated and used only as a
   * functional signal on delete/error surfaces. */
  --danger: #cf6f6f;

  /* One face for the whole server. --sans/--serif alias it so any stray
   * reference still resolves to the mono. */
  --mono: "OfficeCodePro", ui-monospace, "SF Mono", "JetBrains Mono", Menlo, monospace;
  --sans: var(--mono);
  --serif: var(--mono);

  /* Hard console corners — no rounding anywhere. */
  --r-sm: 0;
  --r-md: 0;
  --r-lg: 0;

  --rail-w: 292px;

  color-scheme: dark;
}
* { box-sizing: border-box; }
[hidden] { display: none !important; }
html, body {
  margin: 0;
  padding: 0;
  background: var(--bg);
  color: var(--text);
  font-family: var(--mono);
  font-size: 18px;
  line-height: 1.55;
  min-height: 100vh;
  -webkit-font-smoothing: antialiased;
  text-rendering: optimizeLegibility;
}

/* Faint neutral dot-grid. */
.grid-bg {
  position: fixed;
  inset: 0;
  pointer-events: none;
  background-image:
    radial-gradient(circle at 1px 1px, rgba(255, 255, 255, 0.045) 1px, transparent 0);
  background-size: 30px 30px;
  z-index: 0;
  mask-image: radial-gradient(ellipse at 30% 0%, black 20%, transparent 78%);
}
/* Pages own their own layout — only the wizard/login use .page-main. */
.page-main {
  position: relative;
  z-index: 1;
  max-width: 920px;
  margin: 0 auto;
  padding: 56px 24px 96px;
  display: grid;
  gap: 44px;
}

/* ---- Unified wordmark + status ---- */
.samo-wm {
  display: inline-flex;
  align-items: baseline;
  gap: 10px;
  font-family: var(--mono);
  font-weight: 700;
  letter-spacing: 0.02em;
  line-height: 1;
  color: var(--text);
  text-transform: lowercase;
}
.samo-wm .word { color: var(--text); display: inline-block; }
.samo-wm .word.dim { color: var(--muted); }
.samo-wm.head,
.samo-wm.hero {
  flex-direction: column;
  align-items: flex-start;
  gap: 4px;
}
.samo-wm.head { font-size: clamp(2.1rem, 5vw, 3.6rem); letter-spacing: 0.01em; }
.samo-wm.hero { font-size: clamp(2.6rem, 7vw, 4.8rem); letter-spacing: 0.005em; }
.samo-wm.bar  { font-size: 1.43rem; letter-spacing: 0.05em; gap: 8px; }

.samo-status {
  display: inline-flex;
  align-items: center;
  gap: 9px;
  font-family: var(--mono);
  font-size: 0.95rem;
  letter-spacing: 0.14em;
  text-transform: uppercase;
  color: var(--muted);
}
.samo-status .dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--accent);
  box-shadow: 0 0 9px var(--accent);
  display: inline-block;
  flex: none;
}
.samo-status.pulse .dot { animation: samoPulse 2s ease-in-out infinite; }
.samo-status.bar { font-size: 0.9rem; letter-spacing: 0.12em; }
.samo-status.bar .dot { width: 7px; height: 7px; box-shadow: 0 0 7px var(--accent); }
@keyframes samoPulse { 0%,100%{opacity:1} 50%{opacity:0.3} }

/* Standalone page header: wordmark + optional ledger on the right. */
.samo-head {
  display: grid;
  grid-template-columns: 1fr auto;
  align-items: end;
  gap: 24px;
}
.samo-head .samo-status { margin-top: 14px; }

.samo-ledger {
  display: grid;
  gap: 6px;
  text-align: right;
  font-family: var(--mono);
  font-size: 0.95rem;
  letter-spacing: 0.12em;
  text-transform: uppercase;
}
.samo-ledger > div {
  display: grid;
  grid-template-columns: 1fr auto;
  gap: 12px;
  color: var(--text);
}
.samo-ledger .label { color: var(--muted); }
.samo-ledger .value { color: var(--text); }

/* ---- Card — boxed surface for setup steps and the login panel. ---- */
.card {
  background: var(--surface);
  border: 1px solid var(--line);
  padding: 34px;
  position: relative;
}
/* Corner ticks — a technical-drawing motif, plain white hairline. */
.card::before,
.card::after {
  content: "";
  position: absolute;
  width: 13px;
  height: 13px;
  pointer-events: none;
}
.card::before {
  top: -1px;
  left: -1px;
  border-top: 1px solid var(--line-strong);
  border-left: 1px solid var(--line-strong);
}
.card::after {
  bottom: -1px;
  right: -1px;
  border-bottom: 1px solid var(--line-strong);
  border-right: 1px solid var(--line-strong);
}

.card-head {
  font-family: var(--mono);
  font-size: 0.95rem;
  letter-spacing: 0.2em;
  text-transform: uppercase;
  color: var(--text-dim);
  margin-bottom: 16px;
  display: flex;
  align-items: center;
  gap: 8px;
}
.card-head .caret { color: var(--accent); }

/* ---- Form primitives ---- */
.field {
  display: grid;
  gap: 8px;
  margin-bottom: 18px;
}
.field-label {
  font-family: var(--mono);
  font-size: 0.92rem;
  letter-spacing: 0.14em;
  text-transform: uppercase;
  color: var(--muted);
}
.field input,
.field select,
.field textarea {
  font-family: var(--mono);
  font-size: 1.25rem;
  padding: 14px 16px;
  background: var(--bg);
  color: var(--text);
  border: 1px solid var(--line-strong);
  border-radius: 0;
  outline: none;
  -webkit-appearance: none;
  appearance: none;
}
.field input::placeholder { color: var(--ghost); }
.field input:focus,
.field select:focus,
.field textarea:focus {
  border-color: var(--accent);
  box-shadow: inset 0 0 0 1px var(--accent-soft);
}
.field select {
  background-image: linear-gradient(45deg, transparent 50%, var(--text-dim) 50%), linear-gradient(135deg, var(--text-dim) 50%, transparent 50%);
  background-position: calc(100% - 16px) center, calc(100% - 11px) center;
  background-size: 6px 6px, 6px 6px;
  background-repeat: no-repeat;
  padding-right: 36px;
}

.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  margin-top: 10px;
}
/* Buttons: sharp, bordered, no glow, no fill-blob — terminal, not material. */
.btn {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  padding: 13px 22px;
  border-radius: 0;
  border: 1px solid transparent;
  font-family: var(--mono);
  font-size: 1rem;
  font-weight: 700;
  letter-spacing: 0.12em;
  text-transform: uppercase;
  cursor: pointer;
  text-decoration: none;
  transition: background 100ms ease, color 100ms ease, border-color 100ms ease;
}
.btn:disabled { opacity: 0.5; cursor: progress; }
.btn.primary {
  background: var(--accent);
  color: #000;
  border-color: var(--accent);
}
.btn.primary:hover:not(:disabled) { background: #d8d8d8; border-color: #d8d8d8; }
.btn.ghost {
  background: transparent;
  color: var(--text-dim);
  border-color: var(--line-strong);
}
.btn.ghost:hover:not(:disabled) { color: var(--text); border-color: var(--text); }
.btn.danger {
  background: transparent;
  color: var(--danger);
  border-color: color-mix(in srgb, var(--danger) 55%, transparent);
}
.btn.danger:hover:not(:disabled) {
  border-color: var(--danger);
  background: color-mix(in srgb, var(--danger) 12%, transparent);
}

.error-line {
  margin-top: 14px;
  padding: 11px 14px;
  border: 1px solid color-mix(in srgb, var(--danger) 60%, transparent);
  background: color-mix(in srgb, var(--danger) 8%, transparent);
  color: var(--danger);
  font-family: var(--mono);
  font-size: 1.02rem;
  letter-spacing: 0.03em;
}

/* Shared utility text. */
p.lede {
  margin: 0;
  color: var(--text-dim);
  font-family: var(--mono);
  font-size: 1.18rem;
  line-height: 1.65;
  max-width: 62ch;
}
.kind-chip {
  display: inline-block;
  font-family: var(--mono);
  font-size: 0.9rem;
  letter-spacing: 0.14em;
  text-transform: uppercase;
  padding: 3px 8px;
  border: 1px solid var(--line-strong);
  color: var(--text-dim);
  line-height: 1;
  background: var(--surface-2);
}

a { color: var(--accent); text-decoration: none; }
a:hover { color: var(--accent-strong); text-decoration: underline; }

::selection { background: var(--accent); color: #000; }

/* Scrollbars — neutral grey, the one place a rounded thumb is fine. */
* { scrollbar-width: thin; scrollbar-color: var(--surface-high) transparent; }
*::-webkit-scrollbar { width: 10px; height: 10px; }
*::-webkit-scrollbar-track { background: transparent; }
*::-webkit-scrollbar-thumb {
  background: var(--surface-high);
  border: 2px solid transparent;
  background-clip: padding-box;
}
*::-webkit-scrollbar-thumb:hover { background: var(--muted); }
`
