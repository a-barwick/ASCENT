import { PROTOCOL_VERSION } from "@ascent/protocol";
import { describe, expect, it, vi } from "vitest";

import { GameClient, ProtocolVersionError, createCommandEnvelope, type FetchLike } from "./client";

describe("GameClient", () => {
  it("reads a protocol-compatible game snapshot", async () => {
    const request = vi.fn<FetchLike>().mockResolvedValue(
      response({
        protocolVersion: PROTOCOL_VERSION,
        snapshotId: "snapshot-1",
        topic: "game.company-helios",
        sequence: 9,
        generatedAt: "2077-05-24T14:38:04.182Z",
        payload: { markets: [] },
      }),
    );

    const snapshot = await new GameClient(request).getGame();

    expect(snapshot.sequence).toBe(9);
    expect(request).toHaveBeenCalledWith(
      "/api/v1/game",
      expect.objectContaining({ credentials: "include" }),
    );
  });

  it("surfaces an unauthenticated response without hiding its status", async () => {
    const request = vi.fn<FetchLike>().mockResolvedValue(
      response(
        {
          protocolVersion: PROTOCOL_VERSION,
          error: { code: "unauthenticated", message: "Start a development session." },
        },
        401,
      ),
    );

    const promise = new GameClient(request).getGame();

    await expect(promise).rejects.toMatchObject({
      status: 401,
      code: "unauthenticated",
      message: "Start a development session.",
    });
  });

  it("preserves a safe validation failure from a command", async () => {
    const request = vi.fn<FetchLike>().mockResolvedValue(
      response(
        {
          protocolVersion: PROTOCOL_VERSION,
          errorCode: "invalid_quantity",
          safeMessage: "Quantity exceeds available inventory.",
        },
        422,
      ),
    );

    const promise = new GameClient(request).sendCommand(
      createCommandEnvelope(
        "market.place_order",
        { quantity: -1 },
        {
          actorId: "player-1",
          companyId: "company-1",
          commandId: "97e31a8c-51af-4eb5-9fd4-dae578c2a15c",
          idempotencyKey: "retry-test",
        },
      ),
    );

    await expect(promise).rejects.toMatchObject({
      status: 422,
      code: "invalid_quantity",
      message: "Quantity exceeds available inventory.",
      retryable: false,
    });
  });

  it("classifies an offline fetch as retryable", async () => {
    const request = vi.fn<FetchLike>().mockRejectedValue(new TypeError("network down"));

    const promise = new GameClient(request).getEvents(4);

    await expect(promise).rejects.toMatchObject({
      status: 0,
      code: "offline",
      retryable: true,
    });
  });

  it("rejects a mismatched protocol version", async () => {
    const request = vi.fn<FetchLike>().mockResolvedValue(
      response({
        protocolVersion: "0.9.0",
        snapshotId: "snapshot-old",
        topic: "game.company-helios",
        sequence: 1,
        generatedAt: "2077-05-24T14:38:04.182Z",
        payload: {},
      }),
    );

    await expect(new GameClient(request).getGame()).rejects.toBeInstanceOf(ProtocolVersionError);
  });

  it("uses a raw UUID command id and a distinct retry key", () => {
    const command = createCommandEnvelope(
      "chat.send",
      { channelId: "operations", body: "Ready." },
      { actorId: "player-1", companyId: "company-1" },
    );

    expect(command.commandId).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i,
    );
    expect(command.idempotencyKey).toMatch(/^retry-/);
  });
});

function response(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}
