# im.gamer App ↔ panel bridge (v1)

Same file in both repos: FLVX `vite-frontend/docs/panel-app-bridge.md` and leikwan-local-bridge
`docs/panel-app-bridge.md`. Change both together. FLVX plan 294 (v0, 2026-11-22), plan 297 (round 2, 2026-11-23),
plan 301 (1.0.10-panel, 2026-11-24: 手机本地 = the VPS stack — see the last section).

## What it is

The im.gamer Android App (flavor `panel`, applicationId `com.localbridge.leikwan.panel`, 1.0.10-panel / versionCode 4)
is a WebView of the owner's own FLVX panel plus the phone's own LeiKwan client (the shared connect engine of 重仓利群:
BridgeService, leases, warm pool, run log). The panel page drives the phone's local link through this bridge.

**Plan 297 rule: on 利群汇聚 the App and the browser behave the same (fork.162 is the standard). The only difference:
the App has one extra entry 「手机本地」** (admin only this round; normal users see the web page as is).

- **App vs browser** is decided only by the injected object `window.panelApp` — never by the User-Agent.
  Browsers (desktop or phone) never have it, so they never show 手机本地 (not in 入口机, not in 换主入口) or the App nav.
- In App mode the panel nav shows only 利群汇聚; login, 个人中心 and the 订阅 editor (from 生成订阅 「详细设置」) open too.
- The local link never depends on the panel: everything the core needs (客户端 ID, Agent Token, vendor leases, run
  log, the entry choice) lives on the phone. If the panel cannot be reached, the App shows a small native fallback page
  with the local status and 断开 / 重连.
- In App mode the panel registers **no service worker** and unregisters an old one (plan 299, `admin-main.tsx`): an
  offline load must fail so the fallback page shows (a cached shell would load with every API call failing).
- While the App is in the background the WebView is paused (`onPause` + `pauseTimers`), so the page does not keep
  polling the panel; it resumes when the App comes to the front (plan 299, 1.0.9-panel).

## Transport and security

- The App calls `WebViewCompat.addWebMessageListener(webView, "panelApp", rules, listener)` (androidx.webkit,
  feature `WEB_MESSAGE_LISTENER`). `rules` = exactly one origin: the saved panel origin (`https://host[:port]`).
  The object is injected only into documents of that origin; frames are never served (`isMainFrame` required).
- Every message is re-checked in the listener: `sourceOrigin` must equal the saved origin and `isMainFrame` must be
  true; anything else is dropped and run-logged (`DEST_REFUSED`, site `panel_bridge`). No `addJavascriptInterface`.
- If the WebView lacks `WEB_MESSAGE_LISTENER` (very old system WebView) the App shows the panel without the bridge
  (= browser behaviour) and a native notice 「系统 WebView 太旧，手机本地不可用」. There is no fallback interface.
- The panel origin is entered by the user on the first-run page (https only, no path, no userinfo). It is the only
  new destination: `Destinations.panel(url)` allows exactly that scheme/host/port. The WebView refuses every other
  network request it sees (`shouldInterceptRequest` → empty 403) and opens other-origin links in the system browser
  (service-worker fetches and WebSockets bypass `shouldInterceptRequest`; the page registers no service worker in App mode).
- Credentials never cross the bridge: the page can only ask the App to open its native credential dialog.

## Wire format

JSON strings both ways. `v` = protocol version (1). Unknown `v` → ignored.

```
page → App   {"v":1,"id":7,"method":"connect","params":{}}
App → page   {"v":1,"id":7,"ok":true,"result":{…}}
App → page   {"v":1,"id":7,"ok":false,"error":{"code":"credentials","message":"先填商家的客户端 ID 和 Agent Token"}}
App → page   {"v":1,"event":"state","data":PhoneState}            (push, no id)
```

