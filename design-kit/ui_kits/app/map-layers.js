/* What the basemap shows — the reader's choice, not a fixed picture.
 *
 * WHY THIS EXISTS
 * ---------------
 * The archive has carried streets, buildings, parks, schools, shops and
 * transport since the first build: planetiler's default profile emits the whole
 * OpenMapTiles set and `docs/tiles.md` names `poi` in it. What was missing was
 * layers in style.json that drew any of it — so "the map has no features" was a
 * statement about our selection, never about the data. A style is a selection,
 * not an inventory.
 *
 * With the layers added, the opposite risk arrives: a city at z16 carrying every
 * shop in it is a second dataset competing with the readings the reader came
 * for. So the categories are switchable, and this is the control.
 *
 * THE OPTIONS COME FROM THE STYLE, NOT FROM A LIST HERE
 * ----------------------------------------------------
 * Every layer in style.json carries `metadata["airbg:group"]`. This file reads
 * those groups off the mounted style and offers one checkbox per group. A list
 * of layer ids typed into this file would be a second thing free to drift from
 * the style it claims to describe — the same reason the neighbour labels read
 * `borders_bg` out of the asset instead of a list in the renderer (§5.2).
 *
 * Add a layer to the style tomorrow and it appears here with no change to this
 * file. A group with no catalogue string shows its own key, which is a visible
 * gap rather than a silent omission (§5.12).
 *
 * IT IS NOT OFFERED WHEN THERE IS NOTHING TO SWITCH
 * ------------------------------------------------
 * Opened from file://, from a preview host, with WebGL refused, or after a tile
 * error, the SVG basemap draws and there are no tile layers at all. A control
 * over layers that do not exist is the dead control this system has hit more
 * often than any other defect, so the whole disclosure is `hidden` until
 * map-tiles.js reports a mounted camera.
 */
(function () {
  var KEY = 'airbg:map-layers';

  /* The order a reader thinks in: the ground first, then what is built on it,
   * then what is in the buildings. A group absent from the style is skipped, so
   * this is an ordering, not a second list of what exists. */
  var ORDER = [
    'base', 'water', 'roads', 'street-names', 'buildings', 'places', 'boundaries',
    'poi-education', 'poi-health', 'poi-shop', 'poi-transport', 'poi-other'
  ];

  function t(key, fallback) {
    var s = window.AIRBG_T ? window.AIRBG_T(key) : key;
    return (s && s !== key) ? s : fallback;
  }

  function stored() {
    try { return JSON.parse(localStorage.getItem(KEY)) || {}; } catch (e) { return {}; }
  }
  function store(state) {
    try { localStorage.setItem(KEY, JSON.stringify(state)); } catch (e) { /* private mode */ }
  }

  document.querySelectorAll('[data-od-id="map-layers"]').forEach(function (root) {
    var btn = root.querySelector('button');
    var panel = root.querySelector('.colmenu__panel');
    var legend = panel.querySelector('legend');
    /* The options are appended to the fieldset itself, beside its legend —
     * the column menu's own shape (§5.11). A wrapper div would sit between
     * the fieldset and the rules that lay its options out. */
    var list = panel.querySelector('fieldset');
    var map = null, groups = [];

    /* One record of one state. `aria-expanded` already carries whether the
     * panel is open, so nothing toggles a class beside it (§10). */
    function open(yes) {
      btn.setAttribute('aria-expanded', yes ? 'true' : 'false');
      panel.hidden = !yes;
    }
    btn.addEventListener('click', function () {
      open(btn.getAttribute('aria-expanded') !== 'true');
    });
    /* Escape closes and returns focus to the button; a click outside closes
     * without stealing it (§5.11). */
    root.addEventListener('keydown', function (e) {
      if (e.key === 'Escape' && !panel.hidden) { open(false); btn.focus(); }
    });
    document.addEventListener('mousedown', function (e) {
      if (!panel.hidden && !root.contains(e.target)) open(false);
    });

    function apply(group, on) {
      if (!map) return;
      map.getStyle().layers.forEach(function (l) {
        if (l.metadata && l.metadata['airbg:group'] === group) {
          map.setLayoutProperty(l.id, 'visibility', on ? 'visible' : 'none');
        }
      });
    }

    /* Rebuilt on a language change, because every label here is composed at
     * render time and `data-i18n` cannot reach copy that did not exist when the
     * swap walked the DOM (§5.12). */
    function render() {
      btn.textContent = t('layers.button', 'Слоеве');
      btn.setAttribute('title', t('layers.title', 'Какво да показва картата'));
      legend.textContent = t('layers.legend', 'Показвай на картата');
      list.querySelectorAll('.colmenu__opt').forEach(function (n) { n.remove(); });

      var state = stored();
      groups.forEach(function (g) {
        var label = document.createElement('label');
        label.className = 'colmenu__opt';
        var input = document.createElement('input');
        input.type = 'checkbox';
        input.checked = state[g] !== false;      // everything on until switched off
        input.setAttribute('data-layer-group', g);
        var span = document.createElement('span');
        span.textContent = t('layers.' + g, g);
        label.appendChild(input);
        label.appendChild(span);
        list.appendChild(label);

        input.addEventListener('change', function () {
          var s = stored();
          s[g] = input.checked;
          store(s);
          apply(g, input.checked);
        });
        apply(g, input.checked);
      });
    }
    document.addEventListener('airbg:languagechange', function () { if (map) render(); });

    /* map-tiles.js owns whether a camera exists and says so on this event. This
     * file never probes for one: two things deciding whether the basemap is
     * live is two things free to disagree about it. */
    document.addEventListener('airbg:basemapchange', function (e) {
      var d = e.detail || {};
      if (d.state !== 'tiles' || !d.map) {
        map = null;
        root.hidden = true;
        open(false);
        return;
      }
      map = d.map;
      groups = ORDER.filter(function (g) {
        return map.getStyle().layers.some(function (l) {
          return l.metadata && l.metadata['airbg:group'] === g;
        });
      });
      /* A style whose layers carry no groups yields no options, and a panel of
       * nothing is worse than no panel. */
      if (!groups.length) { map = null; root.hidden = true; return; }
      root.hidden = false;
      render();
    });
  });
})();
