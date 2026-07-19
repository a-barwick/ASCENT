<script lang="ts">
  import {
    PROTOCOL_VERSION,
    type CommandEnvelope,
    type CommandResultEnvelope,
  } from "@ascent/protocol";
  import { onMount } from "svelte";

  import { ApiError, GameClient, createCommandEnvelope, describeApiError } from "$lib/api/client";
  import MarketChart from "$lib/components/market/MarketChart.svelte";
  import OrderBook from "$lib/components/market/OrderBook.svelte";
  import Panel from "$lib/components/terminal/Panel.svelte";
  import {
    canIssueCommands,
    freshnessAt,
    type Freshness,
    type Side,
    type SnapshotSource,
  } from "$lib/domain/game";
  import { formatChange, formatCurrency, formatNumber, formatTime } from "$lib/domain/market";

  let { data } = $props();
  // The terminal owns its live projection after hydration; navigation does not replace command state.
  // svelte-ignore state_referenced_locally
  const initialData = data;

  let envelope = $state(initialData.snapshot);
  let source = $state<SnapshotSource>(initialData.source);
  let authRequired = $state(initialData.authRequired);
  let terminalMessage = $state<string | null>(initialData.message);
  let actionError = $state<string | null>(null);
  let actionSuccess = $state<string | null>(null);
  let commandBusy = $state<string | null>(null);
  let refreshing = $state(false);
  let now = $state(Date.now());
  let lastCommand = $state<CommandResultEnvelope | null>(null);
  let retryCommand = $state<CommandEnvelope | null>(null);
  let retryLabel = $state("");

  const snapshot = $derived(envelope?.payload ?? null);
  let selectedMarketId = $state(initialData.snapshot?.payload.markets.at(0)?.id ?? "");
  const selectedMarket = $derived(
    snapshot?.markets.find((market) => market.id === selectedMarketId) ??
      snapshot?.markets.at(0) ??
      null,
  );
  const freshness = $derived.by<Freshness>(() => {
    if (source === "fixture") return "expired";
    if (source === "stale") return "stale";
    return freshnessAt(envelope?.generatedAt, now);
  });
  const authorityReady = $derived(canIssueCommands(snapshot?.actor, freshness, source));
  const commandsEnabled = $derived(authorityReady && commandBusy === null);
  const sourceLabel = $derived(
    source === "authority"
      ? "Authoritative"
      : source === "fixture"
        ? "Fixture / degraded"
        : source === "stale"
          ? "Authority / stale"
          : "Unavailable",
  );
  const commandGateReason = $derived(
    authRequired
      ? "Start an authenticated development session."
      : source === "fixture"
        ? "Fixture views are read-only."
        : freshness !== "fresh"
          ? "Commands pause while the snapshot is stale."
          : "Waiting for the authority.",
  );

  let orderSide = $state<Side>("buy");
  let orderPrice = $state(initialData.snapshot?.payload.markets.at(0)?.lastPrice ?? 0);
  let orderQuantity = $state(25);
  let productionFacilityId = $state(initialData.snapshot?.payload.facilities.at(0)?.id ?? "");
  let productionQuantity = $state(40);
  let deviceName = $state("");
  let panelDeviceId = $state(initialData.snapshot?.payload.devices.at(0)?.id ?? "");
  let panelId = $state(initialData.snapshot?.payload.panels.at(0)?.id ?? "");
  let panelMessage = $state("");
  let chatBody = $state("");
  let compensationTarget = $state(initialData.snapshot?.payload.operatorAudit.at(0)?.id ?? "");
  let compensationReason = $state("");

  const client = new GameClient((input, init) => globalThis.fetch(input, init));

  onMount(() => {
    const timer = window.setInterval(() => {
      now = Date.now();
    }, 1_000);
    const poller = window.setInterval(() => {
      void pollEvents();
    }, 5_000);
    return () => {
      window.clearInterval(timer);
      window.clearInterval(poller);
    };
  });

  function chooseMarket(marketId: string): void {
    selectedMarketId = marketId;
    const market = snapshot?.markets.find((candidate) => candidate.id === marketId);
    if (market) orderPrice = market.lastPrice;
  }

  function marketLabel(marketId: string): string {
    const market = snapshot?.markets.find((candidate) => candidate.id === marketId);
    return market ? `${market.commodity} / ${market.location}` : marketId;
  }

  async function startSession(): Promise<void> {
    actionError = null;
    actionSuccess = null;
    commandBusy = "session";
    try {
      await client.createDevSession();
      authRequired = false;
      if (await refreshGame(false)) {
        actionSuccess = "Development operator session authenticated.";
      } else {
        actionError = terminalMessage ?? "The session started, but the game snapshot did not load.";
      }
    } catch (error) {
      actionError = describeApiError(error);
    } finally {
      commandBusy = null;
    }
  }

  async function refreshGame(showLoading = true): Promise<boolean> {
    if (showLoading) refreshing = true;
    try {
      const nextEnvelope = await client.getGame();
      envelope = nextEnvelope;
      source = "authority";
      authRequired = false;
      terminalMessage = null;
      actionError = null;
      if (!nextEnvelope.payload.markets.some((market) => market.id === selectedMarketId)) {
        chooseMarket(nextEnvelope.payload.markets.at(0)?.id ?? "");
      }
      if (
        !nextEnvelope.payload.facilities.some((facility) => facility.id === productionFacilityId)
      ) {
        productionFacilityId = nextEnvelope.payload.facilities.at(0)?.id ?? "";
      }
      if (!nextEnvelope.payload.devices.some((device) => device.id === panelDeviceId)) {
        panelDeviceId = nextEnvelope.payload.devices.at(0)?.id ?? "";
      }
      if (!nextEnvelope.payload.panels.some((panel) => panel.id === panelId)) {
        panelId = nextEnvelope.payload.panels.at(0)?.id ?? "";
      }
      if (!nextEnvelope.payload.operatorAudit.some((entry) => entry.id === compensationTarget)) {
        compensationTarget = nextEnvelope.payload.operatorAudit.at(0)?.id ?? "";
      }
      return true;
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) authRequired = true;
      source = envelope ? "stale" : "unavailable";
      terminalMessage = describeApiError(error);
      return false;
    } finally {
      refreshing = false;
    }
  }

  async function pollEvents(): Promise<void> {
    if (!envelope || authRequired || source === "fixture" || commandBusy) return;
    try {
      const batch = await client.getEvents(envelope.sequence);
      if (batch.latestSequence > envelope.sequence || source === "stale") {
        await refreshGame(false);
      }
    } catch (error) {
      source = "stale";
      terminalMessage = describeApiError(error);
    }
  }

  async function issueCommand(
    type: string,
    payload: Record<string, unknown>,
    successLabel: string,
  ): Promise<void> {
    if (!snapshot || !authorityReady) {
      actionError = commandGateReason;
      return;
    }
    const command = createCommandEnvelope(type, payload, {
      actorId: snapshot.actor.id,
      companyId: snapshot.membership.companyId,
      expectedVersion: snapshot.company.version,
    });
    await submitCommand(command, successLabel);
  }

  async function submitCommand(command: CommandEnvelope, successLabel: string): Promise<void> {
    actionError = null;
    actionSuccess = null;
    commandBusy = command.type;
    try {
      const result = await client.sendCommand(command);
      lastCommand = result;
      if (result.status === "rejected" || result.status === "failed") {
        actionError = result.safeMessage ?? `Command ${result.status}.`;
        retryCommand = null;
        return;
      }
      retryCommand = null;
      actionSuccess = `${successLabel} ${result.status}. No balance is shown until the authority refreshes.`;
      await refreshGame(false);
    } catch (error) {
      actionError = describeApiError(error);
      retryCommand = error instanceof ApiError && error.retryable ? command : null;
      retryLabel = successLabel;
      source = envelope ? "stale" : "unavailable";
    } finally {
      commandBusy = null;
    }
  }

  async function placeOrder(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    if (!selectedMarket || orderPrice <= 0 || orderQuantity <= 0) {
      actionError = "Price and quantity must both be greater than zero.";
      return;
    }
    await issueCommand(
      "market.place_order",
      {
        marketId: selectedMarket.id,
        side: orderSide,
        orderType: "limit",
        price: orderPrice,
        quantity: orderQuantity,
      },
      "Limit order",
    );
  }

  async function cancelOrder(orderId: string): Promise<void> {
    await issueCommand("market.cancel_order", { orderId }, "Order cancellation");
  }

  async function runProduction(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    if (!productionFacilityId || productionQuantity <= 0) {
      actionError = "Choose a facility and enter a positive production quantity.";
      return;
    }
    await issueCommand(
      "production.run",
      { facilityId: productionFacilityId, quantity: productionQuantity },
      "Production run",
    );
  }

  async function deliverFreight(shipmentId: string): Promise<void> {
    await issueCommand("freight.deliver", { shipmentId }, "Freight delivery");
  }

  async function registerDevice(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    const name = deviceName.trim();
    if (!name) {
      actionError = "Enter a device name.";
      return;
    }
    await issueCommand(
      "device.register",
      { name, capabilities: ["panel.receive"] },
      "Device registration",
    );
    if (!actionError) deviceName = "";
  }

  async function sendPanel(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    const message = panelMessage.trim();
    if (!panelDeviceId || !panelId || !message) {
      actionError = "Choose a device and panel and enter a message.";
      return;
    }
    await issueCommand(
      "device.panel_send",
      { deviceId: panelDeviceId, panelId, message },
      "Panel message",
    );
    if (!actionError) panelMessage = "";
  }

  async function sendChat(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    const body = chatBody.trim();
    if (!body) {
      actionError = "Enter a company operations message.";
      return;
    }
    await issueCommand("chat.send", { channelId: "company-operations", body }, "Chat message");
    if (!actionError) chatBody = "";
  }

  async function compensate(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    const reason = compensationReason.trim();
    if (!compensationTarget || !reason) {
      actionError = "Choose an audit event and record a compensation reason.";
      return;
    }
    await issueCommand(
      "operator.compensate",
      { targetEventId: compensationTarget, reason },
      "Compensating command",
    );
    if (!actionError) compensationReason = "";
  }
