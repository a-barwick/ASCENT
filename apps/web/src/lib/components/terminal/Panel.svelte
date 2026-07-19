<script lang="ts">
  import type { Snippet } from "svelte";

  interface Props {
    title: string;
    eyebrow?: string;
    status?: string;
    class?: string;
    children: Snippet;
  }

  let { title, eyebrow, status, class: className = "", children }: Props = $props();
</script>

<section class={`panel ${className}`} aria-label={title}>
  <header class="panel__header">
    <div>
      {#if eyebrow}<span class="panel__eyebrow">{eyebrow}</span>{/if}
      <h2>{title}</h2>
    </div>
    {#if status}<span class="panel__status">{status}</span>{/if}
  </header>
  <div class="panel__body">
    {@render children()}
  </div>
</section>
