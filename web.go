package main

// indexHTML is the control panel at GET /: self-contained (no external
// assets), it polls /status, /readings and /events, reads /config once and
// posts to the API. Text from the server is only ever set with textContent.
// The pages (Dashboard, Sensor, Event log, Settings) are sections switched by
// the URL's hash; the sidebar collapses, and is a drawer on narrow screens.
const indexHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>libre2sim</title>
<style>
:root {
  --bg: #f4f6fa; --card: #fff; --fg: #1f2633; --muted: #6b7385; --line: #e6e9f0; --soft: #f7f8fb;
  --accent: #4f6bed; --accent-soft: #eef1fe; --ok: #1f9d6a; --ok-soft: #e7f6ef; --warn: #c2620c; --warn-soft: #fdf1e5;
  --nfc: #7c4dcc; --nfc-soft: #f2edfb;
  --side: #161b26; --side-fg: #aab2c5; --side-hi: #fff; --side-hover: #222a3a;
  --shadow: 0 1px 2px rgba(20, 28, 45, .04), 0 4px 16px rgba(20, 28, 45, .06);
  --radius: 14px; --sidew: 232px; --sidew-collapsed: 68px;
}
* { box-sizing: border-box; }
html, body { margin: 0; }
body { font: 14px/1.45 -apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif; color: var(--fg); background: var(--bg); }
button, input, select { font: inherit; color: inherit; }

/* Sidebar */
.side { position: fixed; inset: 0 auto 0 0; width: var(--sidew); background: var(--side); color: var(--side-fg); display: flex;
  flex-direction: column; transition: width .2s ease, transform .2s ease; z-index: 20; overflow: hidden; }
.brand { display: flex; align-items: center; gap: 10px; height: 64px; padding: 0 20px; color: var(--side-hi); font-weight: 650;
  font-size: 16px; white-space: nowrap; }
