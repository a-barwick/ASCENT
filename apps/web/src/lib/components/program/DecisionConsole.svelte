<script lang="ts">
  import Sparkline from "$lib/components/ui/Sparkline.svelte";
  import {
    choiceById,
    decisionChoices,
    evidence,
    type DecisionChoiceId,
    type EvidenceId,
  } from "$lib/domain/program";

  interface Props {
    selected: DecisionChoiceId | null;
    resolved: DecisionChoiceId | null;
    locked?: boolean;
    onselect: (choice: DecisionChoiceId) => void;
    onconfirm: () => void;
  }

  let { selected, resolved, locked = false, onselect, onconfirm }: Props = $props();
  let activeEvidence = $state<EvidenceId>("engineering");

  const evidencePanel = $derived(evidence[activeEvidence]);
  const selectedChoice = $derived(choiceById(selected));

  function moneyDelta(value: number): string {
    if (value === 0) return "No added cost";
    return `${value < 0 ? "−" : "+"}${Math.abs(value) / 1_000_000}M CR`;
  }

  function timeDelta(value: number): string {
    if (value === 0) return "Window held";
    const hours = Math.floor(Math.abs(value) / 60);
    const minutes = Math.abs(value) % 60;
    return `${value < 0 ? "−" : "+"}${hours}h ${String(minutes).padStart(2, "0")}m`;
  }

  function riskDelta(value: number): string {
    return `${value > 0 ? "+" : "−"}${Math.abs(value)} pts risk`;
  }
</script>

<div class="anomaly-brief">
  <div class="anomaly-code">
    <span>FCR-28 / QA-1044</span>
    <strong>Flight-critical anomaly</strong>
  </div>
  <p>
    Stage-2 fuel turbopump bearing shows an unexplained radial signature. The stack cannot enter the
    commitment sequence until you disposition it.
  </p>
  <div class="decision-deadline">
    <span>Director action</span>
    <strong>{locked ? "Decision closed" : "Required before readiness poll"}</strong>
  </div>
</div>

<div class="evidence-tabs" aria-label="Anomaly evidence">
  {#each Object.entries(evidence) as [id, panel]}
    <button
      type="button"
      aria-pressed={activeEvidence === id}
      class:active={activeEvidence === id}
      onclick={() => (activeEvidence = id as EvidenceId)}
    >
      {panel.label}
    </button>
  {/each}
</div>

<section class="evidence-panel" aria-label={`${evidencePanel.label} evidence`}>
  <div>
    <span>{evidencePanel.label} assessment</span>
    <strong>{evidencePanel.headline}</strong>
    <ul>
      {#each evidencePanel.points as point}
        <li>{point}</li>
      {/each}
    </ul>
  </div>
  <Sparkline
    values={evidencePanel.signal}
    label={`${evidencePanel.label} evidence signal`}
    tone={activeEvidence === "engineering"
      ? "negative"
      : activeEvidence === "supplier"
        ? "negative"
        : "neutral"}
  />
</section>

<fieldset class="decision-options">
  <legend>Choose a program response</legend>
  {#each decisionChoices as choice}
    <button
      type="button"
      class="decision-option"
      data-choice={choice.id}
      class:selected={selected === choice.id}
      class:resolved={resolved === choice.id}
      class:deemphasized={resolved !== null && resolved !== choice.id}
      aria-pressed={selected === choice.id}
      disabled={resolved !== null || locked}
      onclick={() => onselect(choice.id)}
    >
      <span class="choice-strategy">{choice.strategy}</span>
      <strong>{choice.label}</strong>
      <span class="choice-description">{choice.description}</span>
      <span class="choice-impact">
        <b>{moneyDelta(choice.costDelta)}</b>
        <b>{timeDelta(choice.windowDeltaMinutes)}</b>
        <b class:negative-impact={choice.riskDelta > 0}>{riskDelta(choice.riskDelta)}</b>
      </span>
    </button>
  {/each}
</fieldset>

<div class="decision-action">
  {#if resolved}
    <div class="decision-locked">
      <span>Disposition recorded</span>
      <strong>{choiceById(resolved)?.label}</strong>
    </div>
  {:else if locked}
    <div class="decision-locked decision-locked--warning">
      <span>Decision window closed</span>
      <strong>The reserved range corridor has been released.</strong>
    </div>
  {:else}
    <div>
      <span>{selectedChoice ? "Projected consequence" : "No response selected"}</span>
      <strong>{selectedChoice?.consequence ?? "Compare the evidence before committing."}</strong>
    </div>
    <button
      class="primary-control"
      data-action="authorize-disposition"
      type="button"
      disabled={!selectedChoice}
      onclick={onconfirm}
    >
      Authorize disposition
    </button>
  {/if}
</div>
