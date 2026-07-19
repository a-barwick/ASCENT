<script lang="ts">
  interface Props {
    risk: number;
    confidence: number;
  }

  let { risk, confidence }: Props = $props();

  const radius = 50;
  const circumference = 2 * Math.PI * radius;
  const offset = $derived(circumference * (1 - Math.min(100, Math.max(0, risk)) / 100));
  const tone = $derived(risk >= 50 ? "critical" : risk >= 25 ? "warning" : "positive");
</script>

<div class={`risk-gauge risk-gauge--${tone}`}>
  <svg viewBox="0 0 120 120" role="img" aria-label={`Modeled mission loss risk ${risk}%`}>
    <circle class="risk-track" cx="60" cy="60" r={radius}></circle>
    <circle
      class="risk-value"
      cx="60"
      cy="60"
      r={radius}
      stroke-dasharray={circumference}
      stroke-dashoffset={offset}
    ></circle>
  </svg>
  <div>
    <strong>{risk}%</strong>
    <span>Mission loss exposure</span>
    <small>{confidence}% decision confidence</small>
  </div>
</div>
