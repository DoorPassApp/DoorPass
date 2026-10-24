// DoorPass main process: tray, window, settings, and the bridge to
// doorpass-helper, which does the privileged networking.

const { app, BrowserWindow, Menu, Tray, ipcMain, nativeImage, nativeTheme, shell } = require('electron');
const { execFile } = require('child_process');
const crypto = require('crypto');
const fs = require('fs');
const path = require('path');
const { HelperClient } = require('./helper-client');
const servers = require('./servers.json');

const isMac = process.platform === 'darwin';
const POLL_MS = 3000;

let win = null;
let tray = null;
let client = null;
let quitting = false;
let lastStatus = { helper: 'unknown' };

// ---- Paths, settings and token ------------------------------------------------

const userFile = (name) => path.join(app.getPath('userData'), name);

function helperBinary() {
  const exe = isMac ? 'doorpass-helper' : 'doorpass-helper.exe';
  if (app.isPackaged) return path.join(process.resourcesPath, 'helper', exe);
  const os = isMac ? 'mac' : 'win';
  return path.join(__dirname, 'helper', `${os}-${process.arch}`, exe);
}

const defaultSettings = {
  url: servers[1].url, // Cloudflare, as on Android.
  customUrl: '',
  mode: 'full',
  autoConnect: false,
  queryLog: false, // Domain names are only recorded while checking something.
};

function loadSettings() {
  try {
    return { ...defaultSettings, ...JSON.parse(fs.readFileSync(userFile('settings.json'), 'utf8')) };
  } catch (_) {
    return { ...defaultSettings };
  }
}

function saveSettings(s) {
  fs.mkdirSync(app.getPath('userData'), { recursive: true });
  fs.writeFileSync(userFile('settings.json'), JSON.stringify(s, null, 2));
}

let settings = null;

// Settings and the helper token from before the rename to DoorPass live in the
// old "Intra" user data folder; carry them over once.
function migrateFromIntra() {
  const oldDir = path.join(app.getPath('appData'), 'Intra');
  for (const name of ['helper-token', 'settings.json']) {
    const from = path.join(oldDir, name);
    const to = userFile(name);
    if (fs.existsSync(from) && !fs.existsSync(to)) {
      fs.mkdirSync(path.dirname(to), { recursive: true });
      fs.copyFileSync(from, to);
      if (name === 'helper-token') fs.chmodSync(to, 0o600);
    }
  }
}

// The token is shared with the helper at install time; it keeps other local
// programs and web pages from driving the helper.
function loadToken() {
  const file = userFile('helper-token');
  try {
    const t = fs.readFileSync(file, 'utf8').trim();
    if (/^[0-9a-f]{64}$/.test(t)) return t;
  } catch (_) { /* first run */ }
  const t = crypto.randomBytes(32).toString('hex');
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, t, { mode: 0o600 });
  return t;
}

// ---- Server selection --------------------------------------------------------

function serverFor(url) {
  return servers.find((s) => s.url === url) || { name: 'Custom server', url, ips: '' };
}

function activeServer() {
  return serverFor(settings.url === 'custom' ? settings.customUrl : settings.url);
}

// ---- Helper install (one admin prompt) ----------------------------------------

function sh(s) {
  return `'${s.replace(/'/g, `'\\''`)}'`;
}

function ps(s) {
  return `'${s.replace(/'/g, "''")}'`;
}

// macOS privacy protection (TCC) stops the root shell behind the admin prompt
// from reading ~/Documents, ~/Downloads and mounted volumes, wherever the app
// happens to live. Stage the helper in a private temp dir it can read.
function stageHelper() {
  const dir = fs.mkdtempSync(path.join(require('os').tmpdir(), 'doorpass-helper-'));
  const bin = path.join(dir, 'doorpass-helper');
  fs.copyFileSync(helperBinary(), bin);
  fs.chmodSync(bin, 0o755);
  return { bin, cleanup: () => fs.rmSync(dir, { recursive: true, force: true }) };
}

