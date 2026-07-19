import type { OrderBook } from "./game";

export type { Market, OrderBook, OrderLevel, PricePoint } from "./game";

export function formatCurrency(value: number, compact = false, currency = "CR"): string {
  if (currency === "CR") {
    return `${compactNumber(value, compact)} CR`;
  }
  return new Intl.NumberFormat("en-US", {
    style: "currency",
    currency,
    notation: compact ? "compact" : "standard",
    maximumFractionDigits: 2,
  }).format(value);
}

export function formatNumber(value: number, maximumFractionDigits = 1): string {
  if (!Number.isFinite(value)) return "—";
  return new Intl.NumberFormat("en-US", {
    maximumFractionDigits,
  }).format(value);
}

export function formatChange(value: number): string {
  if (!Number.isFinite(value)) return "—";
  const sign = value > 0 ? "+" : "";
  return `${sign}${value.toFixed(1)}%`;
}

export function formatTime(value: string): string {
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) return "Unknown";
  return new Intl.DateTimeFormat("en-US", {
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
    timeZone: "UTC",
  }).format(date);
}

export function bestPrices(book: OrderBook): { bid: number; ask: number; spread: number } {
  const bid = book.bids.at(0)?.price ?? 0;
  const ask = book.asks.at(0)?.price ?? 0;
  return {
    bid,
    ask,
    spread: Math.max(0, Math.round((ask - bid + Number.EPSILON) * 100) / 100),
  };
}

function compactNumber(value: number, compact: boolean): string {
  if (!Number.isFinite(value)) return "—";
  return new Intl.NumberFormat("en-US", {
    notation: compact ? "compact" : "standard",
    maximumFractionDigits: 2,
  }).format(value);
}
