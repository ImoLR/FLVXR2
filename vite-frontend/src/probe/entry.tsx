// Plan 187: standalone entry of the public probe page. Loads React, the theme
// tokens and the probe page only: no router, no admin pages, no service worker.
// Only the default theme is registered: the theme loader also pulls the
// example theme's button and with it the whole icon library (~110 KB gz).
import ReactDOM from "react-dom/client";

import ProbeApp from "./probe-app";

import defaultTheme from "@/themes/default";
import { initThemeSystem, registerTheme } from "@/themes/registry";

export function mountProbe() {
  registerTheme(defaultTheme);
  initThemeSystem();
  ReactDOM.createRoot(document.getElementById("root")!).render(<ProbeApp />);
}
