import path from "path";

import { defineConfig, loadEnv, type Plugin } from "vite";
import react from "@vitejs/plugin-react";
import { VitePWA } from "vite-plugin-pwa";

// One id per build: written into index.html (<meta name="flvx-build">) and
// emitted as dist/version.json. A running client whose id differs from the
// served version.json is stale (see src/utils/build-freshness.ts). Plan 209:
// the id is not baked into the JS, so chunks whose code did not change keep
// their hashed names across releases (and stay cached). The timestamp makes
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
  transformIndexHtml() {
    return [
      {
        tag: "meta",
        attrs: { name: "flvx-build", content: BUILD_ID },
        injectTo: "head-prepend",
      },
    ];
  },
});

// The admin app shell: entry + admin-main + their static imports (filled by
// adminPreloadPlugin, read by the service worker manifest filter below).
const shellFiles = new Set<string>();

// Plan 209: index.html lists the admin app shell (admin-main + its static
// imports) as modulepreload, so it downloads together with the entry instead
// of after it. Not on the public probe page (/tz) or a known probe-only host,
// which never load the admin app.
const adminPreloadPlugin = (): Plugin => ({
  name: "flvx-admin-preload",
  apply: "build",
  transformIndexHtml: {
    order: "post",
    handler(_html, ctx) {
      const bundle = ctx.bundle;

      if (!bundle) {
        return [];
      }
      const chunks = Object.values(bundle).filter(
        (item) => item.type === "chunk",
      );
      const shell = chunks.find(
        (chunk) =>
          chunk.type === "chunk" &&
          (chunk.facadeModuleId ?? "").endsWith("/src/admin-main.tsx"),
      );

      if (!shell || shell.type !== "chunk") {
        return [];
      }
      const files: string[] = [];
      const visit = (fileName: string) => {
        if (files.includes(fileName)) {
          return;
        }
        files.push(fileName);
        const chunk = bundle[fileName];

        if (chunk && chunk.type === "chunk") {
          chunk.imports.forEach(visit);
        }
      };

      visit(shell.fileName);
      const hrefs = JSON.stringify(files.map((file) => `/${file}`));

      shellFiles.clear();
      files.forEach((file) => shellFiles.add(file));
      chunks.forEach((chunk) => {
        if (chunk.type === "chunk" && chunk.isEntry) {
          shellFiles.add(chunk.fileName);
          chunk.imports.forEach((file) => shellFiles.add(file));
        }
      });

      return [
        {
          tag: "script",
          children:
            `(function(){try{if(/^\\/tz(\\/|$)/.test(location.pathname)||` +
            `localStorage.getItem("flvx_probe_host:"+location.hostname)==="1")return;}catch(e){}` +
            `${hrefs}.forEach(function(h){var l=document.createElement("link");` +
            `l.rel="modulepreload";l.href=h;document.head.appendChild(l);});})();`,
          injectTo: "head",
        },
      ];
    },
  },
});

export default defineConfig({
  plugins: [
    react(),
    versionJsonPlugin(),
    adminPreloadPlugin(),
    VitePWA({
      filename: "flvx-sw.js",
      registerType: "autoUpdate",
      injectRegister: "auto",
      includeAssets: ["favicon.ico"],
      // Plan 209: the PWA icons (~1 MB of PNGs) are not precached; the
      // browser fetches them itself when the app is installed. Precaching
      // them cost every first visit (and, with the old cache wipe, every
      // release) that megabyte.
      includeManifestIcons: false,
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
        // Plan 209: page loads (index.html) are network-first instead of
        // served from the precache. After a release the next load is the new
        // build straight away — no stale build first, no forced cache wipe,
        // no second download of every chunk. Offline the last copy is used.
        // index.html is no longer precached for the same reason.
        navigateFallback: null,
        // The page request starts while the service worker boots.
        navigationPreload: true,
        runtimeCaching: [
          {
            urlPattern: ({ request, url }) =>
              request.mode === "navigate" &&
              !/^\/(api|s|flow|system-info)(\/|$)/.test(url.pathname),
            handler: "NetworkFirst",
            options: {
              cacheName: "flvx-pages",
              expiration: { maxEntries: 8 },
              cacheableResponse: { statuses: [200] },
            },
          },
        ],
        cleanupOutdatedCaches: true,
        maximumFileSizeToCacheInBytes: 5 * 1024 * 1024,
        // Plan 209: precache only the app shell (login page) plus CSS and
        // small files. Page chunks are cached by the browser's HTTP cache
        // (immutable, 1 year) when first used; precaching every chunk made
        // each first visit and each release download all pages at once,
        // competing with the page being opened.
        manifestTransforms: [
          (entries) => ({
            manifest:
              shellFiles.size === 0
                ? entries
                : entries.filter(
                    (entry) =>
                      !/^assets\/.*\.js$/.test(entry.url) ||
                      shellFiles.has(entry.url) ||
                      /^assets\/workbox-window/.test(entry.url),
                  ),
            warnings: [],
          }),
        ],
        // SW scripts and recovery/version files must never be precached.
        globIgnores: [
          "**/node_modules/**/*",
          "**/flvx-sw.js",
          "**/sw.js",
          "**/version.json",
          "**/stale-client-reset.js",
          "index.html",
        ],
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
      output: {
        // Plan 209: Vite's dynamic-import helper gets its own tiny chunk.
        // Left in the entry chunk, every lazy chunk imports the entry, whose
        // name changes with each release — and so would theirs.
        manualChunks(id) {
          if (id.includes("vite/preload-helper")) {
            return "preload-helper";
          }

          return undefined;
        },
      },
    },
  },
});
