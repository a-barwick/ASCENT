<script lang="ts">
  import type { ReadinessItem } from "$lib/domain/program";

  interface Props {
    items: ReadinessItem[];
  }

  let { items }: Props = $props();
</script>

<div class="readiness-matrix">
  <div class="readiness-heading" aria-hidden="true">
    <span>System / owner</span>
    <span>Evidence</span>
    <span>State</span>
  </div>
  {#each items as item}
    <article class="readiness-row">
      <div class="readiness-name">
        <strong>{item.label}</strong>
        <small>{item.owner}</small>
      </div>
      <div class="readiness-evidence">
        <div
          class="readiness-bar"
          role="progressbar"
          aria-label={`${item.label} readiness`}
          aria-valuemin="0"
          aria-valuemax="100"
          aria-valuenow={item.value}
        >
          <span
            class:status-hold={item.status === "hold"}
            class:status-watch={item.status === "watch" || item.status === "waived"}
            style={`width:${item.value}%`}
          ></span>
        </div>
        <small>{item.note}</small>
      </div>
      <div class={`state-chip state-chip--${item.status}`}>
        <b>{item.value}</b>
        <span>{item.status}</span>
      </div>
    </article>
  {/each}
</div>
