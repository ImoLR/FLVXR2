import ReactDOM from "react-dom/client";
import { BrowserRouter } from "react-router-dom";
import { registerSW } from "virtual:pwa-register";

import App from "./App.tsx";
import { Provider } from "./provider.tsx";

import "@/styles/globals.css";
import {
  showBuildUpdatedToast,
  showBuildUpdating,
} from "@/components/build-update-feedback";
import {
  attachServiceWorkerRegistration,
  startFrontendFreshnessWatch,
} from "@/utils/build-freshness";

const updateSW = registerSW({
  immediate: true,
  onNeedRefresh() {
    showBuildUpdating();
    void updateSW(true);
  },
  onRegisteredSW(_swScriptUrl, registration) {
    if (registration) {
      attachServiceWorkerRegistration(registration);
    }
  },
});

showBuildUpdatedToast();
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
