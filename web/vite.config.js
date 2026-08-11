import { svelte } from '@sveltejs/vite-plugin-svelte'

export default {
  // '.' rather than 'web': vite.config.js already lives inside web/, and every
  // script in package.json runs with npm's cwd there (`cd web && npm run
  // build`). `root` resolves relative to process.cwd(), not to this file's own
  // location — setting it to 'web' here double-nests to web/web when invoked
  // the documented way. Verified in Step 5 by checking where the build output
  // actually landed.
  root: '.',
  plugins: [svelte()],
  build: {
    outDir: '../internal/web/dist',
    emptyOutDir: true,
    // Load-bearing: hashed filenames are how the app gets
    // `Cache-Control: immutable` without ever serving a stale bundle.
    // Without the manifest, Go cannot know the hashed name.
    manifest: true,
    rollupOptions: { input: 'src/main.js' },
  },
}