.logo { flex: none; width: 28px; height: 28px; border-radius: 8px; background: linear-gradient(135deg, #6d83f2, #4f6bed);
  display: grid; place-items: center; color: #fff; font-size: 13px; font-weight: 700; }
.nav { list-style: none; margin: 8px 0 0; padding: 0 12px; display: grid; gap: 2px; }
.nav a { display: flex; align-items: center; gap: 12px; padding: 10px 12px; border-radius: 10px; color: inherit; text-decoration: none;
  white-space: nowrap; }
.nav a:hover { background: var(--side-hover); color: var(--side-hi); }
.nav a.active { background: var(--accent); color: #fff; }
.nav svg, .collapse svg { flex: none; width: 20px; height: 20px; stroke: currentColor; fill: none; stroke-width: 1.8;
  stroke-linecap: round; stroke-linejoin: round; }
.side-foot { margin-top: auto; padding: 12px; }
.collapse { display: flex; align-items: center; gap: 12px; width: 100%; padding: 10px 12px; border: 0; border-radius: 10px;
  background: none; color: var(--side-fg); cursor: pointer; white-space: nowrap; }
.collapse:hover { background: var(--side-hover); color: var(--side-hi); }
body.collapsed .side { width: var(--sidew-collapsed); }
body.collapsed .side .txt { opacity: 0; pointer-events: none; }
body.collapsed .collapse svg { transform: rotate(180deg); }
.txt { transition: opacity .15s; }

/* Main */
.main { margin-left: var(--sidew); transition: margin-left .2s ease; min-height: 100vh; }
body.collapsed .main { margin-left: var(--sidew-collapsed); }
.top { position: sticky; top: 0; z-index: 10; display: flex; align-items: center; gap: 12px; height: 64px; padding: 0 28px;
  background: rgba(244, 246, 250, .85); backdrop-filter: blur(8px); -webkit-backdrop-filter: blur(8px); border-bottom: 1px solid var(--line); }
.top h1 { font-size: 18px; margin: 0; font-weight: 650; }
.top .spacer { flex: 1; }
.menu { display: none; border: 0; background: none; padding: 6px; border-radius: 8px; cursor: pointer; }
.menu svg { width: 22px; height: 22px; stroke: var(--fg); fill: none; stroke-width: 1.8; stroke-linecap: round; }
.content { padding: 24px 28px 40px; max-width: 1200px; }
.disclaimer { background: var(--warn-soft); color: var(--warn); border-radius: 10px; padding: 8px 14px; font-size: 13px; margin: 0 0 20px; }
.page { display: none; } .page.active { display: block; }

.grid { display: grid; gap: 20px; }
.g4 { grid-template-columns: repeat(4, 1fr); } .g2 { grid-template-columns: repeat(2, 1fr); } .g3 { grid-template-columns: 2fr 1fr; }
.card { background: var(--card); border-radius: var(--radius); box-shadow: var(--shadow); padding: 20px; min-width: 0; }
.card h2 { font-size: 13px; margin: 0 0 14px; font-weight: 600; color: var(--muted); text-transform: uppercase; letter-spacing: .05em; }
.stat .label { color: var(--muted); font-size: 12.5px; }
.stat .value { font-size: 24px; font-weight: 650; margin-top: 4px; }
.stat .hint { color: var(--muted); font-size: 12.5px; margin-top: 2px; }
.big { font-size: 48px; font-weight: 700; line-height: 1.05; letter-spacing: -.01em; }
.mid { font-size: 32px; font-weight: 650; line-height: 1.1; }
.unit { font-size: 15px; color: var(--muted); font-weight: 500; }
.sub { color: var(--muted); font-size: 13px; }
.note { color: var(--muted); font-size: 12.5px; margin: 12px 0 0; }
.pair { display: grid; grid-template-columns: 1fr 1fr; gap: 20px; margin-bottom: 8px; }

dl { display: grid; grid-template-columns: max-content 1fr; gap: 6px 16px; margin: 0; }
dt { color: var(--muted); } dd { margin: 0; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12.5px; overflow-wrap: anywhere; }

.row { display: flex; flex-wrap: wrap; gap: 10px; align-items: flex-end; margin-bottom: 12px; }
.field { display: grid; gap: 4px; font-size: 12.5px; color: var(--muted); }
input[type=number], select { width: 8.5em; padding: 7px 10px; background: var(--soft); border: 1px solid var(--line); border-radius: 8px; }
input:focus, select:focus, button:focus-visible, a:focus-visible { outline: 2px solid var(--accent); outline-offset: 1px; }
.btn { padding: 8px 14px; border-radius: 8px; border: 1px solid var(--line); background: var(--card); cursor: pointer; font-weight: 500;
  box-shadow: 0 1px 2px rgba(20, 28, 45, .05); }
.btn:hover { border-color: #cfd5e2; background: var(--soft); }
.btn.primary { background: var(--accent); border-color: var(--accent); color: #fff; }
.btn.primary:hover { filter: brightness(1.06); }
.btn.danger { color: var(--warn); }
.btn:disabled { opacity: .45; cursor: default; background: var(--card); }
.btn.on { background: var(--accent-soft); border-color: var(--accent); color: var(--accent); }
.toggle { display: flex; gap: 10px; align-items: center; cursor: pointer; }
.toggle input { width: 18px; height: 18px; accent-color: var(--accent); }

.pills { display: flex; flex-wrap: wrap; gap: 8px; }
.pill { font-size: 12px; border-radius: 99px; padding: 3px 10px; background: var(--soft); color: var(--muted); }
.pill.good { background: var(--ok-soft); color: var(--ok); } .pill.bad { background: var(--warn-soft); color: var(--warn); }

svg.chart { width: 100%; height: 180px; display: block; margin-top: 8px; }
.spark { fill: none; stroke: var(--accent); stroke-width: 2; vector-effect: non-scaling-stroke; }
.area { fill: var(--accent-soft); stroke: none; }
.axis { stroke: var(--line); stroke-width: 1; vector-effect: non-scaling-stroke; }
.future { fill: var(--soft); opacity: .6; }
.band { fill: var(--accent-soft); opacity: .7; stroke: none; }
.now { stroke: var(--fg); stroke-width: 1.5; vector-effect: non-scaling-stroke; }
.nowlabel { font-size: 10px; font-weight: 600; fill: var(--fg); }
.chartwrap { position: relative; }
.chartwrap svg.chart { cursor: crosshair; touch-action: pan-y; }
.hoverline { stroke: var(--muted); stroke-width: 1; stroke-dasharray: 2 3; vector-effect: non-scaling-stroke; }
.tip { position: absolute; top: 4px; z-index: 5; width: 330px; max-width: calc(100% - 8px); background: var(--card); border: 1px solid var(--line);
  border-radius: 10px; box-shadow: 0 8px 24px rgba(20, 30, 60, .14); padding: 10px 12px; font-size: 12.5px; pointer-events: none; }
.tip h4 { margin: 0 0 2px; font-size: 13px; }
.tip .big { font-size: 17px; font-weight: 700; margin: 2px 0 4px; }
.tip .row { display: flex; justify-content: space-between; gap: 10px; margin-top: 7px; font-weight: 600; }
.tip .row code { font-weight: 500; }
.tip .why { color: var(--muted); margin-top: 1px; font-size: 11.5px; line-height: 1.35; }
.marker { stroke: var(--warn); stroke-width: 1; stroke-dasharray: 3 3; vector-effect: non-scaling-stroke; }
.axislabel { fill: var(--muted); font-size: 10px; }

.log { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12px; }
.log div { display: grid; grid-template-columns: 5.5em 4.8em 1fr; gap: 8px; padding: 7px 4px; border-bottom: 1px solid var(--line); }
.log div:last-child { border-bottom: 0; }
.log .t { color: var(--muted); } .log .m { overflow-wrap: anywhere; }
.tag { justify-self: start; align-self: start; font-size: 10.5px; font-weight: 650; padding: 1px 7px; border-radius: 6px; }
.c-nfc .tag { background: var(--nfc-soft); color: var(--nfc); } .c-ble .tag { background: var(--accent-soft); color: var(--accent); }
.c-sensor .tag { background: var(--warn-soft); color: var(--warn); }
#log { max-height: 60vh; overflow-y: auto; }
.empty { color: var(--muted); padding: 8px 4px; }

.scrim { display: none; }
@media (max-width: 1000px) { .g4 { grid-template-columns: repeat(2, 1fr); } .g3, .g2 { grid-template-columns: 1fr; } }
@media (max-width: 760px) {
  .side, body.collapsed .side { width: var(--sidew); transform: translateX(-100%); }
  body.collapsed .side .txt { opacity: 1; pointer-events: auto; }
  body.drawer .side { transform: none; box-shadow: 0 0 40px rgba(0, 0, 0, .3); }
  body.drawer .scrim { display: block; position: fixed; inset: 0; background: rgba(15, 20, 30, .35); z-index: 15; }
  .main, body.collapsed .main { margin-left: 0; }
  .menu { display: inline-flex; }
  .side-foot { display: none; }
  .top { padding: 0 16px; } .content { padding: 16px 16px 32px; }
  .g4 { grid-template-columns: 1fr 1fr; gap: 12px; } .pair { grid-template-columns: 1fr; }
  .big { font-size: 40px; }
}
</style></head><body>

<aside class="side" id="side">
  <div class="brand"><span class="logo">L2</span><span class="txt">libre2sim</span></div>
  <ul class="nav">
    <li><a href="#dashboard" data-page="dashboard" title="Dashboard"><svg viewBox="0 0 24 24"><rect x="3" y="3" width="7" height="9" rx="1.5"/><rect x="14" y="3" width="7" height="5" rx="1.5"/><rect x="14" y="12" width="7" height="9" rx="1.5"/><rect x="3" y="16" width="7" height="5" rx="1.5"/></svg><span class="txt">Dashboard</span></a></li>
    <li><a href="#sensor" data-page="sensor" title="Sensor"><svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="8"/><circle cx="12" cy="12" r="3"/></svg><span class="txt">Sensor</span></a></li>
    <li><a href="#events" data-page="events" title="Event log"><svg viewBox="0 0 24 24"><path d="M4 6h16M4 12h16M4 18h10"/></svg><span class="txt">Event log</span></a></li>
    <li><a href="#settings" data-page="settings" title="Settings"><svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z"/></svg><span class="txt">Settings</span></a></li>
  </ul>
  <div class="side-foot"><button class="collapse" id="collapse" title="Collapse the sidebar"><svg viewBox="0 0 24 24"><path d="M15 6l-6 6 6 6"/></svg><span class="txt">Collapse</span></button></div>
</aside>
<div class="scrim" id="scrim"></div>

<div class="main">
<header class="top">
  <button class="menu" id="menu" aria-label="Open the menu"><svg viewBox="0 0 24 24"><path d="M4 7h16M4 12h16M4 17h16"/></svg></button>
  <h1 id="title">Dashboard</h1><span class="spacer"></span><div class="pills" id="toppills"></div>
</header>
<div class="content">
<p class="disclaimer">For development use only. Not an Abbott product. All data is simulated: never use it for therapy or to make a treatment decision.</p>

<section class="page" id="page-dashboard">
  <div class="grid g4">
    <div class="card stat"><div class="label">Sensor minute</div><div class="value" id="statMin">&mdash;</div><div class="hint" id="statWear">&nbsp;</div></div>
    <div class="card stat"><div class="label">Phone</div><div class="value" id="statPhone">&mdash;</div><div class="hint" id="statAdv">&nbsp;</div></div>
    <div class="card stat"><div class="label">Unlocks</div><div class="value" id="statUnlocks">&mdash;</div><div class="hint" id="statUnlockHint">&nbsp;</div></div>
    <div class="card stat"><div class="label">Glucose model</div><div class="value" id="statModel">&mdash;</div><div class="hint" id="statModelHint">&nbsp;</div></div>
  </div>
  <div class="grid g3" style="margin-top:20px">
    <div class="card"><h2>Readings</h2>
      <div class="pair">
        <div><div class="sub">Current</div><div class="big"><span id="cur">&mdash;</span> <span class="unit">mg/dL</span></div>
          <div class="sub"><span id="curMmol">&mdash;</span> mmol/L &middot; minute <span id="curMin">&mdash;</span></div>
          <div class="sub" id="trend">&nbsp;</div></div>
        <div><div class="sub">Next</div><div class="mid"><span id="next">&mdash;</span> <span class="unit">mg/dL</span></div>
          <div class="sub"><span id="nextMmol">&mdash;</span> mmol/L &middot; minute <span id="nextMin">&mdash;</span></div>
          <div class="sub">sent in <b id="countdown">&mdash;</b></div>
          <div class="sub" id="nextNote">&nbsp;</div></div>
      </div>
      <div class="chartwrap"><svg class="chart" id="chart" viewBox="0 0 600 180" preserveAspectRatio="none" role="img" aria-label="Glucose over the last 60 minutes and the next 15"></svg>
        <div class="tip" id="tip" hidden></div></div>
      <p class="note" id="chartNote">The last 60 sensor minutes and the next 15, after the line marked now. The large hollow dot is the next reading. Hover over a minute for its raw values.</p></div>
    <div class="card"><h2>Recent events</h2><div class="log" id="recent"></div>
      <p class="note"><a href="#events">All events &rarr;</a></p></div>
  </div>
</section>

<section class="page" id="page-sensor">
  <div class="grid g2">
    <div class="card"><h2>Glucose</h2>
      <div class="row"><label class="field">model <select id="kind"><option>sine</option><option>fixed</option><option>walk</option></select></label>
        <label class="field">mg/dL <input id="value" type="number" value="120"></label></div>
      <div class="row"><label class="field">amplitude <input id="amp" type="number" value="40"></label>
        <label class="field">period (min) <input id="period" type="number" value="240"></label>
        <button class="btn primary" onclick="setGlucose()">Set</button></div>
      <p class="note">A new model applies from the next minute on; minutes already made up stay as they were.</p></div>
    <div class="card"><h2>Link</h2>
      <div class="row"><button class="btn" onclick="post('/disconnect')">Drop the link</button></div>
      <div class="row"><label class="field">minutes <input id="outMinutes" type="number" value="20"></label>
        <button class="btn" onclick="post('/out-of-range', {minutes: +val('outMinutes')})">Go out of range</button></div>
      <p class="note">Dropping the link lets the app reconnect at once. Out of range hangs up and stays silent that long.</p></div>
    <div class="card"><h2>Faults</h2>
      <div class="row"><label class="field">next packet <select id="fault" style="width:auto"><option value="badcrc">badcrc: checksum error</option><option value="short">short: 45 bytes</option><option value="none">none: disarm</option></select></label>
        <button class="btn" onclick="post('/fault', {kind: val('fault')})">Arm</button></div>
      <p class="note">One-shot: only the next packet is spoiled. LibreTransmitter parses only at exactly 46 bytes, so a short packet leaves its buffer stale until it reconnects.</p></div>
    <div class="card"><h2>Sensor life</h2>
      <div class="row"><button class="btn" onclick="post('/activate')">Activate</button>
        <button class="btn" id="endWarmup" onclick="post('/end-warmup')">End warmup now</button>
        <button class="btn danger" onclick="if (confirm('End the sensor now?')) post('/terminate')">Terminate</button></div>
      <div class="row"><label class="field">wear days <input id="wearDays" type="number" value="15"></label>
        <label class="field">warmup (min) <input id="warmup" type="number" value="60"></label>
        <label class="field">age (min) <input id="age" type="number" value="0"></label>
        <button class="btn" onclick="if (confirm('Replace the sensor? libre2sim restarts and the event log starts over.')) post('/new', {wearDays: +val('wearDays'), warmupMinutes: +val('warmup'), ageMinutes: +val('age')})">New sensor</button></div>
      <p class="note">Age 0 is a sensor still in its applicator: scan it in the app, or Activate it here. A new sensor restarts libre2sim.</p></div>
  </div>
  <div class="card" style="margin-top:20px"><h2>Status</h2><div class="pills" id="pills" style="margin-bottom:14px"></div><dl id="status"></dl></div>
</section>

<section class="page" id="page-events">
  <div class="card"><h2>Event log</h2>
    <div class="row" id="filters"></div>
    <div class="log" id="log"></div>
    <p class="note">The latest 200 events, kept in memory only. Pausing stops the display, not the recording.</p></div>
</section>

<section class="page" id="page-settings">
  <div class="card"><h2>Live settings</h2>
    <label class="toggle"><input type="checkbox" id="strict" onchange="post('/config', {strictUnlock: this.checked}).then(loadConfig)"> Strict unlock: refuse replayed unlock counts</label>
    <p class="note">The only setting that can change while libre2sim runs.</p></div>
  <div class="grid g2" style="margin-top:20px">
    <div class="card"><h2>Launch (read-only)</h2><dl id="launch"></dl>
      <p class="note">The flags libre2sim was started with; change them by restarting it.</p></div>
    <div class="card"><h2>Sensor identity</h2><dl id="identity"></dl></div>
  </div>
</section>
</div></div>

<script>
function val(id) { return document.getElementById(id).value; }
function el(id) { return document.getElementById(id); }
async function post(path, body) {
  try {
    const r = await fetch(path, {method: 'POST', body: JSON.stringify(body || {})});
    if (!r.ok) alert(await r.text());
  } catch (e) { alert(e); }
  refresh();
}
function setGlucose() { post('/glucose', {kind: val('kind'), value: +val('value'), amplitude: +val('amp'), periodMinutes: +val('period')}); }

// Pages and sidebar
const titles = {dashboard: 'Dashboard', sensor: 'Sensor', events: 'Event log', settings: 'Settings'};
function route() {
  const page = titles[location.hash.slice(1)] ? location.hash.slice(1) : 'dashboard';
  for (const p of document.querySelectorAll('.page')) p.classList.toggle('active', p.id === 'page-' + page);
  for (const a of document.querySelectorAll('.nav a')) a.classList.toggle('active', a.dataset.page === page);
  el('title').textContent = titles[page];
  document.body.classList.remove('drawer');
}
window.addEventListener('hashchange', route);
function store(k, v) { try { if (v === undefined) return localStorage.getItem(k); localStorage.setItem(k, v); } catch (e) { return null; } }
if (store('libre2sim.collapsed') === '1') document.body.classList.add('collapsed');
el('collapse').onclick = () => { const c = document.body.classList.toggle('collapsed'); store('libre2sim.collapsed', c ? '1' : '0'); };
el('menu').onclick = () => document.body.classList.add('drawer');
el('scrim').onclick = () => document.body.classList.remove('drawer');

function fill(dl, obj) {
  dl.replaceChildren();
  for (const [k, v] of Object.entries(obj)) {
    const dt = document.createElement('dt'); dt.textContent = k;
    const dd = document.createElement('dd'); dd.textContent = v !== null && typeof v === 'object' ? JSON.stringify(v) : String(v);
    dl.append(dt, dd);
  }
}
function mmol(v) { return (v / 18.0182).toFixed(1); }
function signed(d) { return d === undefined || d === null ? '' : (d > 0 ? '+' : '') + d; }
function duration(min) { const d = Math.floor(min / 1440), h = Math.floor(min % 1440 / 60), m = min % 60; return d ? d + 'd ' + h + 'h' : h ? h + 'h ' + m + 'm' : m + 'm'; }

let nextAtLocal = null;
function tickCountdown() {
  if (nextAtLocal === null) { el('countdown').textContent = '—'; return; }
  const s = Math.max(0, (nextAtLocal - Date.now()) / 1000);
  el('countdown').textContent = Math.floor(s / 60) + ':' + String(Math.floor(s % 60)).padStart(2, '0');
}

function drawChart(h) {
  const svg = el('chart'); svg.replaceChildren();
  const ns = 'http://www.w3.org/2000/svg';
  const vals = h.mgdl || [], first = h.firstMinute || 0;
  const pts = [];
  vals.forEach((v, i) => { if (v > 0) pts.push([i, v]); });
  // The coming minutes, at their place after the latest reading; warmup ones
  // have no reading. A walk's are its mean path, with ±2 SD around it.
  const ahead = (h.ahead || []).map(r => ({i: r.minute - first, v: r.mgdl, sd: r.estimate ? r.noiseSD : 0, warmup: r.warmup}));
  const fut = ahead.filter(r => !r.warmup && r.v > 0);
  if (!pts.length && !fut.length) return;
  const all = pts.map(p => p[1]).concat(fut.map(r => r.v - 2 * r.sd), fut.map(r => r.v + 2 * r.sd));
  let lo = Math.min.apply(null, all), hi = Math.max.apply(null, all);
  if (hi - lo < 20) { const m = (hi + lo) / 2; lo = m - 10; hi = m + 10; }
  lo -= 5; hi += 5;
  const end = Math.max(vals.length - 1 + 15, ahead.length ? ahead[ahead.length - 1].i : 0, h.wearEnds ? h.wearEnds - first : 0);
  const n = Math.max(end, 1);
  const x = i => 30 + (i / n) * 562, y = v => 162 - ((v - lo) / (hi - lo)) * 150;
  chartGeo = {h: h, n: n, x: x, y: y, first: first, latest: vals.length - 1, ahead: ahead};
  function add(tag, attrs, text) {
    const e = document.createElementNS(ns, tag);
    for (const k in attrs) e.setAttribute(k, attrs[k]);
    if (text !== undefined) e.textContent = text;
    svg.append(e); return e;
  }
  [lo + 5, (lo + hi) / 2, hi - 5].forEach(v => {
    add('line', {x1: 30, x2: 592, y1: y(v), y2: y(v), 'class': 'axis'});
    add('text', {x: 0, y: y(v) + 3, 'class': 'axislabel'}, String(Math.round(v)));
  });
  // Now: the latest minute plus how far the clock is into the next one.
  const latest = vals.length - 1;
  const into = h.next ? Math.min(Math.max(1 - h.next.inSeconds / 60, 0), 1) : 0;
  const nowX = x(latest + into);
  if (fut.length) {
    add('rect', {x: nowX, y: 8, width: Math.max(592 - nowX, 0), height: 158, 'class': 'future'});
  }
  if (pts.length) {
    const line = pts.map(p => x(p[0]).toFixed(1) + ',' + y(p[1]).toFixed(1));
    add('polygon', {points: x(pts[0][0]).toFixed(1) + ',166 ' + line.join(' ') + ' ' + x(pts[pts.length - 1][0]).toFixed(1) + ',166', 'class': 'area'});
    add('polyline', {points: line.join(' '), 'class': 'spark'});
  }
  if (fut.some(r => r.sd > 0)) {
    const up = fut.map(r => x(r.i).toFixed(1) + ',' + y(r.v + 2 * r.sd).toFixed(1));
    const down = fut.slice().reverse().map(r => x(r.i).toFixed(1) + ',' + y(r.v - 2 * r.sd).toFixed(1));
    add('polygon', {points: up.concat(down).join(' '), 'class': 'band'});
  }
  const last = pts[pts.length - 1];
  if (fut.length) {
    const path = (last && fut[0].i === last[0] + 1 ? [last] : []).concat(fut.map(r => [r.i, r.v]));
    add('polyline', {points: path.map(p => x(p[0]).toFixed(1) + ',' + y(p[1]).toFixed(1)).join(' '), 'class': 'spark', 'stroke-dasharray': '4 4'});
  }
  // Marks: where the warmup ends and where the wear ends, inside the window.
  const firstReading = ahead.find(r => !r.warmup);
  if (ahead.length && ahead[0].warmup && firstReading) mark(firstReading.i, 'first reading');
  if (h.wearEnds) mark(h.wearEnds - first, 'wear ends');
  function mark(i, label) {
    add('line', {x1: x(i), x2: x(i), y1: 8, y2: 166, 'class': 'marker'});
    add('text', {x: Math.min(x(i) + 4, 540), y: 18, 'class': 'axislabel'}, label);
  }
  add('line', {x1: nowX, x2: nowX, y1: 4, y2: 166, 'class': 'now'});
  add('text', {x: Math.min(nowX + 4, 570), y: 176, 'class': 'nowlabel'}, 'now');
  add('text', {x: 30, y: 176, 'class': 'axislabel'}, '−' + latest + ' min');
  if (fut.length) add('text', {x: 592, y: 176, 'class': 'axislabel', 'text-anchor': 'end'}, '+' + (n - latest) + ' min');
  if (last) add('circle', {cx: x(last[0]), cy: y(last[1]), r: 4, fill: 'var(--accent)'});
  fut.forEach((r, k) => add('circle', {cx: x(r.i), cy: y(r.v), r: k === 0 && h.next && r.i === h.next.minute - first ? 4 : 2.2,
    fill: 'var(--card)', stroke: 'var(--accent)', 'stroke-width': k === 0 ? 2 : 1.4, 'vector-effect': 'non-scaling-stroke'}));
  if (hoverMinute !== null) showTip(hoverMinute - first);
}

// Hover: the minute under the pointer, with its reading and the raw values
// the BLE packet carries for it.
let chartGeo = null, hoverMinute = null;
function hoverAt(ev) {
  if (!chartGeo) return;
  const svg = el('chart'), r = svg.getBoundingClientRect();
  const vx = (ev.clientX - r.left) / r.width * 600;
  const g = chartGeo, last = g.ahead.length ? g.ahead[g.ahead.length - 1].i : g.latest;
  const i = Math.max(0, Math.min(last, Math.round((vx - 30) / 562 * g.n)));
  hoverMinute = g.first + i;
  showTip(i);
}
function hideTip() {
  hoverMinute = null; el('tip').hidden = true;
  const old = el('chart').querySelector('.hover'); if (old) old.remove();
}
function showTip(i) {
  const g = chartGeo, h = g.h, tip = el('tip'), svg = el('chart');
  let v, rec, ahead = null;
  if (i <= g.latest) { v = h.mgdl[i]; rec = (h.records || [])[i]; }
  else {
    const k = g.ahead.findIndex(a => a.i === i);
    if (k < 0) { hideTip(); return; }
    ahead = h.ahead[k]; v = ahead.mgdl; rec = (h.aheadRecords || [])[k];
  }
  // The guide line and dot.
  const old = svg.querySelector('.hover'); if (old) old.remove();
  const ns = 'http://www.w3.org/2000/svg', grp = document.createElementNS(ns, 'g');
  grp.setAttribute('class', 'hover');
  const ln = document.createElementNS(ns, 'line');
  [['x1', g.x(i)], ['x2', g.x(i)], ['y1', 8], ['y2', 166], ['class', 'hoverline']].forEach(a => ln.setAttribute(a[0], a[1]));
  grp.append(ln);
  if (v > 0) {
    const c = document.createElementNS(ns, 'circle');
    [['cx', g.x(i)], ['cy', g.y(v)], ['r', 5], ['fill', 'none'], ['stroke', 'var(--fg)'], ['stroke-width', 1.5], ['vector-effect', 'non-scaling-stroke']].forEach(a => c.setAttribute(a[0], a[1]));
    grp.append(c);
  }
  svg.append(grp);

  const minute = g.first + i;
  const at = h.activatedMs ? new Date(h.activatedMs + minute * 60000).toLocaleTimeString([], {hour: '2-digit', minute: '2-digit'}) : '';
  tip.replaceChildren();
  function add(tag, cls, text) { const e = document.createElement(tag); if (cls) e.className = cls; if (text !== undefined) e.textContent = text; tip.append(e); return e; }
  function row(k, val, why) {
    const r = add('div', 'row'); const a = document.createElement('span'); a.textContent = k;
    const b = document.createElement('code'); b.textContent = val; r.append(a, b);
    if (why) add('div', 'why', why);
  }
  add('h4', '', 'Minute ' + minute + (at ? ' · ' + at : '') + (ahead ? ' · coming' : (i === g.latest ? ' · latest' : '')));
  if (!(v > 0)) {
    add('div', 'big', 'No reading');
    add('div', 'why', 'Still in warmup: the record\'s raw glucose is 0, and the app skips records like that.');
    if (rec) row('BLE record', rec.ble, 'Raw 0, with the usual temperature fields.');
  } else {
    add('div', 'big', (ahead && ahead.estimate ? '≈ ' : '') + v + ' mg/dL · ' + mmol(v) + ' mmol/L');
    if (ahead) add('div', 'why', ahead.estimate
      ? 'An estimate: the walk model adds random noise when the minute arrives; about 95% of readings fall within ±' + (2 * ahead.noiseSD).toFixed(1) + ' mg/dL of this. The raw values below are for this estimate.'
      : 'Exact: sent at ' + new Date(ahead.atMs).toLocaleTimeString() + '. The model is deterministic, so this is what the sensor will send.');
    else add('div', 'why', 'The value libre2sim made up for this minute (the parsed reading).');
    if (rec) {
      const c = h.calibration || {};
      row('Raw glucose', String(rec.raw),
        'Bits 0–13 of the record. The app computes 65·(raw − i3)/(i4 − i3) with the sensor\'s calibration (i3 = ' + c.i3 + ', i4 = ' + c.i4 + '), corrects it for temperature and applies its i2 = ' + c.i2 + ' tables; libre2sim picks the raw value that comes back as this reading.');
      row('App computes', rec.appMgdl.toFixed(2) + ' mg/dL', 'LibreTransmitter\'s result before rounding, and before any slope or offset set in the app.');
    }
    const t = h.temperature;
    if (t) row('Raw temperature', t.raw + ', adj. ' + signed(t.adjustment),
      'Bits 14–25 hold ' + t.raw + ' >> 2, bits 26–30 the adjustment >> 2 and bit 31 its sign. With i6 = ' + (h.calibration || {}).i6 + ': ' + t.celsius + ' °C. The same in every record libre2sim writes.');
    if (rec) row('BLE record', rec.ble, 'These fields as the packet carries them (before encryption), least significant bit first.');
  }
  tip.hidden = false;
  // Beside the pointer's minute, on whichever side has room.
  const wrapW = svg.parentElement.clientWidth, px = g.x(i) / 600 * svg.clientWidth, w = tip.offsetWidth;
  tip.style.left = (px + 14 + w <= wrapW ? px + 14 : Math.max(0, px - 14 - w)) + 'px';
}

function showReadings(s, h) {
  const g = s.glucose;
  el('cur').textContent = g ? g : '—';
  el('curMmol').textContent = g ? mmol(g) : '—';
  el('curMin').textContent = s.lifeCount !== undefined ? s.lifeCount : '—';
  const t = s.trend, parts = [];
  if (t) {
    if (t.delta1 !== undefined) parts.push(signed(t.delta1) + ' in 1 min');
    if (t.delta5 !== undefined) parts.push(signed(t.delta5) + ' in 5 min');
    if (t.delta15 !== undefined) parts.push(signed(t.delta15) + ' in 15 min');
  }
  el('trend').textContent = parts.length ? 'trend ' + parts.join(' · ') : ' ';
  const n = s.nextReading;
  if (n) {
    nextAtLocal = Date.now() + n.inSeconds * 1000;
    el('nextMin').textContent = n.minute;
    if (n.warmup) {
      el('next').textContent = 'no reading'; el('nextMmol').textContent = '—';
      el('nextNote').textContent = 'still in warmup';
    } else {
      el('next').textContent = (n.estimate ? '≈ ' : '') + n.mgdl;
      el('nextMmol').textContent = mmol(n.mgdl);
      el('nextNote').textContent = n.estimate ? 'estimate: the walk model adds random noise (about ±' + (2 * n.noiseSD).toFixed(1) + ' mg/dL) when the minute arrives' : 'exact: the model is deterministic';
    }
  } else {
    nextAtLocal = null;
    el('next').textContent = '—'; el('nextMmol').textContent = '—'; el('nextMin').textContent = '—';
    el('nextNote').textContent = s.activated ? 'the sensor is not running' : 'not activated';
  }
  tickCountdown();
  if (h) {
    drawChart(h);
    const est = (h.ahead || []).some(r => r.estimate);
    el('chartNote').textContent = 'The last 60 sensor minutes and the next 15, after the line marked now. The large hollow dot is the next reading. Hover over (or tap) a minute for its raw values. ' +
      (est ? 'The coming readings are the walk model\'s expected path; the shaded band is where about 95% of its random readings will fall.' : 'The coming readings are exact: the model is deterministic.');
  }
}

function pill(text, cls) {
  const p = document.createElement('span'); p.className = 'pill ' + cls; p.textContent = text; return p;
}
function statusPills(s) {
  const out = [pill(s.connected ? 'phone connected' : 'no phone', s.connected ? 'good' : ''),
    pill(s.advertising ? 'advertising' : 'not advertising', s.advertising ? 'good' : ''),
    pill(s.streamingEnabled ? 'streaming enabled' : 'streaming not enabled', s.streamingEnabled ? 'good' : '')];
  if (s.fault) out.push(pill('fault armed: ' + s.fault, 'bad'));
  if (s.outOfRangeUntil) out.push(pill('out of range until ' + new Date(s.outOfRangeUntil).toLocaleTimeString(), 'bad'));
  if (s.terminated) out.push(pill('terminated', 'bad'));
  return out;
}
function showStatus(s) {
  el('pills').replaceChildren(...statusPills(s));
  const top = [];
  if (s.fault) top.push(pill('fault armed', 'bad'));
  if (s.outOfRangeUntil) top.push(pill('out of range', 'bad'));
  if (s.terminated) top.push(pill('terminated', 'bad'));
  top.push(pill(s.connected ? 'connected' : 'not connected', s.connected ? 'good' : ''));
  el('toppills').replaceChildren(...top);
  const rest = {};
  for (const [k, v] of Object.entries(s)) if (k !== 'glucose' && k !== 'nextReading' && k !== 'trend') rest[k] = v;
  fill(el('status'), rest);

  const lc = s.lifeCount;
  el('statMin').textContent = lc !== undefined && s.activated ? lc : '—';
  el('statWear').textContent = !s.activated ? 'in its applicator' : s.terminated ? 'terminated'
    : lc < s.warmupMinutes ? 'warmup: ' + duration(s.warmupMinutes - lc) + ' left'
    : lc >= s.wearMinutes ? 'worn out' : duration(s.wearMinutes - lc) + ' of wear left';
  el('endWarmup').disabled = !(s.activated && !s.terminated && lc < s.warmupMinutes && lc < s.wearMinutes);
  el('statPhone').textContent = s.connected ? 'Connected' : 'Not connected';
  el('statAdv').textContent = s.advertising ? 'advertising' : s.streamingEnabled ? 'not advertising' : 'scan it to enable streaming';
  el('statUnlocks').textContent = s.unlocksAccepted !== undefined ? s.unlocksAccepted : '—';
  el('statUnlockHint').textContent = (s.unlocksRefused ? s.unlocksRefused + ' refused · ' : '') + 'count ' + (s.unlockCount !== undefined ? s.unlockCount : '—');
  const m = s.glucoseMode || {};
  el('statModel').textContent = m.kind ? m.kind[0].toUpperCase() + m.kind.slice(1) : '—';
  el('statModelHint').textContent = m.kind === 'sine' ? m.value + ' ± ' + m.amplitude + ' mg/dL, ' + m.periodMinutes + ' min' : m.kind ? 'around ' + m.value + ' mg/dL' : ' ';
}

let events = [], lastSeq = 0, filter = 'all', paused = false;
function eventRow(e) {
  const d = document.createElement('div'); d.className = 'c-' + e.cat;
  const t = document.createElement('span'); t.className = 't'; t.textContent = new Date(e.time).toLocaleTimeString([], {hour12: false});
  const c = document.createElement('span'); c.className = 'tag'; c.textContent = e.cat.toUpperCase();
  const m = document.createElement('span'); m.className = 'm'; m.textContent = e.kind + ': ' + e.msg;
  d.append(t, c, m); return d;
}
function renderLog() {
  const box = el('log'); box.replaceChildren();
  for (let i = events.length - 1; i >= 0; i--) {
    if (filter !== 'all' && events[i].cat !== filter) continue;
    box.append(eventRow(events[i]));
  }
  if (!box.childElementCount) { const p = document.createElement('div'); p.className = 'empty'; p.textContent = 'No events yet.'; box.append(p); }
  const recent = el('recent'); recent.replaceChildren();
  for (let i = events.length - 1; i >= 0 && recent.childElementCount < 8; i--) recent.append(eventRow(events[i]));
  if (!recent.childElementCount) { const p = document.createElement('div'); p.className = 'empty'; p.textContent = 'No events yet.'; recent.append(p); }
}
function buildFilters() {
  const box = el('filters');
  [['all', 'All'], ['nfc', 'NFC'], ['ble', 'BLE'], ['sensor', 'Sensor']].forEach(([k, label]) => {
    const b = document.createElement('button'); b.textContent = label; b.dataset.k = k;
    b.className = 'btn' + (k === filter ? ' on' : '');
    b.onclick = () => { filter = k; for (const x of box.querySelectorAll('button[data-k]')) x.className = 'btn' + (x.dataset.k === k ? ' on' : ''); renderLog(); };
    box.append(b);
  });
  const p = document.createElement('button'); p.textContent = 'Pause'; p.className = 'btn'; p.style.marginLeft = 'auto';
  p.onclick = () => { paused = !paused; p.textContent = paused ? 'Resume' : 'Pause'; p.className = 'btn' + (paused ? ' on' : ''); if (!paused) pollEvents(); };
  box.append(p);
}
async function pollEvents() {
  if (paused) return;
  const r = await (await fetch('/events?since=' + lastSeq)).json();
  if (r.last < lastSeq) { events = []; lastSeq = 0; return pollEvents(); }
  if (r.events.length || !lastSeq) {
    events = events.concat(r.events).slice(-500);
    renderLog();
  }
  lastSeq = r.last;
}

async function loadConfig() {
  try {
    const c = await (await fetch('/config')).json();
    el('strict').checked = c.strictUnlock;
    fill(el('launch'), c.launch);
    fill(el('identity'), c.sensor);
  } catch (e) {}
}

async function refresh() {
  try {
    const [s, h] = await Promise.all([fetch('/status').then(r => r.json()), fetch('/readings?minutes=60&ahead=15').then(r => r.json())]);
    showReadings(s, h); showStatus(s);
    await pollEvents();
  } catch (e) {}
}
el('chart').addEventListener('pointermove', hoverAt);
el('chart').addEventListener('pointerdown', hoverAt);
el('chart').addEventListener('pointerleave', ev => { if (ev.pointerType !== 'touch') hideTip(); });
document.addEventListener('pointerdown', ev => { if (hoverMinute !== null && !el('chart').contains(ev.target)) hideTip(); });
route(); buildFilters(); loadConfig(); refresh();
setInterval(refresh, 1500); setInterval(tickCountdown, 250);
</script></body></html>`
