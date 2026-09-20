import { svelte } from '@sveltejs/vite-plugin-svelte'
import { copyFileSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import { gzipSync } from 'node:zlib'

// MapLibre GL JS ships its tiling/parsing work in a SEPARATE worker script
// (maplibre-gl-worker.mjs) that it loads itself at runtime via
// `new URL('./maplibre-gl-worker.mjs', import.meta.url)` — a pattern baked
// into MapLibre's own pre-built bundle, invisible to Rollup's static import
// analysis, so `vite build` never picks it up as an asset to emit. Caught by
// hand verification: no console error (Worker() failures are silent unless
// you attach an 'error' listener, which MapLibre does not for this), no
// visible symptom beyond an empty map — the network tab was the only place
// the 404 for GET /static/build/assets/maplibre-gl-worker.mjs showed up.
// The worker script itself then imports a second file the same way
// (maplibre-gl-shared.mjs, code shared between the main thread and the
// worker) — that 404 only appeared once the first one was fixed and the
// worker script actually started running.
//
// Fixed by copying both files into dist/assets/ ourselves, under the exact
// unhashed names MapLibre computes relative to its own chunk's/worker's URL —
// neither is content-addressed like every other asset here, so a maplibre-gl
// version bump needs these files' content to change without the request URL
// changing; the immutable Cache-Control this project applies to the whole
// /static/build/ tree (see internal/web/pages.go) will hide that change
// behind a stale cache until the max-age expires. A real gap, accepted here
// because MapLibre pins an exact version and upgrades are a deliberate,
// infrequent event — not something this task should solve by making two files
// under an otherwise-uniform tree behave differently.
function copyMapLibreWorker() {
  return {
    name: 'copy-maplibre-gl-worker',
    closeBundle() {
      const destDir = path.resolve('../internal/web/dist/assets')
      mkdirSync(destDir, { recursive: true })
      for (const name of ['maplibre-gl-worker.mjs', 'maplibre-gl-shared.mjs']) {
        copyFileSync(
          path.resolve('node_modules/maplibre-gl/dist', name),
          path.join(destDir, name),
        )
      }
    },
  }
}

// internal/web/dist/.keep is the only tracked file under dist/, and it is what
// makes `go:embed dist/*` compile on a fresh clone that has never run a build.
// `emptyOutDir: true` wipes it, so a contributor who builds and commits deletes
// it and breaks the Go build. Re-created here rather than turning off
// emptyOutDir, which would leave stale hashed bundles piling up in the tree.
function keepDistTracked() {
  return {
    name: 'keep-dist-tracked',
    closeBundle() {
      writeFileSync(path.resolve('../internal/web/dist/.keep'), '')
    },
  }
}

// Keyed to this plugin's own node:zlib measurement, which disagrees with
// Vite's native reporter on the same bytes. See docs/map-rendering.md.
const MAP_CHUNK_GZIP_BUDGET_BYTES = 290 * 1024

// writeBundle sees the real written bytes, unlike a test reading dist after the
// fact, which would pass vacuously on a stale build. A chunk that no longer
// matches is a build failure, not a silent no-op — see docs/map-rendering.md.
function checkMapChunkSize() {
  return {
    name: 'check-map-chunk-size',
    writeBundle(options, bundle) {
      let mapChunkFound = false
      for (const chunk of Object.values(bundle)) {
        const bytes = readFileSync(path.join(options.dir, chunk.fileName))
        const gzipBytes = gzipSync(bytes).length
        console.log(`[bundle size] ${chunk.fileName}: ${(gzipBytes / 1024).toFixed(2)} KB gz`)
        if (chunk.type === 'chunk' && chunk.facadeModuleId?.endsWith('/islands/map.js')) {
          mapChunkFound = true
          if (gzipBytes > MAP_CHUNK_GZIP_BUDGET_BYTES) {
            throw new Error(
              `map chunk is ${(gzipBytes / 1024).toFixed(2)} KB gzipped, over the ${(MAP_CHUNK_GZIP_BUDGET_BYTES / 1024).toFixed(0)} KB budget (docs/map-rendering.md) — lazy-load wind/timelapse out of it`,
            )
          }
        }
      }
      if (!mapChunkFound) {
        throw new Error(
          'map chunk size guard found no chunk with facadeModuleId ending in /islands/map.js — the chunk-identifying assumption broke (chunking change, map.js rename/merge) and the gzip budget is no longer enforced; update checkMapChunkSize in vite.config.js (docs/map-rendering.md)',
        )
      }
    },
  }
}

export default {
  // '.' rather than 'web': vite.config.js already lives inside web/, and every
  // script in package.json runs with npm's cwd there (`cd web && npm run
  // build`). `root` resolves relative to process.cwd(), not to this file's own
  // location — setting it to 'web' here double-nests to web/web when invoked
  // the documented way. Verified in Step 5 by checking where the build output
  // actually landed.
  root: '.',
  // Go serves the built tree under /static/build/ (see internal/web/assets.go's
  // assetPrefix), not from the domain root. The entry script/CSS path is
  // prefixed by Go when it reads the manifest, but Vite itself bakes absolute
  // URLs into the bundle for anything it resolves at runtime — a dynamic
  // island's own chunk and its CSS — using whatever `base` says. Left at the
  // default '/', those chunk requests 404 against the real server (caught by
  // hand verification: the browser asked for /assets/map-*.css, got Go's
  // catch-all 404 page back as text/html, and the map island failed to mount).
  base: '/static/build/',
  plugins: [svelte(), copyMapLibreWorker(), keepDistTracked(), checkMapChunkSize()],
  build: {
    outDir: '../internal/web/dist',
    emptyOutDir: true,
    // Load-bearing: hashed filenames are how the app gets
    // `Cache-Control: immutable` without ever serving a stale bundle.
    // Without the manifest, Go cannot know the hashed name.
    manifest: true,
    // Off so one gzip number per file reaches the log, not two that disagree;
    // checkMapChunkSize prints every file, assets included.
    reportCompressedSize: false,
    // 'theme' is a CSS-only entry: it exists so the design kit's tokens are
    // inlined into the build instead of restated in internal/web/static.
    rollupOptions: { input: { main: 'src/main.js', theme: 'src/styles/theme.css' } },
  },
  // Svelte's package.json exports a separate build per condition: 'browser'
  // resolves to the client runtime (the one with a working mount()/$effect),
  // the default/node condition resolves to the server-rendering runtime,
  // whose mount() only throws lifecycle_function_unavailable. Vite sets the
  // 'browser' condition itself for `vite build`/`vite dev`, but Vitest runs
  // under plain Node with no bundler-set condition, so a component test
  // importing 'svelte' silently got the server build and every mount() call
  // failed. Forced here only under `vitest` (`process.env.VITEST`) so the
  // real build keeps Vite's own resolution untouched.
  resolve: process.env.VITEST ? { conditions: ['browser'] } : undefined,
  // Vitest's default include glob (**/*.{test,spec}.*) also matches
  // web/e2e/*.spec.js — Playwright's own test files, run only by
  // `npx playwright test` from internal/e2e's Go driver, never by Vitest.
  test: { exclude: ['**/node_modules/**', 'e2e/**'] },
}
