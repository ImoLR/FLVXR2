// Plan 133 r2 — interactive Playwright driver for the LAB UI (one Chromium, many contexts).
// Serves the branch dist on 127.0.0.1:16597 (proxy /api,/flow,/system-info -> lab panel) and
// accepts JSON commands on 127.0.0.1:16598 (POST /cmd), so a tester can click step by step:
//   {"op":"open","ctx":"a","role":"admin","device":"desktop|h5","theme":"light|dark","route":"/node"}
//   {"op":"goto","ctx":"a","route":"/forward"}            {"op":"reload","ctx":"a"}  {"op":"back"|"forward","ctx":"a"}
//   {"op":"click","ctx":"a","text":"新增","exact":true,"nth":0} | {"selector":"css"} | {"role":"button","name":"确定"}
//   {"op":"dblclick", ...same target...}                  {"op":"fill","ctx":"a","selector":"input[...]","value":"x"} | {"label":"名称"} | {"placeholder":"..."}
//   {"op":"press","ctx":"a","key":"Escape"}               {"op":"wait","ms":500}
//   {"op":"shot","ctx":"a","name":"file-stem","full":false} {"op":"text","ctx":"a","selector":"[role=dialog]"}
//   {"op":"eval","ctx":"a","js":"document.title"}          {"op":"reqs","ctx":"a","clear":true}  (API log since last clear)
//   {"op":"throttle","ctx":"a","ms":1500}                  {"op":"close","ctx":"a"}  {"op":"quit"}
// Every non-127.0.0.1 request is aborted. LAB ONLY.
const fs = require('fs');
const path = require('path');
const http = require('http');
const net = require('net');
const {chromium} = require('/root/projects/mmwx-custom-flvx-share/frontend/node_modules/playwright');

const DIST = process.env.DIST || '/root/tmp-work/qa-r2/dist';
const PORT = Number(process.env.UI_PORT || 16597);
const CTL = Number(process.env.CTL_PORT || 16598);
const OUT = process.env.OUT || '/root/flvx-workers/runs/qa-sweep-r2/screens/click';
const BACKEND = {host: process.env.BACKEND_HOST || '203.0.113.2', port: Number(process.env.BACKEND_PORT || 16481)};
const tokens = JSON.parse(fs.readFileSync(process.env.TOKENS || '/root/tmp-work/qa-r2/tokens.json', 'utf8'));
fs.mkdirSync(OUT, {recursive: true});

function serve() {
  const types = {'.js': 'text/javascript', '.css': 'text/css', '.html': 'text/html', '.json': 'application/json', '.svg': 'image/svg+xml', '.png': 'image/png', '.woff2': 'font/woff2', '.webmanifest': 'application/manifest+json'};
  const server = http.createServer((req, res) => {
    const url = new URL(req.url, `http://127.0.0.1:${PORT}`);
    if (url.pathname.startsWith('/api/') || url.pathname.startsWith('/flow/')) {
      const p = http.request({...BACKEND, path: req.url, method: req.method, headers: {...req.headers, host: `${BACKEND.host}:${BACKEND.port}`}}, (r) => { res.writeHead(r.statusCode, r.headers); r.pipe(res); });
      p.on('error', () => { res.writeHead(502); res.end(); });
      req.pipe(p);
      return;
    }
    let file = path.join(DIST, decodeURIComponent(url.pathname));
    if (!file.startsWith(DIST + '/') || !fs.existsSync(file) || !fs.statSync(file).isFile()) file = path.join(DIST, 'index.html');
    res.writeHead(200, {'Content-Type': types[path.extname(file)] || 'application/octet-stream', 'Cache-Control': 'no-store'});
    fs.createReadStream(file).pipe(res);
  });
  server.on('upgrade', (req, sock, head) => {
    const up = net.connect(BACKEND.port, BACKEND.host, () => {
      up.write(`${req.method} ${req.url} HTTP/1.1\r\n` + Object.entries({...req.headers}).map(([k, v]) => `${k}: ${v}`).join('\r\n') + '\r\n\r\n');
      up.write(head); up.pipe(sock); sock.pipe(up);
    });
    up.on('error', () => sock.destroy()); sock.on('error', () => up.destroy());
  });
  server.listen(PORT, '127.0.0.1');
  return server;
}

function payload(token) { return JSON.parse(Buffer.from(token.split('.')[1].replace(/-/g, '+').replace(/_/g, '/'), 'base64').toString()); }

let browser;
const ctxs = {};

