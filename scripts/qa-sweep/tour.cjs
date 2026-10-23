// Plan 133 QA sweep — UI tour on the LAB panel (neutralised prod copy).
// Serves the branch dist on 127.0.0.1:16597 and proxies /api + /system-info to the lab
// panel (203.0.113.2:16481). Every non-127.0.0.1 request is aborted.
//   node tour.cjs visits.json OUTDIR      → screenshots + log.json (console errors, slow/failed requests)
//   node tour.cjs --poll visits.json OUTDIR → each visit stays open `hold` s and counts requests per path
const fs = require('fs');
const path = require('path');
const http = require('http');
const net = require('net');
const {chromium} = require('/root/projects/mmwx-custom-flvx-share/frontend/node_modules/playwright');

const poll = process.argv[2] === '--poll';
const args = process.argv.slice(poll ? 3 : 2);
const visits = JSON.parse(fs.readFileSync(args[0], 'utf8'));
const out = args[1];
fs.mkdirSync(out, {recursive: true});
const DIST = process.env.DIST || '/root/tmp-work/qa-sweep/dist';
const PORT = Number(process.env.UI_PORT || 16597);
const BACKEND = {host: process.env.BACKEND_HOST || '203.0.113.2', port: Number(process.env.BACKEND_PORT || 16481)};
const tokens = JSON.parse(fs.readFileSync(process.env.TOKENS || '/root/tmp-work/qa-sweep/tokens.json', 'utf8'));
const THROTTLE = process.env.THROTTLE_MS ? Number(process.env.THROTTLE_MS) : 0;

function serve() {
  const types = {'.js': 'text/javascript', '.css': 'text/css', '.html': 'text/html', '.json': 'application/json', '.svg': 'image/svg+xml', '.png': 'image/png', '.woff2': 'font/woff2', '.webmanifest': 'application/manifest+json'};
  const server = http.createServer((req, res) => {
    const url = new URL(req.url, `http://127.0.0.1:${PORT}`);
    if (url.pathname.startsWith('/api/') || url.pathname.startsWith('/flow/')) {
      const go = () => {
        const p = http.request({...BACKEND, path: req.url, method: req.method, headers: {...req.headers, host: `${BACKEND.host}:${BACKEND.port}`}}, (r) => { res.writeHead(r.statusCode, r.headers); r.pipe(res); });
        p.on('error', () => { res.writeHead(502); res.end(); });
        req.pipe(p);
      };
      if (THROTTLE) setTimeout(go, THROTTLE); else go();
      return;
    }
    let file = path.join(DIST, decodeURIComponent(url.pathname));
    if (!file.startsWith(DIST + '/') || !fs.existsSync(file) || !fs.statSync(file).isFile()) file = path.join(DIST, 'index.html');
    res.writeHead(200, {'Content-Type': types[path.extname(file)] || 'application/octet-stream', 'Cache-Control': 'no-store'});
    fs.createReadStream(file).pipe(res);
  });
  // websocket upgrade passthrough (/system-info)
  server.on('upgrade', (req, sock, head) => {
    const up = net.connect(BACKEND.port, BACKEND.host, () => {
      up.write(`${req.method} ${req.url} HTTP/1.1\r\n` + Object.entries({...req.headers, host: `${BACKEND.host}:${BACKEND.port}`}).map(([k, v]) => `${k}: ${v}`).join('\r\n') + '\r\n\r\n');
      up.write(head); up.pipe(sock); sock.pipe(up);
    });
    up.on('error', () => sock.destroy()); sock.on('error', () => up.destroy());
  });
  server.listen(PORT, '127.0.0.1');
  return server;
}

function payload(token) { return JSON.parse(Buffer.from(token.split('.')[1].replace(/-/g, '+').replace(/_/g, '/'), 'base64').toString()); }

