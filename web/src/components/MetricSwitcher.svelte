<script>
  // A radio set, not a row of toggle buttons (DESIGN.md §5.6). The metrics are
  // mutually exclusive, so the platform control for "one of these" is what the
  // reader gets: one Tab stop with arrow-key roving, and the chosen metric
  // announced as the selected radio rather than as one pressed button in a
  // row. `name` groups them; the page mounts one switcher, and two sharing a
  // name would silently become a single group.
  let { options, selected, onselect, legend, name = 'metric' } = $props()
</script>

<fieldset class="switcher">
  <legend>{legend}</legend>
  <div class="switcher__set">
    {#each options as option (option.metric)}
      <label class="switcher__opt">
        <!-- The input is the control and is visually hidden by the kit, so the
             span beside it is what gets painted. checked is bound to the store's
             value rather than left to the browser: the metric also changes from
             the URL and from the map, and an uncontrolled radio would keep
             whatever was last clicked. -->
        <input
          type="radio"
          {name}
          value={option.metric}
          checked={option.metric === selected}
          onchange={() => onselect(option.metric)}
        >
        <span>{option.label}</span>
      </label>
    {/each}
  </div>
</fieldset>
