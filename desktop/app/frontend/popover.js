// The menu bar / tray popover: one door button, status, and a way into the
// main window.
const $ = (id) => document.getElementById(id);

let current = null;
let busy = false;

function uptime(since) {
  const s = Math.max(0, Math.floor((Date.now() - since) / 1000));
  const hh = Math.floor(s / 3600);
  const mm = String(Math.floor((s % 3600) / 60)).padStart(2, '0');
  const ss = String(s % 60).padStart(2, '0');
  return `Connected for ${hh ? `${hh}:` : ''}${mm}:${ss}`;
}

function render() {
  const st = current;
  if (!st) return;
  const on = !!st.connected;
  const helperOk = st.helper === 'ok';
  document.body.classList.toggle('on', on);

  $('power').disabled = busy || !helperOk;
  $('power').setAttribute('aria-pressed', String(on));
  $('power-label').textContent = busy ? (on ? 'Turning off…' : 'Turning on…') : (on ? 'Turn off' : 'Turn on');
  $('setup').hidden = helperOk;

  const name = (st.connectedServer || st.server || {}).name || '';
  if (!helperOk) {
    $('state-title').textContent = 'Setup needed';
    $('state-detail').textContent = 'Install the DoorPass helper to turn it on.';
  } else if (on) {
    $('state-title').textContent = 'Protected';
    $('state-detail').textContent = name;
  } else {
    $('state-title').textContent = 'Off';
    $('state-detail').textContent = st.lastError ? `Last error: ${st.lastError}` : name;
  }
  if (busy) $('state-title').textContent = on ? 'Turning off…' : 'Turning on…';
  tick();
}

function tick() {
  const since = current && current.connected && current.since;
  $('state-uptime').hidden = !since;
  if (since) $('state-uptime').textContent = uptime(since);
}

function showError(msg) {
  $('error').textContent = msg || '';
  $('error').hidden = !msg;
}

$('power').addEventListener('click', async () => {
  if (busy || !current) return;
  busy = true;
  showError('');
  render();
  try {
    current = await (current.connected ? window.doorpass.disconnect() : window.doorpass.connect());
  } catch (e) {
    showError(e.message);
  } finally {
    busy = false;
    render();
  }
});
// The gear opens DoorPass's native menu (mode, server, Preferences, About,
// Quit) just below the button.
$('open').addEventListener('click', () => {
  const r = $('open').getBoundingClientRect();
  window.doorpass.openMenu(Math.round(r.left), Math.round(r.bottom + 4));
});
$('setup').addEventListener('click', () => window.doorpass.openWindow());

window.doorpass.onStatus((st) => { current = st; render(); });
window.doorpass.onError(showError);
setInterval(tick, 1000);
window.doorpass.getStatus().then((st) => { current = st; render(); }).catch((e) => showError(e.message));