async function openPage(browser, v, log) {
  const mobile = v.device === 'h5';
  const ctx = await browser.newContext({
    viewport: mobile ? {width: 412, height: v.height || 915} : {width: 1440, height: v.height || 1000},
    isMobile: mobile, hasTouch: mobile, deviceScaleFactor: mobile ? 2 : 1, serviceWorkers: 'block', locale: 'zh-CN', timezoneId: 'Asia/Taipei', colorScheme: v.theme || 'light',
    userAgent: mobile ? 'Mozilla/5.0 (Linux; Android 14; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0 Mobile Safari/537.36' : undefined,
  });
  await ctx.route('**/*', (r) => (new URL(r.request().url()).hostname === '127.0.0.1' ? r.continue() : r.abort()));
  if (v.role && v.role !== 'anon') {
    const token = tokens[v.role];
    const p = payload(token);
    await ctx.addCookies([{name: 'flvx_session', value: token, domain: '127.0.0.1', path: '/'}]);
    await ctx.addInitScript(({token, p, theme}) => {
      const kv = {token, user_id: String(p.sub), role_id: String(p.role_id), name: p.user, admin: String(p.role_id === 0), restricted: 'false', 'flvx:theme': theme};
      for (const [k, val] of Object.entries(kv)) localStorage.setItem(k, val);
    }, {token, p, theme: v.theme || 'light'});
  }
  const page = await ctx.newPage();
  page.setDefaultTimeout(15000);
  const tag = `${v.name}`;
  page.on('pageerror', (e) => log.errors.push({visit: tag, pageerror: e.message.slice(0, 300)}));
  page.on('console', (m) => { if (['error', 'warning'].includes(m.type()) && !/Download the React DevTools|WebSocket connection/.test(m.text())) log.errors.push({visit: tag, console: m.type(), text: m.text().slice(0, 300)}); });
  const starts = new Map();
  page.on('request', (r) => starts.set(r, Date.now()));
  page.on('requestfinished', async (r) => {
    const u = new URL(r.url());
    if (!u.pathname.startsWith('/api/')) return;
    const ms = Date.now() - (starts.get(r) || Date.now());
    let code = null;
    try { const resp = await r.response(); const j = await resp.json(); code = j.code; if (code !== 0) log.apiErrors.push({visit: tag, path: u.pathname, code, msg: (j.msg || '').slice(0, 120)}); } catch { /* not json */ }
    log.requests.push({visit: tag, method: r.method(), path: u.pathname, ms, code, t: Date.now()});
  });
  page.on('requestfailed', (r) => { const u = new URL(r.url()); if (u.hostname === '127.0.0.1') log.failed.push({visit: tag, path: u.pathname, err: r.failure()?.errorText}); });
  const sep = v.route.includes('?') ? '&' : '?';
  await page.goto(`http://127.0.0.1:${PORT}` + v.route + (mobile ? `${sep}h5=true` : ''), {waitUntil: 'networkidle'}).catch((e) => log.errors.push({visit: tag, goto: e.message.slice(0, 200)}));
  await page.waitForTimeout(v.settle || 1500);
  return {ctx, page};
}

async function act(page, step) {
  // step: {click: text|selector, role?, nth?} | {tab: name} | {fill: selector, value} | {wait: ms} | {shot: name, full?} | {press: key} | {eval: js}
  if (step.wait) return page.waitForTimeout(step.wait);
  if (step.tab) { await page.getByRole('tab', {name: step.tab, exact: true}).first().click(); return page.waitForTimeout(900); }
  if (step.clickText) { await page.getByText(step.clickText, {exact: !!step.exact}).nth(step.nth || 0).click({timeout: 8000}); return page.waitForTimeout(step.after || 800); }
  if (step.clickRole) { await page.getByRole(step.clickRole, {name: step.name, exact: !!step.exact}).nth(step.nth || 0).click({timeout: 8000}); return page.waitForTimeout(step.after || 800); }
  if (step.click) { await page.locator(step.click).nth(step.nth || 0).click({timeout: 8000}); return page.waitForTimeout(step.after || 800); }
  if (step.fill) { await page.locator(step.fill).nth(step.nth || 0).fill(step.value); return page.waitForTimeout(200); }
  if (step.press) { await page.keyboard.press(step.press); return page.waitForTimeout(500); }
  if (step.eval) return page.evaluate(step.eval);
  return null;
}

(async () => {
  const server = serve();
  const browser = await chromium.launch({executablePath: '/root/.cache/ms-playwright/chromium_headless_shell-1243/chrome-headless-shell-linux64/chrome-headless-shell'});
  const log = {errors: [], apiErrors: [], requests: [], failed: [], shots: [], texts: {}, stepErrors: []};
  for (const v of visits) {
    const t0 = Date.now();
    const {ctx, page} = await openPage(browser, v, log);
    try {
      for (const step of v.steps || []) {
        try {
          if (step.shot) {
            await page.waitForTimeout(300);
            const file = `${step.shot}.png`;
            await page.screenshot({path: path.join(out, file), fullPage: step.full !== false, animations: 'disabled'});
            log.shots.push({visit: v.name, file, title: step.title || step.shot});
          } else if (step.text) {
            log.texts[step.text] = (await page.locator(step.selector || 'body').first().innerText().catch(() => '')).slice(0, step.max || 6000);
          } else await act(page, step);
        } catch (e) { log.stepErrors.push({visit: v.name, step, err: e.message.split('\n')[0].slice(0, 200)}); }
      }
      if (poll) {
        const before = log.requests.length;
        const hold = (v.hold || 120) * 1000;
        const start = Date.now();
        await page.waitForTimeout(hold);
        const reqs = log.requests.slice(before).filter((r) => r.visit === v.name && r.t >= start);
        const per = {};
        for (const r of reqs) per[r.path] = (per[r.path] || 0) + 1;
        log.poll = log.poll || [];
        log.poll.push({visit: v.name, seconds: hold / 1000, total: reqs.length, perMinute: +(reqs.length / (hold / 60000)).toFixed(1), paths: per});
        console.log('poll', v.name, reqs.length, JSON.stringify(per));
      }
    } finally {
      await ctx.close();
    }
    console.log('visit', v.name, ((Date.now() - t0) / 1000).toFixed(1) + 's');
  }
  await browser.close();
  server.close();
  const slow = log.requests.filter((r) => r.ms > 1000);
  fs.writeFileSync(path.join(out, poll ? 'poll-log.json' : 'tour-log.json'), JSON.stringify({...log, slow, requestCount: log.requests.length, requests: undefined}, null, 1));
  console.log('errors', log.errors.length, 'apiErrors', log.apiErrors.length, 'slow', slow.length, 'failed', log.failed.length, 'stepErrors', log.stepErrors.length);
  process.exit(0);
})();
