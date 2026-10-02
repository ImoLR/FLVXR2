import ReactDOM from "react-dom/client";
import { BrowserRouter } from "react-router-dom";
import { registerSW } from "virtual:pwa-register";

import App from "./App.tsx";
import { Provider } from "./provider.tsx";
import "@/styles/globals.css";

// Long-lived tabs never navigate, so the browser never re-checks sw.js on its
// own. Check periodically while the tab is hidden (the autoUpdate reload is
// invisible there) and when it comes back to the foreground, so a stale tab
// reloads into the new release before the user starts interacting with it.
const SW_HIDDEN_CHECK_INTERVAL_MS = 30 * 60 * 1000;
const SW_VISIBLE_CHECK_MIN_GAP_MS = 5 * 60 * 1000;

const watchForSWUpdates = (registration: ServiceWorkerRegistration) => {
  let lastCheckAt = Date.now();

  const checkForUpdate = () => {
    if (!navigator.onLine || registration.installing) {
      return;
    }
    lastCheckAt = Date.now();
    void registration.update().catch(() => undefined);
  };

  window.setInterval(() => {
    if (document.visibilityState === "hidden") {
      checkForUpdate();
    }
  }, SW_HIDDEN_CHECK_INTERVAL_MS);

  document.addEventListener("visibilitychange", () => {
    if (
      document.visibilityState === "visible" &&
      Date.now() - lastCheckAt >= SW_VISIBLE_CHECK_MIN_GAP_MS
    ) {
      checkForUpdate();
    }
  });
};

const updateSW = registerSW({
  immediate: true,
  onNeedRefresh() {
    void updateSW(true);
  },
  onRegisteredSW(_swScriptUrl, registration) {
    if (registration) {
      watchForSWUpdates(registration);
    }
  },
});

ReactDOM.createRoot(document.getElementById("root")!).render(
  <BrowserRouter>
    <Provider>
      <>
        <App />
      </>
    </Provider>
  </BrowserRouter>,
);
