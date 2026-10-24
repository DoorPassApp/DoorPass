const { contextBridge, ipcRenderer } = require('electron');

const call = async (channel, ...args) => {
  const res = await ipcRenderer.invoke(channel, ...args);
  if (!res.ok) throw new Error(res.error);
  return res.value;
};

const on = (channel) => (fn) => ipcRenderer.on(channel, (_e, payload) => fn(payload));

contextBridge.exposeInMainWorld('doorpass', {
  platform: process.platform,
  getServers: () => call('get-servers'),
  getStatus: () => call('get-status'),
  connect: () => call('connect'),
  disconnect: () => call('disconnect'),
  probe: () => call('probe'),
  installHelper: () => call('install-helper'),
  uninstallHelper: () => call('uninstall-helper'),
  updateSettings: (patch) => call('update-settings', patch),
  getLoginItem: () => call('get-login-item'),
  onStatus: on('status'),
  onQuery: on('query'),
  onQueriesReset: on('queries-reset'),
  onError: on('error'),
});