The page sends with `window.panelApp.postMessage(json)` and listens with
`window.panelApp.addEventListener("message", e => e.data)` (or `onmessage`). Replies go to the frame that asked
(`replyProxy`); `state` events go to the last frame that called `getState`.

## Methods

| method | params | result | notes |
|---|---|---|---|
| `getInfo` | – | `AppInfo` | app/core versions, device name, origin, reserved calls |
| `getState` | – | `PhoneState` | also subscribes this page to `state` events |
| `getEntry` | – | `{entry}` | plan 297: this phone's entry, `"local"` (手机本地) or `"server"` (default) |
| `setEntry` | `{entry: "local" \| "server"}` | `PhoneState` | plan 297: picked in 授权 → 换主入口; see below. Anything else → `bad_request` |
| `connect` | – | `PhoneState` | manual 「连接」: no credentials → `credentials` error; **allowed with the battery not 无限制** (plan 297) |
| `disconnect` | – | `PhoneState` | = 停止 (all local ports close) |
| `setAutoConnect` | `{on: bool}` | `PhoneState` | 「打开 App 自动连接」, per phone, **default off** (plan 297) |
| `setWarm` | `{on: bool}` | `PhoneState` | 预热池 (same switch as 重仓利群's) |
| `getLog` | `{maxChars?: int}` | `{text, bytes, truncated}` | tail of the run log (≤ 64 k chars by default) |
| `copyLog` | – | `{copied: bool}` | whole run log (≤ 200 k chars) to the clipboard, natively |
| `shareLog` | – | `{shared: bool}` | Android share sheet (text) |
| `openBatterySettings` | – | `{opened: bool}` | the 无限制 request dialog / app settings |
| `editCredentials` | – | `{saved: bool}` | native dialog: 客户端 ID + Agent Token (stored on the phone only) |
| `changePanel` | – | `{opened: true}` | back to the native first-run page (change the panel address) |
| `speedTest` | reserved | error `not_implemented` | 本机测速 hook, not built yet |

Error codes: `credentials` (没填客户端 ID / Agent Token), `busy`, `not_implemented`, `bad_request`, `failed` (message
says why). (`battery` is no longer returned: the battery gate applies to the automatic connect only.)

### Entry choice and connecting (plan 297)

- The entry is **phone-only**: stored in the App (`panel_app` prefs, key `entry`), never written to the server grant.
  The server 主入口 keeps serving the user's other devices; the web page always shows the server 主入口.
- `setEntry("local")`: saves it; if 「打开 App 自动连接」 is on, connects at once under the auto-connect rules (account
  saved, battery 无限制 — otherwise `autoConnectBlocked` says why). If it is off, the page shows 「连接」.
- `setEntry("server")`: saves it and disconnects 手机本地 (it is no longer the entry).
- App start (and coming back to the App while the last auto-connect was blocked): auto-connect runs only when the entry
  is `local`, auto-connect is on, the account is saved and the battery is 无限制.
- Manual `connect` never waits for the battery: the connect panel's battery block turns red instead.

## Types

```ts
interface PhoneState {
  connection: "idle" | "connecting" | "connected" | "partial" | "failed" | "disconnecting";
  running: boolean;            // the service runs (断开 offered; also while it retries after a failure)
  stage?: string;              // 读取账号 / 连接商家 / 等待商家授权 / 启动入口
  ready: number; total: number;
  error?: string;              // Chinese: 「连接失败 · 原因」
  errorCode?: string;
  startedAt?: number; updatedAt: number;
  forwards: {
    forwardId: number; name?: string; route?: string;
    localPort: number;         // 127.0.0.1:<vendor port>, no offset
    state: "ready" | "connecting" | "failed" | "off";
    reason?: string;
    warm?: boolean | null;     // null = 预热 not supported for this forward
    udp?: boolean;
  }[];
  entry: "local" | "server";   // plan 297: this phone's entry (default server)
  autoConnect: boolean;        // default false (plan 297)
  autoConnectBlocked?: string; // why it did not run (电池 / 账号)
  warm: { on: boolean; supported: boolean };
  battery: { unrestricted: boolean };
  credentials: { set: boolean; clientIdHint?: string };   // hint = last 3 digits
  logBytes: number;            // ≤ 2 MiB
}

interface AppInfo {
  app: { name: string; version: string; versionCode: number; flavor: "panel" };
  core: { version: string; agentVersion?: string };       // agentVersion = what we report to the vendor
  device: { name: string };
  bridge: 1;
  origin: string;
  reserved?: string[];         // ["speedTest"]
}
```

## What the panel does with it (plan 297)

- **入口机**: exactly the web (fork.162) — the list, 「＋」 / tap = the details drawer, no selecting there. The App adds
  one row 「手机本地」 first; its drawer lists its modules in the same capability schema as a server entry (预热池 on,
  no 首包过滤 / 日志快切 / 快切触发 / 手机备线) plus the phone parts: per-专线 local port + state + reason, run log
  查看 / 复制 / 分享, App + core versions, 商家在线设备 x/y (from the panel's vendor usage view), credentials dialog,
  「打开 App 自动连接」.
- **Switching entry only in 授权 → 换主入口** (admin's own grant): the App's options gain 「手机本地」 with the
  per-forward preview (`POST leikwan/grant/preview {userId, local: true, phone: {warm, app}}`). Picking it =
  `setEntry("local")`, no server write. Picking a server entry = `setEntry("server")` + the normal fork.162 flow when
  it differs from the server 主入口 (if it equals it: no server write).
- While 手机本地 is the entry: the 授权 card shows 主入口 手机本地 (+ 「其他设备 <server 主入口>」) and the connect
  panel (state, 连接 / 断开, 「打开 App 自动连接」, the three tips 电池设为「无限制」 / 节点地址改成 127.0.0.1 /
  分应用代理里绕过, short notes). Own forward rows follow the phone: address `127.0.0.1:<vendor port>`, chips from the
  phone's capability (`leikwan-rule/list {phone}` → `phoneLimits`), grey 「手机本地未连接」 while not connected;
  复制链接 = `leikwan-rule/link {id, local: true}`.
- **Subscriptions** follow the entry they were made / updated under (`user_subscription.entry_mode` main | local):
  生成订阅 in the App while the entry is 手机本地 offers 「新建手机本地订阅」 / 「把这条改成手机本地」
  (`leikwan-rule/sub {entry: "local", action: "create" | "convert"}`); a 手机本地 sub's 利群 nodes are
  `127.0.0.1:<vendor port>` and it carries the 「手机本地」 badge (derived from entry_mode, never the name; client title
  「<name> · 手机本地」) in every list. 「改成跟主入口」 (App or browser, also the 订阅 editor's 「利群入口」 row) drops
  the badge and the nodes follow the server 主入口 again. No `?lk=` URL parameter any more.
- **Admin is not held by 「只开主入口」**: an admin's forwards are open on every 利群入口 (only vendor client limits
  apply). Normal users: unchanged.

## Vendor device slot

The vendor identity (AndroidKeyStore EC key + no_backup files) is per app installation and per applicationId, so the
im.gamer App is **one more vendor device** on the account, separate from 重仓利群 / 利群原版 on the same phone. The
drawer shows 商家在线设备 x/y and warns when the account is full. The three apps listen on the same vendor ports:
run only one of them at a time.

## Plan 301 (1.0.10-panel): 手机本地 runs what the VPS entries run

Until 1.0.9-panel the App's data plane was 重仓利群's (预热池 only). From 1.0.10-panel it is 利群原版's protection
engine (日志快切 v12 = the Baidu port, LogSwitchRelay; shared source `app/src/protect`) driven by a **phone plan** the
panel hands out: per forward 日志快切 on/off, the 备线 address, pool sizes and the return check, plus the global
trigger modes and the v12 pool rules — the same values the panel writes into 百度云广州's files. Still one bridge
version (`v: 1`); new calls are announced in `AppInfo.features` (1.0.9-panel has none → the panel shows the old caps).

```ts
interface AppInfo { …; features?: string[] }   // 1.0.10-panel: ["warm","logswitch","triggers","backup","plan","diag","tcpPing"]

interface PhonePlan {
  v: 1;
  ref: string;                 // the 利群入口 whose values these are, e.g. "百度云广州" (shown on the phone)
  updatedAt: number;           // ms; the App keeps the newest it was given
  global: {
    triggers: Record<string, "switch" | "record" | "off">;   // LogSwitch.TRIGGERS keys; missing key = App default
    holdS?: number; leaseSettleS?: number; returnCheckS?: number;
    warm: { standby: number; paceMs: number; bytes4: boolean; zeroFin: boolean; burst: false };
    coalesce: boolean;
  };
  forwards: {
    forwardId: number;
    port: number;              // the vendor port (= local port on 127.0.0.1)
    logswitch: boolean;        // 日志快切 to `backup`
    backup: string;            // "ip:port" of the 备线 forward's entry (IP literal only; "" = none)
    warm: number;              // main pool (0 = no 预热)
    backupWarm: number;        // backup pool (switched); idle standby = global.warm.standby
    returnCheck: boolean;
  }[];
  entries: { name: string; host: string }[];   // the 利群入口 IPs (tcpPing 本机 → 入口 only)
}
```

| method | params | result | notes |
|---|---|---|---|
| `setPlan` | `PhonePlan` | `{applied: bool, unchanged: bool, forwards: int, ref, refusedBackups: int}` | stored on the phone (prefs `panel_plan`), applied at once (`settingsChanged`: no reconnect, fronts swap). **Same content as the stored plan (`updatedAt` aside) → `{applied: true, unchanged: true}`, nothing stored, nothing re-applied** (the panel sends it on every page load). Different content with an older `updatedAt` → `applied: false`. Invalid → `bad_request` |
| `diag` | `{forwardId}` | `PhoneDiag` | 手机本地 诊断: ≤ 3 TCP handshakes (1.5 s each) to the relay ingress the core uses (else the fastest current lease entry) and to the 备线; 全程 from the core's status (latest_tcp / active_udp, like the VPS status file). No addresses in the reply. Off the UI thread, reply ≤ 10 s; one at a time (+ 4 waiting, more → `busy`) |
| `tcpPing` | `{host, port, count?≤5, timeoutMs?≤3000}` | `{ok, avgMs, loss, count}` | 本机 → 入口 for a server entry: only `host` ∈ plan.entries and `port` ∈ plan.forwards[].port; anything else (or no plan yet) → `bad_request` (fixed destinations rule). `count` clamped 1–5 (default 3), `timeoutMs` 200–3000 (default 1500) |

```ts
interface PhoneDiag {
  forwardId: number; found: boolean; state?: string;
  ingress?: { ok: boolean; avgMs: number; loss: number; inUse: boolean };     // 手机 → 专线入口
  total?: { source: "udp" | "tcp"; rttMs: number; ageS: number; loss?: number }; // 手机 → 落地 (the core)
  backup?: { ok: boolean; avgMs: number; loss: number };                       // 手机 → 备线入口
}
```

PhoneState additions (1.0.10-panel):

```ts
forwards[i].protect?: "logswitch" | "warm" | "off";   // what runs in front of the core on that port
forwards[i].switched?: boolean;                       // 日志快切: new connections go to the 备线 now
forwards[i].backup?: boolean;                         // a 备线 is set for it (from the plan)
plan?: { ref: string; updatedAt: number; forwards: number } | null;
```

Details as built (1.0.10-panel; App repo `app/src/panel/PanelPlan.java`, `PanelPlane.java`, `PanelDiag.java`):

- **Validation** (`bad_request` with a short Chinese reason): `v` = 1; `updatedAt` a whole number > 0; `forwards` an array
  of ≤ 64, each `forwardId` a whole number > 0 (no duplicates) and `port` 1–65535; a present trigger key must be
  `switch` / `record` / `off`; `holdS` 1–300, `leaseSettleS` 0–600, `returnCheckS` 0.1–30; booleans must be booleans;
  `ref` ≤ 32 characters. Clamped instead: `warm` / `backupWarm` / `warm.standby` to 0–16, `warm.paceMs` to 20–2000.
  Unknown keys are ignored; `burst` is never on (not part of the phone). Missing global values = the engine defaults
  (Baidu's: hold 30 s, settle 30 s, return check 2.5 s, standby 2, pace 100 ms, bytes4 / zeroFin on, coalesce off).
- **A 备线 the App will not dial** — a host name, a missing port, or a loopback / unspecified / multicast / link-local /
  broadcast address — is refused **for that forward only**: it runs as if `logswitch: false` (预热池 with its `warm`),
  `forwards[i].backup` is false, the reply counts it in `refusedBackups`, the run log gets `DEST_REFUSED · panel_plan`.
  The rest of the plan applies. An entry whose `host` is not an IP literal is dropped (it can't be pinged).
- **What runs per forward** (`forwards[i].protect`): `logswitch` = the plan says `logswitch` with a valid `backup` and the
  forward carries TCP; else `warm` = the 预热池 switch is on and the plan's `warm` > 0 (pool size = that `warm`); else
  `off` (the core on its own port). A forward the plan does not list (or no plan at all) = 1.0.9-panel: `warm` (4) while
  the switch is on. 预热池 switch off → `logswitch` keeps running with pools 0; `warm` forwards go `off`.
  `forwards[i].warm` = pools really run there. A use port another app holds → `state: "failed"` with the reason.
- **The plan is cleared** when the App's panel address is changed (换面板地址): the new panel sends its own.
- **Run log**: one `连接保护` line per applied plan (「已应用面板下发的手机方案（参照 ref） · N 条转发」, no addresses);
  日志快切 on / off / skip / … lines as 利群原版 (at most one a minute per forward and kind).
- **`diag`**: `state` = that forward's PhoneState state (`ready` / `connecting` / `failed` / `off`); `found` = the plan
  or the phone knows the forward. `ingress` is left out when there is no current lease entry (not connected);
  `inUse: false` = the core named no current entry, so the fastest one was measured (one handshake each, the rest on it).
  `total.source`: `udp` only with answered probes (`probe_sent` > 0 and `probe_received` > 0; then `loss`), else `tcp`;
  an rtt ≤ 0 or > 2000 ms is no measurement (no `total`); `ageS` = seconds since the core's `selected_at`, -1 unknown.
  `backup` only when the plan has a 备线 for it. A target that times out is not tried again (the rest count as lost).
  Errors: `bad_request` (no / bad `forwardId`), `busy`, `failed`.
- **`loss` is a percentage** (0–100, one decimal) in `diag` and `tcpPing`; `avgMs` has one decimal (0 when none
  answered); `tcpPing.count` = the handshakes asked for.

Destinations: a plan's `backup` and `entries` addresses are IP literals the owner's own panel designated; the App adds
exactly those `ip:port` pairs (backups) and `entry ip` + forward ports (tcpPing) to its allowlist and nothing else.
Names are refused (never resolved). The vendor host, lease entries and loopback are unchanged.

The panel pushes the plan (`POST leikwan/phone/plan`, admin) whenever 利群汇聚 opens in the App and when it changes.
Without a plan the App runs 预热池 only (as 1.0.9-panel). 首包过滤 is not a phone module and is never shown
(plan 301: hidden everywhere in the UI; on the phone only this phone's own apps reach the ports, so `eof1s` acts).
