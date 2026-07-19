import type { SnapshotEnvelope } from "@ascent/protocol";

export type SnapshotSource = "authority" | "fixture" | "stale" | "unavailable";
export type Freshness = "fresh" | "stale" | "expired" | "unknown";
export type Side = "buy" | "sell";

export type GameSnapshotEnvelope = Omit<SnapshotEnvelope, "payload"> & {
  payload: GameSnapshot;
};

export interface GameSnapshot {
  systemTime: string;
  actor: Actor;
  membership: Membership;
  company: Company;
  markets: Market[];
  openOrders: OpenOrder[];
  trades: Trade[];
  inventory: InventoryPosition[];
  facilities: Facility[];
  productionTrace: TraceNode[];
  freight: FreightShipment[];
  devices: Device[];
  panels: DevicePanel[];
  chat: ChatMessage[];
  alerts: Alert[];
  operatorAudit: AuditEntry[];
  indices: MarketIndex[];
}

export interface Actor {
  id: string;
  displayName: string;
  status: "authenticated" | "expired" | "anonymous";
}

export interface Membership {
  companyId: string;
  role: string;
  permissions: string[];
}

export interface Company {
  id: string;
  name: string;
  version: number;
  cash: number;
  totalAssets: number;
  totalLiabilities: number;
  netWorth: number;
  creditRating: string;
  availableCredit: number;
  debtToEquityRatio: number;
  statements: CompanyStatement[];
}

export interface CompanyStatement {
  label: string;
  value: number;
  change: number | null;
}

export interface Market {
  id: string;
  location: string;
  commodity: string;
  unit: string;
  currency: string;
  lastPrice: number;
  change24Hour: number;
  volume24Hour: number;
  spread: number;
  history: PricePoint[];
  orderBook: OrderBook;
}

export interface PricePoint {
  label: string;
  value: number;
}

export interface OrderBook {
  bids: OrderLevel[];
  asks: OrderLevel[];
}

export interface OrderLevel {
  price: number;
  quantity: number;
  orders: number;
}

export interface OpenOrder {
  id: string;
  marketId: string;
  side: Side;
  orderType: "limit";
  price: number;
  quantity: number;
  filledQuantity: number;
  status: "open" | "partially_filled" | "cancelled";
  createdAt: string;
}

export interface Trade {
  id: string;
  marketId: string;
  side: Side;
  price: number;
  quantity: number;
  total: number;
  counterparty: string;
  occurredAt: string;
}

export interface InventoryPosition {
  id: string;
  commodity: string;
  location: string;
  quantity: number;
  reserved: number;
  unit: string;
}

export interface Facility {
  id: string;
  name: string;
  type: string;
  location: string;
  utilization: number;
  change: number;
  status: "operational" | "constrained" | "offline";
  inputCommodity: string;
  outputCommodity: string;
  capacity: number;
  capacityUnit: string;
}

export interface TraceNode {
  id: string;
  label: string;
  value: string;
  change: number;
  depth: number;
  status: "input" | "process" | "output" | "constraint";
}

export interface FreightShipment {
  id: string;
  origin: string;
  destination: string;
  cargo: string;
  quantity: number;
  unit: string;
  status: "scheduled" | "in_transit" | "ready" | "delivered";
  eta: string;
}

export interface Device {
  id: string;
  name: string;
  status: "online" | "offline" | "pending";
  lastSeenAt: string | null;
  capabilities: string[];
}

export interface DevicePanel {
  id: string;
  name: string;
  deviceId: string;
  status: "ready" | "busy" | "offline";
  lastMessage: string | null;
}

export interface ChatMessage {
  id: string;
  channelId: string;
  actorId: string;
  actorName: string;
  body: string;
  kind: "player" | "system";
  occurredAt: string;
}

export interface Alert {
  id: string;
  severity: "info" | "warning" | "critical";
  summary: string;
  occurredAt: string;
}

export interface AuditEntry {
  id: string;
  actorName: string;
  action: string;
  target: string;
  outcome: "committed" | "rejected" | "compensated";
  occurredAt: string;
}

export interface MarketIndex {
  name: string;
  value: number;
  change: number;
}

export function freshnessAt(
  generatedAt: string | null | undefined,
  now = Date.now(),
  staleAfterMs = 30_000,
  expireAfterMs = 120_000,
): Freshness {
  if (!generatedAt) return "unknown";
  const generated = Date.parse(generatedAt);
  if (!Number.isFinite(generated)) return "unknown";

  const age = Math.max(0, now - generated);
  if (age > expireAfterMs) return "expired";
  if (age > staleAfterMs) return "stale";
  return "fresh";
}

export function canIssueCommands(
  actor: Actor | null | undefined,
  freshness: Freshness,
  source: SnapshotSource,
): boolean {
  return actor?.status === "authenticated" && freshness === "fresh" && source === "authority";
}

export function chartRange(history: PricePoint[]): { minimum: number; maximum: number } | null {
  const values = history.map((point) => point.value).filter(Number.isFinite);
  if (values.length === 0) return null;

  const minimumValue = Math.min(...values);
  const maximumValue = Math.max(...values);
  const padding = Math.max(2, (maximumValue - minimumValue) * 0.05);
  return {
    minimum: minimumValue - padding,
    maximum: maximumValue + padding,
  };
}

export function bookDepthMaximum(book: OrderBook): number {
  const quantities = [...book.bids, ...book.asks]
    .map((level) => level.quantity)
    .filter((quantity) => Number.isFinite(quantity) && quantity > 0);
  return quantities.length === 0 ? 1 : Math.max(...quantities);
}
