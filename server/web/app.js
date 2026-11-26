(() => {
  'use strict';

  const $ = (sel, root = document) => root.querySelector(sel);
  const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));

  const state = {
    snap: null,
    selected: new Set(),
    anchor: null,
    lastNotice: null,
    settings: null,
    settingsDirty: false,
    serverIndex: new Map(),
    sort: null, // { key, dir: 1 | -1 } when a column heading was clicked, view only
    openGroups: new Set(), // Group Split Videos: the links whose parts are shown
    search: '', // Search Names: only names with this in them are shown (lower case)
  };

  // ---------- api ----------
  async function api(path, body, method) {
    const m = method || (body === undefined ? 'GET' : 'POST');
    const opt = { method: m, credentials: 'same-origin', headers: {} };
    if (m !== 'GET') {
      opt.headers['X-RP'] = '1';
      opt.headers['Content-Type'] = 'application/json';
      opt.body = JSON.stringify(body === undefined ? {} : body);
    }
    const res = await fetch('/api/' + path, opt);
    let data = null;
    try { data = await res.json(); } catch (_) { /* no body */ }
    if (!res.ok) throw new Error((data && data.error) || res.statusText || 'Request failed');
    return data;
  }

  // ---------- formatting ----------
  function fmtBytes(n) {
    if (!n || n <= 0) return '';
    const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
    let i = 0;
    while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
    return (i === 0 ? String(Math.round(n)) : n.toFixed(n >= 100 ? 0 : 1)) + ' ' + units[i];
  }
  // seconds as hh:mm:ss, blank when not known yet (negative)
  function fmtClock(s) {
    if (s === undefined || s === null || s < 0) return '';
    s = Math.round(s);
    const p = (n) => String(n).padStart(2, '0');
    return p(Math.floor(s / 3600)) + ':' + p(Math.floor(s / 60) % 60) + ':' + p(s % 60);
  }
  function fmtSpeed(n) { return n > 0 ? fmtBytes(n) + '/s' : ''; }
  function fmtEta(s) {
    if (!isFinite(s) || s <= 0) return '';
    s = Math.round(s);
    if (s < 60) return s + 's';
    const m = Math.floor(s / 60);
    if (m < 60) return m + 'm ' + String(s % 60).padStart(2, '0') + 's';
    const h = Math.floor(m / 60);
    if (h < 24) return h + 'h ' + String(m % 60).padStart(2, '0') + 'm';
    return Math.floor(h / 24) + 'd ' + (h % 24) + 'h';
  }
  function plural(n, one, many) { return n + ' ' + (n === 1 ? one : (many || one + 's')); }

  // every server gets its own colour, spread around the colour wheel in order of appearance
  function hueFor(server) {
    if (!state.serverIndex.has(server)) state.serverIndex.set(server, state.serverIndex.size);
    return Math.round((state.serverIndex.get(server) * 137.508 + 212) % 360);
  }

  // ---------- toasts ----------
  function toast(text, level, ms) {
    if (!text) return;
    const el = document.createElement('div');
    el.className = 'toast' + (level && level !== 'info' ? ' ' + level : '');
    el.textContent = text;
    $('#toasts').appendChild(el);
    while ($('#toasts').children.length > 5) $('#toasts').firstChild.remove();
    setTimeout(() => el.remove(), ms || (level === 'error' ? 9000 : level === 'warn' ? 12000 : 4500));
  }

  // ---------- theme ----------
  // ---------- resizable columns ----------
  // Without a saved layout the Name column takes the leftover width. After the
  // first drag every column has a fixed width, saved on the server so it is
  // kept after the program is closed (the window's own storage can't be used:
  // its address changes with every start).
  const COLS = ['act', 'num', 'name', 'status', 'progress', 'speed', 'eta', 'size', 'full', 'server', 'category', 'start', 'end', 'duration'];
  const DEFAULT_COLS = { act: 54, num: 42, name: 0, status: 140, progress: 124, speed: 84, eta: 84, size: 80, full: 92, server: 142, category: 110, start: 80, end: 80, duration: 92 };
  const MIN_COL = 30;
  let colsKey = '';
  let resizing = false;
  function setColumnWidths(w) {
    const cols = $('#cols').children;
    const table = $('#grid');
    let sum = 0;
    COLS.forEach((k, i) => {
      const v = w ? w[k] : DEFAULT_COLS[k];
      cols[i].style.width = v ? v + 'px' : '';
      sum += v || 0;
    });
    table.style.width = w ? sum + 'px' : '';
    table.style.minWidth = w ? '0' : '';
  }
  function applyColumns(saved) {
    const key = JSON.stringify(saved || null);
    if (resizing || key === colsKey) return;
    colsKey = key;
    setColumnWidths(saved && COLS.every((k) => saved[k] > 0) ? saved : null);
  }
  async function saveColumns(widths) {
    colsKey = JSON.stringify(widths || null);
    try { await api('columns', { widths }); } catch (err) { toast(err.message, 'error'); }
  }
  function initColumnResize() {
    $$('#grid thead th').forEach((th, i) => {
      const grip = document.createElement('span');
      grip.className = 'rz';
      grip.title = 'Drag to resize';
      grip.addEventListener('click', (e) => e.stopPropagation());
      grip.addEventListener('pointerdown', (e) => {
        if (e.button !== 0) return;
        e.preventDefault();
        e.stopPropagation();
        grip.setPointerCapture(e.pointerId);
        resizing = true;
        grip.classList.add('drag');
        document.body.classList.add('resizing');
        const w = {};
        $$('#grid thead th').forEach((t, j) => { w[COLS[j]] = Math.round(t.getBoundingClientRect().width); });
        const startX = e.clientX, start = w[COLS[i]];
        setColumnWidths(w); // freeze what is shown now, so nothing jumps
        const move = (ev) => { w[COLS[i]] = Math.max(MIN_COL, Math.round(start + ev.clientX - startX)); setColumnWidths(w); };
        const up = () => {
          grip.removeEventListener('pointermove', move);
          grip.removeEventListener('pointerup', up);
          grip.removeEventListener('pointercancel', up);
          grip.classList.remove('drag');
          document.body.classList.remove('resizing');
          resizing = false;
          saveColumns(w);
        };
        grip.addEventListener('pointermove', move);
        grip.addEventListener('pointerup', up);
        grip.addEventListener('pointercancel', up);
      });
      th.appendChild(grip);
    });
  }
  initColumnResize();
  initSortHeads();
  $('#resetColumns').addEventListener('click', async () => {
    if (!(await confirmDialog('Reset the column widths?', 'Every column goes back to its normal width.', 'Reset widths'))) return;
    await saveColumns(null);
    colsKey = '';
    applyColumns(null);
    toast('Column widths reset.', 'info');
  });

  function applyTheme(theme) {
    const root = document.documentElement;
    if (theme === 'light' || theme === 'dark') root.dataset.theme = theme;
    else delete root.dataset.theme;
    try { localStorage.setItem('rp-theme', theme === 'light' || theme === 'dark' ? theme : 'system'); } catch (_) { /* private mode */ }
  }

  // ---------- table ----------
  const rows = new Map(); // id -> row refs
  const tbody = $('#rows');

  function makeRow(id) {
    const tr = document.createElement('tr');
    tr.dataset.id = String(id);
    tr.innerHTML =
      '<td class="c-act"><input type="checkbox" aria-label="Active"></td>' +
      '<td class="c-n"></td>' +
      '<td class="c-name"><span class="nm"></span><span class="tag" hidden>playlist saved</span></td>' +
      '<td class="c-st"><svg class="ic forced" hidden><use href="#i-bolt"/></svg><span class="stt"></span><span class="pill" hidden></span><span class="det"></span></td>' +
      '<td class="c-pr"><div class="bar"><i></i><b></b></div></td>' +
      '<td class="num c-sp"></td><td class="num c-eta"></td><td class="num c-sz"></td><td class="num c-fz"></td>' +
      '<td class="c-sv"><span class="chip" hidden></span></td>' +
      '<td class="c-cat"></td>' +
      '<td class="num c-t1"></td><td class="num c-t2"></td><td class="num c-t3"></td>';
    const q = (s) => tr.querySelector(s);
    return {
      tr, v: {}, cb: q('input'), n: q('.c-n'), nm: q('.nm'), tag: q('.tag'), forced: q('.forced'),
      stt: q('.stt'), pill: q('.pill'), det: q('.det'), fill: q('.bar i'), pct: q('.bar b'),
      sp: q('.c-sp'), eta: q('.c-eta'), sz: q('.c-sz'), fz: q('.c-fz'), chip: q('.chip'), cat: q('.c-cat'), t1: q('.c-t1'), t2: q('.c-t2'), t3: q('.c-t3'),
    };
  }

  function statusOf(it) {
    switch (it.status) {
      case 'paused': return { label: 'Paused', cls: 'paused' };
      case 'queued':
        if (!it.active) return { label: 'Inactive', cls: 'inactive' };
        if (it.detail) return { label: it.detail, cls: 'working' };
        return { label: 'Waiting', cls: 'queued', pill: /^(Next|#\d+)$/.test(it.hint) ? it.hint : '' };
      case 'resolving': return { label: it.detail || 'Getting playlist', cls: 'working' };
      case 'downloading': return { label: 'Downloading', cls: 'downloading', detail: it.detail };
      case 'complete': return it.detail ? { label: it.detail, cls: 'working' } : { label: 'Finished', cls: 'complete' };
      case 'error': return { label: 'Error: ' + (it.error || 'failed'), cls: 'error' };
      default: return { label: it.status, cls: 'queued' };
    }
  }

  // The Status sort goes by groups, in the order set in Settings → Table (the
  // boxes). Rows in the same group keep the queue order.
  const STATUS_BOXES = {
    downloading: 'Downloading/Converting', error: 'Error', finished: 'Finished',
    checking: 'Checking Server', waiting: 'Waiting', paused: 'Paused', inactive: 'Inactive',
  };
  function statusGroup(it) {
    const cls = statusOf(it).cls;
    if (it.status === 'downloading' || (it.status === 'complete' && it.detail)) return 'downloading';
    if (it.status === 'complete') return 'finished';
    if (cls === 'working') return 'checking'; // and the other steps before a download
    if (cls === 'queued') return 'waiting';
    return cls; // inactive, paused, error
  }
  function statusRank(it) {
    const order = (state.snap && state.snap.statusOrder) || Object.keys(STATUS_BOXES);
    const i = order.indexOf(statusGroup(it));
    return i < 0 ? order.length : i;
  }

  function put(r, key, value, apply) {
    if (r.v[key] !== value) { r.v[key] = value; apply(value); }
  }

  function updateRow(r, it, index, part) {
    const st = statusOf(it);
    put(r, 'cls', ['s-' + st.cls, it.active ? '' : 'inactive', it.next ? 'next' : '', state.selected.has(it.id) ? 'selected' : '', part ? 'part' : ''].join(' '),
      (v) => { r.tr.className = v.replace(/\s+/g, ' ').trim(); });
    put(r, 'active', it.active, (v) => { r.cb.checked = v; });
    put(r, 'n', index + 1, (v) => { r.n.textContent = v; });
    put(r, 'name', it.name, (v) => { r.nm.textContent = v; });
    put(r, 'tag', it.m3u8, (v) => { r.tag.hidden = !v; });
    put(r, 'title', it.url + (it.error ? '\n' + it.error : ''), (v) => { r.nm.parentNode.title = v; });
    put(r, 'forced', it.forced && it.status !== 'complete', (v) => { r.forced.hidden = !v; r.forced.setAttribute('aria-label', 'Force started'); });
    put(r, 'stt', st.label, (v) => { r.stt.textContent = v; r.stt.parentNode.title = v; });
    put(r, 'pill', st.pill || '', (v) => { r.pill.hidden = !v; r.pill.textContent = v; });
    put(r, 'det', st.detail || '', (v) => { r.det.textContent = v; });
    const pct = Math.max(0, Math.min(100, it.percent || 0));
    put(r, 'pct', pct, (v) => { r.fill.style.width = v + '%'; });
    const pctLabel = it.status === 'complete' ? '100%' : (pct > 0 ? pct.toFixed(1) + '%' : '');
    put(r, 'pctLabel', pctLabel, (v) => { r.pct.textContent = v; });
    const running = it.status === 'downloading';
    put(r, 'sp', running ? fmtSpeed(it.speed) : '', (v) => { r.sp.textContent = v; });
    put(r, 'eta', running ? fmtEta(it.etaSec) : '', (v) => { r.eta.textContent = v; });
    put(r, 'sz', fmtBytes(it.bytes), (v) => { r.sz.textContent = v; });
    // exact once finished, otherwise an estimate from the part downloaded so far
    put(r, 'fz', it.fullSize > 0 ? (it.status === 'complete' ? '' : '~') + fmtBytes(it.fullSize) : '', (v) => {
      r.fz.textContent = v;
      r.fz.title = v && v[0] === '~' ? 'Estimate, worked out from the part downloaded so far' : '';
    });
    put(r, 'server', it.server || '', (v) => {
      r.chip.hidden = !v;
      r.chip.textContent = v;
      r.chip.title = v ? 'Downloads from ' + v : '';
      if (v) r.chip.style.setProperty('--h', hueFor(v));
    });
    put(r, 'cat', it.categoryName || '', (v) => { r.cat.textContent = v; r.cat.title = v; });
    put(r, 'start', fmtClock(it.startSec), (v) => { r.t1.textContent = v; });
    put(r, 'end', fmtClock(it.endSec), (v) => { r.t2.textContent = v; });
    put(r, 'dur', fmtClock(it.durSec), (v) => { r.t3.textContent = v; });
  }

  // ---------- sorting (what is shown only; the queue keeps its own order) ----------
  // Text sorts A to Z first, numbers and times largest/longest first. Clicking the
  // same heading again reverses it, a third click goes back to the queue order.
  const num = (v) => (typeof v === 'number' && v >= 0 ? v : null);
  const SORTS = {
    name: { text: true, get: (it) => it.name },
    status: { text: true, get: (it) => (it.rank !== undefined ? it.rank : statusRank(it)), up: 'in the set order', down: 'reversed' },
    progress: { get: (it) => (it.status === 'complete' ? 100 : num(it.percent)) },
    speed: { get: (it) => (it.status === 'downloading' ? num(it.speed) : null) },
    eta: { get: (it) => (it.status === 'downloading' ? num(it.etaSec) : null) },
    size: { get: (it) => num(it.bytes) },
    full: { get: (it) => num(it.fullSize) },
    server: { text: true, get: (it) => it.server },
    category: { text: true, get: (it) => it.categoryName },
    start: { get: (it) => num(it.startSec) },
    end: { get: (it) => num(it.endSec) },
    duration: { get: (it) => num(it.durSec) },
  };
  const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' });
  function sortUnits(list) {
    const s = state.sort && SORTS[state.sort.key];
    if (!s) return list;
    const dir = state.sort.dir;
    list.forEach((u) => { u.v = s.get(u.it); });
    return list.sort((a, b) => {
      const ea = a.v === null || a.v === undefined || a.v === '';
      const eb = b.v === null || b.v === undefined || b.v === '';
      if (ea !== eb) return ea ? 1 : -1; // nothing to compare always goes last
      if (!ea) {
        const c = s.text ? collator.compare(String(a.v), String(b.v)) : a.v - b.v;
        if (c !== 0) return c * dir;
      }
      return a.q - b.q;
    });
  }

  // ---------- Group Split Videos: the parts of one link shown as one row ----------
  // What is shown is a list of units: a row, or a group ({ it: its summary, parts }).
  // A group sorts as one row by its summary; its parts are sorted among themselves
  // (by name, so name, name_01, ..., when no column is sorted).
  function viewUnits() {
    const items = state.snap.items;
    const byLink = new Map();
    if (state.snap.groupSplit) items.forEach((it, q) => {
      if (!byLink.has(it.url)) byLink.set(it.url, []);
      byLink.get(it.url).push({ it, q });
    });
    const units = [];
    const done = new Set();
    items.forEach((it, q) => {
      const parts = byLink.get(it.url);
      if (!parts || parts.length < 2) { units.push({ it, q }); return; }
      if (done.has(it.url)) return;
      done.add(it.url);
      if (state.sort) sortUnits(parts); else parts.sort((a, b) => collator.compare(a.it.name, b.it.name) || a.q - b.q);
      units.push({ it: groupSummary(it.url, parts.map((p) => p.it)), q, parts });
    });
    // Search Names: a row is shown when its name has the text in it; a group when
    // one of its parts has (it then shows only those parts, but sums up all of them)
    const has = (it) => !state.search || String(it.name || '').toLowerCase().includes(state.search);
    const shown = units.filter((u) => {
      if (!u.parts) return has(u.it);
      u.parts = u.parts.filter((p) => has(p.it));
      return u.parts.length > 0;
    });
    return sortUnits(shown);
  }
  function viewIds() { // what is shown, top to bottom, with the parts of closed groups
    if (!state.snap) return [];
    return viewUnits().flatMap((u) => (u.parts ? u.parts.map((p) => p.it.id) : [u.it.id]));
  }
  function groupSummary(link, parts) {
    const names = parts.map((p) => p.name);
    const base = names[0].replace(/_\d{2}$/, '');
    const finished = (p) => p.status === 'complete' && !p.detail;
    const left = parts.filter((p) => !finished(p));
    // the status shown is the one of the parts still to do that comes first in the Status sort order
    const lead = (left.length ? left : parts).reduce((a, b) => (statusRank(b) < statusRank(a) ? b : a));
    const done = parts.length - left.length;
    const running = parts.filter((p) => p.status === 'downloading');
    const weight = (p) => (p.durSec > 0 ? p.durSec : 1);
    const all = (f) => parts.every((p) => p[f] > 0);
    const add = (f) => parts.reduce((s, p) => s + (p[f] > 0 ? p[f] : 0), 0);
    const cats = new Set(parts.map((p) => p.categoryName || ''));
    const starts = parts.map((p) => p.startSec).filter((v) => v >= 0);
    const ends = parts.map((p) => p.endSec).filter((v) => v >= 0);
    return {
      link, parts, lead, done,
      name: names.every((n) => n === base || n.startsWith(base + '_')) ? base : names[0],
      status: done === parts.length ? 'complete' : running.length ? 'downloading' : lead.status,
      rank: statusRank(lead),
      percent: parts.reduce((s, p) => s + weight(p) * (p.status === 'complete' ? 100 : Math.max(0, p.percent || 0)), 0) / parts.reduce((s, p) => s + weight(p), 0),
      speed: running.reduce((s, p) => s + (p.speed || 0), 0),
      etaSec: running.length ? Math.max(...running.map((p) => (p.etaSec > 0 ? p.etaSec : 0))) : -1,
      bytes: add('bytes'),
      fullSize: all('fullSize') ? add('fullSize') : 0,
      categoryName: cats.size === 1 ? [...cats][0] : '',
      server: '',
      startSec: starts.length ? Math.min(...starts) : -1,
      endSec: ends.length ? Math.max(...ends) : -1,
      durSec: all('durSec') ? add('durSec') : -1,
    };
  }
  const groupRows = new Map(); // link -> row refs
  function makeGroupRow(link) {
    const r = makeRow(0);
    delete r.tr.dataset.id;
    r.tr.dataset.group = link;
    r.toggle = document.createElement('button');
    r.toggle.type = 'button';
    r.toggle.className = 'gtoggle';
    r.nm.parentNode.insertBefore(r.toggle, r.nm);
    return r;
  }
  function updateGroupRow(r, u) {
    const s = u.it, parts = s.parts, n = parts.length;
    const st = s.done === n ? { label: 'Finished', cls: 'complete' } : statusOf(s.lead);
    const live = parts.filter((p) => p.status !== 'complete');
    const sel = parts.every((p) => state.selected.has(p.id));
    put(r, 'cls', ['grp', 's-' + st.cls, sel ? 'selected' : ''].join(' '), (v) => { r.tr.className = v.replace(/\s+/g, ' ').trim(); });
    put(r, 'active', live.length === 0 ? 'none' : live.every((p) => p.active) ? 'all' : live.some((p) => p.active) ? 'some' : 'off', (v) => {
      r.cb.checked = v === 'all'; r.cb.indeterminate = v === 'some'; r.cb.disabled = v === 'none';
    });
    put(r, 'n', u.q + 1, (v) => { r.n.textContent = v; });
    put(r, 'open', state.openGroups.has(s.link), (v) => {
      r.toggle.textContent = v ? '−' : '+';
      r.toggle.setAttribute('aria-expanded', String(v));
      r.toggle.setAttribute('aria-label', (v ? 'Hide' : 'Show') + ' the parts');
    });
    put(r, 'name', s.name + ' (' + n + ' parts)', (v) => { r.nm.textContent = v; });
    put(r, 'title', s.link, (v) => { r.nm.parentNode.title = v; });
    put(r, 'stt', st.label, (v) => { r.stt.textContent = v; r.stt.parentNode.title = v; });
    put(r, 'det', s.done < n ? s.done + '/' + n + ' Finished' : '', (v) => { r.det.textContent = v; });
    const pct = Math.max(0, Math.min(100, s.percent || 0));
    put(r, 'pct', pct, (v) => { r.fill.style.width = v + '%'; });
    put(r, 'pctLabel', s.status === 'complete' ? '100%' : (pct > 0 ? pct.toFixed(1) + '%' : ''), (v) => { r.pct.textContent = v; });
    const running = s.status === 'downloading';
    put(r, 'sp', running ? fmtSpeed(s.speed) : '', (v) => { r.sp.textContent = v; });
    put(r, 'eta', running ? fmtEta(s.etaSec) : '', (v) => { r.eta.textContent = v; });
    put(r, 'sz', fmtBytes(s.bytes), (v) => { r.sz.textContent = v; });
    put(r, 'fz', s.fullSize > 0 ? (s.status === 'complete' ? '' : '~') + fmtBytes(s.fullSize) : '', (v) => { r.fz.textContent = v; });
    put(r, 'cat', s.categoryName, (v) => { r.cat.textContent = v; r.cat.title = v; });
    put(r, 'start', fmtClock(s.startSec), (v) => { r.t1.textContent = v; });
    put(r, 'end', fmtClock(s.endSec), (v) => { r.t2.textContent = v; });
    put(r, 'dur', fmtClock(s.durSec), (v) => { r.t3.textContent = v; });
  }
  function partIds(link) { return state.snap.items.filter((it) => it.url === link).map((it) => it.id); }
  // the rows and groups shown, top to bottom, with the ids each one stands for
  function shownEntries() {
    const out = [];
    for (const u of viewUnits()) {
      if (!u.parts) { const r = rows.get(u.it.id); if (r) out.push({ ids: [u.it.id], tr: r.tr }); continue; }
      const g = groupRows.get(u.it.link);
      if (g) out.push({ ids: u.parts.map((p) => p.it.id), tr: g.tr });
      if (state.openGroups.has(u.it.link)) for (const p of u.parts) { const r = rows.get(p.it.id); if (r) out.push({ ids: [p.it.id], tr: r.tr }); }
    }
    return out;
  }
  function paintSortHeads() {
    $$('#grid thead th').forEach((th) => {
      const on = state.sort && th.dataset.col === state.sort.key;
      th.classList.toggle('sorted', !!on);
      const si = th.querySelector('.si');
      if (si) si.textContent = on ? (state.sort.dir === 1 ? '▲' : '▼') : '';
      if (on) th.setAttribute('aria-sort', state.sort.dir === 1 ? 'ascending' : 'descending');
      else th.removeAttribute('aria-sort');
    });
    const sorted = !!state.sort;
    const hint = {
      top: 'The selected rows go to the top of the queue, in the order shown.',
      bottom: 'The selected rows go to the bottom of the queue, in the opposite order to the one shown.',
      up: 'The selected rows are put in the order shown, then moved up one step.',
      down: 'The selected rows are put in the order shown, then moved down one step.',
    };
    $$('.toolbar [data-action="top"], .toolbar [data-action="up"], .toolbar [data-action="down"], .toolbar [data-action="bottom"]').forEach((b) => {
      if (b.dataset.title === undefined) b.dataset.title = b.title;
      b.title = sorted ? b.dataset.title + '. With a column sorted: ' + hint[b.dataset.action].toLowerCase() : b.dataset.title;
    });
  }
  function setSort(key, dir) {
    const s = SORTS[key];
    state.sort = s ? { key, dir } : null;
    paintSortHeads();
    paintSelection();
    if (state.snap) renderTable();
  }
  // Move buttons while a column is sorted: the sort decides the order the rows end up in.
  // Top puts the selected rows first, in the order shown (A-Z, largest first, ...). Bottom
  // puts them last in the opposite order (Z-A, ...). Up/Down first make the whole list the
  // order shown, then move the selection one step. Afterwards the table shows the queue again.
  async function moveRows(action) {
    const sel = new Set(selectedIds());
    if (!sel.size) return;
    if (!state.sort) return act(action, selectedIds());
    const shown = viewIds();
    const picked = shown.filter((id) => sel.has(id));
    try {
      if (action === 'top') await api('action', { action: 'arrangetop', ids: picked });
      else if (action === 'bottom') await api('action', { action: 'arrangebottom', ids: picked.slice().reverse() });
      else {
        await api('action', { action: 'arrangetop', ids: shown });
        await api('action', { action, ids: picked });
      }
    } catch (err) {
      toast(err.message, 'error');
    }
    state.sort = null;
    paintSortHeads();
    paintSelection();
    await refresh();
  }
  function clickHead(key) {
    const s = SORTS[key];
    if (!s) { state.sort = null; } // "#": back to the queue order
    else {
      const first = s.text ? 1 : -1;
      if (!state.sort || state.sort.key !== key) state.sort = { key, dir: first };
      else if (state.sort.dir === first) state.sort = { key, dir: -first };
      else state.sort = null;
    }
    paintSortHeads();
    paintSelection();
    if (state.snap) renderTable();
  }
  async function setForce(value) {
    try {
      const current = await api('settings');
      state.settings = await api('settings', Object.assign({}, current, { forceNameSort: value }), 'PUT');
      refresh();
    } catch (err) { toast(err.message, 'error'); }
  }
  function initSortHeads() {
    $$('#grid thead th').forEach((th) => {
      const key = th.dataset.col;
      if (!key || key === 'act') return;
      th.classList.add('sortable');
      const si = document.createElement('span');
      si.className = 'si';
      th.appendChild(si);
      th.addEventListener('click', () => { if (!resizing) clickHead(key); });
    });
    $('#grid thead').addEventListener('contextmenu', (e) => {
      const th = e.target.closest('th');
      e.preventDefault();
      if (!th || !th.dataset.col || th.dataset.col === 'act') return;
      const key = th.dataset.col;
      const label = Array.from(th.childNodes).filter((n) => n.nodeType === 3).map((n) => n.textContent).join('').trim() || key;
      const s = SORTS[key];
      const list = [];
      if (s) {
        const first = s.text ? 1 : -1;
        const on = (d) => !!state.sort && state.sort.key === key && state.sort.dir === d;
        list.push({ label: 'Sort by ' + label + (s.up ? ' (' + s.up + ')' : s.text ? ' (A to Z)' : (first === -1 ? ' (largest first)' : '')), fn: () => setSort(key, first), on: on(first) });
        list.push({ label: 'Sort by ' + label + (s.down ? ' (' + s.down + ')' : s.text ? ' (Z to A)' : ' (smallest first)'), fn: () => setSort(key, -first), on: on(-first) });
      }
      if (key === 'name') {
        // unlike the view sorts above, this one keeps the queue itself in name order
        const f = state.snap && state.snap.forceNameSort;
        list.push({ label: 'Force ' + label + ' Sort A to Z', fn: () => setForce(f === 'az' ? '' : 'az'), on: f === 'az' });
        list.push({ label: 'Force ' + label + ' Sort Z to A', fn: () => setForce(f === 'za' ? '' : 'za'), on: f === 'za' });
      }
      if (state.sort) list.push({ label: 'Back to the queue order', fn: () => setSort(null) });
      else if (!s) list.push({ label: 'Queue order (no sort)', fn: () => setSort(null), on: true });
      openMenu(e.clientX, e.clientY, list);
    });
  }

  function renderTable() {
    const items = state.snap.items;
    const keep = new Set(), keepGroups = new Set();
    let i = 0;
    const place = (tr) => { if (tbody.children[i] !== tr) tbody.insertBefore(tr, tbody.children[i] || null); i++; };
    const show = ({ it, q }, part) => {
      let r = rows.get(it.id);
      if (!r) { r = makeRow(it.id); rows.set(it.id, r); }
      updateRow(r, it, q, part);
      place(r.tr);
    };
    for (const u of viewUnits()) {
      if (!u.parts) { keep.add(u.it.id); show(u, false); continue; }
      const link = u.it.link;
      keepGroups.add(link);
      let g = groupRows.get(link);
      if (!g) { g = makeGroupRow(link); groupRows.set(link, g); }
      updateGroupRow(g, u);
      place(g.tr);
      const open = state.openGroups.has(link);
      for (const p of u.parts) {
        keep.add(p.it.id);
        if (open) show(p, true);
        else if (rows.has(p.it.id)) rows.get(p.it.id).tr.remove(); // kept, just not shown
      }
    }
    for (const [id, r] of rows) {
      if (!keep.has(id)) { r.tr.remove(); rows.delete(id); state.selected.delete(id); }
    }
    for (const [link, g] of groupRows) {
      if (!keepGroups.has(link)) { g.tr.remove(); groupRows.delete(link); }
    }
    $('#empty').hidden = items.length > 0;
    $('#noMatch').hidden = !(items.length > 0 && keep.size === 0);
    // header checkbox: all active / none / mixed
    const live = items.filter((it) => it.status !== 'complete');
    const on = live.filter((it) => it.active).length;
    const all = $('#allActive');
    all.checked = live.length > 0 && on === live.length;
    all.indeterminate = on > 0 && on < live.length;
    all.disabled = live.length === 0;
  }

  // ---------- selection ----------
  function orderedIds() { return state.snap ? state.snap.items.map((it) => it.id) : []; } // queue order
  function selectedIds() { return orderedIds().filter((id) => state.selected.has(id)); }

  function paintSelection() {
    for (const [id, r] of rows) {
      const sel = state.selected.has(id);
      r.tr.classList.toggle('selected', sel);
      // keep the cached class string in step so the next update does not undo this
      r.v.cls = r.tr.className;
    }
    for (const [link, g] of groupRows) {
      const ids = partIds(link);
      g.tr.classList.toggle('selected', ids.length > 0 && ids.every((id) => state.selected.has(id)));
      g.v.cls = g.tr.className;
    }
    const has = state.selected.size > 0;
    $$('[data-needs="sel"]').forEach((b) => {
      b.disabled = !has;
    });
  }

  // pick: the ids of the row clicked (all the parts for a group row)
  function selectRow(pick, ev) {
    const ids = viewIds(); // a range follows what is shown
    if (ev.shiftKey && state.anchor != null && ids.includes(state.anchor)) {
      const at = pick.map((id) => ids.indexOf(id)).concat(ids.indexOf(state.anchor));
      const lo = Math.min(...at), hi = Math.max(...at);
      if (!(ev.ctrlKey || ev.metaKey)) state.selected.clear();
      for (let i = lo; i <= hi; i++) state.selected.add(ids[i]);
    } else if (ev.ctrlKey || ev.metaKey) {
      const all = pick.every((id) => state.selected.has(id));
      pick.forEach((id) => (all ? state.selected.delete(id) : state.selected.add(id)));
      state.anchor = pick[0];
    } else {
      state.selected.clear();
      pick.forEach((id) => state.selected.add(id));
      state.anchor = pick[0];
    }
    paintSelection();
  }
  function rowIds(tr) { return tr.dataset.group ? partIds(tr.dataset.group) : [Number(tr.dataset.id)]; }
  // Expand/Collapse groups (shown with Group Split Videos on): opens every
  // group, or closes them all when every group is open already.
  $('#expandGroups').addEventListener('click', () => {
    const count = new Map();
    state.snap.items.forEach((it) => count.set(it.url, (count.get(it.url) || 0) + 1));
    const links = [...count].filter(([, n]) => n > 1).map(([link]) => link);
    const allOpen = links.every((link) => state.openGroups.has(link));
    links.forEach((link) => { if (allOpen) state.openGroups.delete(link); else state.openGroups.add(link); });
    renderTable();
    paintSelection();
  });
  function toggleGroup(link) {
    if (state.openGroups.has(link)) state.openGroups.delete(link); else state.openGroups.add(link);
    renderTable();
    paintSelection();
  }

  // ---------- drag a box to select rows (like Explorer) ----------
  // Dragging on the list draws a box and selects the rows it touches. Ctrl
  // toggles the rows it touches, Shift adds them, otherwise they replace the
  // selection. Near the top or bottom edge the list scrolls along.
  const wrap = $('#tableWrap');
  let dragged = false; // a box was just drawn: the click that follows must not change the selection
  wrap.addEventListener('click', (e) => { if (dragged) { dragged = false; e.stopPropagation(); e.preventDefault(); } }, true);
  wrap.addEventListener('pointerdown', (e) => {
    dragged = false; // a new gesture: nothing left over from the last one
    if (e.button !== 0 || e.pointerType === 'touch' || resizing) return;
    if (e.target.closest('input, button, a, select, thead, .rz')) return;
    if (wrap.scrollHeight <= 0) return;
    const box = wrap.getBoundingClientRect();
    // the scrollbars are not part of the list
    if (e.clientX > box.left + wrap.clientWidth || e.clientY > box.top + wrap.clientHeight) return;
    const startX = e.clientX, startY = e.clientY;
    const originX = startX - box.left + wrap.scrollLeft, originY = startY - box.top + wrap.scrollTop;
    const base = new Set(state.selected);
    const additive = e.ctrlKey || e.metaKey, adding = e.shiftKey;
    let band = null, active = false, lastX = startX, lastY = startY, timer = 0;
    const headH = $('#grid thead').getBoundingClientRect().height;

    function update() {
      const b = wrap.getBoundingClientRect();
      const x = lastX - b.left + wrap.scrollLeft, y = lastY - b.top + wrap.scrollTop;
      const left = Math.min(originX, x), right = Math.max(originX, x);
      const top = Math.min(originY, y), bottom = Math.max(originY, y);
      band.style.left = left + 'px'; band.style.top = top + 'px';
      band.style.width = (right - left) + 'px'; band.style.height = (bottom - top) + 'px';
      const t = $('#grid').getBoundingClientRect();
      const tl = t.left - b.left + wrap.scrollLeft, tr = t.right - b.left + wrap.scrollLeft;
      const overTable = right >= tl && left <= tr;
      const next = additive || adding ? new Set(base) : new Set();
      if (overTable) {
        for (const en of shownEntries()) {
          const rb = en.tr.getBoundingClientRect();
          const rTop = rb.top - b.top + wrap.scrollTop, rBottom = rb.bottom - b.top + wrap.scrollTop;
          if (rBottom < top || rTop > bottom) continue;
          for (const id of en.ids) { if (additive && base.has(id)) next.delete(id); else next.add(id); }
        }
      }
      state.selected = next;
      paintSelection();
    }
    function autoscroll() {
      const b = wrap.getBoundingClientRect();
      let dy = 0;
      if (lastY < b.top + headH + 20) dy = -Math.ceil((b.top + headH + 20 - lastY) / 3);
      else if (lastY > b.bottom - 20) dy = Math.ceil((lastY - (b.bottom - 20)) / 3);
      if (dy) { wrap.scrollTop += dy; update(); }
    }
    function move(ev) {
      lastX = ev.clientX; lastY = ev.clientY;
      if (!active) {
        if (Math.abs(lastX - startX) < 4 && Math.abs(lastY - startY) < 4) return;
        active = true;
        band = document.createElement('div');
        band.className = 'marquee';
        wrap.appendChild(band);
        wrap.setPointerCapture(e.pointerId);
        document.body.classList.add('selecting');
        timer = setInterval(autoscroll, 30);
      }
      update();
    }
    function end() {
      wrap.removeEventListener('pointermove', move);
      wrap.removeEventListener('pointerup', end);
      wrap.removeEventListener('pointercancel', end);
      clearInterval(timer);
      if (!active) return;
      band.remove();
      document.body.classList.remove('selecting');
      try { wrap.releasePointerCapture(e.pointerId); } catch (_) { /* already released */ }
      const ids = viewIds().filter((id) => state.selected.has(id));
      state.anchor = ids.length ? ids[0] : null;
      dragged = true; // cleared by the click that ends this gesture, or by the next pointerdown
    }
    wrap.addEventListener('pointermove', move);
    wrap.addEventListener('pointerup', end);
    wrap.addEventListener('pointercancel', end);
  });

  tbody.addEventListener('click', (e) => {
    const tr = e.target.closest('tr');
    if (!tr || e.target.closest('.c-act')) return;
    if (e.target.closest('.gtoggle')) { toggleGroup(tr.dataset.group); return; }
    selectRow(rowIds(tr), e);
  });
  // double-click: show the file where it is now (unfinished or finished folder)
  tbody.addEventListener('dblclick', (e) => {
    const tr = e.target.closest('tr');
    if (!tr || e.target.closest('.c-act') || e.target.closest('input, button, a')) return;
    if (tr.dataset.group) { toggleGroup(tr.dataset.group); return; } // a group opens or closes
    revealFile(Number(tr.dataset.id));
  });
  async function revealFile(id) {
    try { await api('reveal', { id }); } catch (err) { toast(err.message, 'error'); }
  }
  tbody.addEventListener('contextmenu', (e) => {
    const tr = e.target.closest('tr');
    e.preventDefault();
    if (!tr) return;
    const ids = rowIds(tr);
    if (!ids.every((id) => state.selected.has(id))) { state.selected.clear(); ids.forEach((id) => state.selected.add(id)); state.anchor = ids[0]; paintSelection(); }
    openMenu(e.clientX, e.clientY);
  });
  $('#tableWrap').addEventListener('click', (e) => {
    if (e.target.closest('tbody') || e.target.closest('thead')) return;
    state.selected.clear(); paintSelection();
  });
  tbody.addEventListener('change', async (e) => {
    if (!e.target.matches('input[type="checkbox"]')) return;
    const tr = e.target.closest('tr');
    const mine = rowIds(tr).filter((id) => !tr.dataset.group || state.snap.items.find((it) => it.id === id).status !== 'complete');
    const ids = mine.every((id) => state.selected.has(id)) && state.selected.size > mine.length ? selectedIds() : mine;
    await act(e.target.checked ? 'activate' : 'deactivate', ids);
  });
  $('#allActive').addEventListener('change', async (e) => {
    const ids = state.snap.items.filter((it) => it.status !== 'complete').map((it) => it.id);
    await act(e.target.checked ? 'activate' : 'deactivate', ids);
  });

  // ---------- actions ----------
  async function refresh() {
    try { apply(await api('state')); } catch (_) { /* the event stream will catch up */ }
  }

  async function act(action, ids, extra) {
    try {
      const res = await api('action', Object.assign({ action, ids: ids || [] }, extra || {}));
      if (res && res.message) toast(res.message, /not started/.test(res.message) ? 'warn' : 'info');
    } catch (err) {
      toast(err.message, 'error');
    }
    refresh();
  }

  $$('.toolbar [data-action]').forEach((btn) => {
    btn.addEventListener('click', () => {
      const action = btn.dataset.action;
      const needs = btn.dataset.needs === 'sel';
      const ids = selectedIds();
      if (needs && ids.length === 0) return;
      if (/^(top|up|down|bottom)$/.test(action)) { moveRows(action); return; }
      if (action === 'remove') { removeRows(ids); return; }
      act(action, needs ? ids : []);
    });
  });

  $('#recoveryBtn').addEventListener('click', () => act('dismissrecovery', []));
  $('#openDone').addEventListener('click', () => openFolder('done', undefined, true));
  async function openFolder(which, id, redirect) {
    try { await api('open', id !== undefined ? { which, id } : redirect ? { which, redirect } : { which }); } catch (err) { toast(err.message, 'error'); }
  }

  $$('#modeSeg button').forEach((btn) => {
    btn.addEventListener('click', async () => {
      paintMode(btn.dataset.mode);
      try { await api('mode', { mode: btn.dataset.mode }); } catch (err) { toast(err.message, 'error'); }
      refresh();
    });
  });
  function paintMode(mode) {
    $$('#modeSeg button').forEach((b) => b.setAttribute('aria-checked', String(b.dataset.mode === mode)));
  }

  // ---------- add ----------
  async function addLinks() {
    const box = $('#urlInput');
    const text = box.value;
    if (!text.trim()) { toast('Paste one or more links first.', 'warn'); box.focus(); return; }
    try {
      const res = await api('add', { text, category: addCategoryId() });
      const problems = res.problems || [];
      if (res.added > 0) toast('Added ' + plural(res.added, 'row') + '.', 'ok');
      const lineProblems = problems.filter((p) => /^line \d+:/.test(p));
      if (problems.length === 0 || (problems.length > 0 && problems.every((p) => /^already in the list/.test(p)))) {
        box.value = '';
      } else if (lineProblems.length === problems.length) {
        // keep just the lines that need fixing
        const bad = new Set(lineProblems.map((p) => Number(/^line (\d+):/.exec(p)[1])));
        box.value = text.replace(/\r\n/g, '\n').split('\n').filter((_, i) => bad.has(i + 1)).join('\n');
      }
      if (problems.length) {
        const shown = problems.slice(0, 4).join('\n') + (problems.length > 4 ? '\n…and ' + (problems.length - 4) + ' more' : '');
        toast(shown, res.added > 0 ? 'warn' : 'error', 10000);
      }
    } catch (err) {
      toast(err.message, 'error');
    }
    refresh();
  }
  $('#addBtn').addEventListener('click', addLinks);
  $('#urlInput').addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) { e.preventDefault(); addLinks(); }
  });

  // ---------- categories ----------
  const addSel = $('#addCategory');
  const EDIT_CATS = '__edit';
  let lastCats = '';
  function addCategoryId() {
    const v = addSel.value;
    return v && v !== EDIT_CATS ? v : '';
  }
  function renderCategorySelect(cats) {
    const key = JSON.stringify(cats);
    if (key === lastCats) return;
    lastCats = key;
    let want = '';
    try { want = localStorage.getItem('rp-add-category') || ''; } catch (_) { /* private mode */ }
    if (addSel.value && addSel.value !== EDIT_CATS) want = addSel.value;
    addSel.innerHTML = '';
    const add = (value, label) => { const o = document.createElement('option'); o.value = value; o.textContent = label; addSel.appendChild(o); };
    add('', 'No category');
    cats.forEach((c) => add(c.id, c.name));
    add(EDIT_CATS, 'Edit categories…');
    addSel.value = cats.some((c) => c.id === want) ? want : '';
  }
  addSel.addEventListener('change', () => {
    if (addSel.value === EDIT_CATS) {
      addSel.value = '';
      openSettings().then(() => $('#categoriesGroup').scrollIntoView({ block: 'start' }));
      return;
    }
    try { localStorage.setItem('rp-add-category', addSel.value); } catch (_) { /* private mode */ }
  });

  async function changeCategory(ids, catId) {
    const items = state.snap.items.filter((it) => ids.includes(it.id) && (it.category || '') !== catId);
    if (!items.length) return;
    const cat = (state.snap.categories || []).find((c) => c.id === catId);
    const target = cat ? '“' + cat.name + '”' : 'no category';
    const hasFiles = items.some((it) => it.bytes > 0 || it.status === 'complete');
    const what = plural(items.length, 'row');
    const ok = hasFiles
      ? await confirmDialog('Change category to ' + target + '?',
        what + ' will be moved to ' + target + '. Their files in the old category’s folders (the unfinished video, the finished video and any saved playlist) will be deleted, and each download starts again from the beginning in the new folders. Downloads that are running are stopped first.',
        'Delete files and restart')
      : await confirmDialog('Change category to ' + target + '?',
        what + ' will be moved to ' + target + ' and start in its folders. Nothing has been downloaded for ' + (items.length === 1 ? 'it' : 'them') + ' yet, so no files are deleted.',
        'Change category');
    if (ok) act('setcategory', items.map((it) => it.id), { category: catId });
  }

  // ---------- Search Names: filter the list by name as you type ----------
  const nameSearch = $('#nameSearch');
  nameSearch.addEventListener('input', () => {
    state.search = nameSearch.value.trim().toLowerCase();
    if (state.snap) { renderTable(); paintSelection(); } // rows filtered out are deselected
  });
  nameSearch.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && nameSearch.value) { e.stopPropagation(); nameSearch.value = ''; nameSearch.dispatchEvent(new Event('input')); }
  });

  // ---------- context menu ----------
  const menu = $('#ctxMenu');
  const MENU = [
    ['Start', 'start'], ['Pause', 'pause'], ['Force start', 'forcestart'], '-',
    ['Set active', 'activate'], ['Set inactive', 'deactivate'], '-',
    ['Move to top', 'top'], ['Move up', 'up'], ['Move down', 'down'], ['Move to bottom', 'bottom'], '-',
    ['Reset server', 'resetserver'], '-', // Set category goes in before the rule above Get playlist
    ['Get playlist (.m3u8)', 'm3u8'], ['Copy link', 'copy'], ['Copy extended link', 'copyext'], ['Show file in folder', 'reveal'], ['Open Done folder', 'opendone'], '-',
    ['Remove…', 'remove', 'danger'],
  ];
  function categoryMenu() {
    const cats = (state.snap && state.snap.categories) || [];
    if (!cats.length) return [];
    const ids = selectedIds();
    const current = new Set(state.snap.items.filter((it) => ids.includes(it.id)).map((it) => it.category || ''));
    const out = ['-', { head: 'Set category' }];
    out.push({ label: 'No category', cat: '', on: current.size === 1 && current.has('') });
    cats.forEach((c) => out.push({ label: c.name, cat: c.id, on: current.size === 1 && current.has(c.id) }));
    return out;
  }
  function openMenu(x, y, custom) {
    menu.innerHTML = '';
    let items = MENU.slice();
    if (custom) items = custom;
    else {
      const at = items.findIndex((m) => m[1] === 'm3u8');
      const extra = categoryMenu();
      if (extra.length) items.splice(at >= 0 ? at - 1 : items.length, 0, ...extra);
    }
    items.forEach((m) => {
      if (m && m.head) {
        const li = document.createElement('li');
        li.className = 'head'; li.textContent = m.head; li.setAttribute('role', 'presentation');
        menu.appendChild(li);
        return;
      }
      if (m && typeof m === 'object' && !Array.isArray(m)) {
        const li = document.createElement('li');
        li.setAttribute('role', 'menuitem');
        const t = document.createElement('span'); t.className = 'tick'; t.textContent = m.on ? '✓' : '';
        li.appendChild(t); li.appendChild(document.createTextNode(m.label));
        li.addEventListener('click', () => { closeMenu(); if (m.fn) m.fn(); else changeCategory(selectedIds(), m.cat); });
        menu.appendChild(li);
        return;
      }
      const li = document.createElement('li');
      if (m === '-') { li.className = 'rule'; li.setAttribute('role', 'separator'); }
      else {
        li.textContent = m[0];
        li.setAttribute('role', 'menuitem');
        if (m[2]) li.className = m[2];
        li.addEventListener('click', () => { closeMenu(); runMenu(m[1]); });
      }
      menu.appendChild(li);
    });
    menu.hidden = false;
    const w = menu.offsetWidth, h = menu.offsetHeight;
    menu.style.left = Math.max(4, Math.min(x, window.innerWidth - w - 4)) + 'px';
    menu.style.top = Math.max(4, Math.min(y, window.innerHeight - h - 4)) + 'px';
  }
  function closeMenu() { menu.hidden = true; }
  document.addEventListener('click', (e) => { if (!menu.contains(e.target)) closeMenu(); });
  window.addEventListener('blur', closeMenu);
  window.addEventListener('resize', closeMenu);
  $('#tableWrap').addEventListener('scroll', closeMenu);

  async function runMenu(action) {
    const ids = selectedIds();
    if (action === 'opendone') return ids.length ? openFolder('done', ids[ids.length - 1]) : openFolder('done');
    if (ids.length === 0) return;
    if (action === 'reveal') return revealFile(ids[ids.length - 1]);
    if (action === 'copy') {
      const urls = state.snap.items.filter((it) => state.selected.has(it.id)).map((it) => it.url).join('\n');
      try { await navigator.clipboard.writeText(urls); toast('Copied ' + plural(ids.length, 'link') + '.', 'ok'); }
      catch (_) { toast('Could not copy to the clipboard.', 'error'); }
      return;
    }
    if (action === 'copyext') { // ["link","start","end","length of the video"], one line per selected row
      const mine = () => state.snap.items.filter((it) => state.selected.has(it.id));
      const unknown = () => mine().filter((it) => it.endSec < 0).length;
      const coming = () => mine().filter((it) => it.endSec < 0 && it.lengthComing).length;
      if (coming()) { // being looked up in the background: give it up to 8 seconds. A lookup that failed is not waited for.
        toast('Finding the length of ' + plural(coming(), 'video') + '…', 'info');
        for (let i = 0; i < 16 && coming(); i++) { await new Promise((r) => setTimeout(r, 500)); await refresh(); }
      }
      const t = (s) => fmtClock(s).replace(/^0/, '');
      const lines = mine().map((it) => {
        if (it.endSec < 0) return it.url;
        const f = [it.url, t(it.startSec), t(it.endSec)];
        if (it.videoSec > 0) f.push(t(it.videoSec));
        return '[' + f.map((x) => JSON.stringify(x)).join(',') + ']';
      });
      const plain = unknown();
      try {
        await navigator.clipboard.writeText(lines.join('\n'));
        toast('Copied ' + plural(lines.length, 'extended link') + '.' + (plain ? ' ' + plural(plain, 'video') + ' stayed a plain link: its length could not be found.' : ''), plain ? 'warn' : 'ok');
      } catch (_) { toast('Could not copy to the clipboard.', 'error'); }
      return;
    }
    if (action === 'remove') return removeRows(ids);
    if (/^(top|up|down|bottom)$/.test(action)) return moveRows(action);
    return act(action, ids);
  }

  // Removing rows always asks first. The files stay unless the box is ticked.
  async function removeRows(ids) {
    if (!ids.length) return;
    const r = await askDialog(
      'Remove ' + plural(ids.length, 'row') + ' from the list?',
      ids.length === 1 ? 'The row is taken off the list.' : 'The rows are taken off the list.',
      'Remove',
      ids.length === 1 ? 'Also delete the file from disk (finished or unfinished video). This can’t be undone.' : 'Also delete their files from disk (finished and unfinished videos). This can’t be undone.',
      'Remove and delete files');
    if (r.ok) act(r.checked ? 'removefiles' : 'remove', ids);
  }

  // askDialog shows the shared question box. With checkLabel it also shows a tick
  // box that starts off; the answer is { ok, checked }. okChecked, when given,
  // is the label of the OK button while the box is ticked.
  function askDialog(title, text, okLabel, checkLabel, okChecked) {
    return new Promise((resolve) => {
      const dlg = $('#confirmDlg');
      const box = $('#confirmCheck');
      $('#confirmTitle').textContent = title;
      $('#confirmText').textContent = text;
      $('#confirmOk').textContent = okLabel;
      box.checked = false;
      $('#confirmCheckRow').hidden = !checkLabel;
      $('#confirmCheckText').textContent = checkLabel || '';
      box.onchange = () => { $('#confirmOk').textContent = box.checked && okChecked ? okChecked : okLabel; };
      dlg.addEventListener('close', () => resolve({ ok: dlg.returnValue === 'ok', checked: !!checkLabel && box.checked }), { once: true });
      dlg.returnValue = 'cancel';
      dlg.showModal();
    });
  }
  async function confirmDialog(title, text, okLabel) {
    return (await askDialog(title, text, okLabel)).ok;
  }

  // ---------- keyboard ----------
  function typing(e) {
    const t = e.target;
    return t && (t.tagName === 'INPUT' && t.type !== 'checkbox' || t.tagName === 'TEXTAREA' || t.isContentEditable);
  }
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') {
      if (document.querySelector('dialog[open]')) return; // the dialog handles Escape itself
      if (!menu.hidden) { closeMenu(); return; }
      if (!$('#settingsPage').hidden) { closeSettings(); return; }
      if (!typing(e)) { state.selected.clear(); paintSelection(); }
      return;
    }
    if (typing(e) || !$('#settingsPage').hidden || document.querySelector('dialog[open]')) return;
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'a') {
      e.preventDefault();
      viewIds().forEach((id) => state.selected.add(id)); // what is shown (Search Names)
      paintSelection();
    } else if (e.key === 'Delete') {
      const ids = selectedIds();
      if (ids.length) removeRows(ids);
    } else if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      const list = shownEntries(); // a closed group is one step
      if (!list.length) return;
      e.preventDefault();
      const cur = state.anchor != null ? list.findIndex((en) => en.ids.length === 1 ? en.ids[0] === state.anchor : en.ids[0] === state.anchor && en.ids.every((id) => state.selected.has(id))) : -1;
      const at = cur >= 0 ? cur : list.findIndex((en) => en.ids.includes(state.anchor));
      const next = list[Math.max(0, Math.min(list.length - 1, at + (e.key === 'ArrowDown' ? 1 : -1)))];
      state.selected.clear(); next.ids.forEach((id) => state.selected.add(id)); state.anchor = next.ids[0];
      paintSelection();
      next.tr.scrollIntoView({ block: 'nearest' });
    }
  });

  // ---------- status bar, banner, notices ----------
  function renderStatus(snap) {
    const t = snap.totals;
    $('#stDl').textContent = t.downloading + ' downloading';
    $('#stWait').textContent = t.queued + ' waiting';
    $('#stPaused').textContent = t.paused + ' paused';
    $('#stDone').textContent = t.complete + ' finished';
    $('#stErrBox').hidden = t.error === 0;
    $('#stErr').textContent = t.error + ' failed';
    $('#stSpeed').textContent = fmtSpeed(t.speed) || '0 B/s';
    $('#stVersion').textContent = snap.app ? 'v' + snap.app.replace(/^v/, '') : '';
    $('#credsBanner').hidden = !snap.needCreds;
    $('#recoveryBanner').hidden = !snap.recovery;
    $('#recoveryText').textContent = snap.recovery || '';
  }

  function showNotices(snap) {
    const list = snap.notices || [];
    if (state.lastNotice === null) { // first snapshot: do not replay old messages
      state.lastNotice = list.length ? list[list.length - 1].id : 0;
      return;
    }
    list.filter((n) => n.id > state.lastNotice).forEach((n) => {
      toast(n.text, n.level === 'error' ? 'error' : n.text.startsWith('Completed') || n.text.startsWith('Saved') ? 'ok' : n.level);
      state.lastNotice = n.id;
    });
  }

  function apply(snap) {
    state.snap = snap;
    renderTable();
    paintSelection();
    paintMode(snap.mode);
    $('#expandGroups').hidden = !snap.groupSplit;
    renderCategorySelect(snap.categories || []);
    renderStatus(snap);
    showNotices(snap);
    $('#stConn').hidden = true;
    if (snap.theme) applyTheme(snap.theme);
    applyColumns(snap.columns);
    paintListener(snap.listener);
  }

  // ---------- link listener (main window button) ----------
  // Links the listener found go at the bottom of the link box, one per line, under the last one.
  // The first snapshot only notes where the list is, so a reload never re-adds old links.
  function putLinksInBox(l) {
    const links = (l && l.links) || [];
    const top = links.length ? links[links.length - 1].id : 0;
    if (state.lastLink == null) { state.lastLink = top; return; }
    const fresh = links.filter((x) => x.id > state.lastLink).map((x) => x.line);
    state.lastLink = Math.max(state.lastLink, top);
    if (!fresh.length) return;
    const box = $('#urlInput');
    const have = new Set(box.value.split('\n').map((s) => s.trim()).filter(Boolean));
    const add = fresh.filter((u) => !have.has(u) && have.add(u));
    if (!add.length) return;
    const cur = box.value.replace(/\s+$/, '');
    box.value = (cur ? cur + '\n' : '') + add.join('\n') + '\n';
    box.scrollTop = box.scrollHeight; // show what was just added
    box.dispatchEvent(new Event('input', { bubbles: true }));
  }
  function paintListener(l) {
    putLinksInBox(l);
    const b = $('#listenBtn');
    const on = !!(l && l.enabled);
    b.setAttribute('aria-pressed', on ? 'true' : 'false');
    let t = on ? 'Link listener is on: links you copy are added to your text file. Click to switch off.' : 'Link listener is off. Click to switch on.';
    if (on && l.added) t += ' Added ' + l.added + ' so far.';
    if (on && l.error) t += ' Problem: ' + l.error;
    b.title = t;
  }
  $('#listenBtn').addEventListener('click', async () => {
    const on = $('#listenBtn').getAttribute('aria-pressed') === 'true';
    try {
      await api('listen', { enabled: !on });
      state.settings = null; // re-read the next time settings open
    } catch (err) {
      toast(err.message, 'error', 7000);
      if (!on && /text file/i.test(err.message)) {
        await openSettings();
        $('#listenGroup').scrollIntoView({ block: 'start' });
        (F.listenArchive.checked ? F.listenFile : F.listenArchive).focus();
      }
    }
    refresh();
  });

  // ---------- settings page ----------
  const page = $('#settingsPage');
  const F = {
    ua: $('#setUA'), cookie: $('#setCookie'),
    done: $('#setDone'), inc: $('#setIncomplete'), m3u8: $('#setM3U8'),
    dataDir: $('#setDataDir'),
    parallel: $('#setParallel'), res: $('#setRes'),
    firstPieces: $('#setFirstPieces'), uniqueServers: $('#setUniqueServers'), uniqueWait: $('#setUniqueWait'), groupSplit: $('#setGroupSplit'), convertMp4: $('#setConvertMp4'), ffmpegDir: $('#setFFmpegDir'), folderRedirect: $('#setFolderRedirect'), deleteAfterConvert: $('#setDeleteAfterConvert'), autoStart: $('#setAutoStart'),
    listenOn: $('#setListenOn'), listenFile: $('#setListenFile'), listenArchive: $('#setListenArchive'), listenBackups: $('#setListenBackups'), listenPat: $('#setListenPatterns'), listenSkip: $('#setListenSkip'), listenPrivate: $('#setListenPrivate'),
  };

  // Backups are copies of the archive, so they only exist while the archive is
  // on. The choice is kept while the archive is switched off, so switching it
  // back on brings the backups tick back.
  let backupsWanted = true;
  function syncArchive() {
    const on = F.listenArchive.checked;
    F.listenFile.disabled = !on;
    F.listenBackups.disabled = !on;
    F.listenBackups.checked = on && backupsWanted;
  }
  F.listenArchive.addEventListener('change', syncArchive);
  F.listenBackups.addEventListener('change', () => { backupsWanted = F.listenBackups.checked; });

  // Delete After Conversion needs Convert to MP4.
  function syncExperimental() {
    if (!F.convertMp4.checked) F.deleteAfterConvert.checked = false;
    F.deleteAfterConvert.disabled = !F.convertMp4.checked;
  }
  F.convertMp4.addEventListener('change', syncExperimental);

  function fillSettings(s) {
    fillStatusOrder(s.statusOrder);
    F.groupSplit.checked = !!s.groupSplit;
    F.ua.value = s.userAgent || '';
    F.cookie.value = s.cookie || '';
    F.done.value = s.doneDir || '';
    F.inc.value = s.incompleteDir || '';
    F.m3u8.value = s.m3u8Dir || '';
    F.dataDir.value = s.dataDir || '';
    renderCategoryEditor(s.categories || []);
    F.parallel.value = s.maxParallel || 0;
    F.res.value = s.maxResolution || 0;
    F.firstPieces.checked = !!s.firstPieces;
    F.uniqueServers.checked = !!s.uniqueServers;
    F.uniqueWait.value = s.uniqueWait || 1;
    F.convertMp4.checked = !!s.convertMp4;
    F.ffmpegDir.value = s.ffmpegDir || '';
    F.folderRedirect.replaceChildren(new Option('No Category', ''), ...(s.categories || []).map((c) => new Option(c.name, c.id)));
    F.folderRedirect.value = s.folderRedirect || '';
    F.deleteAfterConvert.checked = !!s.deleteAfterConvert;
    syncExperimental();
    F.autoStart.checked = !!s.autoStart;
    const L = s.listen || {};
    F.listenOn.checked = !!L.enabled;
    F.listenFile.value = L.file || '';
    F.listenArchive.checked = !L.noArchive;
    backupsWanted = !L.noBackups;
    syncArchive();
    F.listenPat.value = (L.patterns || []).join('\n');
    F.listenSkip.checked = L.skipDuplicates !== false;
    F.listenPrivate.checked = !!L.allowPrivate;
    $('#listenTestOut').hidden = true;
    $$('input[name="theme"]').forEach((r) => { r.checked = r.value === (s.theme || 'system'); });
    setDirty(false);
  }

  function readSettings() {
    const theme = ($('input[name="theme"]:checked') || {}).value || 'system';
    return {
      userAgent: F.ua.value, cookie: F.cookie.value,
      doneDir: F.done.value, incompleteDir: F.inc.value, m3u8Dir: F.m3u8.value,
      dataDir: F.dataDir.value,
      categories: readCategoryEditor(),
      maxParallel: parseInt(F.parallel.value, 10) || 0, maxResolution: parseInt(F.res.value, 10) || 0,
      firstPieces: F.firstPieces.checked, uniqueServers: F.uniqueServers.checked, uniqueWait: parseFloat(F.uniqueWait.value) || 1, forceNameSort: state.settings ? state.settings.forceNameSort || '' : '', convertMp4: F.convertMp4.checked, ffmpegDir: F.ffmpegDir.value.trim(), folderRedirect: F.folderRedirect.value, deleteAfterConvert: F.deleteAfterConvert.checked, autoStart: F.autoStart.checked,
      listen: { enabled: F.listenOn.checked, file: F.listenFile.value, noArchive: !F.listenArchive.checked, noBackups: !backupsWanted, patterns: F.listenPat.value.split('\n'), skipDuplicates: F.listenSkip.checked, allowPrivate: F.listenPrivate.checked },
      theme, mode: state.settings ? state.settings.mode : 'hybrid',
      statusOrder: $$('#statusOrder .sbox').map((b) => b.dataset.key), groupSplit: F.groupSplit.checked,
    };
  }

  // ---------- Status sort order (settings page): boxes dragged left to right ----------
  const sorder = $('#statusOrder');
  function fillStatusOrder(order) {
    const keys = (order && order.length ? order : Object.keys(STATUS_BOXES)).filter((k) => STATUS_BOXES[k]);
    sorder.textContent = '';
    for (const k of keys) {
      const b = document.createElement('button');
      b.type = 'button'; b.className = 'sbox'; b.draggable = true; b.dataset.key = k;
      b.textContent = STATUS_BOXES[k]; b.setAttribute('role', 'listitem');
      sorder.appendChild(b);
    }
  }
  let dragBox = null;
  sorder.addEventListener('dragstart', (e) => {
    dragBox = e.target.closest('.sbox');
    if (!dragBox) return;
    dragBox.classList.add('dragging');
    e.dataTransfer.effectAllowed = 'move';
    e.dataTransfer.setData('text/plain', dragBox.dataset.key);
  });
  sorder.addEventListener('dragover', (e) => {
    if (!dragBox) return;
    e.preventDefault();
    const over = e.target.closest('.sbox');
    if (!over || over === dragBox) return;
    const r = over.getBoundingClientRect();
    sorder.insertBefore(dragBox, e.clientX < r.left + r.width / 2 ? over : over.nextSibling);
  });
  sorder.addEventListener('drop', (e) => e.preventDefault());
  sorder.addEventListener('dragend', () => {
    if (!dragBox) return;
    dragBox.classList.remove('dragging');
    dragBox = null;
    setDirty(true);
  });
  sorder.addEventListener('keydown', (e) => {
    const b = e.target.closest('.sbox');
    if (!b || (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight')) return;
    e.preventDefault();
    if (e.key === 'ArrowLeft' && b.previousElementSibling) sorder.insertBefore(b, b.previousElementSibling);
    else if (e.key === 'ArrowRight' && b.nextElementSibling) sorder.insertBefore(b.nextElementSibling, b);
    else return;
    b.focus();
    setDirty(true);
  });

  function setDirty(d) {
    state.settingsDirty = d;
    $('#savedNote').textContent = d ? 'You have unsaved changes.' : '';
  }

  // ---------- categories editor (settings page) ----------
  const catList = $('#catList');
  const CAT_FOLDERS = [
    ['incompleteDir', 'Unfinished videos'],
    ['doneDir', 'Finished videos'],
    ['m3u8Dir', 'Playlists (.m3u8)'],
  ];
  function browseInto(input, button) {
    button.addEventListener('click', async () => {
      button.disabled = true;
      try {
        const res = await api('browse', { start: input.value.trim() });
        if (res.path) { input.value = res.path; setDirty(true); }
      } catch (err) { toast(err.message, 'error'); }
      button.disabled = false;
    });
  }
  function addCategoryCard(c) {
    const card = document.createElement('div');
    card.className = 'cat';
    card.dataset.id = c.id || '';
    const head = document.createElement('div');
    head.className = 'row';
    const name = document.createElement('input');
    name.type = 'text'; name.className = 'cat-name'; name.value = c.name || '';
    name.placeholder = 'Category name'; name.maxLength = 40; name.spellcheck = false; name.autocomplete = 'off';
    name.setAttribute('aria-label', 'Category name');
    const del = document.createElement('button');
    del.type = 'button'; del.className = 'btn'; del.textContent = 'Remove';
    del.addEventListener('click', () => { card.remove(); setDirty(true); });
    head.append(name, del);
    const sub = document.createElement('div');
    sub.className = 'sub';
    CAT_FOLDERS.forEach(([key, label]) => {
      const lab = document.createElement('label'); lab.textContent = label;
      const row = document.createElement('div'); row.className = 'row';
      const input = document.createElement('input');
      input.type = 'text'; input.dataset.key = key; input.value = c[key] || '';
      input.placeholder = 'Same as the normal folder'; input.spellcheck = false; input.autocomplete = 'off';
      input.setAttribute('aria-label', label + ' for this category');
      const browse = document.createElement('button');
      browse.type = 'button'; browse.className = 'btn'; browse.textContent = 'Browse…';
      browseInto(input, browse);
      row.append(input, browse);
      sub.append(lab, row);
    });
    card.append(head, sub);
    catList.appendChild(card);
    return card;
  }
  function renderCategoryEditor(cats) {
    catList.innerHTML = '';
    if (!cats.length) {
      const none = document.createElement('div');
      none.className = 'help'; none.id = 'catNone';
      none.textContent = 'No categories yet.';
      catList.appendChild(none);
      return;
    }
    cats.forEach(addCategoryCard);
  }
  function readCategoryEditor() {
    return $$('.cat', catList).map((card) => {
      const c = { id: card.dataset.id || '', name: card.querySelector('.cat-name').value };
      card.querySelectorAll('input[data-key]').forEach((i) => { c[i.dataset.key] = i.value; });
      return c;
    });
  }
  $('#catAdd').addEventListener('click', () => {
    const none = $('#catNone'); if (none) none.remove();
    const card = addCategoryCard({});
    card.querySelector('.cat-name').focus();
    setDirty(true);
  });

  async function openSettings() {
    try {
      state.settings = await api('settings');
      fillSettings(state.settings);
    } catch (err) { toast(err.message, 'error'); return; }
    page.hidden = false;
    F.ua.focus();
  }
  let closing = false;
  async function closeSettings() {
    if (closing) return;
    if (state.settingsDirty) {
      closing = true;
      const ok = await confirmDialog('Close settings?', 'Your changes have not been saved.', 'Discard changes');
      closing = false;
      if (!ok) return;
    }
    page.hidden = true;
    setDirty(false);
  }
  // A different cookie is only saved after asking.
  // If the answer is No nothing is saved and the page stays as it is.
  async function saveSettings() {
    const next = readSettings();
    const had = state.settings ? (state.settings.cookie || '').trim() : '';
    const changed = had !== '' && next.cookie.trim() !== had; // the first cookie replaces nothing
    if (changed && !(await cookieDialog(next.cookie.trim().length))) return;
    try {
      state.settings = await api('settings', next, 'PUT');
      fillSettings(state.settings);
      applyTheme(state.settings.theme);
      $('#savedNote').textContent = 'Settings saved.';
      if (changed) cookieToast(next.cookie.trim().length);
      refresh();
    } catch (err) { toast(err.message, 'error'); }
  }

  $('#quitBtn').addEventListener('click', async () => {
    const t = state.snap ? state.snap.totals : null;
    const busy = t && (t.downloading + t.queued) > 0;
    if (busy && !(await confirmDialog('Shut down Recurbate?',
      'Downloads that are running or waiting are paused and saved. They continue the next time you start Recurbate.', 'Shut down'))) return;
    $('#stopping').hidden = false;
    try {
      await api('quit', {});
    } catch (err) {
      $('#stopping').hidden = true;
      toast(err.message, 'error');
      return;
    }
    $('#stoppingTitle').textContent = 'Recurbate has shut down';
    $('#stoppingText').textContent = 'Everything is saved. You can close this window.';
    window.close(); // works for the app window; a browser tab keeps the message above
  });
  $('#settingsBtn').addEventListener('click', openSettings);
  $('#credsBtn').addEventListener('click', openSettings);
  $('#settingsBack').addEventListener('click', closeSettings);
  $('#settingsClose').addEventListener('click', closeSettings);
  $('#settingsSave').addEventListener('click', () => saveSettings());
  page.addEventListener('input', (e) => {
    if (e.target && e.target.name === 'theme') return; // saved straight away, not a pending edit
    setDirty(true);
  });
  $$('input[name="theme"]').forEach((r) => r.addEventListener('change', async () => {
    // the theme applies and is stored straight away; other edits wait for Save
    applyTheme(r.value);
    try {
      const current = state.settings || (await api('settings'));
      state.settings = await api('settings', Object.assign({}, current, { theme: r.value }), 'PUT');
    } catch (err) { toast(err.message, 'error'); }
  }));
  $('#listenTestBtn').addEventListener('click', async () => {
    const out = $('#listenTestOut');
    out.hidden = false;
    try {
      const res = await api('listen/test', { patterns: F.listenPat.value.split('\n'), text: $('#setListenTest').value });
      out.classList.remove('bad');
      if (!res.box.length) out.textContent = 'No links found in this text.';
      else {
        out.textContent = 'Link box (the whole entry is kept when one was copied):\n' + res.box.join('\n') +
          '\n\nText file (plain links only):\n' + res.links.join('\n');
      }
    } catch (err) {
      out.classList.add('bad');
      out.textContent = err.message;
    }
  });
  $('#revealCookie').addEventListener('click', (e) => {
    const hidden = F.cookie.classList.toggle('masked');
    e.target.textContent = hidden ? 'Show cookie' : 'Hide cookie';
  });
  // The text on the clipboard. Read by the program itself, so there is no permission prompt.
  async function clipboardText() {
    let text = '', why = '';
    try { text = ((await api('clipboard', {})).text || '').trim(); }
    catch (err) {
      why = err.message;
      if (/not available here|only available on Windows/.test(why)) { // not Windows: the browser's own read, which may ask
        try { text = (await navigator.clipboard.readText()).trim(); why = ''; } catch (_) { /* refused */ }
      }
    }
    return { text, why };
  }
  // The question asked before any cookie is replaced (main window and Settings).
  function cookieQuestion(len) {
    return 'The saved cookie will be replaced with the new one (' + len + ' characters). Downloads keep going and use it from their next request.';
  }
  // Asks before a cookie is replaced; true to go ahead.
  async function cookieDialog(len) {
    return (await askDialog('Replace the cookie?', cookieQuestion(len), 'Replace cookie')).ok;
  }
  // Replaces the cookie (nothing is paused); the Settings page, if open, shows the new one.
  async function replaceCookie(cookie) {
    try {
      await api('cookie', { cookie });
      state.settings = await api('settings');
      F.cookie.value = state.settings.cookie || '';
      cookieToast(cookie.length);
      refresh();
    } catch (err) { toast(err.message, 'error', 7000); }
  }
  function cookieToast(len) { toast('Cookie replaced (' + len + ' characters).', 'ok'); }
  // Replace cookie (main window and Settings): ask, then save the copied text as the cookie.
  async function replaceFromClipboard(settingsOpen) {
    const { text, why } = await clipboardText();
    if (!text) {
      if (settingsOpen) F.cookie.focus();
      toast('Nothing to replace the cookie with. ' + (why ? why + ' ' : 'The clipboard has no text. ') + 'Copy the cookie first, then press Replace cookie again' + (settingsOpen ? ', or press Ctrl+V in the box.' : '.'), 'warn', 8000);
      return;
    }
    const cookie = text.replace(/\s*[\r\n]+\s*/g, ' ');
    if (await cookieDialog(cookie.length)) replaceCookie(cookie);
  }
  $('#cookieBtn').addEventListener('click', () => replaceFromClipboard(false));
  $('#replaceCookie').addEventListener('click', () => replaceFromClipboard(true));
  $$('[data-browse]').forEach((b) => b.addEventListener('click', async () => {
    const field = $('#' + b.dataset.browse);
    b.disabled = true;
    try {
      const res = await api('browse', { start: field.value.trim() });
      if (res.path) { field.value = res.path; setDirty(true); }
    } catch (err) { toast(err.message, 'error'); }
    b.disabled = false;
  }));
  $$('[data-open]').forEach((b) => b.addEventListener('click', () => openFolder(b.dataset.open)));

  // ---------- live updates ----------
  function connect() {
    const es = new EventSource('/api/events');
    let base = null; // the stream's own last snapshot: changed rows go on top of it, not on one /api/state gave
    es.onmessage = (e) => {
      try {
        const snap = JSON.parse(e.data);
        if (!snap.items) { // only the rows that changed (every message after the first)
          const byId = new Map(base.items.map((it) => [it.id, it]));
          snap.changed.forEach((it) => byId.set(it.id, it));
          snap.items = (snap.order || base.items.map((it) => it.id)).map((id) => byId.get(id));
        }
        base = snap;
        apply(snap);
      } catch (err) { console.error(err); }
    };
    es.onerror = () => {
      // the browser reconnects by itself; if the program is gone say so
      setTimeout(() => { if (es.readyState !== 1) $('#stConn').hidden = false; }, 4000);
    };
  }

  window.addEventListener('beforeunload', (e) => {
    if (state.snap && state.snap.totals.downloading > 0) {
      e.preventDefault();
      e.returnValue = 'Downloads are running. Closing pauses them; they continue next time Recurbate starts.';
    }
  });

  // test hook (no effect in normal use)
  window.__rp = { state, apply, hueFor, fmtBytes, fmtEta, fmtClock };

  connect();
  refresh();
})();
