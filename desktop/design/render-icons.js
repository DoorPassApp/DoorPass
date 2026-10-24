// Renders DoorPass's icons from the SVGs below into desktop/app/assets.
// Run from desktop/app: ./node_modules/.bin/electron ../design/render-icons.js
//
// The glyph is a door: closed (outline with a knob) when off, swung open with
// the doorway filled with light when on.

const { app, BrowserWindow } = require('electron');
const fs = require('fs');
const path = require('path');

const OUT = path.join(__dirname, '..', 'app', 'assets');

// 24-unit glyphs.
const GROUND = 'M2 21h20';
const CLOSED_FRAME = 'M5.5 21V4.5A1.5 1.5 0 0 1 7 3h10A1.5 1.5 0 0 1 18.5 4.5V21';
const OPEN_FRAME = 'M9 21V4.5A1.5 1.5 0 0 1 10.5 3h8.5A1.5 1.5 0 0 1 20.5 4.5V21';
const OPEN_LIGHT = 'M10.1 21V4.1h9.3V21Z';
const OPEN_PANEL = 'M9 3.2 3.4 5.4v14.2l5.6 1.4';

function glyph(on, { stroke, light, panel, glow }) {
  const s = `fill="none" stroke="${stroke}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"`;
  if (!on) {
    return `<path d="${CLOSED_FRAME}" ${s}/><circle cx="15.3" cy="12.4" r="1.2" fill="${stroke}"/><path d="${GROUND}" ${s}/>`;
  }
  return [
    glow ? `<path d="${OPEN_LIGHT}" fill="${glow}" filter="url(#glow)"/>` : '',
    `<path d="${OPEN_LIGHT}" fill="${light}"/>`,
    panel ? `<path d="${OPEN_PANEL}Z" fill="${panel}"/>` : '',
    `<path d="${OPEN_PANEL}" ${s}/>`,
    `<circle cx="5.9" cy="12.4" r="1.05" fill="${stroke}"/>`,
    `<path d="${OPEN_FRAME}" ${s}/>`,
    `<path d="${GROUND}" ${s}/>`,
  ].join('');
}

// Menu bar (template: black + alpha) and tray (coloured) glyph icons.
function smallIcon(on, color, light = color) {
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24">${glyph(on, { stroke: color, light })}</svg>`;
}

// App icon on the macOS 824/1024 grid, in the forest palette
// (#0F2A1D #375534 #6B9071 #AEC3B0 #E3EED4) with a warm gold door light
// (#FFF6D8 -> #F2C45A).
function appIcon(on) {
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 1024">
  <defs>
    <linearGradient id="bg" x1="0" y1="0" x2="0" y2="1">
      <stop offset="0" stop-color="#375534"/>
      <stop offset="1" stop-color="#0f2a1d"/>
    </linearGradient>
    <linearGradient id="light" x1="0" y1="0" x2="0" y2="1">
      <stop offset="0" stop-color="#fff6d8"/>
      <stop offset=".55" stop-color="#f8dc8c"/>
      <stop offset="1" stop-color="#f2c45a"/>
    </linearGradient>
    <filter id="glow" x="-50%" y="-50%" width="200%" height="200%">
      <feGaussianBlur stdDeviation="1.6"/>
    </filter>
  </defs>
  <rect x="100" y="100" width="824" height="824" rx="184" fill="url(#bg)"/>
  <rect x="100.5" y="100.5" width="823" height="823" rx="183.5" fill="none" stroke="#ffffff" stroke-opacity=".08"/>
  <g transform="translate(152 152) scale(30)">
    ${on ? glyph(true, { stroke: '#aec3b0', light: 'url(#light)', panel: '#1d3a28', glow: '#f2c45a' }) : glyph(false, { stroke: '#6b9071' })}
  </g>
</svg>`;
}

const jobs = [
  ['icon.png', appIcon(true), 1024],
  ['icon-off.png', appIcon(false), 1024],
  ['trayOnTemplate.png', smallIcon(true, '#000'), 18],
  ['trayOnTemplate@2x.png', smallIcon(true, '#000'), 36],
  ['trayOffTemplate.png', smallIcon(false, '#000'), 18],
  ['trayOffTemplate@2x.png', smallIcon(false, '#000'), 36],
  ['tray-on.png', smallIcon(true, '#aec3b0', '#f2c45a'), 16],
  ['tray-on@2x.png', smallIcon(true, '#aec3b0', '#f2c45a'), 32],
  ['tray-off.png', smallIcon(false, '#9aa0a6'), 16],
  ['tray-off@2x.png', smallIcon(false, '#9aa0a6'), 32],
];

async function render(svg, size) {
  const win = new BrowserWindow({
    width: size, height: size, show: false, frame: false, transparent: true,
    webPreferences: { offscreen: true },
  });
  win.webContents.setFrameRate(1);
  const html = `<html><body style="margin:0;background:transparent">
    <img src="data:image/svg+xml;base64,${Buffer.from(svg).toString('base64')}" width="${size}" height="${size}" style="display:block"></body></html>`;
  const painted = new Promise((resolve) => win.webContents.on('paint', (_e, _d, img) => resolve(img)));
  await win.loadURL(`data:text/html;base64,${Buffer.from(html).toString('base64')}`);
  const img = await painted;
  win.destroy();
  return img.resize({ width: size, height: size, quality: 'best' });
}

app.on('window-all-closed', () => { /* keep rendering */ });

app.whenReady().then(async () => {
  for (const [name, svg, size] of jobs) {
    const img = await render(svg, size);
    fs.writeFileSync(path.join(OUT, name), img.toPNG());
    console.log(name, img.getSize());
  }
  fs.writeFileSync(path.join(__dirname, 'icon.svg'), appIcon(true));
  fs.writeFileSync(path.join(__dirname, 'icon-off.svg'), appIcon(false));
  app.exit(0);
});