</script>

<svelte:head>
  <title>ASCENT / Cislunar Operations</title>
  <meta
    name="description"
    content="Operate a cislunar industrial company across markets, production, freight, and company systems."
  />
</svelte:head>

<div class="terminal-shell" aria-busy={refreshing || commandBusy !== null}>
  <header class="topbar">
    <a class="brand" href="/" aria-label="ASCENT terminal home">
      <img src="/ascent-mark.svg" alt="" />
      <span>ASCENT</span>
    </a>
    <div class="system-time">
      <span>Simulation time</span>
      <strong>{snapshot?.systemTime ?? "Awaiting authority"}</strong>
    </div>
    <nav aria-label="Primary terminal">
      <a class="active" href="#company">Company</a>
      <a href="#markets">Markets</a>
      <a href="#production">Production</a>
      <a href="#network">Network</a>
      <a href="#operator">Operator</a>
    </nav>
    <div class="identity">
      <span class:status-warning={source !== "authority"} class="connection__dot"></span>
      <span>{snapshot?.actor.displayName ?? "No session"}</span>
      <strong>{sourceLabel} · v{PROTOCOL_VERSION}</strong>
    </div>
  </header>

  <div class="terminal-notices" aria-live="polite">
    {#if source !== "authority" || freshness !== "fresh" || terminalMessage}
      <section class="notice notice--warning">
        <div>
          <strong>{sourceLabel}</strong>
          <span>
            {terminalMessage ??
              `Snapshot ${freshness}; sequence ${envelope?.sequence ?? "unavailable"}.`}
          </span>
        </div>
        {#if authRequired}
          <button type="button" onclick={() => void startSession()} disabled={commandBusy !== null}>
            Start dev session
          </button>
        {:else}
          <button type="button" onclick={() => void refreshGame()} disabled={refreshing}>
            {refreshing ? "Refreshing…" : "Retry authority"}
          </button>
        {/if}
      </section>
    {/if}
    {#if commandBusy}
      <section class="notice">
        <strong>Command pending</strong>
        <span>{commandBusy} is awaiting an authoritative result.</span>
      </section>
    {/if}
    {#if actionError}
      <section class="notice notice--error" role="alert">
        <div>
          <strong>Command not confirmed</strong>
          <span>{actionError}</span>
        </div>
        {#if retryCommand}
          <button
            type="button"
            disabled={!authorityReady || commandBusy !== null}
            onclick={() => retryCommand && void submitCommand(retryCommand, retryLabel)}
          >
            Retry same key
          </button>
        {/if}
      </section>
    {/if}
    {#if actionSuccess}
      <section class="notice notice--success">
        <strong>Authority receipt</strong>
        <span>{actionSuccess}</span>
        {#if lastCommand}<code>{lastCommand.commandId}</code>{/if}
      </section>
    {/if}
  </div>

  {#if snapshot}
    <main class="workspace">
      <Panel
        title="Company & identity"
        eyebrow="Authenticated context"
        status={snapshot.membership.role}
        class="company-panel"
      >
        <div class="company-name" id="company">
          <span>Operating company</span>
          <strong>{snapshot.company.name}</strong>
          <small>{snapshot.actor.displayName} · {snapshot.actor.id}</small>
        </div>
        <div class="metric-grid">
          <div class="metric metric--wide">
            <span>Cash / ledger projection</span>
            <strong>{formatCurrency(snapshot.company.cash, true)}</strong>
            <small>Snapshot v{snapshot.company.version}</small>
          </div>
          <div class="metric">
            <span>Total assets</span>
            <strong>{formatCurrency(snapshot.company.totalAssets, true)}</strong>
          </div>
          <div class="metric">
            <span>Liabilities</span>
            <strong>{formatCurrency(snapshot.company.totalLiabilities, true)}</strong>
          </div>
          <div class="metric">
            <span>Net worth</span>
            <strong>{formatCurrency(snapshot.company.netWorth, true)}</strong>
          </div>
          <div class="metric">
            <span>Credit</span>
            <strong>{snapshot.company.creditRating}</strong>
            <small>{formatCurrency(snapshot.company.availableCredit, true)} available</small>
          </div>
        </div>
        <ul class="statement-list" aria-label="Company operating statement">
          {#each snapshot.company.statements as statement}
            <li>
              <span>{statement.label}</span>
              <strong>{formatCurrency(statement.value, true)}</strong>
              {#if statement.change !== null}
                <small class:positive={statement.change > 0} class:negative={statement.change < 0}>
                  {formatChange(statement.change)}
                </small>
              {/if}
            </li>
          {:else}
            <li class="empty-state">No statement periods have closed.</li>
          {/each}
        </ul>
      </Panel>

      <Panel
        title={selectedMarket
          ? `${selectedMarket.commodity} / ${selectedMarket.location}`
          : "Markets"}
        eyebrow="Two-location spot exchange"
        status={`${sourceLabel} · seq ${envelope?.sequence ?? "—"}`}
        class="chart-panel"
      >
        <div class="segmented" id="markets" aria-label="Market location">
          {#each snapshot.markets as market}
            <button
              type="button"
              class:active={market.id === selectedMarket?.id}
              aria-pressed={market.id === selectedMarket?.id}
              onclick={() => chooseMarket(market.id)}
            >
              <span>{market.commodity}</span>
              <small>{market.location}</small>
            </button>
          {:else}
            <p class="empty-state">No markets are open in this snapshot.</p>
          {/each}
        </div>
        {#if selectedMarket}
          <div class="market-summary">
            <div>
              <span>Last</span>
              <strong>{formatNumber(selectedMarket.lastPrice, 2)}</strong>
              <small>{selectedMarket.currency} / {selectedMarket.unit}</small>
            </div>
            <div>
              <span>24h change</span>
              <strong
                class:positive={selectedMarket.change24Hour > 0}
                class:negative={selectedMarket.change24Hour < 0}
              >
                {formatChange(selectedMarket.change24Hour)}
              </strong>
            </div>
            <div>
              <span>Volume</span>
              <strong>{formatNumber(selectedMarket.volume24Hour, 0)}</strong>
              <small>{selectedMarket.unit}</small>
            </div>
            <div>
              <span>Spread</span>
              <strong>{formatNumber(selectedMarket.spread, 2)}</strong>
              <small>{selectedMarket.currency}</small>
            </div>
          </div>
          <MarketChart
            history={selectedMarket.history}
            unit={selectedMarket.unit}
            label={`${selectedMarket.commodity} at ${selectedMarket.location}`}
          />
        {/if}
      </Panel>

      <Panel
        title="Order book"
        eyebrow="Committed limit orders"
        status={selectedMarket?.location ?? "No market"}
        class="book-panel"
      >
        {#if selectedMarket}
          <OrderBook
            book={selectedMarket.orderBook}
            currency={selectedMarket.currency}
            unit={selectedMarket.unit}
          />
        {:else}
          <p class="empty-state">Select an active market to inspect its book.</p>
        {/if}
      </Panel>

      <Panel
        title="Order ticket"
        eyebrow="Economic command"
        status="Limit only"
        class="order-panel"
      >
        <form class="command-form" onsubmit={placeOrder}>
          <fieldset disabled={!commandsEnabled}>
            <label>
              <span>Side</span>
              <select bind:value={orderSide}>
                <option value="buy">Buy</option>
                <option value="sell">Sell</option>
              </select>
            </label>
            <label>
              <span>Limit price / CR</span>
              <input type="number" min="0.01" step="0.01" bind:value={orderPrice} required />
            </label>
            <label>
              <span>Quantity / {selectedMarket?.unit ?? "unit"}</span>
              <input type="number" min="0.01" step="0.01" bind:value={orderQuantity} required />
            </label>
            <div class="command-summary">
              <span>Maximum notional</span>
              <strong>{formatCurrency(orderPrice * orderQuantity, false)}</strong>
            </div>
            <button class="primary-control" type="submit">
              {commandBusy === "market.place_order" ? "Awaiting authority…" : "Submit limit order"}
            </button>
          </fieldset>
          {#if !commandsEnabled}<small class="form-note">{commandGateReason}</small>{/if}
        </form>
      </Panel>

      <Panel
        title="Open orders"
        eyebrow="Company exposure"
        status={`${snapshot.openOrders.length} active`}
        class="orders-panel"
      >
        <div class="table-scroll">
          <table class="data-table">
            <thead>
              <tr>
                <th scope="col">Market</th>
                <th scope="col">Side</th>
                <th scope="col">Price</th>
                <th scope="col">Open</th>
                <th scope="col">Action</th>
              </tr>
            </thead>
            <tbody>
              {#each snapshot.openOrders as order}
                <tr>
                  <th scope="row">{marketLabel(order.marketId)}</th>
                  <td class:positive={order.side === "buy"} class:negative={order.side === "sell"}>
                    {order.side}
                  </td>
                  <td>{formatNumber(order.price, 2)}</td>
                  <td>{formatNumber(order.quantity - order.filledQuantity, 2)}</td>
                  <td>
                    <button
                      class="table-action"
                      type="button"
                      disabled={!commandsEnabled}
                      onclick={() => void cancelOrder(order.id)}
                    >
                      Cancel
                    </button>
                  </td>
                </tr>
              {:else}
                <tr><td class="empty-state" colspan="5">No open company orders.</td></tr>
              {/each}
            </tbody>
          </table>
        </div>
      </Panel>

      <Panel
        title="Trade tape"
        eyebrow="Committed fills"
        status={`${snapshot.trades.length} recent`}
        class="trades-panel"
      >
        <ol class="activity-list">
          {#each snapshot.trades as trade}
            <li>
              <div>
                <strong
                  class:positive={trade.side === "buy"}
                  class:negative={trade.side === "sell"}
                >
                  {trade.side}
                  {formatNumber(trade.quantity, 2)}
                </strong>
                <span>{marketLabel(trade.marketId)}</span>
              </div>
              <div>
                <strong>{formatNumber(trade.price, 2)} CR</strong>
                <time datetime={trade.occurredAt}>{formatTime(trade.occurredAt)}Z</time>
              </div>
            </li>
          {:else}
            <li class="empty-state">No committed trades in this window.</li>
          {/each}
        </ol>
      </Panel>

      <Panel
        title="Inventory"
        eyebrow="Location custody"
        status={`${snapshot.inventory.length} positions`}
        class="inventory-panel"
      >
        <div class="table-scroll">
          <table class="data-table">
            <thead>
              <tr>
                <th scope="col">Commodity</th>
                <th scope="col">Location</th>
                <th scope="col">Available</th>
                <th scope="col">Reserved</th>
              </tr>
            </thead>
            <tbody>
              {#each snapshot.inventory as position}
                <tr>
                  <th scope="row">{position.commodity}</th>
                  <td>{position.location}</td>
                  <td>{formatNumber(position.quantity - position.reserved, 1)} {position.unit}</td>
                  <td>{formatNumber(position.reserved, 1)} {position.unit}</td>
                </tr>
              {:else}
                <tr><td class="empty-state" colspan="4">No inventory positions.</td></tr>
              {/each}
            </tbody>
          </table>
        </div>
      </Panel>

      <Panel
        title="Facilities & production"
        eyebrow="Inputs become outputs"
        status={`${snapshot.facilities.length} facilities`}
        class="production-panel"
      >
        <div class="production-layout" id="production">
          <div class="capacity-list">
            {#each snapshot.facilities as facility}
              <article class="capacity-row">
                <div>
                  <strong>{facility.name}</strong>
                  <span>{facility.inputCommodity} → {facility.outputCommodity}</span>
                </div>
                <div class="capacity-bar" aria-label={`${facility.utilization}% utilized`}>
                  <span style={`width:${Math.min(100, Math.max(0, facility.utilization))}%`}></span>
                </div>
                <b>{facility.utilization}%</b>
                <small class:warning={facility.status !== "operational"}>{facility.status}</small>
              </article>
            {:else}
              <p class="empty-state">No facilities assigned to this company.</p>
            {/each}
          </div>
          <form class="command-form compact-form" onsubmit={runProduction}>
            <fieldset disabled={!commandsEnabled || snapshot.facilities.length === 0}>
              <label>
                <span>Facility</span>
                <select bind:value={productionFacilityId}>
                  {#each snapshot.facilities as facility}
                    <option value={facility.id}>{facility.name}</option>
                  {/each}
                </select>
              </label>
              <label>
                <span>Run quantity</span>
                <input
                  type="number"
                  min="0.01"
                  step="0.01"
                  bind:value={productionQuantity}
                  required
                />
              </label>
              <button class="primary-control" type="submit">Schedule production run</button>
            </fieldset>
          </form>
        </div>
        <ol class="trace" aria-label="Production and profitability trace">
          {#each snapshot.productionTrace as node}
            <li style={`--depth:${node.depth}`}>
              <span>{node.label}</span>
              <strong>{node.value}</strong>
              <small class:positive={node.change > 0} class:negative={node.change < 0}>
                {formatChange(node.change)}
              </small>
            </li>
          {:else}
            <li class="empty-state">No production trace is available.</li>
          {/each}
        </ol>
      </Panel>

      <Panel
        title="Freight"
        eyebrow="Location settlement"
        status={`${snapshot.freight.length} shipments`}
        class="freight-panel"
      >
        <ol class="activity-list">
          {#each snapshot.freight as shipment}
            <li>
              <div>
                <strong
                  >{shipment.cargo} · {formatNumber(shipment.quantity, 1)} {shipment.unit}</strong
                >
                <span>{shipment.origin} → {shipment.destination}</span>
              </div>
              <div>
                <span class:positive={shipment.status === "delivered"}>{shipment.status}</span>
                {#if shipment.status === "ready"}
                  <button
                    class="table-action"
                    type="button"
                    disabled={!commandsEnabled}
                    onclick={() => void deliverFreight(shipment.id)}
                  >
                    Deliver
                  </button>
                {:else}
                  <time datetime={shipment.eta}>{formatTime(shipment.eta)}Z</time>
                {/if}
              </div>
            </li>
          {:else}
            <li class="empty-state">No freight assigned.</li>
          {/each}
        </ol>
      </Panel>

      <Panel
        title="Devices & panels"
        eyebrow="Disposable display edge"
        status={`${snapshot.devices.length} registered`}
        class="device-panel"
      >
        <div id="network" class="device-list">
          {#each snapshot.devices as device}
            <div>
              <span class:positive={device.status === "online"}>{device.status}</span>
              <strong>{device.name}</strong>
              <small>{device.capabilities.join(", ") || "No capabilities"}</small>
            </div>
          {:else}
            <p class="empty-state">No registered devices.</p>
          {/each}
        </div>
        <form class="command-form compact-form" onsubmit={registerDevice}>
          <fieldset disabled={!commandsEnabled}>
            <label>
              <span>Register display device</span>
              <input bind:value={deviceName} maxlength="60" placeholder="Flight desk display" />
            </label>
            <button type="submit">Register</button>
          </fieldset>
        </form>
        <form class="command-form compact-form panel-form" onsubmit={sendPanel}>
          <fieldset disabled={!commandsEnabled || snapshot.panels.length === 0}>
            <label>
              <span>Device</span>
              <select bind:value={panelDeviceId}>
                {#each snapshot.devices as device}
                  <option value={device.id}>{device.name}</option>
                {/each}
              </select>
            </label>
            <label>
              <span>Panel</span>
              <select bind:value={panelId}>
                {#each snapshot.panels as panel}
                  <option value={panel.id}>{panel.name}</option>
                {/each}
              </select>
            </label>
            <label class="full-field">
              <span>Message</span>
              <input bind:value={panelMessage} maxlength="160" placeholder="Operational brief" />
            </label>
            <button class="primary-control full-field" type="submit">Send to panel</button>
          </fieldset>
        </form>
      </Panel>

      <Panel
        title="Company operations chat"
        eyebrow="Coordination"
        status={`${snapshot.chat.length} messages`}
        class="chat-panel"
      >
        <ol class="chat-log">
          {#each snapshot.chat as message}
            <li class:system-message={message.kind === "system"}>
              <div>
                <strong>{message.actorName}</strong>
                <time datetime={message.occurredAt}>{formatTime(message.occurredAt)}Z</time>
              </div>
              <p>{message.body}</p>
            </li>
          {:else}
            <li class="empty-state">No messages in company operations.</li>
          {/each}
        </ol>
        <form class="chat-form" onsubmit={sendChat}>
          <label>
            <span class="sr-only">Company operations message</span>
            <input bind:value={chatBody} maxlength="500" placeholder="Message company operations" />
          </label>
          <button type="submit" disabled={!commandsEnabled}>Send</button>
        </form>
      </Panel>

      <Panel
        title="Operator audit"
        eyebrow="Committed command trace"
        status={`${snapshot.operatorAudit.length} entries`}
        class="operator-panel"
      >
        <ol class="audit-log" id="operator">
          {#each snapshot.operatorAudit as entry}
            <li>
              <span class:positive={entry.outcome === "committed"}>{entry.outcome}</span>
              <strong>{entry.action}</strong>
              <small>{entry.actorName} · {entry.target} · {formatTime(entry.occurredAt)}Z</small>
            </li>
          {:else}
            <li class="empty-state">No operator activity in this window.</li>
          {/each}
        </ol>
        {#if snapshot.membership.permissions.includes("operator.compensate")}
          <form class="command-form compact-form compensation-form" onsubmit={compensate}>
            <fieldset disabled={!commandsEnabled || snapshot.operatorAudit.length === 0}>
              <label>
                <span>Compensate event</span>
                <select bind:value={compensationTarget}>
                  {#each snapshot.operatorAudit as entry}
                    <option value={entry.id}>{entry.action} / {entry.target}</option>
                  {/each}
                </select>
              </label>
              <label>
                <span>Reason</span>
                <input
                  bind:value={compensationReason}
                  maxlength="240"
                  placeholder="Required audit reason"
                />
              </label>
              <button class="danger-control" type="submit">Issue compensation</button>
            </fieldset>
          </form>
        {/if}
      </Panel>

      <Panel
        title="Alerts"
        eyebrow="Economic and system exceptions"
        status={`${snapshot.alerts.length} open`}
        class="alerts-panel"
      >
        <ul class="incidents">
          {#each snapshot.alerts as alert}
            <li>
              <span
                class:warning={alert.severity === "warning"}
                class:negative={alert.severity === "critical"}
              >
                {alert.severity}
              </span>
              <strong>{alert.summary}</strong>
              <time datetime={alert.occurredAt}>{formatTime(alert.occurredAt)}Z</time>
            </li>
          {:else}
            <li class="empty-state">No open alerts.</li>
          {/each}
        </ul>
      </Panel>
    </main>

    <footer class="ticker" aria-label="Market indices and snapshot freshness">
      {#each snapshot.indices as index}
        <div>
          <span>{index.name}</span>
          <strong>{formatNumber(index.value, 1)}</strong>
          <small class:positive={index.change > 0} class:negative={index.change < 0}>
            {formatChange(index.change)}
          </small>
        </div>
      {/each}
      <p>
        {sourceLabel} · {freshness} · seq {envelope?.sequence ?? "—"} · generated
        {envelope ? formatTime(envelope.generatedAt) : "—"}Z
      </p>
    </footer>
  {:else}
    <main class="terminal-unavailable">
      <section>
        <span>Authority state</span>
        <h1>{authRequired ? "Operator session required" : "Terminal unavailable"}</h1>
        <p>{terminalMessage ?? "No game snapshot could be loaded."}</p>
        <button
          class="primary-control"
          type="button"
          onclick={() => void (authRequired ? startSession() : refreshGame())}
        >
          {authRequired ? "Start development session" : "Retry authority"}
        </button>
      </section>
    </main>
  {/if}

  <nav class="mobile-nav" aria-label="Mobile terminal">
    <a href="#company">Company</a>
    <a href="#markets">Trade</a>
    <a href="#production">Produce</a>
    <a href="#network">Network</a>
    <a href="#operator">Audit</a>
  </nav>
</div>
