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
      case 'complete': return { label: 'Finished', cls: 'complete' };
      case 'error': return { label: 'Error: ' + (it.error || 'failed'), cls: 'error' };
      default: return { label: it.status, cls: 'queued' };
    }
  }

  function put(r, key, value, apply) {
    if (r.v[key] !== value) { r.v[key] = value; apply(value); }
  }

  function updateRow(r, it, index) {
    const st = statusOf(it);
    put(r, 'cls', ['s-' + st.cls, it.active ? '' : 'inactive', it.next ? 'next' : '', state.selected.has(it.id) ? 'selected' : ''].join(' '),
      (v) => { r.tr.className = v.replace(/\s+/g, ' ').trim(); });
    put(r, 'active', it.active, (v) => { r.cb.checked = v; });
    put(r, 'n', index + 1, (v) => { r.n.textContent = v; });
    put(r, 'name', it.name, (v) => { r.nm.textContent = v; });
    put(r, 'title', it.url + (it.error ? '\n' + it.error : ''), (v) => { r.nm.parentNode.title = v; });
    put(r, 'tag', it.m3u8, (v) => { r.tag.hidden = !v; });
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
    status: { text: true, get: (it) => statusOf(it).label },
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
  function viewItems() {
    const items = state.snap.items;
    const s = state.sort && SORTS[state.sort.key];
    if (!s) return items.map((it, i) => ({ it, q: i }));
    const dir = state.sort.dir;
    return items.map((it, i) => ({ it, q: i, v: s.get(it) })).sort((a, b) => {
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
  function viewIds() { return state.snap ? viewItems().map((x) => x.it.id) : []; }
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
        list.push({ label: 'Sort by ' + label + (s.text ? ' (A to Z)' : (first === -1 ? ' (largest first)' : '')), fn: () => setSort(key, first), on: on(first) });
        list.push({ label: 'Sort by ' + label + (s.text ? ' (Z to A)' : ' (smallest first)'), fn: () => setSort(key, -first), on: on(-first) });
      }
      if (state.sort) list.push({ label: 'Back to the queue order', fn: () => setSort(null) });
      else if (!s) list.push({ label: 'Queue order (no sort)', fn: () => setSort(null), on: true });
      openMenu(e.clientX, e.clientY, list);
    });
  }

  function renderTable() {
    const items = state.snap.items;
    const keep = new Set();
    viewItems().forEach(({ it, q }, i) => {
      keep.add(it.id);
      let r = rows.get(it.id);
      if (!r) { r = makeRow(it.id); rows.set(it.id, r); }
      updateRow(r, it, q);
      if (tbody.children[i] !== r.tr) tbody.insertBefore(r.tr, tbody.children[i] || null);
    });
    for (const [id, r] of rows) {
      if (!keep.has(id)) { r.tr.remove(); rows.delete(id); state.selected.delete(id); }
    }
    $('#empty').hidden = items.length > 0;
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
    const has = state.selected.size > 0;
    $$('[data-needs="sel"]').forEach((b) => {
      b.disabled = !has;
    });
  }

  function selectRow(id, ev) {
    const ids = viewIds(); // a range follows what is shown
    if (ev.shiftKey && state.anchor != null && ids.includes(state.anchor)) {
      const a = ids.indexOf(state.anchor);
      const b = ids.indexOf(id);
      const [lo, hi] = a < b ? [a, b] : [b, a];
      if (!(ev.ctrlKey || ev.metaKey)) state.selected.clear();
      for (let i = lo; i <= hi; i++) state.selected.add(ids[i]);
    } else if (ev.ctrlKey || ev.metaKey) {
      if (state.selected.has(id)) state.selected.delete(id); else state.selected.add(id);
      state.anchor = id;
    } else {
      state.selected.clear();
      state.selected.add(id);
      state.anchor = id;
    }
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
        for (const [id, r] of rows) {
          const rb = r.tr.getBoundingClientRect();
          const rTop = rb.top - b.top + wrap.scrollTop, rBottom = rb.bottom - b.top + wrap.scrollTop;
          if (rBottom < top || rTop > bottom) continue;
          if (additive && base.has(id)) next.delete(id); else next.add(id);
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
    selectRow(Number(tr.dataset.id), e);
  });
  // double-click: show the file where it is now (unfinished or finished folder)
  tbody.addEventListener('dblclick', (e) => {
    const tr = e.target.closest('tr');
    if (!tr || e.target.closest('.c-act') || e.target.closest('input, button, a')) return;
    revealFile(Number(tr.dataset.id));
  });
  async function revealFile(id) {
    try { await api('reveal', { id }); } catch (err) { toast(err.message, 'error'); }
  }
  tbody.addEventListener('contextmenu', (e) => {
    const tr = e.target.closest('tr');
    e.preventDefault();
    if (!tr) return;
    const id = Number(tr.dataset.id);
    if (!state.selected.has(id)) { state.selected.clear(); state.selected.add(id); state.anchor = id; paintSelection(); }
    openMenu(e.clientX, e.clientY);
  });
  $('#tableWrap').addEventListener('click', (e) => {
    if (e.target.closest('tbody') || e.target.closest('thead')) return;
    state.selected.clear(); paintSelection();
  });
  tbody.addEventListener('change', async (e) => {
    if (!e.target.matches('input[type="checkbox"]')) return;
    const id = Number(e.target.closest('tr').dataset.id);
    const ids = state.selected.has(id) && state.selected.size > 1 ? selectedIds() : [id];
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

  $('#openDone').addEventListener('click', () => openFolder('done'));
  async function openFolder(which, id) {
    try { await api('open', id === undefined ? { which } : { which, id }); } catch (err) { toast(err.message, 'error'); }
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
    const hasFiles = items.some((it) => it.bytes > 0 || it.status === 'complete' || it.m3u8);
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

  // ---------- find links on a page ----------
  const scanDlg = $('#scanDlg');
  $('#scanBtn').addEventListener('click', () => { scanDlg.showModal(); $('#scanUrl').focus(); });
  $('#scanCancel').addEventListener('click', () => scanDlg.close());
  $('#scanForm').addEventListener('submit', async (e) => {
    e.preventDefault();
    const url = $('#scanUrl').value.trim();
    if (!url) return;
    const go = $('#scanGo');
    go.disabled = true; go.textContent = 'Searching…';
    try {
      const res = await api('scan', { url });
      const urls = res.urls || [];
      if (urls.length === 0) {
        toast('No video links were found on that page.', 'warn');
      } else {
        const box = $('#urlInput');
        box.value = (box.value.trim() ? box.value.trim() + '\n' : '') + urls.join('\n');
        toast('Found ' + plural(urls.length, 'link') + '. Check them, then press Add to list.', 'ok');
        scanDlg.close();
      }
    } catch (err) {
      toast(err.message, 'error', 9000);
    } finally {
      go.disabled = false; go.textContent = 'Find links';
    }
  });

  // ---------- context menu ----------
  const menu = $('#ctxMenu');
  const MENU = [
    ['Start', 'start'], ['Pause', 'pause'], ['Force start', 'forcestart'], '-',
    ['Set active', 'activate'], ['Set inactive', 'deactivate'], '-',
    ['Move to top', 'top'], ['Move up', 'up'], ['Move down', 'down'], ['Move to bottom', 'bottom'], '-',
    ['Get playlist (.m3u8)', 'm3u8'], ['Copy link', 'copy'], ['Show file in folder', 'reveal'], ['Open Done folder', 'opendone'], '-',
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
      orderedIds().forEach((id) => state.selected.add(id));
      paintSelection();
    } else if (e.key === 'Delete') {
      const ids = selectedIds();
      if (ids.length) removeRows(ids);
    } else if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      const ids = viewIds();
      if (!ids.length) return;
      e.preventDefault();
      const cur = state.anchor != null ? ids.indexOf(state.anchor) : -1;
      const next = Math.max(0, Math.min(ids.length - 1, cur + (e.key === 'ArrowDown' ? 1 : -1)));
      state.selected.clear(); state.selected.add(ids[next]); state.anchor = ids[next];
      paintSelection();
      const r = rows.get(ids[next]);
      if (r) r.tr.scrollIntoView({ block: 'nearest' });
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
    autoStart: $('#setAutoStart'), autoResume: $('#setAutoResume'),
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

  function fillSettings(s) {
    F.ua.value = s.userAgent || '';
    F.cookie.value = s.cookie || '';
    F.done.value = s.doneDir || '';
    F.inc.value = s.incompleteDir || '';
    F.m3u8.value = s.m3u8Dir || '';
    F.dataDir.value = s.dataDir || '';
    renderCategoryEditor(s.categories || []);
    F.parallel.value = s.maxParallel || 0;
    F.res.value = s.maxResolution || 0;
    F.autoStart.checked = !!s.autoStart;
    F.autoResume.checked = !!s.autoResume;
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
      autoStart: F.autoStart.checked, autoResume: F.autoResume.checked,
      listen: { enabled: F.listenOn.checked, file: F.listenFile.value, noArchive: !F.listenArchive.checked, noBackups: !backupsWanted, patterns: F.listenPat.value.split('\n'), skipDuplicates: F.listenSkip.checked, allowPrivate: F.listenPrivate.checked },
      theme, mode: state.settings ? state.settings.mode : 'hybrid',
    };
  }

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
  // A different cookie is only saved after asking (already asked: asked = true).
  // If the answer is No nothing is saved and the page stays as it is.
  async function saveSettings(asked) {
    const next = readSettings();
    const had = state.settings ? (state.settings.cookie || '').trim() : '';
    const changed = had !== '' && next.cookie.trim() !== had; // the first cookie replaces nothing
    if (changed && asked !== true && !(await confirmDialog('Replace the cookie?', cookieQuestion(next.cookie.trim().length), 'Replace cookie'))) return;
    const paused = changed ? runningCount() : 0;
    try {
      state.settings = await api('settings', next, 'PUT');
      fillSettings(state.settings);
      applyTheme(state.settings.theme);
      $('#savedNote').textContent = 'Settings saved.';
      if (changed) cookieToast(paused, next.cookie.trim().length);
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
    return 'The saved cookie will be replaced with the text you last copied (' + len + ' characters). Downloads that are running or waiting will be paused. They will stay paused until they are started manually.';
  }
  function cookieToast(paused, len) {
    toast(paused ? 'Downloads Paused to Replace Cookies' : 'Cookie replaced (' + len + ' characters).', 'ok');
  }
  function runningCount() {
    const items = state.snap ? state.snap.items : [];
    return items.filter((it) => it.status === 'queued' || it.status === 'resolving' || it.status === 'downloading').length;
  }
  // Replace cookie on the main window: ask first, then save the copied text as the cookie.
  $('#cookieBtn').addEventListener('click', async () => {
    const { text, why } = await clipboardText();
    if (!text) {
      toast('Nothing to replace the cookie with. ' + (why ? why + ' ' : 'The clipboard has no text. ') + 'Copy the cookie first, then press this button again.', 'warn', 8000);
      return;
    }
    const cookie = text.replace(/\s*[\r\n]+\s*/g, ' ');
    if (!(await confirmDialog('Replace the cookie?', cookieQuestion(cookie.length), 'Replace cookie'))) return;
    try {
      const res = await api('cookie', { cookie });
      state.settings = null; // re-read the next time settings open
      cookieToast(res.paused || 0, cookie.length);
      refresh();
    } catch (err) { toast(err.message, 'error', 7000); }
  });
  // Replace cookie in Settings: take what is on the clipboard, ask, then save the settings with it.
  $('#replaceCookie').addEventListener('click', async () => {
    const { text, why } = await clipboardText();
    if (!text) {
      F.cookie.focus();
      toast('Nothing to replace the cookie with. ' + (why ? why + ' ' : 'The clipboard has no text. ') + 'Copy the cookie and press Replace cookie again, or press Ctrl+V in the box.', 'warn', 8000);
      return;
    }
    const cookie = text.replace(/\s*[\r\n]+\s*/g, ' ');
    if (!(await confirmDialog('Replace the cookie?', cookieQuestion(cookie.length), 'Replace cookie'))) return;
    F.cookie.value = cookie;
    setDirty(true);
    saveSettings(true);
  });
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
    es.onmessage = (e) => {
      try { apply(JSON.parse(e.data)); } catch (err) { console.error(err); }
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
