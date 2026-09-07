<script>
  // The ARIA 1.2 combobox: a text input that OWNS a listbox. Focus never leaves
  // the input — the active option is pointed at with aria-activedescendant — so
  // the reader hears each option without losing the caret they are still
  // typing into. That is also why the options are <li>, not buttons: a button
  // would take focus, and a control that steals the caret mid-word cannot be
  // typed into.
  //
  // The kit's own mockup stops at mouse picking. Its stylesheet and its
  // screen-reader hint both describe arrow keys and an active descendant, so
  // that contract is what is built here rather than the shorter thing the
  // mockup happens to do.
  import { matchAreas, exactMatch, splitMark } from '../lib/find.js'

  let { areas, lang = 'bg', label, placeholder, hint, empty, onpick, id = 'area-find' } = $props()

  let query = $state('')
  let open = $state(false)
  // The index of the keyboard cursor, not of a chosen thing: -1 means the
  // reader is still typing and Enter should fall back to an exact match.
  let active = $state(-1)

  const listId = `${id}-listbox`
  const matches = $derived(matchAreas(areas, query, lang))
  const activeId = $derived(active >= 0 && active < matches.length ? `${id}-opt-${active}` : null)

  function show() {
    open = true
    active = -1
  }
  function hide() {
    open = false
    active = -1
  }

  function pick(match) {
    if (!match) return
    query = match.name
    hide()
    onpick(match)
  }

  function move(step) {
    if (!matches.length) return
    if (!open) open = true
    // Wraps, and an untouched cursor entering from the top lands on the first
    // option going down and on the last going up.
    const n = matches.length
    active = active < 0 ? (step > 0 ? 0 : n - 1) : (active + step + n) % n
  }

  function onkeydown(e) {
    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault()
        move(1)
        return
      case 'ArrowUp':
        e.preventDefault()
        move(-1)
        return
      case 'Home':
        if (!open) return
        e.preventDefault()
        active = 0
        return
      case 'End':
        if (!open) return
        e.preventDefault()
        active = matches.length - 1
        return
      case 'Enter': {
        // The cursor if the reader moved it; otherwise only an unambiguous
        // query. A half-typed name never navigates.
        const target = active >= 0 ? matches[active] : exactMatch(matches, query, lang)
        if (!target) return
        e.preventDefault()
        pick(target)
        return
      }
      case 'Escape':
        // Two stages: the list goes first, the text second. One Escape that
        // did both would throw away a query the reader only wanted to see
        // past.
        if (open) {
          e.preventDefault()
          hide()
          return
        }
        if (query) {
          e.preventDefault()
          query = ''
        }
        return
      default:
    }
  }
</script>

<div class="field field--search combobox toolbar__find">
  <label class="field__label" for={id}>{label}</label>
  <input
    class="input"
    {id}
    type="search"
    autocomplete="off"
    role="combobox"
    aria-expanded={open}
    aria-controls={listId}
    aria-autocomplete="list"
    aria-activedescendant={activeId}
    aria-describedby="{id}-hint"
    {placeholder}
    bind:value={query}
    oninput={show}
    onfocus={show}
    onkeydown={onkeydown}
    onblur={() => setTimeout(hide, 0)}
  >
  <!-- Announced, never painted: a sighted reader infers arrow keys from the
       open list, a screen-reader user does not, and a visible line of
       instructions under a search field is clutter for both. -->
  <span class="sr-only" id="{id}-hint">{hint}</span>
  <ul class="combobox__list" id={listId} role="listbox" hidden={!open}>
    {#if matches.length === 0}
      <!-- An absence stated plainly, not an error: typing a name this network
           has no area for is an ordinary thing to do. -->
      <li class="combobox__empty">{empty}</li>
    {:else}
      {#each matches as match, i (match.name)}
        {@const parts = splitMark(match.name, match.at, match.len)}
        <!-- mousedown, not click: click arrives after blur has already closed
             the list, so a mouse pick would land on nothing. -->
        <li
          class="combobox__opt"
          id="{id}-opt-{i}"
          role="option"
          aria-selected={i === active}
          onmousedown={(e) => { e.preventDefault(); pick(match) }}
        >{parts.before}<mark>{parts.hit}</mark>{parts.after}</li>
      {/each}
    {/if}
  </ul>
</div>
