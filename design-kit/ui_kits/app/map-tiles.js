/* The vector basemap: airbg's own PMTiles archive, under the data layer.
 *
 * WHY THIS EXISTS
 * ---------------
 * The design system spent several passes hand-capturing roads, streets and
 * district boundaries from Overpass on the premise that "a tile server is a
 * third-party origin, which §1 forbids". That premise was wrong: airbg serves
 * its OWN basemap from its own listener (`internal/tiles/tiles.go`), an
 * OpenMapTiles-schema planetiler build of the Bulgaria extract. The app's own
 * CSP has read `connect-src 'self' https://tiles.airbg.org` all along.
 *
 * So this file replaces the hand-built basemap with the real one, and the kit
 * stops validating a picture the reader never sees (§5.2b).
 *
 * WHAT IT DOES NOT DO
 * -------------------
 * It does not touch the DATA. Provinces, readings, markers and labels are
 * still drawn by map-render.js, over the tiles, from the served scales. The
 * basemap says where you are; it never says what the air is doing.
 *
 * WHERE THIS RUNS, AND WHY THAT SETTLES CORS
 * ------------------------------------------
 * The kit ships inside the app repo and is served by the app itself at
 * `https://airbg.org/design-kit/ui_kits/app/`. Its origin is therefore
 * `https://airbg.org` — exactly the origin `tiles.airbg.org` already answers
 * with in `Access-Control-Allow-Origin`. Nothing was widened to make this
 * work: the kit moved to the origin that was already allowed, which is why
 * the CORS prompt written for the app repo was never needed.
 *
 * Opened any OTHER way — `file://`, a preview daemon on loopback, a copy on
 * some other host — the origin does not match, the style read fails, and the
 * SVG basemap draws instead. That is a supported state, not a fault.
 *
 * THE THINGS THAT CAN GO WRONG, AND WHAT HAPPENS THEN
 * --------------------------------------------------
 * 1. The origin refuses this reader (see above).
 * 2. WebGL is unavailable (headless export, an old machine, a blocked
 *    context).
 *
 * In EITHER case this file stands down and the SVG basemap draws exactly as
 * it did before. That is deliberate and it is the whole safety property here:
 * a style whose layers match nothing renders a blank map with no error, which
 * is the documented failure in the app's own docs/tiles.md, and a blank map is
 * the worst outcome this system can ship. Fall back visibly, never silently.
 */
