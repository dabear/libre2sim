package main

// indexHTML is the control panel at GET /: self-contained (no external
// assets), it polls /status, /readings and /events, reads /config once and
// posts to the API. Text from the server is only ever set with textContent.
const indexHTML = `<!doctype html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>libre2sim</title>
<style>
:root { color-scheme: light dark; --fg:#1d1d1f; --bg:#f5f5f7; --card:#fff; --muted:#6e6e73; --accent:#0a66c2; --line:#d2d2d7; --warn:#b45309; --ok:#1a7f37; }
@media (prefers-color-scheme: dark) { :root { --fg:#f5f5f7; --bg:#1c1c1e; --card:#2c2c2e; --muted:#a1a1a6; --accent:#4c9fff; --line:#48484a; --warn:#f59e0b; --ok:#3fb950; } }
* { box-sizing: border-box; }
body { font: 15px -apple-system, system-ui, sans-serif; color: var(--fg); background: var(--bg); margin: 0; padding: 16px; }
main { max-width: 900px; margin: 0 auto; }
h1 { font-size: 20px; margin: 0 0 4px; } .note { color: var(--muted); font-size: 13px; margin: 0 0 12px; }
.disclaimer { border: 1px solid var(--warn); color: var(--warn); border-radius: 8px; padding: 6px 10px; font-size: 13px; margin: 0 0 16px; }
section { background: var(--card); border-radius: 10px; padding: 14px 16px; margin-bottom: 12px; }
h2 { font-size: 14px; margin: 0 0 10px; color: var(--muted); font-weight: 600; }
.cols { display: grid; grid-template-columns: repeat(auto-fit, minmax(240px, 1fr)); gap: 12px; }
.big { font-size: 40px; font-weight: 600; line-height: 1.1; } .unit { font-size: 14px; color: var(--muted); font-weight: 400; }
.mid { font-size: 28px; font-weight: 600; } .label { color: var(--muted); font-size: 12px; text-transform: uppercase; letter-spacing: .04em; }
.sub { color: var(--muted); font-size: 13px; }
dl { display: grid; grid-template-columns: max-content 1fr; gap: 4px 12px; margin: 0; }
dt { color: var(--muted); } dd { margin: 0; font-family: ui-monospace, monospace; font-size: 13px; overflow-wrap: anywhere; }
.row { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; margin-bottom: 8px; }
input, select { font: inherit; padding: 4px 6px; width: 7em; color: inherit; background: var(--bg); border: 1px solid var(--line); border-radius: 6px; }
input[type=checkbox] { width: auto; }
button { font: inherit; padding: 5px 12px; border-radius: 6px; border: 1px solid var(--accent); background: none; color: var(--accent); cursor: pointer; }
button.on { background: var(--accent); color: var(--bg); }
svg { width: 100%; height: 120px; display: block; }
.spark { fill: none; stroke: var(--accent); stroke-width: 1.5; vector-effect: non-scaling-stroke; }
.axis { stroke: var(--line); stroke-width: 1; vector-effect: non-scaling-stroke; }
.axislabel { fill: var(--muted); font-size: 10px; }
#log { max-height: 340px; overflow-y: auto; font-family: ui-monospace, monospace; font-size: 12px; border-top: 1px solid var(--line); }
#log div { padding: 3px 0; border-bottom: 1px solid var(--line); overflow-wrap: anywhere; }
#log .t { color: var(--muted); } #log .c { display: inline-block; width: 4.5em; font-weight: 600; }
.c-nfc .c { color: var(--ok); } .c-ble .c { color: var(--accent); } .c-sensor .c { color: var(--warn); }
.pill { font-size: 12px; border: 1px solid var(--line); border-radius: 99px; padding: 1px 8px; color: var(--muted); }
.pill.good { color: var(--ok); border-color: var(--ok); } .pill.bad { color: var(--warn); border-color: var(--warn); }
</style></head><body><main>
<h1>libre2sim</h1>
<p class="disclaimer">For development use only. Not an Abbott product. All data is simulated: never use it for therapy or to make a treatment decision.</p>

<section><h2>Readings</h2>
<div class="cols">
<div><div class="label">Current</div><div class="big"><span id="cur">&mdash;</span> <span class="unit">mg/dL</span></div>
<div class="sub"><span id="curMmol">&mdash;</span> mmol/L &middot; minute <span id="curMin">&mdash;</span></div>
<div class="sub" id="trend">&nbsp;</div></div>
<div><div class="label">Next</div><div class="mid"><span id="next">&mdash;</span> <span class="unit">mg/dL</span></div>
<div class="sub"><span id="nextMmol">&mdash;</span> mmol/L &middot; minute <span id="nextMin">&mdash;</span></div>
<div class="sub">sent in <b id="countdown">&mdash;</b></div>
<div class="sub" id="nextNote">&nbsp;</div></div>
</div>
<svg id="chart" viewBox="0 0 600 120" preserveAspectRatio="none" role="img" aria-label="Glucose over the last 60 minutes"></svg>
<div class="sub">The last 60 sensor minutes. A hollow dot is the next reading.</div></section>

<section><h2>Sensor</h2><div class="row" id="pills"></div><dl id="status"></dl></section>

<div class="cols">
<section><h2>Glucose</h2>
<div class="row"><select id="kind"><option>sine</option><option>fixed</option><option>walk</option></select>
<label>mg/dL <input id="value" type="number" value="120"></label></div>
<div class="row"><label>amplitude <input id="amp" type="number" value="40"></label>
<label>period (min) <input id="period" type="number" value="240"></label>
<button onclick="setGlucose()">Set</button></div>
<p class="note">A new mode applies from the next minute on; minutes already made up stay as they were.</p></section>
<section><h2>Link</h2>
<div class="row"><button onclick="post('/disconnect')">Drop the link</button></div>
<div class="row"><label>minutes <input id="outMinutes" type="number" value="20"></label>
<button onclick="post('/out-of-range', {minutes: +val('outMinutes')})">Go out of range</button></div></section>
<section><h2>Faults</h2>
<div class="row"><select id="fault" style="width:auto"><option value="badcrc">badcrc: checksum error</option><option value="short">short: 45 bytes</option><option value="none">none: disarm</option></select>
<button onclick="post('/fault', {kind: val('fault')})">Arm for the next packet</button></div>
<p class="note">One-shot: only the next packet is spoiled. LibreTransmitter parses only at exactly 46 bytes, so a short packet leaves its buffer stale until it reconnects.</p></section>
</div>

<section><h2>Sensor life</h2>
<div class="row"><button onclick="post('/activate')">Activate (start the applicator sensor)</button>
<button onclick="if (confirm('End the sensor now?')) post('/terminate')">Terminate</button></div>
<div class="row"><label>wear days <input id="wearDays" type="number" value="15"></label>
<label>warmup (min) <input id="warmup" type="number" value="60"></label>
<label>age (min) <input id="age" type="number" value="0"></label>
<button onclick="if (confirm('Replace the sensor? libre2sim restarts and the event log starts over.')) post('/new', {wearDays: +val('wearDays'), warmupMinutes: +val('warmup'), ageMinutes: +val('age')})">Start a new sensor</button></div>
<p class="note">Age 0 is a sensor still in its applicator: scan it in the app, or Activate it here. A new sensor restarts libre2sim.</p></section>

<section><h2>Event log</h2>
<div class="row" id="filters"></div>
<div id="log"></div>
<p class="note">The latest 200 events, kept in memory only. Pausing stops the display, not the recording.</p></section>

<section><h2>Config</h2>
<div class="row"><label><input type="checkbox" id="strict" onchange="post('/config', {strictUnlock: this.checked}).then(loadConfig)"> strict unlock (refuse replayed unlock counts)</label></div>
<div class="cols"><div><div class="label">Launch (read-only)</div><dl id="launch"></dl></div>
<div><div class="label">Sensor identity</div><dl id="identity"></dl></div></div>
<p class="note">Launch settings are the flags libre2sim was started with; change them by restarting it. Strict unlock can change at run time.</p></section>
</main><script>
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

let nextAtLocal = null;
function tickCountdown() {
  if (nextAtLocal === null) { el('countdown').textContent = '—'; return; }
  const s = Math.max(0, (nextAtLocal - Date.now()) / 1000);
  el('countdown').textContent = Math.floor(s / 60) + ':' + String(Math.floor(s % 60)).padStart(2, '0');
}

function drawChart(h) {
  const svg = el('chart'); svg.replaceChildren();
  const ns = 'http://www.w3.org/2000/svg';
  const vals = h.mgdl || [];
  const pts = [];
  vals.forEach((v, i) => { if (v > 0) pts.push([i, v]); });
  const next = h.next && h.next.mgdl > 0 ? [vals.length, h.next.mgdl] : null;
  if (!pts.length) return;
  const all = pts.map(p => p[1]).concat(next ? [next[1]] : []);
  let lo = Math.min.apply(null, all), hi = Math.max.apply(null, all);
  if (hi - lo < 20) { const m = (hi + lo) / 2; lo = m - 10; hi = m + 10; }
  lo -= 5; hi += 5;
  const n = Math.max(vals.length + (next ? 1 : 0) - 1, 1);
  const x = i => 28 + (i / n) * 566, y = v => 112 - ((v - lo) / (hi - lo)) * 104;
  function add(tag, attrs, text) {
    const e = document.createElementNS(ns, tag);
    for (const k in attrs) e.setAttribute(k, attrs[k]);
    if (text !== undefined) e.textContent = text;
    svg.append(e); return e;
  }
  [lo + 5, hi - 5].forEach(v => {
    add('line', {x1: 28, x2: 594, y1: y(v), y2: y(v), 'class': 'axis'});
    add('text', {x: 0, y: y(v) + 3, 'class': 'axislabel'}, String(Math.round(v)));
  });
  add('polyline', {points: pts.map(p => x(p[0]).toFixed(1) + ',' + y(p[1]).toFixed(1)).join(' '), 'class': 'spark'});
  const last = pts[pts.length - 1];
  add('circle', {cx: x(last[0]), cy: y(last[1]), r: 3, fill: 'var(--accent)'});
  if (next) {
    add('line', {x1: x(last[0]), y1: y(last[1]), x2: x(next[0]), y2: y(next[1]), 'class': 'spark', 'stroke-dasharray': '3 3'});
    add('circle', {cx: x(next[0]), cy: y(next[1]), r: 3, fill: 'var(--card)', stroke: 'var(--accent)', 'stroke-width': 1.5});
  }
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
  el('trend').textContent = parts.length ? 'trend ' + parts.join(' · ') : ' ';
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
  if (h) drawChart(h);
}

function pill(text, cls) {
  const p = document.createElement('span'); p.className = 'pill ' + cls; p.textContent = text; return p;
}
function showStatus(s) {
  const pills = el('pills'); pills.replaceChildren();
  pills.append(pill(s.connected ? 'phone connected' : 'no phone', s.connected ? 'good' : ''),
    pill(s.advertising ? 'advertising' : 'not advertising', s.advertising ? 'good' : ''),
    pill(s.streamingEnabled ? 'streaming enabled' : 'streaming not enabled', s.streamingEnabled ? 'good' : ''));
  if (s.fault) pills.append(pill('fault armed: ' + s.fault, 'bad'));
  if (s.outOfRangeUntil) pills.append(pill('out of range until ' + new Date(s.outOfRangeUntil).toLocaleTimeString(), 'bad'));
  if (s.terminated) pills.append(pill('terminated', 'bad'));
  const rest = {};
  for (const [k, v] of Object.entries(s)) if (k !== 'glucose' && k !== 'nextReading' && k !== 'trend') rest[k] = v;
  fill(el('status'), rest);
}

let events = [], lastSeq = 0, filter = 'all', paused = false;
function renderLog() {
  const box = el('log'); box.replaceChildren();
  for (let i = events.length - 1; i >= 0; i--) {
    const e = events[i];
    if (filter !== 'all' && e.cat !== filter) continue;
    const d = document.createElement('div'); d.className = 'c-' + e.cat;
    const t = document.createElement('span'); t.className = 't'; t.textContent = new Date(e.time).toLocaleTimeString([], {hour12: false}) + ' ';
    const c = document.createElement('span'); c.className = 'c'; c.textContent = e.cat.toUpperCase();
    const m = document.createElement('span'); m.textContent = e.kind + ': ' + e.msg;
    d.append(t, c, m); box.append(d);
  }
}
function buildFilters() {
  const box = el('filters');
  [['all', 'All'], ['nfc', 'NFC'], ['ble', 'BLE'], ['sensor', 'Sensor']].forEach(([k, label]) => {
    const b = document.createElement('button'); b.textContent = label; b.dataset.k = k;
    b.className = k === filter ? 'on' : '';
    b.onclick = () => { filter = k; for (const x of box.querySelectorAll('button[data-k]')) x.className = x.dataset.k === k ? 'on' : ''; renderLog(); };
    box.append(b);
  });
  const p = document.createElement('button'); p.textContent = 'Pause';
  p.onclick = () => { paused = !paused; p.textContent = paused ? 'Resume' : 'Pause'; p.className = paused ? 'on' : ''; if (!paused) pollEvents(); };
  box.append(p);
}
async function pollEvents() {
  if (paused) return;
  const r = await (await fetch('/events?since=' + lastSeq)).json();
  if (r.last < lastSeq) { events = []; lastSeq = 0; return pollEvents(); }
  if (r.events.length) {
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
    const [s, h] = await Promise.all([fetch('/status').then(r => r.json()), fetch('/readings?minutes=60').then(r => r.json())]);
    showReadings(s, h); showStatus(s);
    await pollEvents();
  } catch (e) {}
}
buildFilters(); loadConfig(); refresh();
setInterval(refresh, 1500); setInterval(tickCountdown, 250);
</script></body></html>`
