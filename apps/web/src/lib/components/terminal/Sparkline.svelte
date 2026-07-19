<script lang="ts">
  interface Props {
    values: number[];
    label: string;
    tone?: "neutral" | "positive" | "negative";
  }

  let { values, label, tone = "neutral" }: Props = $props();

  const width = 160;
  const height = 48;
  const padding = 3;
  const points = $derived.by(() => {
    if (values.length === 0) return "";
    const minimum = Math.min(...values);
    const maximum = Math.max(...values);
    const range = maximum - minimum || 1;
    return values
      .map((value, index) => {
        const x = padding + (index / Math.max(values.length - 1, 1)) * (width - padding * 2);
        const y = height - padding - ((value - minimum) / range) * (height - padding * 2);
        return `${x.toFixed(1)},${y.toFixed(1)}`;
      })
      .join(" ");
  });
</script>

<svg
  class:tone-positive={tone === "positive"}
  class:tone-negative={tone === "negative"}
  viewBox={`0 0 ${width} ${height}`}
  role="img"
  aria-label={label}
  preserveAspectRatio="none"
>
  <line x1="0" y1={height / 2} x2={width} y2={height / 2} class="midline" />
  <polyline {points} />
</svg>

<style>
  svg {
    display: block;
    width: 100%;
    min-width: 7rem;
    height: 3rem;
    color: var(--chart-primary);
  }

  .midline {
    stroke: var(--border-subtle);
    stroke-width: 1;
    stroke-dasharray: 2 3;
  }

  polyline {
    fill: none;
    stroke: currentColor;
    stroke-width: 1.5;
    vector-effect: non-scaling-stroke;
  }

  .tone-positive {
    color: var(--positive);
  }

  .tone-negative {
    color: var(--negative);
  }
</style>
