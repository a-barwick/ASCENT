import { describe, expect, it } from "vitest";

import { bookDepthMaximum, canIssueCommands, chartRange, freshnessAt, type Actor } from "./game";

const actor: Actor = {
  id: "player-1",
  displayName: "Operator",
  status: "authenticated",
};

describe("snapshot freshness", () => {
  const now = Date.parse("2077-05-24T14:38:30.000Z");

  it("distinguishes fresh, stale, expired, and unknown snapshots", () => {
    expect(freshnessAt("2077-05-24T14:38:20.000Z", now)).toBe("fresh");
    expect(freshnessAt("2077-05-24T14:37:59.000Z", now)).toBe("stale");
    expect(freshnessAt("2077-05-24T14:36:00.000Z", now)).toBe("expired");
    expect(freshnessAt("not-a-date", now)).toBe("unknown");
    expect(freshnessAt(null, now)).toBe("unknown");
  });

  it("opens the command gate only for fresh authenticated authority state", () => {
    expect(canIssueCommands(actor, "fresh", "authority")).toBe(true);
    expect(canIssueCommands(actor, "stale", "authority")).toBe(false);
    expect(canIssueCommands(actor, "fresh", "fixture")).toBe(false);
    expect(canIssueCommands({ ...actor, status: "expired" }, "fresh", "authority")).toBe(false);
  });
});

describe("empty market helpers", () => {
  it("returns no chart range for empty or non-finite history", () => {
    expect(chartRange([])).toBeNull();
    expect(chartRange([{ label: "now", value: Number.NaN }])).toBeNull();
  });

  it("builds a padded range for a single settled value", () => {
    expect(chartRange([{ label: "now", value: 308.25 }])).toEqual({
      minimum: 306.25,
      maximum: 310.25,
    });
  });

  it("uses a safe book denominator when both sides are empty", () => {
    expect(bookDepthMaximum({ bids: [], asks: [] })).toBe(1);
    expect(
      bookDepthMaximum({
        bids: [{ price: 2, quantity: 8, orders: 1 }],
        asks: [{ price: 3, quantity: 14, orders: 2 }],
      }),
    ).toBe(14);
  });
});
