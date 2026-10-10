import ReactDOM from "react-dom/client";
import { BrowserRouter } from "react-router-dom";
import { registerSW } from "virtual:pwa-register";

import App from "./App.tsx";
import { Provider } from "./provider.tsx";

import "@/styles/globals.css";
import {
  attachServiceWorkerRegistration,
  startFrontendFreshnessWatch,
} from "@/utils/build-freshness";

const updateSW = registerSW({
  immediate: true,
  onNeedRefresh() {
    void updateSW(true);
  },
  onRegisteredSW(_swScriptUrl, registration) {
    if (registration) {
      attachServiceWorkerRegistration(registration);
    }
  },
});

startFrontendFreshnessWatch();

ReactDOM.createRoot(document.getElementById("root")!).render(
  <BrowserRouter>
    <Provider>
      <>
        <App />
      </>
    </Provider>
  </BrowserRouter>,
);
