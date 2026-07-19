import { describe, expect, it } from "vitest";

import { bestPrices, formatChange } from "./market";

describe("market formatting", () => {
  it("makes positive changes explicit", () => {
    expect(formatChange(1.7)).toBe("+1.7%");
    expect(formatChange(-0.4)).toBe("-0.4%");
  });

  it("derives the visible spread from the best levels", () => {
    expect(
      bestPrices({
        bids: [{ price: 307.8, quantity: 10, orders: 1 }],
        asks: [{ price: 308.75, quantity: 12, orders: 2 }],
      }),
    ).toEqual({ bid: 307.8, ask: 308.75, spread: 0.95 });
  });

  it("does not expose a negative spread for an invalid fixture", () => {
    expect(
      bestPrices({
        bids: [{ price: 10, quantity: 1, orders: 1 }],
        asks: [{ price: 9, quantity: 1, orders: 1 }],
      }).spread,
    ).toBe(0);
  });
});
