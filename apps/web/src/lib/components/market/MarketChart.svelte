<script lang="ts">
  import type { PricePoint } from "$lib/domain/market";
  import { formatNumber } from "$lib/domain/market";
  import { chartRange } from "$lib/domain/game";

  interface Props {
    history: PricePoint[];
    unit: string;
    label?: string;
  }

  let { history, unit, label = "Market price" }: Props = $props();

  const width = 720;
  const height = 220;
  const padding = { top: 18, right: 42, bottom: 28, left: 12 };
  const range = $derived(chartRange(history));
  const minimum = $derived(range?.minimum ?? 0);
  const maximum = $derived(range?.maximum ?? 1);
  const points = $derived.by(() => {
    const range = maximum - minimum || 1;
    return history
      .map((point, index) => {
        const x =
          padding.left +
          (index / Math.max(history.length - 1, 1)) * (width - padding.left - padding.right);
        const y =
          padding.top + ((maximum - point.value) / range) * (height - padding.top - padding.bottom);
        return `${x.toFixed(1)},${y.toFixed(1)}`;
      })
      .join(" ");
  });
  const areaPoints = $derived(
    `${padding.left},${height - padding.bottom} ${points} ${width - padding.right},${height - padding.bottom}`,
  );
</script>

<div class="chart">
  {#if range}
    <svg viewBox={`0 0 ${width} ${height}`} role="img" aria-label={`${label} history per ${unit}`}>
      <defs>
        <linearGradient id="price-fill" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stop-color="var(--chart-primary)" stop-opacity="0.18" />
          <stop offset="100%" stop-color="var(--chart-primary)" stop-opacity="0" />
        </linearGradient>
      </defs>
      {#each [0.25, 0.5, 0.75] as line}
        <line
          x1={padding.left}
          y1={padding.top + line * (height - padding.top - padding.bottom)}
          x2={width - padding.right}
          y2={padding.top + line * (height - padding.top - padding.bottom)}
          class="grid"
        />
      {/each}
      <polygon points={areaPoints} fill="url(#price-fill)" />
      <polyline {points} class="series" />
      {#each history as point, index}
        {@const x =
          padding.left +
          (index / Math.max(history.length - 1, 1)) * (width - padding.left - padding.right)}
        {@const y =
          padding.top +
          ((maximum - point.value) / (maximum - minimum || 1)) *
            (height - padding.top - padding.bottom)}
        <circle cx={x} cy={y} r={index === history.length - 1 ? 3.5 : 2} />
        <text {x} y={height - 8} text-anchor="middle" class="axis-label">{point.label}</text>
      {/each}
      <text x={width - 2} y={padding.top + 4} text-anchor="end" class="value-label">
        {formatNumber(maximum, 2)}
      </text>
      <text x={width - 2} y={height - padding.bottom} text-anchor="end" class="value-label">
        {formatNumber(minimum, 2)}
      </text>
    </svg>
  {:else}
    <p class="empty">No settled price history for this market.</p>
  {/if}
</div>

<style>
  .chart {
    min-height: 14rem;
    display: grid;
    align-items: stretch;
  }

  svg {
    display: block;
    width: 100%;
    height: 100%;
    min-height: 14rem;
  }

  .empty {
    align-self: center;
    margin: 0;
    padding: var(--space-5);
    color: var(--text-dim);
    font: var(--body-sm);
    text-align: center;
  }

  .grid {
    stroke: var(--border-subtle);
    stroke-width: 1;
    stroke-dasharray: 2 4;
  }

  .series {
    fill: none;
    stroke: var(--chart-primary);
    stroke-width: 1.5;
    vector-effect: non-scaling-stroke;
  }

  circle {
    fill: var(--surface-raised);
    stroke: var(--chart-primary);
    stroke-width: 1.5;
  }

  text {
    fill: var(--text-muted);
    font: 9px var(--font-mono);
  }
</style>
