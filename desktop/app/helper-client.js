// Client for doorpass-helper's local API (see desktop/helper/api.go).

const http = require('http');

const HOST = '127.0.0.1';
const PORT = 47121;

class HelperClient {
  constructor(token) {
    this.token = token;
    this.events = null;
  }

  request(method, path, body, timeoutMs = 15000) {
    return new Promise((resolve, reject) => {
      const data = body ? JSON.stringify(body) : null;
      const req = http.request({
        host: HOST,
        port: PORT,
        method,
        path,
        timeout: timeoutMs,
        headers: {
          Authorization: `Bearer ${this.token}`,
          ...(data ? { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(data) } : {}),
        },
      }, (res) => {
        let text = '';
        res.setEncoding('utf8');
        res.on('data', (c) => { text += c; });
        res.on('end', () => {
          let json = null;
          try { json = JSON.parse(text); } catch (_) { /* plain-text error */ }
          if (res.statusCode >= 200 && res.statusCode < 300) {
            resolve(json);
          } else {
            const err = new Error((json && json.error) || text.trim() || `HTTP ${res.statusCode}`);
            err.status = res.statusCode;
            reject(err);
          }
        });
      });
      req.on('timeout', () => req.destroy(new Error('helper timed out')));
      req.on('error', reject);
      if (data) req.write(data);
      req.end();
    });
  }

  status() { return this.request('GET', '/status', null, 3000); }
  connect(url, ips, mode) { return this.request('POST', '/connect', { url, ips, mode }); }
  disconnect() { return this.request('POST', '/disconnect'); }
  probe(url, ips) { return this.request('POST', '/probe', { url, ips }); }
  setQueryLog(enabled) { return this.request('POST', '/querylog', { enabled }); }

  // Streams query events to onEvent, reconnecting until stopEvents(). The
  // helper replays its recent events on every connect, so onOpen is the cue
  // to clear what was shown before.
  startEvents(onEvent, onOpen) {
    this.stopEvents();
    const state = { stopped: false, req: null, timer: null };
    this.events = state;
    const open = () => {
      if (state.stopped) return;
      state.req = http.get({
        host: HOST,
        port: PORT,
        path: '/events',
        headers: { Authorization: `Bearer ${this.token}` },
      }, (res) => {
        if (res.statusCode !== 200) {
          res.resume();
          retry();
          return;
        }
        if (onOpen) onOpen();
        let buf = '';
        res.setEncoding('utf8');
        res.on('data', (chunk) => {
          buf += chunk;
          let i;
          while ((i = buf.indexOf('\n\n')) >= 0) {
            const frame = buf.slice(0, i);
            buf = buf.slice(i + 2);
            const line = frame.split('\n').find((l) => l.startsWith('data: '));
            if (!line) continue;
            try { onEvent(JSON.parse(line.slice(6))); } catch (_) { /* skip bad frame */ }
          }
        });
        res.on('end', retry);
      });
      state.req.on('error', retry);
    };
    const retry = () => {
      if (state.stopped || state.timer) return;
      state.timer = setTimeout(() => { state.timer = null; open(); }, 3000);
    };
    open();
  }

  stopEvents() {
    if (!this.events) return;
    this.events.stopped = true;
    clearTimeout(this.events.timer);
    if (this.events.req) this.events.req.destroy();
    this.events = null;
  }
}

module.exports = { HelperClient };