function runElevated(args) {
  return new Promise((resolve, reject) => {
    if (isMac) {
      const { bin, cleanup } = stageHelper();
      const cmd = [bin, ...args].map(sh).join(' ');
      const script = `do shell script ${JSON.stringify(cmd)} with administrator privileges ` +
        'with prompt "DoorPass needs to install its network helper."';
      execFile('osascript', ['-e', script], (err, _out, stderr) => {
        cleanup();
        if (err) reject(new Error(/User canceled/.test(stderr) ? 'Cancelled' : (stderr.trim() || err.message)));
        else resolve();
      });
    } else {
      const bin = helperBinary();
      const argList = args.map(ps).join(',');
      const cmd = `$p = Start-Process -FilePath ${ps(bin)} -ArgumentList ${argList} -Verb RunAs ` +
        '-WindowStyle Hidden -Wait -PassThru; exit $p.ExitCode';
      execFile('powershell.exe', ['-NoProfile', '-NonInteractive', '-Command', cmd], { windowsHide: true }, (err, _out, stderr) => {
        if (err) reject(new Error(/canceled|cancelled/i.test(stderr) ? 'Cancelled' : (stderr.trim() || `helper exited with ${err.code}`)));
        else resolve();
      });
    }
  });
}

async function waitForHelper(timeoutMs = 15000) {
  const end = Date.now() + timeoutMs;
  while (Date.now() < end) {
    try {
      const st = await client.status();
      if (st.version === app.getVersion()) return st;
    } catch (_) { /* still starting */ }
    await new Promise((r) => setTimeout(r, 500));
  }
  throw new Error('The helper was installed but did not start. See the helper log.');
}

async function installHelper() {
  await runElevated(['install', '--token-file', userFile('helper-token')]);
  await waitForHelper();
  client.startEvents(onQuery, () => send('queries-reset'));
  await refresh();
}

async function uninstallHelper() {
  client.stopEvents();
  await runElevated(['uninstall']);
  await refresh();
}

// ---- State ----------------------------------------------------------------------

async function refresh() {
  let st;
  try {
    let s = await client.status();
    // The helper forgets the query log setting when it restarts.
    if (s.version === app.getVersion() && s.queryLog !== settings.queryLog) {
      s = await client.setQueryLog(settings.queryLog);
    }
    st = { helper: s.version === app.getVersion() ? 'ok' : 'outdated', ...s };
  } catch (e) {
    st = { helper: e.status === 401 ? 'token-mismatch' : 'missing', connected: false, error: e.message };
  }
  st.server = activeServer();
  st.connectedServer = st.url ? serverFor(st.url) : null;
  st.settings = settings;
  lastStatus = st;
  send('status', st);
  updateTray();
  return st;
}

async function connect() {
  const s = activeServer();
  if (!s.url) throw new Error('Enter a custom server URL first.');
  await client.connect(s.url, s.ips, settings.mode);
  return refresh();
}

async function disconnect() {
  await client.disconnect();
  return refresh();
}

function onQuery(ev) {
  send('query', ev);
}

function send(channel, payload) {
  if (win && !win.isDestroyed()) win.webContents.send(channel, payload);
}

// ---- UI -------------------------------------------------------------------------

function createWindow({ minimized = false } = {}) {
  win = new BrowserWindow({
    icon: asset(lastStatus.connected ? 'icon.png' : 'icon-off.png'),
    width: 900,
    height: 680,
    minWidth: 640,
    minHeight: 480,
    title: 'DoorPass',
    backgroundColor: '#f3f6f0',
    show: false,
    autoHideMenuBar: true,
    webPreferences: {
      preload: path.join(__dirname, 'preload.js'),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
    },
  });
  win.loadFile(path.join(__dirname, 'renderer', 'index.html'));
  win.once('ready-to-show', () => (minimized ? win.minimize() : win.show()));
  win.webContents.setWindowOpenHandler(({ url }) => {
    if (url.startsWith('https://')) shell.openExternal(url);
    return { action: 'deny' };
  });
  win.webContents.on('will-navigate', (e) => e.preventDefault());
  // Closing minimizes to the Dock / taskbar; Quit is in the tray menu (and
  // Cmd+Q on macOS).
  win.on('close', (e) => {
    if (!quitting) {
      e.preventDefault();
      win.minimize();
    }
  });
}

function showWindow() {
  if (!win || win.isDestroyed()) createWindow();
  if (win.isMinimized()) win.restore();
  win.show();
  win.focus();
}

const asset = (name) => path.join(__dirname, 'assets', name);

function trayImage(on) {
  const name = isMac ? `tray${on ? 'On' : 'Off'}Template.png` : `tray-${on ? 'on' : 'off'}.png`;
  const img = nativeImage.createFromPath(asset(name));
  if (isMac) img.setTemplateImage(true);
  return img;
}

// The doorway icon is filled while protection is on: menu bar / tray, Dock
// and taskbar.
let shownOn = null;
function updateIcons(on) {
  if (on === shownOn) return;
  shownOn = on;
  if (tray) tray.setImage(trayImage(on));
  const appIcon = nativeImage.createFromPath(asset(on ? 'icon.png' : 'icon-off.png'));
  if (isMac) app.dock.setIcon(appIcon);
  else if (win && !win.isDestroyed()) win.setIcon(appIcon);
}

