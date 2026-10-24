// The window's API, backed by DoorPass's Go service (doorpass.go) through the
// Wails runtime. renderer.js only talks to window.doorpass.
import { Call, Events } from '/wails/runtime.js';

const call = (method, ...args) => Call.ByName(`main.Service.${method}`, ...args);
const on = (name) => (fn) => Events.On(name, (ev) => fn(ev.data));

window.doorpass = {
  getServers: () => call('GetServers'),
  getStatus: () => call('GetStatus'),
  connect: () => call('Connect'),
  disconnect: () => call('Disconnect'),
  probe: () => call('Probe'),
  installHelper: () => call('InstallHelper'),
  uninstallHelper: () => call('UninstallHelper'),
  updateSettings: (patch) => call('UpdateSettings', patch),
  getLoginItem: () => call('GetLoginItem'),
  openWindow: () => call('OpenWindow'),
  openMenu: (x, y) => call('OpenMenu', x, y),
  onStatus: on('status'),
  onQuery: on('query'),
  onQueriesReset: on('queries-reset'),
  onError: on('error'),
};
