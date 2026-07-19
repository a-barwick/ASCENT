<script lang="ts">
  import type { OrderBook } from "$lib/domain/market";
  import { formatNumber } from "$lib/domain/market";
  import { bookDepthMaximum } from "$lib/domain/game";

  interface Props {
    book: OrderBook;
    currency: string;
    unit: string;
  }

  let { book, currency, unit }: Props = $props();
  const maxQuantity = $derived(bookDepthMaximum(book));
</script>

<div class="book" aria-label="Limit order book">
  <div class="book__side">
    <h3>Bids</h3>
    <table>
      <thead>
        <tr>
          <th scope="col">Price <span>{currency}</span></th>
          <th scope="col">Qty <span>{unit}</span></th>
          <th scope="col">Orders</th>
        </tr>
      </thead>
      <tbody>
        {#if book.bids.length === 0}
          <tr><td colspan="3" class="empty">No bids</td></tr>
        {:else}
          {#each book.bids as level}
            <tr style={`--depth:${(level.quantity / maxQuantity) * 100}%`}>
              <td class="bid">{formatNumber(level.price, 2)}</td>
              <td>{formatNumber(level.quantity, 1)}</td>
              <td>{level.orders}</td>
            </tr>
          {/each}
        {/if}
      </tbody>
    </table>
  </div>
  <div class="book__side">
    <h3>Asks</h3>
    <table>
      <thead>
        <tr>
          <th scope="col">Price <span>{currency}</span></th>
          <th scope="col">Qty <span>{unit}</span></th>
          <th scope="col">Orders</th>
        </tr>
      </thead>
      <tbody>
        {#if book.asks.length === 0}
          <tr><td colspan="3" class="empty">No asks</td></tr>
        {:else}
          {#each book.asks as level}
            <tr style={`--depth:${(level.quantity / maxQuantity) * 100}%`}>
              <td class="ask">{formatNumber(level.price, 2)}</td>
              <td>{formatNumber(level.quantity, 1)}</td>
              <td>{level.orders}</td>
            </tr>
          {/each}
        {/if}
      </tbody>
    </table>
  </div>
</div>

<style>
  .book {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    min-height: 100%;
  }

  .book__side + .book__side {
    border-left: 1px solid var(--border);
  }

  .book__side {
    min-width: 0;
  }

  h3 {
    margin: 0;
    padding: var(--space-2) var(--space-3);
    border-bottom: 1px solid var(--border);
    color: var(--text-muted);
    font: var(--label-sm);
    letter-spacing: var(--tracking-wide);
    text-transform: uppercase;
  }

  table {
    width: 100%;
    border-collapse: collapse;
    font-variant-numeric: tabular-nums;
    table-layout: fixed;
  }

  th,
  td {
    overflow: hidden;
    padding: 0.37rem 0.35rem;
    text-align: right;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  th {
    color: var(--text-dim);
    font: var(--label-xs);
    text-transform: uppercase;
  }

  th span {
    color: var(--text-faint);
  }

  td {
    border-top: 1px solid var(--border-subtle);
    background: linear-gradient(90deg, var(--book-depth) var(--depth), transparent var(--depth))
      no-repeat;
    font: var(--numeric-sm);
  }

  .bid {
    color: var(--positive);
  }

  .ask {
    color: var(--negative);
  }

  td.empty {
    height: 4rem;
    background: none;
    color: var(--text-dim);
    font: var(--body-sm);
    text-align: center;
  }

  @media (max-width: 520px) {
    .book {
      grid-template-columns: 1fr;
    }

    .book__side + .book__side {
      border-top: 1px solid var(--border);
      border-left: 0;
    }
  }
</style>