function updateTray() {
  if (!tray) return;
  const st = lastStatus;
  const on = !!st.connected;
  updateIcons(on);
  const label = st.helper !== 'ok' ? 'Helper not installed'
    : on ? `On: ${st.connectedServer ? st.connectedServer.name : st.url}` : 'Off';
  tray.setToolTip(`DoorPass (${label})`);
  const pick = (url) => async () => {
    settings.url = url;
    saveSettings(settings);
    if (on) await connect().catch((e) => send('error', e.message));
    else await refresh();
  };
  tray.setContextMenu(Menu.buildFromTemplate([
    { label, enabled: false },
    {
      label: on ? 'Turn off' : 'Turn on',
      enabled: st.helper === 'ok',
      click: () => (on ? disconnect() : connect()).catch((e) => send('error', e.message)),
    },
    {
      label: 'Server',
      submenu: servers.map((s) => ({
        label: s.name, type: 'radio', checked: settings.url === s.url, click: pick(s.url),
      })),
    },
    { type: 'separator' },
    { label: 'Open DoorPass', click: showWindow },
    { label: 'Quit', click: () => app.quit() },
  ]));
}

// ---- IPC ------------------------------------------------------------------------

function handle(channel, fn) {
  ipcMain.handle(channel, async (_e, ...args) => {
    try {
      return { ok: true, value: await fn(...args) };
    } catch (e) {
      return { ok: false, error: e.message };
    }
  });
}

function registerIpc() {
  handle('get-servers', () => servers);
  handle('get-status', () => refresh());
  handle('connect', () => connect());
  handle('disconnect', () => disconnect());
  handle('install-helper', () => installHelper());
  handle('uninstall-helper', () => uninstallHelper());
  handle('probe', async () => {
    const s = activeServer();
    if (!s.url) throw new Error('Enter a custom server URL first.');
    return client.probe(s.url, s.ips);
  });
  handle('update-settings', async (patch) => {
    const allowed = ['url', 'customUrl', 'mode', 'autoConnect', 'openAtLogin', 'queryLog'];
    for (const k of Object.keys(patch || {})) {
      if (allowed.includes(k)) settings[k] = patch[k];
    }
    if ('openAtLogin' in (patch || {})) {
      app.setLoginItemSettings({ openAtLogin: !!patch.openAtLogin, openAsHidden: true, args: ['--hidden'] });
    }
    saveSettings(settings);
    // Apply a new server or mode right away when already on.
    if (lastStatus.connected && ('url' in patch || 'customUrl' in patch || 'mode' in patch)) {
      return connect();
    }
    return refresh();
  });
  handle('get-login-item', () => app.getLoginItemSettings().openAtLogin);
}

// ---- Lifecycle ------------------------------------------------------------------

if (!app.requestSingleInstanceLock()) {
  app.quit();
} else {
  app.on('second-instance', showWindow);

  app.whenReady().then(async () => {
    nativeTheme.themeSource = 'light'; // Light UI, including native menus, whatever the system theme.
    try { migrateFromIntra(); } catch (_) { /* start fresh */ }
    settings = loadSettings();
    client = new HelperClient(loadToken());
    registerIpc();

    tray = new Tray(trayImage(false));
    tray.on('click', () => { if (!isMac) showWindow(); });

    const st = await refresh();
    if (st.helper === 'ok') {
      client.startEvents(onQuery, () => send('queries-reset'));
      if (settings.autoConnect && !st.connected) await connect().catch(() => {});
    }
    // Opened at login: start minimized in the Dock / taskbar.
    const hidden = process.argv.includes('--hidden') || app.getLoginItemSettings().wasOpenedAsHidden;
    if (hidden && st.helper === 'ok') {
      createWindow({ minimized: true });
    } else {
      showWindow();
    }
    setInterval(() => refresh().catch(() => {}), POLL_MS);
  });

  app.on('activate', showWindow);
  app.on('window-all-closed', () => { /* stay in the tray */ });

  // Quitting turns protection off, so the network never stays pointed at a
  // tunnel nobody can see.
  app.on('before-quit', (e) => {
    if (quitting) return;
    quitting = true;
    if (lastStatus.connected && client) {
      e.preventDefault();
      client.disconnect().catch(() => {}).finally(() => app.quit());
    }
  });
}
