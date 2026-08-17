<script>
  let { options, selected, onselect, legend } = $props()
</script>

<fieldset class="metric-switcher">
  <legend>{legend}</legend>
  {#each options as option (option.metric)}
    <button
      type="button"
      aria-pressed={option.metric === selected}
      onclick={() => onselect(option.metric)}
    >{option.label}</button>
  {/each}
</fieldset>
