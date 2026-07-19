import fixture from "../../../../../fixtures/market.json";

import { PROTOCOL_VERSION } from "@ascent/protocol";

import type { GameSnapshotEnvelope, Market } from "./game";

interface LegacyFixture {
  seed: number;
  generatedAt: string;
  systemTime: string;
  company: {
    id: string;
    name: string;
    cash: number;
    totalAssets: number;
    totalLiabilities: number;
    netWorth: number;
    creditRating: string;
    availableCredit: number;
    debtToEquityRatio: number;
  };
  market: Omit<Market, "orderBook">;
  orderBook: Market["orderBook"];
  facilities: Array<{
    id: string;
    name: string;
    type: string;
    location: string;
    utilization: number;
    change: number;
    status: "operational" | "constrained" | "offline";
  }>;
  indices: GameSnapshotEnvelope["payload"]["indices"];
  incidents: Array<{
    id: string;
    severity: "info" | "warning" | "critical";
    summary: string;
    time: string;
  }>;
  trace: Array<{
    label: string;
    value: string;
    change: number;
    depth: number;
  }>;
}

const seed = fixture as LegacyFixture;

export function fixtureGameSnapshot(): GameSnapshotEnvelope {
  if (!import.meta.env.DEV) {
    throw new Error("The deterministic game fixture is available only in development.");
  }

  const primaryMarket: Market = {
    ...seed.market,
    orderBook: seed.orderBook,
  };
  const orbitMarket: Market = {
    id: "market-orbit-oxygen",
    location: "Lunar orbit",
    commodity: "Liquid oxygen",
    unit: "t",
    currency: "CR",
    lastPrice: 521.35,
    change24Hour: 0.8,
    volume24Hour: 9230,
    spread: 1.2,
    history: seed.market.history.map((point, index) => ({
      label: point.label,
      value: 508.4 + index * 2.05 + (index % 2 === 0 ? 0.8 : -0.6),
    })),
    orderBook: {
      bids: seed.orderBook.bids.map((level, index) => ({
        ...level,
        price: 520.6 - index * 0.55,
        quantity: Math.round(level.quantity * 0.72 * 10) / 10,
      })),
      asks: seed.orderBook.asks.map((level, index) => ({
        ...level,
        price: 521.8 + index * 0.65,
        quantity: Math.round(level.quantity * 0.81 * 10) / 10,
      })),
    },
  };

  return {
    protocolVersion: PROTOCOL_VERSION,
    snapshotId: `fixture-${seed.seed}`,
    topic: `game.${seed.company.id}`,
    sequence: 1842,
    generatedAt: seed.generatedAt,
    expiresAt: seed.generatedAt,
    payload: {
      systemTime: seed.systemTime,
      actor: {
        id: "player-fixture-operator",
        displayName: "Mara Vance",
        status: "authenticated",
      },
      membership: {
        companyId: seed.company.id,
        role: "chief operator",
        permissions: [
          "market.trade",
          "production.run",
          "freight.deliver",
          "device.manage",
          "chat.send",
          "operator.compensate",
        ],
      },
      company: {
        ...seed.company,
        version: 27,
        statements: [
          { label: "Revenue / 24h", value: 3_280_000_000, change: 2.4 },
          { label: "Operating cost / 24h", value: 1_960_000_000, change: 6.1 },
          { label: "EBITDA / 24h", value: 1_320_000_000, change: -4.6 },
        ],
      },
      markets: [primaryMarket, orbitMarket],
      openOrders: [
        {
          id: "order-helios-1840",
          marketId: primaryMarket.id,
          side: "buy",
          orderType: "limit",
          price: 306.1,
          quantity: 120,
          filledQuantity: 40,
          status: "partially_filled",
          createdAt: "2077-05-24T14:32:14.000Z",
        },
      ],
      trades: [
        {
          id: "trade-1841",
          marketId: primaryMarket.id,
          side: "buy",
          price: 307.8,
          quantity: 40,
          total: 12_312,
          counterparty: "Aster Supply",
          occurredAt: "2077-05-24T14:35:42.000Z",
        },
        {
          id: "trade-1838",
          marketId: orbitMarket.id,
          side: "sell",
          price: 520.4,
          quantity: 18,
          total: 9_367.2,
          counterparty: "Cislunar Transit",
          occurredAt: "2077-05-24T14:27:08.000Z",
        },
      ],
      inventory: [
        {
          id: "inventory-water-moon",
          commodity: "Water ice",
          location: "Lunar south pole",
          quantity: 8_420,
          reserved: 1_200,
          unit: "t",
        },
        {
          id: "inventory-oxygen-orbit",
          commodity: "Liquid oxygen",
          location: "Lunar orbit",
          quantity: 3_180,
          reserved: 340,
          unit: "t",
        },
        {
          id: "inventory-propellant-moon",
          commodity: "Methalox propellant",
          location: "Moon",
          quantity: 2_460,
          reserved: 880,
          unit: "t",
        },
      ],
      facilities: seed.facilities.map((facility, index) => ({
        ...facility,
        inputCommodity: index === 0 ? "Regolith" : index === 3 ? "Solar flux" : "Water ice",
        outputCommodity:
          index === 0
            ? "Structural alloy"
            : index === 1
              ? "Purified water"
              : index === 2
                ? "Methalox propellant"
                : "Power",
        capacity: [520, 780, 410, 96][index] ?? 100,
        capacityUnit: index === 3 ? "MWh" : "t/day",
      })),
      productionTrace: seed.trace.map((node, index) => ({
        ...node,
        id: `trace-${index + 1}`,
        status:
          node.depth === 0
            ? "output"
            : node.label.toLowerCase().includes("congestion")
              ? "constraint"
              : node.depth > 2
                ? "input"
                : "process",
      })),
      freight: [
        {
          id: "freight-042",
          origin: "Lunar south pole",
          destination: "Lunar orbit",
          cargo: "Liquid oxygen",
          quantity: 160,
          unit: "t",
          status: "ready",
          eta: "2077-05-24T16:10:00.000Z",
        },
        {
          id: "freight-039",
          origin: "Lunar orbit",
          destination: "LEO",
          cargo: "Methalox propellant",
          quantity: 220,
          unit: "t",
          status: "in_transit",
          eta: "2077-05-25T03:40:00.000Z",
        },
      ],
      devices: [
        {
          id: "device-desk-01",
          name: "Operations wall",
          status: "online",
          lastSeenAt: "2077-05-24T14:37:58.000Z",
          capabilities: ["panel.receive"],
        },
      ],
      panels: [
        {
          id: "panel-market-01",
          name: "Market ribbon",
          deviceId: "device-desk-01",
          status: "ready",
          lastMessage: "LOX spread widening at Lunar orbit",
        },
      ],
      chat: [
        {
          id: "chat-011",
          channelId: "company-operations",
          actorId: "player-dispatch",
          actorName: "Ilya / Dispatch",
          body: "Freight 042 is staged for release.",
          kind: "player",
          occurredAt: "2077-05-24T14:29:00.000Z",
        },
        {
          id: "chat-012",
          channelId: "company-operations",
          actorId: "system",
          actorName: "Settlement",
          body: "Trade 1841 committed and ledger-balanced.",
          kind: "system",
          occurredAt: "2077-05-24T14:35:43.000Z",
        },
      ],
      alerts: seed.incidents.map((incident) => ({
        id: incident.id,
        severity: incident.severity,
        summary: incident.summary,
        occurredAt: `2077-05-24T${incident.time}:00.000Z`,
      })),
      operatorAudit: [
        {
          id: "audit-1841",
          actorName: "Mara Vance",
          action: "market.order.fill",
          target: "trade-1841",
          outcome: "committed",
          occurredAt: "2077-05-24T14:35:43.000Z",
        },
        {
          id: "audit-1839",
          actorName: "Ilya / Dispatch",
          action: "freight.stage",
          target: "freight-042",
          outcome: "committed",
          occurredAt: "2077-05-24T14:29:00.000Z",
        },
      ],
      indices: seed.indices,
    },
  };
}
