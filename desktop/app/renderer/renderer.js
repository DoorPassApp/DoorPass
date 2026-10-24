const $ = (id) => document.getElementById(id);
const MAX_ROWS = 200;

let servers = [];
let current = null;
let busy = false;
const queries = [];

function fmtLatency(ms) {
  return ms == null ? '–' : `${ms} ms`;
}

function fmtTime(ms) {
  return new Date(ms).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

function showError(msg) {
  const el = $('error');
  el.textContent = msg || '';
  el.hidden = !msg;
}

async function run(fn) {
  if (busy) return;
  busy = true;
  render();
  showError('');
  try {
    await fn();
  } catch (e) {
    showError(e.message);
  } finally {
    busy = false;
    render();
  }
}

// ---- Status -----------------------------------------------------------------------

function render() {
  if (!current) return;
  const st = current;
  const on = !!st.connected;
  const helperOk = st.helper === 'ok';

  document.body.classList.toggle('on', on);
  tickUptime();
  $('power').disabled = busy || !helperOk;
  $('power').setAttribute('aria-pressed', String(on));

  if (!helperOk) {
    $('state-title').textContent = 'Setup needed';
    $('state-detail').textContent = 'Install the helper to turn on DoorPass.';
  } else if (on) {
    const name = st.connectedServer ? st.connectedServer.name : st.url;
    $('state-title').textContent = 'Protected';
    $('state-detail').textContent = `${st.mode === 'dns' ? 'Encrypted DNS' : 'Encrypted DNS + TLS splitting'} via ${name}` +
      (st.uplink ? ` · over ${st.uplink}` : '');
  } else {
    $('state-title').textContent = 'Off';
    $('state-detail').textContent = st.lastError ? `Last error: ${st.lastError}` : 'Your DNS lookups are not encrypted by DoorPass.';
  }

  const banner = $('install-banner');
  banner.hidden = helperOk;
  if (st.helper === 'outdated') {
    $('install-title').textContent = 'Helper update';
    $('install-text').textContent = `The installed helper (${st.version}) is a different version from this app. Update it to continue.`;
    $('install').textContent = 'Update helper';
  } else if (st.helper === 'token-mismatch') {
    $('install-title').textContent = 'Helper belongs to another install';
    $('install-text').textContent = 'The running helper was set up by a different copy of DoorPass. Reinstall it to pair it with this one.';
    $('install').textContent = 'Reinstall helper';
  } else {
    $('install-title').textContent = 'One-time setup';
    $('install').textContent = 'Install helper';
  }
  $('install').disabled = busy;

  const s = st.stats || {};
  $('s-queries').textContent = s.queries || 0;
  $('s-latency').textContent = s.queries ? fmtLatency(s.avgLatencyMs) : '–';
  $('s-failures').textContent = s.failures || 0;
  $('s-splits').textContent = s.splitRetries || 0;

  $('helper-info').textContent = helperOk || st.version
    ? `Helper ${st.version || ''}${st.interface ? ` · tunnel ${st.interface}` : ''}`
    : 'Not installed.';
  $('uninstall').disabled = busy || st.helper === 'missing';

  // Don't overwrite a control the user is in the middle of changing.
  const set = st.settings;
  if (set && document.activeElement !== $('custom-url')) {
    if (document.activeElement !== $('server')) $('server').value = set.url;
    $('custom-url').value = set.customUrl || '';
    $('custom-field').hidden = set.url !== 'custom';
    document.querySelectorAll('input[name=mode]').forEach((r) => { r.checked = r.value === set.mode; });
    $('auto-connect').checked = !!set.autoConnect;
    $('query-log').checked = !!set.queryLog;
    $('query-log-panel').hidden = !set.queryLog;
    $('query-log-off').hidden = !!set.queryLog;
  }
  const srv = st.server || {};
  $('server-ips').textContent = srv.ips ? `Bootstrap addresses: ${srv.ips.split(',').join(', ')}` : '';
}

function tickUptime() {
  const el = $('state-uptime');
  const since = current && current.connected && current.since;
  el.hidden = !since;
  if (!since) return;
  const s = Math.max(0, Math.floor((Date.now() - since) / 1000));
  const hh = Math.floor(s / 3600);
  const mm = String(Math.floor((s % 3600) / 60)).padStart(2, '0');
  const ss = String(s % 60).padStart(2, '0');
  el.textContent = `Connected for ${hh ? `${hh}:` : ''}${mm}:${ss}`;
}

setInterval(tickUptime, 1000);

// ---- Queries ------------------------------------------------------------------------

function rowFor(ev) {
  const li = document.createElement('li');
  li.className = `q ${ev.status === 'ok' && ev.rcode !== 'NameError' ? '' : 'bad'}`;
  li.dataset.name = (ev.name || '').toLowerCase();

  const time = document.createElement('span');
  time.className = 'q-time';
  time.textContent = fmtTime(ev.time);

  const name = document.createElement('span');
  name.className = 'q-name';
  name.textContent = ev.name || '(unparsed)';
  name.title = ev.name || '';

  const type = document.createElement('span');
  type.className = 'q-type';
  type.textContent = ev.type || '';

  const lat = document.createElement('span');
  lat.className = 'q-lat';
  lat.textContent = fmtLatency(ev.latencyMs);

  const ans = document.createElement('span');
  ans.className = 'q-ans';
  const answer = ev.status !== 'ok' ? ev.status : ev.rcode && ev.rcode !== 'Success' ? ev.rcode : (ev.answers || []).join(', ');
  ans.textContent = answer;
  ans.title = answer;

  li.append(time, name, type, lat, ans);
  return li;
}

function applyFilter() {
  const f = $('filter').value.trim().toLowerCase();
  let shown = 0;
  for (const li of $('queries').children) {
    const vis = !f || li.dataset.name.includes(f);
    li.hidden = !vis;
    if (vis) shown += 1;
  }
  $('queries-empty').hidden = queries.length > 0;
  $('q-count').textContent = queries.length ? (f ? `${shown} of ${queries.length}` : `${queries.length}`) : '';
}

function addQuery(ev) {
  queries.unshift(ev);
  const list = $('queries');
  list.prepend(rowFor(ev));
  while (queries.length > MAX_ROWS) {
    queries.pop();
    list.lastElementChild.remove();
  }
  applyFilter();
}

function resetQueries() {
  queries.length = 0;
  $('queries').replaceChildren();
  applyFilter();
}

// ---- Wiring --------------------------------------------------------------------------

async function init() {
  servers = await window.doorpass.getServers();
  const sel = $('server');
  for (const s of servers) {
    const opt = document.createElement('option');
    opt.value = s.url;
    opt.textContent = s.name;
    sel.append(opt);
  }
  const custom = document.createElement('option');
  custom.value = 'custom';
  custom.textContent = 'Custom server…';
  sel.append(custom);

  window.doorpass.onStatus((st) => { current = st; render(); });
  window.doorpass.onQuery(addQuery);
  window.doorpass.onQueriesReset(resetQueries);
  window.doorpass.onError(showError);

  $('power').addEventListener('click', () => run(() => (current.connected ? window.doorpass.disconnect() : window.doorpass.connect())));
  $('install').addEventListener('click', () => run(() => window.doorpass.installHelper()));
  $('uninstall').addEventListener('click', () => run(() => window.doorpass.uninstallHelper()));

  // run() re-renders before calling its function, which resets the controls
  // to the saved settings, so every handler reads the new value first.
  sel.addEventListener('change', () => {
    const url = sel.value;
    $('custom-field').hidden = url !== 'custom';
    $('probe-result').textContent = '';
    if (url === 'custom' && !$('custom-url').value) {
      $('custom-url').focus();
      return window.doorpass.updateSettings({ url }).catch((e) => showError(e.message));
    }
    return run(() => window.doorpass.updateSettings({ url }));
  });
  $('custom-url').addEventListener('change', () => {
    const customUrl = $('custom-url').value.trim();
    run(() => window.doorpass.updateSettings({ customUrl }));
  });
  document.querySelectorAll('input[name=mode]').forEach((r) => r.addEventListener('change', () => run(() => window.doorpass.updateSettings({ mode: r.value }))));
  $('auto-connect').addEventListener('change', (e) => {
    const autoConnect = e.target.checked;
    run(() => window.doorpass.updateSettings({ autoConnect }));
  });
  $('query-log').addEventListener('change', (e) => {
    const queryLog = e.target.checked;
    if (!queryLog) resetQueries();
    run(() => window.doorpass.updateSettings({ queryLog }));
  });
  $('login').addEventListener('change', (e) => {
    const openAtLogin = e.target.checked;
    run(() => window.doorpass.updateSettings({ openAtLogin }));
  });
  $('filter').addEventListener('input', applyFilter);

  $('probe').addEventListener('click', () => run(async () => {
    $('probe-result').textContent = 'Testing…';
    try {
      const r = await window.doorpass.probe();
      $('probe-result').textContent = r.ok ? 'Server works.' : `Failed: ${r.error}`;
    } catch (e) {
      $('probe-result').textContent = `Failed: ${e.message}`;
    }
  }));

  $('login').checked = await window.doorpass.getLoginItem();
  current = await window.doorpass.getStatus();
  render();
  applyFilter();
}

init().catch((e) => showError(e.message));
