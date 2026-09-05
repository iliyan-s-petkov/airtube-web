// Applies a stored theme override before the first paint.
//
// Separate from the theme island, and a render-blocking classic script in the
// <head>, because the island arrives with the module bundle — long after the
// page has painted. A reader who chose light on a dark machine would see the
// dark theme flash first. This is the only script on the site that has to run
// before paint, which is why it is one statement in its own file rather than a
// function in the bundle.
//
// An external file, not an inline <script>: script-src has no 'unsafe-inline'.
//
// "auto" is stored as nothing at all. The absence of data-theme is what lets
// prefers-color-scheme decide, so an explicit "auto" value would be a third
// state meaning the same as the first.
try {
  var stored = localStorage.getItem('airbg:theme')
  if (stored === 'light' || stored === 'dark') {
    document.documentElement.dataset.theme = stored
  }
} catch (err) {
  // Storage can throw outright (Safari private browsing, a blocked
  // third-party context). The OS preference is the correct fallback.
}
