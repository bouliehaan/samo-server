import { defineConfig } from "vite";
import { resolve } from "node:path";

// The three pages are served by Go, not by Vite — /app, /setup and /login all
// go through handlers that redirect on setup state and auth. So this is a
// library-style build with one entry per page rather than Vite's HTML mode:
// it emits hashed JS and CSS, plus a manifest that Go reads at startup to
// write the right <link> and <script> tags into each page shell.
export default defineConfig({
  root: import.meta.dirname,
  // base.css references the Office Code Pro faces at /assets/fonts/..., which
  // Go serves from internal/api/assets. Pointing Vite's public dir there lets
  // it resolve those URLs, so a typo in a font path fails the build instead of
  // 404ing in a browser. copyPublicDir keeps it from duplicating the fonts
  // into the bundle — they are already embedded and served by Go.
  publicDir: resolve(import.meta.dirname, "../internal/api"),
  build: {
    copyPublicDir: false,
    // NOT "dist" — the repo's .gitignore ignores dist/ anywhere, which would
    // silently drop this whole directory from commits. A migration was lost
    // to exactly that rule once already.
    outDir: resolve(import.meta.dirname, "../internal/api/web/build"),
    emptyOutDir: true,
    manifest: "manifest.json",
    // Hashed filenames let the assets be served immutable and cached forever;
    // the manifest is what keeps the page shells pointing at the current ones.
    rollupOptions: {
      input: {
        app: resolve(import.meta.dirname, "src/app.js"),
        setup: resolve(import.meta.dirname, "src/setup.js"),
        login: resolve(import.meta.dirname, "src/login.js"),
      },
      output: {
        entryFileNames: "[name]-[hash].js",
        chunkFileNames: "[name]-[hash].js",
        assetFileNames: "[name]-[hash][extname]",
      },
    },
    // The dashboard is served over a LAN from the user's own box. Readable
    // stack traces in the browser console are worth more here than the last
    // few kilobytes, so keep names and ship maps.
    minify: "esbuild",
    sourcemap: true,
    target: "es2022",
  },
});
