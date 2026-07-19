<script lang="ts">
  import { phases, type ProgramPhase } from "$lib/domain/program";

  interface Props {
    current: ProgramPhase;
  }

  let { current }: Props = $props();

  const currentIndex = $derived(phases.findIndex((phase) => phase.id === current));
</script>

<ol class="phase-rail" aria-label="Program campaign phases">
  {#each phases as phase, index}
    <li
      class:complete={index < currentIndex}
      class:active={index === currentIndex}
      aria-current={index === currentIndex ? "step" : undefined}
    >
      <span>{String(index + 1).padStart(2, "0")}</span>
      <div>
        <strong>{phase.label}</strong>
        <small>{phase.detail}</small>
      </div>
    </li>
  {/each}
</ol>
