// Plan 133 r2 — READ-ONLY look at production pages.
// Every request that is not GET and not in the proven read-only POST allowlist
// (findings.md 「第二轮」) is aborted before it leaves the browser; aborted calls are logged.
//   node prod_tour.cjs TOKENS_JSON OUTDIR
const fs = require('fs');
const path = require('path');
const {chromium} = require('/root/projects/mmwx-custom-flvx-share/frontend/node_modules/playwright');
const BASE = 'https://flvxx.imgamer.top';
const ALLOW = new Set(['/api/v1/user/package', '/api/v1/forward/list', '/api/v1/tunnel/list', '/api/v1/tunnel/user/tunnel', '/api/v1/landing/node/list',
  '/api/v1/landing/server/list', '/api/v1/landing/monitor', '/api/v1/monitor/nodes', '/api/v1/monitor/tunnels', '/api/v1/config/list', '/api/v1/user/list']);
const tokens = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'));
const out = process.argv[3];
fs.mkdirSync(out, {recursive: true});
const visits = [
  ['admin', '/dashboard'], ['admin', '/forward'], ['admin', '/landing'], ['admin', '/monitor'], ['admin', '/user'],
  ['u7', '/dashboard'], ['u7', '/forward'], ['u7', '/monitor'], ['u26', '/landing'], ['u26', '/dashboard'], ['u23', '/dashboard'],
];
function payload(t) { return JSON.parse(Buffer.from(t.split('.')[1].replace(/-/g, '+').replace(/_/g, '/'), 'base64').toString()); }
(async () => {
  const browser = await chromium.launch({executablePath: '/root/.cache/ms-playwright/chromium_headless_shell-1243/chrome-headless-shell-linux64/chrome-headless-shell'});
  const log = {blocked: [], allowedPost: [], errors: [], apiErrors: [], slow: [], shots: []};
  for (const [role, route] of visits) {
    const ctx = await browser.newContext({viewport: {width: 1440, height: 1000}, serviceWorkers: 'block', locale: 'zh-CN', timezoneId: 'Asia/Taipei'});
    const tag = `${role}${route}`;
    await ctx.route('**/*', (r) => {
      const req = r.request();
      const u = new URL(req.url());
      if (u.origin !== BASE) return r.abort();
      if (req.method() === 'GET' || req.method() === 'HEAD') return r.continue();
      if (req.method() === 'POST' && ALLOW.has(u.pathname)) { log.allowedPost.push({tag, path: u.pathname}); return r.continue(); }
      log.blocked.push({tag, method: req.method(), path: u.pathname});
      return r.abort();
    });
    const token = tokens[role];
    const p = payload(token);
    await ctx.addCookies([{name: 'flvx_session', value: token, domain: 'flvxx.imgamer.top', path: '/', secure: true}]);
    await ctx.addInitScript(({token, p}) => {
      const kv = {token, user_id: String(p.sub), role_id: String(p.role_id), name: p.user, admin: String(p.role_id === 0), restricted: 'false'};
      for (const [k, v] of Object.entries(kv)) localStorage.setItem(k, v);
    }, {token, p});
    const page = await ctx.newPage();
    const starts = new Map();
    page.on('request', (r) => starts.set(r, Date.now()));
    page.on('requestfinished', async (r) => {
      const u = new URL(r.url());
      if (!u.pathname.startsWith('/api/')) return;
      const ms = Date.now() - (starts.get(r) || Date.now());
      if (ms > 1000) log.slow.push({tag, path: u.pathname, ms});
      try { const j = await (await r.response()).json(); if (j.code !== 0) log.apiErrors.push({tag, path: u.pathname, code: j.code, msg: (j.msg || '').slice(0, 80)}); } catch { /* */ }
    });
    page.on('pageerror', (e) => log.errors.push({tag, pageerror: e.message.slice(0, 200)}));
    page.on('console', (m) => { if (m.type() === 'error' && !/WebSocket|Service Worker|net::ERR_FAILED/.test(m.text())) log.errors.push({tag, console: m.text().slice(0, 200)}); });
    await page.goto(BASE + route, {waitUntil: 'networkidle', timeout: 30000}).catch((e) => log.errors.push({tag, goto: e.message.slice(0, 120)}));
    await page.waitForTimeout(2500);
    const file = `prod-${role}${route.replace(/\//g, '-')}.png`;
    await page.screenshot({path: path.join(out, file), fullPage: false});
    log.shots.push({tag, file, text: (await page.locator('main').innerText().catch(() => '')).slice(0, 400)});
    await ctx.close();
    console.log('visit', tag);
  }
  await browser.close();
  fs.writeFileSync(path.join(out, 'prod-tour-log.json'), JSON.stringify(log, null, 1));
  const uniq = (a) => [...new Set(a.map((x) => `${x.method || 'POST'} ${x.path}`))];
  console.log('blocked', uniq(log.blocked).join(', '));
  console.log('allowedPost', log.allowedPost.length, 'errors', log.errors.length, 'apiErrors', log.apiErrors.length, 'slow', log.slow.length);
})();
