# im.gamer App ↔ panel bridge (v1)

Same file in both repos: FLVX `vite-frontend/docs/panel-app-bridge.md` and leikwan-local-bridge
`docs/panel-app-bridge.md`. Change both together. FLVX plan 294 (v0, 2026-11-22).

## What it is

The im.gamer Android App (flavor `panel`, applicationId `com.localbridge.leikwan.panel`) is a WebView of the owner's
own FLVX panel plus the phone's own LeiKwan client (the shared connect engine of 重仓利群: BridgeService, leases,
warm pool, battery gate, run log). The panel page drives the phone's local link through this bridge; the phone shows up
as one more 入口机 tile 「手机本地」 on the 利群汇聚 page.

- **App vs browser** is decided only by the injected object `window.panelApp` — never by the User-Agent.
  Browsers (desktop or phone) never have it, so they never show 手机本地 or the App nav.
- In App mode the panel shows only 利群汇聚 (v0); login still works.
- The local link never depends on the panel: everything the core needs (客户端 ID, Agent Token, vendor leases, run
  log) lives on the phone. If the panel cannot be reached, the App shows a small native fallback page with the local
  status and 断开 / 重连.

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
  network request (`shouldInterceptRequest` → empty 403) and opens other-origin links in the system browser.
- Credentials never cross the bridge: the page can only ask the App to open its native credential dialog.

## Wire format

JSON strings both ways. `v` = protocol version (1). Unknown `v` → ignored.

```
page → App   {"v":1,"id":7,"method":"connect","params":{}}
App → page   {"v":1,"id":7,"ok":true,"result":{…}}
App → page   {"v":1,"id":7,"ok":false,"error":{"code":"battery","message":"先把电池设为「无限制」"}}
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
| `connect` | – | `PhoneState` | battery gate first (`battery` error); no credentials → `credentials` error |
| `disconnect` | – | `PhoneState` | = 停止 (all local ports close) |
| `setAutoConnect` | `{on: bool}` | `PhoneState` | 「打开 App 自动连接」, default on |
| `setWarm` | `{on: bool}` | `PhoneState` | 预热池 (same switch as 重仓利群's) |
| `getLog` | `{maxChars?: int}` | `{text, bytes, truncated}` | tail of the run log (≤ 64 k chars by default) |
| `copyLog` | – | `{copied: bool}` | whole run log (≤ 200 k chars) to the clipboard, natively |
| `shareLog` | – | `{shared: bool}` | Android share sheet (text) |
| `openBatterySettings` | – | `{opened: bool}` | the 无限制 request dialog / app settings |
| `editCredentials` | – | `{saved: bool}` | native dialog: 客户端 ID + Agent Token (stored on the phone only) |
| `changePanel` | – | `{}` | back to the native first-run page (change the panel address) |
| `speedTest` | reserved | error `not_implemented` | 本机测速 hook, not in v0 |

Error codes: `battery` (电池没设成无限制), `credentials` (没填客户端 ID / Agent Token), `busy`, `not_implemented`,
`bad_request`, `failed` (message says why).

## Types

```ts
interface PhoneState {
  connection: "idle" | "connecting" | "connected" | "partial" | "failed" | "disconnecting";
  running: boolean;            // the service runs (断开 offered; also while it retries after a failure)
  stage?: string;              // 读取账号 / 连接商家 / 等待商家授权 / 启动入口
  ready: number; total: number;
  error?: string;              // Chinese, shown on the red tile 「连接失败 · 点看原因」
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
  autoConnect: boolean;
  autoConnectBlocked?: string; // why it did not run (电池 …)
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

## What the panel does with it

- 入口机 list: 手机本地 first (App only) next to the front machines (火山广州 / 百度广州 / 阿里广州 / 火山上海).
  Tap = select, 「详情」 = drawer (desktop) / ＋ row (H5). Default selection: App → 手机本地 once connected
  (主入口 before that); browser → the grant's 主入口.
- One per-entry schema per forward row: chip coloured = supported + on, grey = supported + off, hidden = not supported
  on that entry, row greyed 「此入口未开」 = the forward does not run there. Fronts: host/list snapshot + live
  agent state + rule.hostValues + 只开主入口. 手机本地: `PhoneState.forwards`.
- 复制链接 / 订阅 follow the selected entry: `leikwan-rule/link {id, entry:"local"|nodeId}`; subscription URL
  `…/s/<token>?lk=local` (127.0.0.1:<vendor port>, same landing params; works on any device that runs a LeiKwan
  client on the same port — 「需要本机开着利群客户端」).
- 在线设备 x/y comes from the panel's own vendor usage view (admin), not from the phone.

## Vendor device slot

The vendor identity (AndroidKeyStore EC key + no_backup files) is per app installation and per applicationId, so the
im.gamer App is **one more vendor device** on the account, separate from 重仓利群 / 利群原版 on the same phone. The
drawer shows 商家在线设备 x/y and warns when the account is full. The three apps listen on the same vendor ports:
run only one of them at a time.
