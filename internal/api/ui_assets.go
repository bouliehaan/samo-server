package api

// samoBaseCSS is the shared design language across the setup wizard and
// dashboard. It establishes the black/amber palette, monospace status chrome,
// and the SAMO/SERVER wordmark used at the top of every page.
const samoBaseCSS = `
:root {
  --bg: #000;
  --surface: #0a0a0a;
  --surface-2: #141414;
  --line: #1d1d1d;
  --text: #fafafa;
  --text-dim: #9c9c9c;
  --muted: #5f5f5f;
  --ghost: #303030;
  --accent: #f59e0b;
  --accent-strong: #fbbf24;
  --danger: #ef4444;
  --sans: -apple-system, BlinkMacSystemFont, "Segoe UI", Inter, ui-sans-serif, sans-serif;
  --mono: ui-monospace, "JetBrains Mono", "Fira Code", "Menlo", monospace;
  color-scheme: dark;
}
* { box-sizing: border-box; }
[hidden] { display: none !important; }
html, body {
  margin: 0;
  padding: 0;
  background: var(--bg);
  color: var(--text);
  font-family: var(--sans);
  font-size: 16px;
  min-height: 100vh;
}
.grid-bg {
  position: fixed;
  inset: 0;
  pointer-events: none;
  background-image:
    radial-gradient(circle at 1px 1px, rgba(255,255,255,0.06) 1px, transparent 0);
  background-size: 28px 28px;
  z-index: 0;
  mask-image: radial-gradient(ellipse at center, black 30%, transparent 80%);
}
/* Pages own their own <main> layout — only the wizard uses .page-main. */
.page-main {
  position: relative;
  z-index: 1;
  max-width: 880px;
  margin: 0 auto;
  padding: 56px 24px 96px;
  display: grid;
  gap: 48px;
}

/* ---- Unified wordmark + status ---- */
/* One component, three size variants (head/hero/bar) so every page reads the
 * same: setup uses .head, login uses .hero, the app shell uses .bar. */
.samo-wm {
  display: inline-flex;
  align-items: baseline;
  gap: 10px;
  font-family: var(--sans);
  font-weight: 900;
  letter-spacing: -0.04em;
  line-height: 0.9;
  color: var(--text);
}
.samo-wm .word { color: var(--text); display: inline-block; }
.samo-wm .word.dim { color: var(--text-dim); }
.samo-wm.head,
.samo-wm.hero {
  flex-direction: column;
  align-items: flex-start;
}
.samo-wm.head { font-size: clamp(2.5rem, 6vw, 4.5rem); gap: 2px; }
.samo-wm.hero { font-size: clamp(3rem, 9vw, 6.5rem); gap: 4px; letter-spacing: -0.045em; }
.samo-wm.bar  { font-size: 1.2rem; letter-spacing: -0.02em; gap: 8px; }

.samo-status {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-family: var(--mono);
  font-size: 0.72rem;
  letter-spacing: 0.18em;
  text-transform: uppercase;
  color: var(--muted);
}
.samo-status .dot {
  width: 8px;
  height: 8px;
  background: var(--accent);
  box-shadow: 0 0 12px var(--accent);
  display: inline-block;
}
.samo-status.pulse .dot { animation: samoPulse 1.8s ease-in-out infinite; }
.samo-status.bar { font-size: 0.66rem; letter-spacing: 0.16em; }
.samo-status.bar .dot { width: 6px; height: 6px; box-shadow: 0 0 8px var(--accent); }
@keyframes samoPulse { 0%,100%{opacity:1} 50%{opacity:0.35} }

/* Standalone page header: wordmark + optional ledger on the right.
 * Used by setup wizard; .samo-head.hero stacks for the login split layout. */
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
  font-size: 0.7rem;
  letter-spacing: 0.16em;
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

.card {
  background: var(--surface);
  border: 1px solid var(--line);
  padding: 28px;
  position: relative;
}
.card::before {
  content: "";
  position: absolute;
  top: -1px;
  left: -1px;
  width: 16px;
  height: 16px;
  border-top: 1px solid var(--accent);
  border-left: 1px solid var(--accent);
  pointer-events: none;
}
.card::after {
  content: "";
  position: absolute;
  bottom: -1px;
  right: -1px;
  width: 16px;
  height: 16px;
  border-bottom: 1px solid var(--accent);
  border-right: 1px solid var(--accent);
  pointer-events: none;
}

.card-head {
  font-family: var(--mono);
  font-size: 0.72rem;
  letter-spacing: 0.22em;
  text-transform: uppercase;
  color: var(--accent);
  margin-bottom: 14px;
  display: flex;
  align-items: center;
  gap: 8px;
}
.card-head .caret {
  color: var(--accent);
}

.field {
  display: grid;
  gap: 6px;
  margin-bottom: 16px;
}
.field-label {
  font-family: var(--mono);
  font-size: 0.7rem;
  letter-spacing: 0.18em;
  text-transform: uppercase;
  color: var(--muted);
}
.field input,
.field select,
.field textarea {
  font-family: var(--mono);
  font-size: 0.95rem;
  padding: 12px 14px;
  background: #000;
  color: var(--text);
  border: 1px solid var(--line);
  outline: none;
  border-radius: 0;
  -webkit-appearance: none;
  appearance: none;
}
.field input::placeholder { color: var(--ghost); }
.field input:focus,
.field select:focus,
.field textarea:focus {
  border-color: var(--accent);
  box-shadow: 0 0 0 1px var(--accent), 0 0 18px -6px var(--accent);
}
.field select {
  background-image: linear-gradient(45deg, transparent 50%, var(--text-dim) 50%), linear-gradient(135deg, var(--text-dim) 50%, transparent 50%);
  background-position: calc(100% - 16px) center, calc(100% - 10px) center;
  background-size: 6px 6px, 6px 6px;
  background-repeat: no-repeat;
  padding-right: 36px;
}

.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  margin-top: 8px;
}
.btn {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  padding: 11px 18px;
  border-radius: 0;
  border: 1px solid transparent;
  font-family: var(--mono);
  font-size: 0.78rem;
  font-weight: 600;
  letter-spacing: 0.18em;
  text-transform: uppercase;
  cursor: pointer;
  text-decoration: none;
  transition: background 90ms ease-out, color 90ms ease-out, border-color 90ms ease-out, box-shadow 90ms ease-out;
}
.btn:disabled {
  opacity: 0.55;
  cursor: progress;
}
.btn.primary {
  background: var(--accent);
  color: #000;
}
.btn.primary:hover:not(:disabled) {
  background: var(--accent-strong);
  box-shadow: 0 0 24px -6px var(--accent);
}
.btn.ghost {
  background: transparent;
  color: var(--text);
  border-color: var(--ghost);
}
.btn.ghost:hover:not(:disabled) {
  border-color: var(--text);
}
.btn.danger {
  background: transparent;
  color: var(--danger);
  border-color: var(--danger);
}

.error-line {
  margin-top: 14px;
  padding: 10px 14px;
  border: 1px solid var(--danger);
  background: color-mix(in srgb, var(--danger) 8%, transparent);
  color: var(--danger);
  font-family: var(--mono);
  font-size: 0.78rem;
  letter-spacing: 0.06em;
}

/* Shared utility text styles used across pages. */
p.lede {
  margin: 0;
  color: var(--text-dim);
  font-family: var(--sans);
  font-size: 0.95rem;
  line-height: 1.55;
  max-width: 64ch;
}
.kind-chip {
  display: inline-block;
  font-family: var(--mono);
  font-size: 0.6rem;
  letter-spacing: 0.18em;
  text-transform: uppercase;
  padding: 3px 8px;
  border: 1px solid var(--accent);
  color: var(--accent);
  line-height: 1;
  background: color-mix(in srgb, var(--accent) 6%, transparent);
}

a { color: var(--accent); text-decoration: none; }
a:hover { text-decoration: underline; }

::selection { background: var(--accent); color: #000; }

/* Subtle scrollbar tuning so panels feel native to the theme. */
* { scrollbar-width: thin; scrollbar-color: var(--ghost) transparent; }
*::-webkit-scrollbar { width: 8px; height: 8px; }
*::-webkit-scrollbar-track { background: transparent; }
*::-webkit-scrollbar-thumb {
  background: var(--ghost);
  border-radius: 0;
}
*::-webkit-scrollbar-thumb:hover { background: var(--accent); }
`
