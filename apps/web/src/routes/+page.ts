import { ApiError, GameClient, describeApiError } from "$lib/api/client";
import type { GameSnapshotEnvelope, SnapshotSource } from "$lib/domain/game";

import type { PageLoad } from "./$types";

export interface TerminalPageData {
  snapshot: GameSnapshotEnvelope | null;
  source: SnapshotSource;
  authRequired: boolean;
  message: string | null;
}

export const load: PageLoad<TerminalPageData> = async ({ fetch }) => {
  const client = new GameClient(fetch);

  try {
    return {
      snapshot: await client.getGame(),
      source: "authority" as const,
      authRequired: false,
      message: null,
    };
  } catch (error) {
    const authRequired = error instanceof ApiError && error.status === 401;
    if (import.meta.env.DEV) {
      const { fixtureGameSnapshot } = await import("$lib/domain/fallback");
      return {
        snapshot: fixtureGameSnapshot(),
        source: "fixture" as const,
        authRequired,
        message: authRequired
          ? "Start a development session to enable commands. Showing a read-only fixture."
          : `${describeApiError(error)} Showing a read-only development fixture.`,
      };
    }

    return {
      snapshot: null,
      source: "unavailable" as const,
      authRequired,
      message: describeApiError(error),
    };
  }
};