(function () {
  /* origins.js owns where the backends live, so this file no longer decides
   * it and no longer makes the kit reviewable only in production (§5.2b). */
  var TILES = (window.AIRBG_ORIGINS && window.AIRBG_ORIGINS.tiles) || 'https://tiles.airbg.org';
  var STYLE = TILES + '/style.json';
  var frames = document.querySelectorAll('[data-od-id="map"], [data-od-id="area-map"]');
  if (!frames.length) return;

  /* One announcement for every basemap state change, so nothing else has to
   * probe for a camera. The layer control (map-layers.js) is the reader of it:
   * it offers nothing while the SVG basemap is drawing, because there are no
   * tile layers to switch. Two things deciding whether the tiles are live is
   * two things free to disagree. */
  function announce(frame, state, map) {
    document.dispatchEvent(new CustomEvent('airbg:basemapchange', {
      detail: { frame: frame, state: state, map: map || null }
    }));
  }

  /* Once the tile path is abandoned it stays abandoned. MapLibre can still emit
   * `load` AFTER an error has torn the canvas out, and the first version let that
   * re-announce `tiles`: the frame said data-basemap="tiles", the layer control
   * re-appeared with every category ticked, and there was no map in the document
   * for any of it to act on. A control offering to switch layers on a map that
   * has been removed is the dead control this system keeps re-inventing — this
   * time built out of its own recovery path. */
  var downed = false;

  function stand_down(why) {
    if (downed) return;
    downed = true;
    /* The diagnosis handle must not outlive the map it points at. Left set, it
     * hands whoever is debugging a blank screen a torn-down map whose
     * getStyle() still answers happily — the same "state claims live when it
     * is not" defect this file just fixed, reintroduced by the tool added to
     * catch it. */
    window.AIRBG_TILEMAP = null;
    frames.forEach(function (f) {
      f.setAttribute('data-basemap', 'local');
      /* The REASON, on the element. `data-basemap="local"` says the SVG drew;
       * it does not say why, and the why is the whole question when someone is
       * looking at a map that should have OSM under it. A console warning is
       * invisible to anyone reviewing in a preview pane with no console — which
       * is most of the people this kit is for. */
      /* The ORIGIN belongs in the reason. A basemap that stands down because
       * the tile host refused this page is indistinguishable, from the outside,
       * from one that stood down because the machine cannot draw it — and the
       * two have completely different fixes. The origin is the fact that tells
       * them apart, and it is the exact string an operator has to allowlist. */
      f.setAttribute('data-basemap-reason', why + ' [origin ' + location.origin + ']');
      /* And ON SCREEN, not only in an attribute. A reader looking at a map with
       * no streets under it cannot tell "this build is broken" from "this
       * machine cannot draw it", and those need completely different responses.
       * The map already says so when it has no DATA (§9.1); a missing BASEMAP
       * is the same class of fact and was the one silent state left. */
      var note = f.querySelector('.map-basemap-note');
      if (!note) {
        note = document.createElement('p');
        note.className = 'map-basemap-note';
        f.appendChild(note);
      }
      /* The catalogue sentence only. `why` is diagnostic English ("WebGL
       * unavailable") and appending it to Bulgarian copy produces a bilingual
       * sentence for a reader who did not ask for one. It stays on
       * data-basemap-reason, where whoever is debugging will look. */
      /* The reason and the origin are ON the note, not only in its title.
       *
       * A hover tooltip cannot be read from a screenshot, and screenshots are
       * how this gets reported. Three different causes produce one identical
       * grey map — the host refused this origin, the renderer has no WebGL, or
       * the style failed — and they have nothing in common as fixes. The line
       * now separates them without anyone having to hover, open a console, or
       * ask me. */
      note.textContent = (window.AIRBG_T ? window.AIRBG_T('map.basemapLocal') :
                          'Схематична карта') + ' · ' + why + ' · ' + location.origin;
      note.setAttribute('title', why + ' [origin ' + location.origin + ']');
      announce(f, 'local');
    });
    // Not console.error: this is a supported state, not a fault.
    /* console.warn, not info: this line is the whole explanation for a screen
     * with no basemap on it, and an info-level message is filtered out of most
     * consoles and most bug reports. A reviewer reporting "zero console errors"
     * while looking at a stood-down map is reporting accurately and still
     * missing the only sentence that mattered. */
    if (window.console) console.warn('map-tiles: SVG basemap in use — ' + why);
  }

  /* MapLibre 6 is ESM-ONLY and has no UMD build, so there is no global to
   * check for and no classic <script> that can load it. It is pulled in with a
   * dynamic import(), which works from this ordinary script and keeps the
   * pages free of type="module" — one loading strategy for every file the kit
   * ships.
   *
   * 6.x also DROPPED `maplibregl.supported()`. Feature-detecting WebGL is now
   * ours to do, and it has to happen before the import: pulling 1 MB of
   * renderer onto a machine that cannot draw it is a pointless download and a
   * pointless failure. */
  function webglOK() {
    try {
      var c = document.createElement('canvas');
      return !!(c.getContext('webgl2') || c.getContext('webgl'));
    } catch (err) { return false; }
  }
  if (!window.pmtiles) { stand_down('pmtiles not loaded'); return; }
  if (!webglOK()) { stand_down('WebGL unavailable'); return; }

  /* Probe before mounting. MapLibre reports a cross-origin style failure as an
   * `error` event after it has already emptied the container, so asking first
   * is what keeps the fallback clean rather than leaving a grey canvas behind. */
  fetch(STYLE, { mode: 'cors' })
    .then(function (r) { return r.ok ? r.json() : Promise.reject(new Error('HTTP ' + r.status)); })
    .then(function (style) {
      /* The served style hardcodes its own public host in sources[].url and in
       * glyphs, so overriding the tiles base repointed the STYLE fetch and
       * nothing else: the archive and the glyphs still went to production, the
       * CSP refused them, and the kit stood down to the SVG basemap. The
       * override looked live — AIRBG_ORIGINS reported the new base — while the
       * only request it actually redirected was the first one.
       *
       * A base that moves some of its own URLs and not the rest is the dead
       * control this system keeps hitting. Rebase whatever the style says about
       * its own origin onto the base the reader asked for. No-op in production,
       * where the two are already the same string. */
      rebase(style, TILES);
      /* Resolved against the DOCUMENT, not this script — the screens all sit
       * at ui_kits/app/, so this is the same ../../ every other asset uses. */
      return import('../../assets/vendor/maplibre-gl.mjs').then(function (gl) {
        mount(style, gl);
      });
    })
    .catch(function (e) { stand_down('style or renderer unavailable (' + e.message + ')'); });

  /* Rewrite every absolute URL in the style that points at the style's OWN
   * origin so it points at `base` instead. Only that origin is touched: a
   * style legitimately referencing a third host keeps it. */
  function rebase(style, base) {
    /* `from` is the origin the STYLE declares about itself, which is the only
     * thing that knows it — deriving it from STYLE instead is worthless, since
     * STYLE is built from `base` and the two are then always equal. The first
     * version did exactly that and was a guaranteed no-op in the one case it
     * existed for. Read it off the style's own source URL. */
    var from = null;
    Object.keys(style.sources || {}).some(function (k) {
      var src = style.sources[k] || {};
      var u = src.url || (Array.isArray(src.tiles) ? src.tiles[0] : null);
      if (typeof u !== 'string') return false;
      var m = u.match(/https?:\/\/[^/]+/);
      if (m) { from = m[0]; return true; }
      return false;
    });
    if (!from && typeof style.glyphs === 'string') {
      var g = style.glyphs.match(/https?:\/\/[^/]+/);
      if (g) from = g[0];
    }
    if (!from || from === base) return;
    var swap = function (u) {
      return (typeof u === 'string' && u.indexOf(from) !== -1) ? u.split(from).join(base) : u;
    };
    if (style.glyphs) style.glyphs = swap(style.glyphs);
    if (style.sprite) style.sprite = swap(style.sprite);
    Object.keys(style.sources || {}).forEach(function (k) {
      var src = style.sources[k];
      if (!src) return;
      if (src.url) src.url = swap(src.url);
      if (Array.isArray(src.tiles)) src.tiles = src.tiles.map(swap);
    });
  }

  function mount(style, gl) {
    /* Named ESM exports now: 6.x has no default export and no global. */
    var proto = new window.pmtiles.Protocol();
    gl.addProtocol('pmtiles', proto.tile);

    frames.forEach(function (frame) {
      var canvas = frame.querySelector('.map-canvas');
      if (!canvas) return;

      var host = document.createElement('div');
      host.className = 'map-tiles';
      host.setAttribute('aria-hidden', 'true');   // the SVG over it carries the names
      frame.insertBefore(host, canvas);

      var map = new gl.Map({
        container: host,
        style: style,
        center: [25.4858, 42.7339],               // the country's own centre
        zoom: 6.2,
        /* The reader can zoom out past Bulgaria, because there is now something
         * out there: the tile basemap runs past the border and the hexes carry
         * readings from the neighbours. Out was previously refused at the
         * country fit, which was right when the map was a Bulgaria-only
         * choropleth and became wrong the moment it stopped being one.
         *
         * 4.5 is where the drawn context window (lon 19,5–31,5, lat 38–46,5)
         * fills the frame — Bulgaria and every neighbour it shares a border
         * with. Below that the map is Europe carrying a Bulgaria-shaped
         * dataset, which answers nothing, and the hexes fall under the 3px
         * floor and stop drawing anyway. A limit the reader reaches and sees
         * stated beats one that lets them zoom into an empty answer. */
        minZoom: 4.5,
        maxZoom: 17,
        /* Bearing and pitch are LOCKED, and that is load-bearing rather than
         * taste: the SVG overlay projects longitude through X() and latitude
         * through Y() separately, which is exact in Web Mercator only while
         * the camera is north-up and flat. Allow rotation and the data layer
         * shears away from the basemap. */
        bearing: 0, pitch: 0, pitchWithRotate: false, dragRotate: false,
        attributionControl: { compact: true }     // ODbL: the credit ships with the data
      });
      map.touchZoomRotate.disableRotation();
      map.keyboard.disableRotation();

      /* The overlay reads the camera through these two functions. `size` and
       * `zoom` ride along so the renderer can set a pixel-space viewBox and
       * decide when the choropleth should yield to the basemap. */
      function publish() {
        var c = map.getCanvas();
        window.AIRBG_MAP_PROJECT = {
          frame: frame,
          zoom: map.getZoom(),
          size: { w: c.clientWidth, h: c.clientHeight },
          x: function (lon) { return map.project([lon, 0]).x; },
          y: function (lat) { return map.project([0, lat]).y; }
        };
      }

      /* One repaint per animation frame, never one per event: a drag fires
       * `move` dozens of times a second and the data pass is not free. Same
       * rule the resize observer already follows. */
      var pending = 0;
      function repaint() {
        if (pending) return;
        pending = requestAnimationFrame(function () {
          pending = 0;
          publish();
          if (window.AIRBG_DATA && window.AIRBG_MAP_DRAW) window.AIRBG_MAP_DRAW();
        });
      }

      /* THE CAMERA HAS TO BE TOLD WHAT THE PAGE IS ABOUT.
       *
       * A province page opens on its subject (§5.2b). The SVG renderer works
       * that framing out for itself — but under the tiles every coordinate is
       * projected by THIS camera, so the fit it computed is discarded and the
       * page opened on the whole country with София-град somewhere inside it.
       * The framing was correct in the code and absent on the screen, which is
       * exactly the class of defect only a browser catches.
       *
       * The renderer publishes the box it wants in lon/lat and never moves the
       * camera; this applies it once per SUBJECT. Keyed that way, a reader who
       * has zoomed in keeps their view — the camera only re-fits when the
       * province itself changes (`airbg:oblastchange`). */
      var fittedKey = null;
      function fitSubject() {
        var b = frame.__airbgWantBounds, k = frame.__airbgWantKey || 'country';
        if (k === fittedKey) return;
        fittedKey = k;
        if (!b) return;
        map.fitBounds(b, { padding: 32, animate: false, maxZoom: 13 });
      }

      map.on('load', function () {
        if (downed) return;   // a tile error already handed this frame back
        frame.setAttribute('data-basemap', 'tiles');
        announce(frame, 'tiles', map);
        publish();
        /* Draw first: the renderer publishes the wanted box as it draws, so
         * there is nothing to fit to until one pass has run. */
        if (window.AIRBG_DATA && window.AIRBG_MAP_DRAW) window.AIRBG_MAP_DRAW();
        fitSubject();
        repaint();
      });

      /* The province can change in place, without a navigation (§5.2b). The
       * renderer redraws on the same event and rewrites the wanted box; this
       * runs after it, so the camera follows the new subject. */
      document.addEventListener('airbg:oblastchange', function () {
        setTimeout(fitSubject, 0);
      });
      map.on('move', repaint);
      map.on('resize', repaint);

      /* If the archive itself fails after the style loaded — a missing range
       * request, a 404 on the pmtiles file — the map would sit there empty.
       * Hand the frame back to the SVG rather than leaving the reader with a
       * blank rectangle. */
      map.on('error', function (e) {
        var msg = (e && e.error && e.error.message) || 'unknown';
        if (frame.getAttribute('data-basemap') === 'failed') return;
        frame.setAttribute('data-basemap', 'failed');
        announce(frame, 'failed');
        window.AIRBG_MAP_PROJECT = null;
        host.remove();
        stand_down('tile error (' + msg + ')');
        if (window.AIRBG_DATA && window.AIRBG_MAP_DRAW) window.AIRBG_MAP_DRAW();
      });

      frame.__airbgTileMap = map;                 // for the zoom/pan controls
      /* Reaching the camera from a devtools console is the difference between
       * one `evaluate` and six screenshots when diagnosing a blank map. It is
       * published only where origins.js already allows an override — i.e. never
       * on airbg.org — so production gains no new global. */
      if (window.AIRBG_ORIGINS && window.AIRBG_ORIGINS.overridden()) {
        window.AIRBG_TILEMAP = map;
      }
    });
  }
})();
