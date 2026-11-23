import ReactDOM from "react-dom/client";
import { BrowserRouter } from "react-router-dom";
import { registerSW } from "virtual:pwa-register";

import App from "./App.tsx";
import { Provider } from "./provider.tsx";

import { currentPageKey } from "@/routes/page-loaders";
import { pages } from "@/routes/pages";
import {
  showBuildUpdatedToast,
  showBuildUpdating,
} from "@/components/build-update-feedback";
import {
  attachServiceWorkerRegistration,
  onServiceWorkerTakeover,
  startFrontendFreshnessWatch,
} from "@/utils/build-freshness";
import { forgetStoredGroupOpenState } from "@/utils/forget-group-open";
import { isAppMode } from "@/utils/app-bridge";

// The admin / user app (everything except the public probe page, see
// main.tsx). Unchanged behaviour: service worker, freshness watch, router.
export function startAdmin() {
  // Plan 289: open groups are not remembered; drop what older builds stored.
  forgetStoredGroupOpenState();
  if (isAppMode()) {
    // Plan 299: no service worker inside the im.gamer App — an offline load
    // must fail so the App shows its own fallback page (手机本地 status,
    // 断开 / 重连) instead of a cached shell whose API calls all fail.
    unregisterServiceWorkers();
  } else {
    startServiceWorker();
  }

  showBuildUpdatedToast();
  startFrontendFreshnessWatch();

  const render = () =>
    ReactDOM.createRoot(document.getElementById("root")!).render(
      <BrowserRouter>
        <Provider>
          <>
            <App />
          </>
        </Provider>
      </BrowserRouter>,
    );
  const key = currentPageKey();

  if (!key) {
    render();

    return;
  }
  // Plan 209: the current page's chunk is already on its way (main.tsx);
  // render once it is in (at most 3 s) so the first paint shows the layout
  // and the page together, and the page's requests start without an extra
  // suspend-and-retry round.
  void Promise.race([
    pages[key].preload().catch(() => undefined),
    new Promise((resolve) => window.setTimeout(resolve, 3000)),
  ]).then(render);
}

function startServiceWorker() {
  const updateSW = registerSW({
    immediate: true,
    onNeedRefresh() {
      showBuildUpdating();
      void updateSW(true);
    },
    // Plan 209: a new service worker taking over reloads only a stale page
    // (page loads are network-first, so this page may already be the
    // deployed build).
    onNeedReload: onServiceWorkerTakeover,
    onRegisteredSW(_swScriptUrl, registration) {
      if (registration) {
        attachServiceWorkerRegistration(registration);
      }
    },
  });
}

// Plan 299: a worker registered by an earlier App build (or before this
// check existed) is dropped; its page cache is no longer used.
function unregisterServiceWorkers() {
  if (typeof navigator === "undefined" || !("serviceWorker" in navigator)) {
    return;
  }
  void navigator.serviceWorker
    .getRegistrations()
    .then((regs) => Promise.all(regs.map((r) => r.unregister())))
    .catch(() => undefined);
}
