import path from "path";

import { defineConfig, loadEnv, type Plugin } from "vite";
import react from "@vitejs/plugin-react";
import { VitePWA } from "vite-plugin-pwa";

// One id per build: baked into the bundle (__APP_BUILD_ID__) and emitted as
// dist/version.json. A running client whose id differs from the served
// version.json is stale (see src/utils/build-freshness.ts). The timestamp makes
// every build unique; the version prefix (CI writes .env.production) is a label.
const buildVersionLabel = (
  loadEnv("production", __dirname, "VITE_").VITE_APP_VERSION || "local"
).replace(/[^0-9A-Za-z._-]/g, "");
const BUILD_ID = `${buildVersionLabel || "local"}-${Date.now().toString(36)}`;

const versionJsonPlugin = (): Plugin => ({
  name: "flvx-version-json",
  apply: "build",
  generateBundle() {
    this.emitFile({
      type: "asset",
      fileName: "version.json",
      source: `${JSON.stringify({ build: BUILD_ID })}\n`,
    });
  },
});

export default defineConfig({
  define: {
    __APP_BUILD_ID__: JSON.stringify(BUILD_ID),
  },
  plugins: [
    react(),
    versionJsonPlugin(),
    VitePWA({
      registerType: "autoUpdate",
      injectRegister: "auto",
      includeAssets: ["favicon.ico", "apple-touch-icon.png"],
      manifest: {
        name: "FLVX",
        short_name: "FLVX",
        description: "FLVX forwarding management panel",
        theme_color: "#2563eb",
        background_color: "#f6f7fb",
        display: "standalone",
        start_url: "/",
        scope: "/",
        icons: [
          {
            src: "pwa-192x192.png",
            sizes: "192x192",
            type: "image/png",
          },
          {
            src: "pwa-512x512.png",
            sizes: "512x512",
            type: "image/png",
          },
          {
            src: "pwa-maskable-512x512.png",
            sizes: "512x512",
            type: "image/png",
            purpose: "maskable",
          },
        ],
      },
      workbox: {
        navigateFallback: "/index.html",
        cleanupOutdatedCaches: true,
        maximumFileSizeToCacheInBytes: 5 * 1024 * 1024,
        // version.json must always come from the network (never precached).
        globIgnores: ["**/node_modules/**/*", "**/version.json"],
        skipWaiting: true,
        clientsClaim: true,
      },
      devOptions: {
        enabled: false,
        type: "module",
      },
    }),
  ],
  base: "/",
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  server: {
    port: 3000,
    host: "0.0.0.0",
  },
  build: {
    outDir: "dist",
    sourcemap: false,
    minify: true,
    rollupOptions: {
      treeshake: false,
    },
  },
});