async function openCtx(c) {
  if (ctxs[c.ctx]) await ctxs[c.ctx].context.close().catch(() => {});
  const mobile = c.device === 'h5';
  const context = await browser.newContext({
    viewport: mobile ? {width: 412, height: c.height || 915} : {width: c.width || 1440, height: c.height || 1000},
    isMobile: mobile, hasTouch: mobile, deviceScaleFactor: mobile ? 2 : 1, serviceWorkers: 'block', locale: 'zh-CN', timezoneId: 'Asia/Taipei', colorScheme: c.theme || 'light',
    userAgent: mobile ? 'Mozilla/5.0 (Linux; Android 14; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0 Mobile Safari/537.36' : undefined,
  });
  const st = {context, reqs: [], errors: [], throttle: 0, dialogs: []};
  await context.route('**/*', async (r) => {
    const u = new URL(r.request().url());
    if (u.hostname !== '127.0.0.1') return r.abort();
    if (st.throttle && u.pathname.startsWith('/api/')) await new Promise((x) => setTimeout(x, st.throttle));
    return r.continue();
  });
  if (c.role && c.role !== 'anon') {
    const token = tokens[c.role];
    const p = payload(token);
    await context.addCookies([{name: 'flvx_session', value: token, domain: '127.0.0.1', path: '/'}]);
    await context.addInitScript(({token, p, theme}) => {
      if (sessionStorage.getItem('qa-init')) return;
      sessionStorage.setItem('qa-init', '1');
      const kv = {token, user_id: String(p.sub), role_id: String(p.role_id), name: p.user, admin: String(p.role_id === 0), restricted: 'false', 'flvx:theme': theme};
      for (const [k, val] of Object.entries(kv)) localStorage.setItem(k, val);
    }, {token, p, theme: c.theme || 'light'});
  }
  const page = await context.newPage();
  page.setDefaultTimeout(8000);
  page.on('pageerror', (e) => st.errors.push({pageerror: e.message.slice(0, 300)}));
  page.on('console', (m) => { if (m.type() === 'error' && !/WebSocket|Service Worker/.test(m.text())) st.errors.push({console: m.text().slice(0, 300)}); });
  page.on('dialog', async (d) => { st.dialogs.push({type: d.type(), message: d.message()}); await d.dismiss().catch(() => {}); });
  const starts = new Map();
  page.on('request', (r) => starts.set(r, Date.now()));
  page.on('requestfinished', async (r) => {
    const u = new URL(r.url());
    if (!u.pathname.startsWith('/api/')) return;
    let code = null, msg = '', dataLen = 0;
    try { const resp = await r.response(); const j = await resp.json(); code = j.code; msg = (j.msg || '').slice(0, 120); dataLen = j.data == null ? 0 : JSON.stringify(j.data).length; } catch { /* */ }
    st.reqs.push({t: Date.now(), ms: Date.now() - (starts.get(r) || Date.now()), method: r.method(), path: u.pathname, body: (r.postData() || '').slice(0, 400), code, msg, dataLen});
  });
  st.page = page;
  ctxs[c.ctx] = st;
  if (c.route) await page.goto(`http://127.0.0.1:${PORT}${c.route}${mobile ? (c.route.includes('?') ? '&' : '?') + 'h5=true' : ''}`, {waitUntil: 'networkidle'}).catch((e) => st.errors.push({goto: e.message.slice(0, 200)}));
  await page.waitForTimeout(c.settle || 1200);
  return {ok: true};
}

function target(page, c) {
  let loc;
  const scope = c.within ? page.locator(c.within).last() : page;
  if (c.selector) loc = scope.locator(c.selector);
  else if (c.role) loc = scope.getByRole(c.role, {name: c.name, exact: !!c.exact});
  else if (c.label) loc = scope.getByLabel(c.label, {exact: !!c.exact});
  else if (c.placeholder) loc = scope.getByPlaceholder(c.placeholder, {exact: !!c.exact});
  else loc = scope.getByText(c.text, {exact: !!c.exact});
  return c.last ? loc.last() : loc.nth(c.nth || 0);
}

