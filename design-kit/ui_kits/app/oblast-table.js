/* oblast-table.js — search, filter and sort for the 28-oblast table (§5.4).
 *
 * External file, not an inline <script>: the app's CSP has no inline allowance
 * (§1), and a kit that demonstrates a pattern the real site cannot ship is
 * worse than no demonstration.
 *
 * Three rules this file exists to enforce:
 *   1. No-data oblasti sort last in EVERY order, ascending included. A reader
 *      looking for bad air must never scroll past eight blanks (§5.4).
 *   2. The count line is always honest — it states the shown total against 28,
 *      so a filtered view can never be mistaken for the whole country.
 *   3. Absence is not an error. Zero search hits gets --muted body copy, no
 *      colour and no icon (§2.3).
 *   4. The combobox filters the table and offers the names; picking one is
 *      always equivalent to having typed it. There is no hidden third state.
 *   5. Column visibility never hides the oblast name, never empties the table,
 *      and never contradicts a sort the reader can still see.
 *   6. Paging is applied LAST, after filter and sort. Both invariants above
 *      therefore survive it: page 3 of an ascending sort still cannot contain a
 *      no-data row above a measured one. Default is "all" — 28 rows fit on one
 *      screen, and four clicks to read a country is worse than one scroll.
 */
(function () {
  'use strict';

  var table = document.querySelector('table[data-od-id="oblast-table"]');
  // Fail loudly. A silent `return` here once let a duplicated data-od-id on an
  // ancestor <main> match first: querySelector handed back the wrapper, the
  // very next line threw, and every control on the page — search, sort, paging,
  // the column menu — was dead with nothing on screen to say so.
  if (!table || !table.tBodies[0]) {
    console.error('oblast-table: no <table data-od-id="oblast-table"> found. ' +
                  'Check that no other element reuses that data-od-id.');
    return;
  }

  var tbody = table.tBodies[0];
  var heads = Array.prototype.slice.call(table.querySelectorAll('th[data-sort-key]'));
  var rows = Array.prototype.slice.call(tbody.rows);
  var search = document.getElementById('oblast-search');
  var listbox = document.getElementById('oblast-listbox');
  var colBtn = document.getElementById('oblast-colmenu-btn');
  var colPanel = document.getElementById('oblast-colmenu-panel');
  var colBoxes = Array.prototype.slice.call(colPanel.querySelectorAll('input[data-col]'));
  var perPage = document.getElementById('oblast-perpage');
  var pager = document.getElementById('oblast-pager');
  var pagerNav = document.getElementById('oblast-pager-nav');
  var pagerStatus = document.getElementById('oblast-pager-status');
  var empty = document.getElementById('oblast-empty');
  var count = document.getElementById('oblast-count');
  var TOTAL = rows.length;

  /* The default filter is "С данни", not "Всички".
 *
 * §5.4 forbids a truncated list, and this is deliberately NOT that. Truncation
 * is silent, unexplained and has no way back; this is a labelled radio sitting
 * above the table with its own state visible, one click from the whole set, and
 * a count line that says how many rows it is holding back and why. The rule was
 * written against a reader who cannot tell "no sensors" from "cut off" — that
 * reader is told both here.
 */
  var state = { key: 'value', dir: 'descending', q: '', filter: 'withdata', perPage: 'all', page: 1 };

  /* data-name holds the Bulgarian name, and that is the row's IDENTITY: the
   * key into the catalogue, stable across languages. It is NOT the label.
   * Reading it directly is what left the combobox in Bulgarian on an English
   * page, and it also meant typing "Sofia" matched nothing — the search was
   * comparing against Cyrillic the reader could no longer see.
   */
  /* i18n.js must be loaded FIRST. It was not: the script tags read
   * oblast-table.js → i18n.js, so every t() on the first paint fell through to
   * its own key and the reader saw literal "runtime.allOption" in the
   * rows-per-page select. A fallback that returns the key is right for a
   * *missing* key and wrong for a missing catalogue — so say so, loudly, once.
   */
  if (!window.AIRBG_T) {
    console.error('oblast-table: i18n.js must load before oblast-table.js. ' +
      'Every string will render as its own key until the script order is fixed.');
  }
  function lang() { return window.AIRBG_LANG ? window.AIRBG_LANG() : 'bg'; }
  function t(key, vars) { return window.AIRBG_T ? window.AIRBG_T(key, vars) : key; }
  function nameOf(row) {
    var bg = row.getAttribute('data-name');
    return window.AIRBG_NAME ? window.AIRBG_NAME(bg) : bg;
  }
  function fold(str) { return str.toLocaleLowerCase(lang()); }

  // Cyrillic sorts with the Bulgarian collator, not by code point: "ь" and the
  // digraph-ish names order the way a Bulgarian reader expects. In English the
  // same names are Latin, so the collator follows the language.
  var collator = new Intl.Collator(lang());

  function hasData(row) { return !row.hasAttribute('data-nodata'); }
  function num(row, attr) { return parseFloat(row.getAttribute(attr)); }

  function compare(a, b) {
    // Invariant 1: absence always sinks, whatever the direction.
    if (hasData(a) !== hasData(b)) return hasData(a) ? -1 : 1;

    var sign = state.dir === 'ascending' ? 1 : -1;
    if (state.key === 'name') {
      return collator.compare(nameOf(a), nameOf(b)) * sign;
    }
    // A silent row has no reading, but it does have sensor counts — so those
    // two columns sort normally even among the silent. On a PM column there is
    // nothing to compare, so they stay alphabetical rather than arbitrary.
    if (!hasData(a) && state.key !== 'sensors') {
      return collator.compare(nameOf(a), nameOf(b));
    }
    var attr = { sensors: 'data-sensors', pm10: 'data-pm10' }[state.key] || 'data-value';
    return (num(a, attr) - num(b, attr)) * sign;
  }

  function matches(row) {
    if (state.filter === 'withdata' && !hasData(row)) return false;
    if (state.filter === 'nodata' && hasData(row)) return false;
    if (!state.q) return true;
    return fold(nameOf(row)).indexOf(state.q) !== -1;
  }

  /* Page sizes are derived from the CURRENTLY VISIBLE row count, not from the
   * table's total. Под филтъра "Без данни" there are 8 rows, and offering 21 or
   * 14 there is offering nothing — both show all 8.
   *
   * Only true divisors qualify, so every page is exactly full and no split ever
   * ends in a stub. (An earlier set — 28/21/14/7 — was described as "the
   * divisors of 28"; 21 is not one. It splits 28 into 21 + 7, which is the very
   * stub the rule exists to prevent.)
   *
   * A page must hold at least MIN_PAGE rows: dividing 8 rows into fours is
   * useful, into twos is not. If nothing qualifies the whole control hides,
   * because a select with one option is a control that cannot do anything.
   */
  var MIN_PAGE = 3;
  var MAX_OPTIONS = 3;

  function sizesFor(count) {
    var out = [];
    for (var d = count - 1; d > 1 && out.length < MAX_OPTIONS; d--) {
      if (count % d === 0 && d >= MIN_PAGE) out.push(d);
    }
    return out;
  }

  // Rebuild only when the visible count actually changes: re-writing the list on
  // every render would clobber the select while the reader is inside it.
  var lastCount = -1;

  function syncPageSizes(count) {
    if (count === lastCount) return;
    lastCount = count;

    var sizes = sizesFor(count);
    var wanted = state.perPage;

    perPage.textContent = '';
    var all = document.createElement('option');
    all.value = 'all';
    all.textContent = t('runtime.allOption');
    perPage.appendChild(all);

    sizes.forEach(function (n) {
      var o = document.createElement('option');
      o.value = String(n);
      o.textContent = String(n);
      perPage.appendChild(o);
    });

    // Keep the reader's choice if it still divides the new set; otherwise the
    // only honest fallback is the whole set.
    if (wanted !== 'all' && sizes.indexOf(parseInt(wanted, 10)) === -1) {
      state.perPage = 'all';
      state.page = 1;
    }
    perPage.value = state.perPage;

    // Nothing worth paging: hide the control rather than show a lone option.
    var wrap = perPage.closest ? perPage.closest('.pager__perpage') : null;
    if (wrap) wrap.hidden = sizes.length === 0;
  }

  // 'all' resolves to a finite size, never Infinity: `(page - 1) * Infinity` is
  // `0 * Infinity` = NaN, and `slice(NaN, NaN)` empties the table.
  function pageSize() {
    var n = state.perPage === 'all' ? rows.length : parseInt(state.perPage, 10);
    return n > 0 ? n : rows.length || 1;
  }

  function render() {
    // Order first...
    rows.slice().sort(compare).forEach(function (row) { tbody.appendChild(row); });

    // ...then narrow. `visible` is the filtered set in sorted order; paging
    // only ever slices this, so it can never reorder anything.
    var visible = rows.filter(matches);
    syncPageSizes(visible.length);
    var size = pageSize();
    var pages = Math.max(1, Math.ceil(visible.length / size));

    // A filter or a search can shrink the set out from under the current page.
    // Clamp rather than showing an empty page the reader did not ask for.
    if (state.page > pages) state.page = pages;
    if (state.page < 1) state.page = 1;

    var from = (state.page - 1) * size;
    var to = from + size;
    var onPage = visible.slice(from, to);

    rows.forEach(function (row) { row.hidden = onPage.indexOf(row) === -1; });

    var blanks = onPage.filter(function (row) { return !hasData(row); }).length;

    heads.forEach(function (th) {
      if (th.getAttribute('data-sort-key') === state.key) th.setAttribute('aria-sort', state.dir);
      else th.removeAttribute('aria-sort');
    });

    // Page sizes are never disabled. An earlier rule greyed out any size at or
    // above the filtered count, on the theory that it duplicated Всички — but
    // 21 against 20 filtered rows is a one-row difference, and the reader sees
    // an option that simply stops working. A size larger than the set is
    // harmless: it shows every row, the pager hides itself at one page, and
    // nothing on screen misleads. The guard protected against a non-problem and
    // created a real one.

    // Only the page BUTTONS hide at one page — the rows-per-page select lives
    // in this same bar and must stay reachable, or the reader loses the control
    // that creates pages in the first place. A single disabled page control is
    // noise; a missing page-size control is a dead end.
    pagerNav.hidden = pages < 2;
    pagerStatus.textContent = t('runtime.pagerStatus', { page: state.page, pages: pages });
    setDisabled('first', state.page === 1);
    setDisabled('prev', state.page === 1);
    setDisabled('next', state.page === pages);
    setDisabled('last', state.page === pages);

    empty.hidden = visible.length !== 0;
    // Invariant 2: the denominator is always 28, never the page and never the
    // filtered set. A paged view states its range so it cannot read as the whole.
    if (visible.length === TOTAL && pages === 1) {
      count.textContent = t('runtime.countAll', { total: TOTAL, blanks: blanks });
    } else if (pages > 1) {
      count.textContent = t('runtime.countPaged', {
        from: from + 1, to: from + onPage.length, shown: visible.length, total: TOTAL });
    } else if (!state.q && state.filter !== 'all') {
      // A filter that hides rows must account for them. Otherwise the default
      // view is indistinguishable from a country with 20 oblasti.
      count.textContent = t(state.filter === 'withdata'
        ? 'runtime.countWithData' : 'runtime.countNoData',
        { shown: visible.length, total: TOTAL, hidden: TOTAL - visible.length });
    } else {
      count.textContent = t('runtime.countFiltered', {
        shown: visible.length, total: TOTAL,
        tail: blanks ? t('runtime.countTail', { blanks: blanks }) : '' });
    }
  }

  function setDisabled(which, off) {
    var btn = pager.querySelector('[data-page="' + which + '"]');
    if (btn) btn.disabled = off;   // markup may omit a control; never throw here
  }

  heads.forEach(function (th) {
    th.querySelector('.th-sort').addEventListener('click', function () {
      var key = th.getAttribute('data-sort-key');
      if (state.key === key) {
        state.dir = state.dir === 'ascending' ? 'descending' : 'ascending';
      } else {
        state.key = key;
        // Names read best A→Я; measurements read best worst-first.
        state.dir = key === 'name' ? 'ascending' : 'descending';
      }
      state.page = 1;
      render();
    });
  });

  /* ------------------------------------------------------------ combobox
   * The search field is an ARIA 1.2 combobox over the 28 names. Three things
   * happen as you type, and they are deliberately separate:
   *   - the TABLE filters live (it always did);
   *   - the LISTBOX shows the matching names, so you can pick instead of type;
   *   - the INPUT inline-completes to the first match, with the added part
   *     selected, so the next keystroke overwrites it.
   * Inline completion only runs when you are ADDING characters. Completing
   * during a backspace re-adds what you just deleted, and the field becomes
   * impossible to clear — the classic autocomplete trap.
   */
  var activeIndex = -1;
  var options = [];
  var lastValue = '';

  function optionRows() {
    var q = state.q;
    return rows.filter(function (row) {
      if (state.filter === 'withdata' && !hasData(row)) return false;
      if (state.filter === 'nodata' && hasData(row)) return false;
      return !q || nameOf(row).toLocaleLowerCase('bg').indexOf(q) !== -1;
    }).sort(function (a, b) {
      // Prefix matches first — typing "Пл" should offer Пловдив before Плевен
      // only if it actually starts that way; otherwise alphabetical.
      var qa = nameOf(a).toLocaleLowerCase('bg').indexOf(state.q) === 0;
      var qb = nameOf(b).toLocaleLowerCase('bg').indexOf(state.q) === 0;
      if (qa !== qb) return qa ? -1 : 1;
      return collator.compare(nameOf(a), nameOf(b));
    });
  }

  function markup(name) {
    if (!state.q) return escapeHtml(name);
    var at = fold(name).indexOf(state.q);
    if (at === -1) return escapeHtml(name);
    return escapeHtml(name.slice(0, at)) +
      '<mark>' + escapeHtml(name.slice(at, at + state.q.length)) + '</mark>' +
      escapeHtml(name.slice(at + state.q.length));
  }

  function escapeHtml(str) {
    return str.replace(/[&<>"]/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c];
    });
  }

  function buildList() {
    options = optionRows();
    listbox.textContent = '';

    if (!options.length) {
      var none = document.createElement('li');
      none.className = 'combobox__empty';
      // Absence is stated, not flagged (§2.3).
      none.textContent = t('runtime.comboEmpty');
      listbox.appendChild(none);
      return;
    }

    options.forEach(function (row, i) {
      var name = nameOf(row);
      var li = document.createElement('li');
      li.className = 'combobox__opt';
      li.id = 'oblast-opt-' + i;
      li.setAttribute('role', 'option');
      li.setAttribute('aria-selected', 'false');

      var label = document.createElement('span');
      label.innerHTML = markup(name);
      li.appendChild(label);

      // The option is the name and nothing else. This list exists to find a
      // row, not to report one: the reading is in the table a line below, and
      // repeating it here turned the dropdown into a second, narrower table.

      // mousedown, not click: click fires after the input's blur, which would
      // have already closed the list out from under the pointer.
      li.addEventListener('mousedown', function (e) {
        e.preventDefault();
        choose(i);
      });
      listbox.appendChild(li);
    });
  }

  function openList() {
    buildList();
    listbox.hidden = false;
    search.setAttribute('aria-expanded', 'true');
  }

  function closeList() {
    listbox.hidden = true;
    search.setAttribute('aria-expanded', 'false');
    search.removeAttribute('aria-activedescendant');
    activeIndex = -1;
  }

  function highlight(i) {
    var items = listbox.querySelectorAll('.combobox__opt');
    Array.prototype.forEach.call(items, function (li) { li.setAttribute('aria-selected', 'false'); });
    activeIndex = i;
    if (i < 0 || !items[i]) { search.removeAttribute('aria-activedescendant'); return; }
    items[i].setAttribute('aria-selected', 'true');
    search.setAttribute('aria-activedescendant', items[i].id);
    // Keep the active option in view without scrollIntoView, which can break
    // the embedded preview.
    var li = items[i];
    if (li.offsetTop < listbox.scrollTop) listbox.scrollTop = li.offsetTop;
    else if (li.offsetTop + li.offsetHeight > listbox.scrollTop + listbox.clientHeight) {
      listbox.scrollTop = li.offsetTop + li.offsetHeight - listbox.clientHeight;
    }
  }

  function choose(i) {
    if (!options[i]) return;
    search.value = nameOf(options[i]);
    state.q = search.value.toLocaleLowerCase('bg');
    state.page = 1;
    closeList();
    render();
  }

  function applyQuery(complete) {
    var typed = search.value;
    state.q = typed.trim().toLocaleLowerCase('bg');
    state.page = 1;

    if (complete && state.q) {
      var first = optionRows()[0];
      // Only complete a genuine prefix. Completing a mid-string match would
      // rewrite what the reader typed into something they did not.
      if (first && nameOf(first).toLocaleLowerCase('bg').indexOf(state.q) === 0 &&
          nameOf(first).length > typed.length) {
        var full = nameOf(first);
        search.value = typed + full.slice(typed.length);
        search.setSelectionRange(typed.length, full.length);
      }
    }

    openList();
    highlight(-1);
    render();
  }

  search.addEventListener('input', function () {
    // Adding characters, not deleting: only then is completion safe.
    var adding = search.value.length > lastValue.length;
    lastValue = search.value;
    applyQuery(adding);
  });

  search.addEventListener('focus', function () {
    lastValue = search.value;
    openList();
  });

  search.addEventListener('blur', function () { closeList(); });

  search.addEventListener('keydown', function (e) {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      if (listbox.hidden) openList();
      if (!options.length) return;
      var next = e.key === 'ArrowDown' ? activeIndex + 1 : activeIndex - 1;
      if (next >= options.length) next = 0;
      if (next < 0) next = options.length - 1;
      highlight(next);
    } else if (e.key === 'Home' && !listbox.hidden && options.length) {
      e.preventDefault(); highlight(0);
    } else if (e.key === 'End' && !listbox.hidden && options.length) {
      e.preventDefault(); highlight(options.length - 1);
    } else if (e.key === 'Enter') {
      if (activeIndex > -1) { e.preventDefault(); choose(activeIndex); }
      else closeList();
    } else if (e.key === 'Escape') {
      // First Escape closes the list; a second clears the field. Escape should
      // never destroy a query the reader can still see a use for.
      if (!listbox.hidden) closeList();
      else if (search.value) { search.value = ''; lastValue = ''; applyQuery(false); closeList(); }
    } else if (e.key === 'Tab') {
      closeList();
    }
  });

  perPage.addEventListener('change', function () {
    state.perPage = perPage.value;
    state.page = 1;
    render();
  });

  pager.querySelectorAll('[data-page]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var visible = rows.filter(matches);
      var pages = Math.max(1, Math.ceil(visible.length / pageSize()));
      var to = btn.getAttribute('data-page');
      if (to === 'first') state.page = 1;
      else if (to === 'prev') state.page -= 1;
      else if (to === 'next') state.page += 1;
      else if (to === 'last') state.page = pages;
      render();
    });
  });

  document.querySelectorAll('[data-od-id="data-filter"] input').forEach(function (input) {
    input.addEventListener('change', function () {
      state.filter = input.value;
      state.page = 1;
      if (!listbox.hidden) openList();   // the option set narrowed with it
      render();
    });
  });

  /* ------------------------------------------------------- column menu
   * Which columns are shown is a view preference, so it persists. The three
   * rules it has to hold are all about not stranding the reader:
   *
   *   - The oblast name is never hideable. It is the row's identity; a table of
   *     anonymous numbers is not a table. It has no checkbox at all rather than
   *     a disabled one, because it is not a choice being denied.
   *   - The last visible data column cannot be switched off. Its checkbox goes
   *     disabled, which says so, instead of the click being silently ignored.
   *   - Hiding the sorted column re-sorts. An order justified by a column the
   *     reader can no longer see is an unexplained order.
   */
  var COLS = ['pm25', 'pm10', 'sensors'];
  var SORT_COL = { value: 'pm25', pm10: 'pm10', sensors: 'sensors' };
  var STORE = 'airbg.oblast-table.columns';

  function hiddenCols() {
    return colBoxes.filter(function (b) { return !b.checked; })
                   .map(function (b) { return b.getAttribute('data-col'); });
  }

  function applyColumns() {
    var hidden = hiddenCols();
    table.setAttribute('data-hidden', hidden.join(' '));

    // Guard the last one: if exactly one data column is left, lock its box.
    var shown = colBoxes.filter(function (b) { return b.checked; });
    colBoxes.forEach(function (b) { b.disabled = shown.length === 1 && b.checked; });

    // The "no data" cell spans the PM columns, so its colspan is not a
    // constant — it is however many of them are currently visible.
    var pmVisible = COLS.slice(0, 2).filter(function (c) { return hidden.indexOf(c) === -1; }).length;
    rows.forEach(function (row) {
      var cell = row.querySelector('.nodata');
      if (!cell) return;
      cell.hidden = pmVisible === 0;
      if (pmVisible > 0) cell.colSpan = pmVisible;
    });

    // If the column the table is sorted by just went away, fall back to the
    // first visible column rather than leaving an order nothing explains.
    if (hidden.indexOf(SORT_COL[state.key] || state.key) !== -1) {
      var fallback = COLS.filter(function (c) { return hidden.indexOf(c) === -1; })[0];
      state.key = fallback === 'pm25' ? 'value' : fallback;
      state.dir = 'descending';
    }

    try { localStorage.setItem(STORE, JSON.stringify(hidden)); } catch (e) { /* private mode */ }
  }

  function restoreColumns() {
    var saved;
    try { saved = JSON.parse(localStorage.getItem(STORE) || '[]'); } catch (e) { saved = []; }
    if (!Array.isArray(saved)) saved = [];
    // Never restore a state with nothing left; a stale or hand-edited value
    // must not be able to empty the table.
    if (saved.length >= COLS.length) saved = [];
    colBoxes.forEach(function (b) {
      b.checked = saved.indexOf(b.getAttribute('data-col')) === -1;
    });
  }

  function openCols() {
    colPanel.hidden = false;
    colBtn.setAttribute('aria-expanded', 'true');
  }
  function closeCols(refocus) {
    colPanel.hidden = true;
    colBtn.setAttribute('aria-expanded', 'false');
    if (refocus) colBtn.focus();
  }

  colBtn.addEventListener('click', function () {
    if (colPanel.hidden) openCols(); else closeCols(false);
  });

  colBoxes.forEach(function (box) {
    box.addEventListener('change', function () { applyColumns(); render(); });
  });

  // Escape closes and returns focus to the button; a click outside just closes.
  colPanel.addEventListener('keydown', function (e) {
    if (e.key === 'Escape') { e.stopPropagation(); closeCols(true); }
  });
  colBtn.addEventListener('keydown', function (e) {
    if (e.key === 'Escape' && !colPanel.hidden) closeCols(true);
  });
  document.addEventListener('mousedown', function (e) {
    if (colPanel.hidden) return;
    if (!colPanel.contains(e.target) && e.target !== colBtn) closeCols(false);
  });

  /* Re-render on a language change. The collator is rebuilt because the sort
   * order genuinely differs: Bulgarian names sort in Cyrillic, English ones in
   * Latin, and "Sofia" does not sit where "Софийска" did. The page-size list is
   * forced to rebuild too, since its labels are translated.
   */
  /* New readings arrive from airbg-data.js. The rows are the same DOM nodes —
   * only their values change — so the reader's sort, filter, search and page
   * all survive a refresh. Rebuilding the rows would silently reset every one
   * of those, which is the "changing it under their cursor" defect (§5.4). */
  document.addEventListener('airbg:datachange', function (e) {
    var data = e.detail;
    if (!data || !data.oblasti) return;
    var by = {};
    data.oblasti.forEach(function (o) { by[o.name_bg] = o; });
    rows.forEach(function (row) {
      var o = by[row.getAttribute('data-name')];
      if (!o) return;
      row.setAttribute('data-sensors', o.sensor_count);
      var cells = row.querySelectorAll('td');
      var sensorsCell = row.querySelector('.sensors');
      if (sensorsCell) sensorsCell.textContent = o.sensor_count;
      var fmt = function (v) {
        return new Intl.NumberFormat(lang(), { maximumFractionDigits: 2 }).format(v);
      };
      if (o.p2 == null) {
        // Falling silent is an ordinary state, not an error (§2.3): the row
        // keeps its count and says so in words.
        row.setAttribute('data-nodata', '');
        row.removeAttribute('data-value'); row.removeAttribute('data-pm10');
      } else {
        row.removeAttribute('data-nodata');
        row.setAttribute('data-value', o.p2);
        row.setAttribute('data-pm10', o.p10 == null ? '' : o.p10);
        var nums = row.querySelectorAll('.num .chip');
        if (nums[0]) setChip(nums[0], fmt(o.p2));
        if (nums[1] && o.p10 != null) setChip(nums[1], fmt(o.p10));
      }
    });
    render();
  });

  // A chip is <swatch><text>: replacing its textContent would delete the
  // swatch, so only the text node moves (§5.12 — write the node, not the box).
  function setChip(chip, text) {
    var swatch = chip.querySelector('.chip__swatch');
    chip.textContent = text;
    if (swatch) chip.insertBefore(swatch, chip.firstChild);
  }

  document.addEventListener('airbg:languagechange', function () {
    collator = new Intl.Collator(lang());
    state.q = search.value ? fold(search.value.trim()) : '';
    lastCount = -1;
    render();
    if (!listbox.hidden) buildList();
  });

  restoreColumns();
  applyColumns();
  render();
})();