async function run(c) {
  if (c.op === 'open') return openCtx(c);
  if (c.op === 'wait') { await new Promise((r) => setTimeout(r, c.ms || 500)); return {ok: true}; }
  if (c.op === 'quit') { setTimeout(() => process.exit(0), 100); return {ok: true}; }
  const st = ctxs[c.ctx];
  if (!st) return {error: 'no ctx ' + c.ctx};
  const page = st.page;
  switch (c.op) {
    case 'goto': await page.goto(`http://127.0.0.1:${PORT}${c.route}`, {waitUntil: 'networkidle'}).catch(() => {}); await page.waitForTimeout(c.settle || 1000); return {ok: true, url: page.url()};
    case 'reload': await page.reload({waitUntil: 'networkidle'}).catch(() => {}); await page.waitForTimeout(c.settle || 1000); return {ok: true};
    case 'back': await page.goBack({waitUntil: 'networkidle'}).catch(() => {}); await page.waitForTimeout(800); return {ok: true, url: page.url()};
    case 'forward': await page.goForward({waitUntil: 'networkidle'}).catch(() => {}); await page.waitForTimeout(800); return {ok: true, url: page.url()};
    case 'click': await target(page, c).click({timeout: c.timeout || 6000, force: !!c.force}); await page.waitForTimeout(c.after ?? 700); return {ok: true};
    case 'dblclick': await target(page, c).dblclick({timeout: c.timeout || 6000}); await page.waitForTimeout(c.after ?? 700); return {ok: true};
    case 'fill': await target(page, c).fill(String(c.value), {timeout: c.timeout || 6000}); await page.waitForTimeout(c.after ?? 150); return {ok: true};
    case 'type': await target(page, c).pressSequentially(String(c.value), {timeout: c.timeout || 6000}); await page.waitForTimeout(c.after ?? 150); return {ok: true};
    case 'check': await target(page, c).check({timeout: c.timeout || 6000}); return {ok: true};
    case 'press': await page.keyboard.press(c.key); await page.waitForTimeout(c.after ?? 400); return {ok: true};
    case 'count': return {count: await target(page, c).count()};
    case 'shot': {
      const file = path.join(OUT, c.name + '.png');
      await page.waitForTimeout(c.pre ?? 250);
      if (c.selector) await page.locator(c.selector).last().screenshot({path: file, animations: 'disabled'});
      else await page.screenshot({path: file, fullPage: !!c.full, animations: 'disabled'});
      return {ok: true, file};
    }
    case 'text': return {text: (await (c.selector ? page.locator(c.selector).last() : page.locator('body')).innerText({timeout: 4000}).catch((e) => 'ERR ' + e.message.slice(0, 100))).slice(0, c.max || 4000)};
    case 'html': return {html: (await page.locator(c.selector || 'body').last().innerHTML().catch(() => '')).slice(0, c.max || 4000)};
    case 'eval': return {value: await page.evaluate(c.js)};
    case 'reqs': { const out = st.reqs.slice(); if (c.clear) st.reqs = []; return {reqs: c.writes ? out.filter((r) => !/list|get|package|info|check|overview|monitor|status|links|limits|ips|history|stat|access/.test(r.path)) : out}; }
    case 'errors': { const out = {errors: st.errors.slice(), dialogs: st.dialogs.slice()}; if (c.clear) { st.errors = []; st.dialogs = []; } return out; }
    case 'throttle': st.throttle = c.ms || 0; return {ok: true};
    case 'newpage': { // second tab in the same context (same session)
      const p2 = await st.context.newPage();
      await p2.goto(`http://127.0.0.1:${PORT}${c.route}`, {waitUntil: 'networkidle'}).catch(() => {});
      ctxs[c.as] = {...st, page: p2, reqs: st.reqs, errors: st.errors, dialogs: st.dialogs};
      return {ok: true};
    }
    case 'close': await st.context.close().catch(() => {}); delete ctxs[c.ctx]; return {ok: true};
    default: return {error: 'bad op'};
  }
}

(async () => {
  serve();
  browser = await chromium.launch({executablePath: '/root/.cache/ms-playwright/chromium_headless_shell-1243/chrome-headless-shell-linux64/chrome-headless-shell'});
  http.createServer((req, res) => {
    let b = '';
    req.on('data', (d) => (b += d));
    req.on('end', async () => {
      let out;
      try {
        const cmds = JSON.parse(b);
        out = [];
        for (const c of Array.isArray(cmds) ? cmds : [cmds]) {
          try { out.push(await run(c)); } catch (e) { out.push({error: e.message.split('\n')[0].slice(0, 300), op: c.op}); if (!c.soft) break; }
        }
      } catch (e) { out = {error: e.message}; }
      res.writeHead(200, {'Content-Type': 'application/json'});
      res.end(JSON.stringify(out));
    });
  }).listen(CTL, '127.0.0.1');
  console.log('pwdrive ready', CTL);
})();
