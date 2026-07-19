import {
  PROTOCOL_VERSION,
  type CommandEnvelope,
  type CommandResultEnvelope,
  type EventEnvelope,
} from "@ascent/protocol";

import type { Actor, GameSnapshotEnvelope } from "$lib/domain/game";

export type FetchLike = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;

export interface DevSessionResponse {
  protocolVersion: string;
  session: {
    id: string;
    actorId: string;
    expiresAt: string;
  };
  actor: Actor;
}

export interface EventBatch {
  protocolVersion: string;
  after: number;
  latestSequence: number;
  events: EventEnvelope[];
}

export interface CommandOptions {
  actorId: string;
  companyId: string;
  expectedVersion?: number;
  commandId?: string;
  idempotencyKey?: string;
  now?: Date;
}

interface ErrorBody {
  errorCode?: string | null;
  safeMessage?: string | null;
  error?: {
    code?: string;
    message?: string;
  };
}

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly retryable: boolean;

  constructor(message: string, status: number, code: string, retryable = false) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.retryable = retryable;
  }
}

export class ProtocolVersionError extends Error {
  readonly receivedVersion: string;

  constructor(receivedVersion: string) {
    super(
      `Protocol ${receivedVersion || "unknown"} is not supported; expected ${PROTOCOL_VERSION}.`,
    );
    this.name = "ProtocolVersionError";
    this.receivedVersion = receivedVersion;
  }
}

export class GameClient {
  constructor(
    private readonly request: FetchLike,
    private readonly baseUrl = "/api/v1",
  ) {}

  createDevSession(): Promise<DevSessionResponse> {
    return this.send<DevSessionResponse>("/dev/session", {
      method: "POST",
      body: JSON.stringify({}),
    });
  }

  getGame(): Promise<GameSnapshotEnvelope> {
    return this.send<GameSnapshotEnvelope>("/game");
  }

  sendCommand(command: CommandEnvelope): Promise<CommandResultEnvelope> {
    return this.send<CommandResultEnvelope>("/commands", {
      method: "POST",
      body: JSON.stringify(command),
    });
  }

  getEvents(after: number): Promise<EventBatch> {
    return this.send<EventBatch>(`/events?after=${encodeURIComponent(String(after))}`);
  }

  private async send<T>(path: string, init: RequestInit = {}): Promise<T> {
    let response: Response;
    try {
      response = await this.request(`${this.baseUrl}${path}`, {
        credentials: "include",
        ...init,
        headers: {
          Accept: "application/json",
          ...(init.body ? { "Content-Type": "application/json" } : {}),
          ...init.headers,
        },
      });
    } catch {
      throw new ApiError(
        "The ASCENT authority is unreachable. Your last committed view has been retained.",
        0,
        "offline",
        true,
      );
    }

    const body = await readJson(response);
    if (!response.ok) {
      const error = body as ErrorBody | null;
      const message =
        error?.safeMessage ??
        error?.error?.message ??
        (response.status === 401
          ? "Your operator session is missing or expired."
          : "The authority rejected this request.");
      const code =
        error?.errorCode ??
        error?.error?.code ??
        (response.status === 401 ? "unauthenticated" : `http_${response.status}`);
      throw new ApiError(message, response.status, code, response.status >= 500);
    }

    if (!body || typeof body !== "object") {
      throw new ApiError(
        "The authority returned an invalid JSON envelope.",
        502,
        "invalid_response",
      );
    }
    assertProtocolVersion(body);
    return body as T;
  }
}

export function createCommandEnvelope(
  type: string,
  payload: Record<string, unknown>,
  options: CommandOptions,
): CommandEnvelope {
  const commandId = options.commandId ?? makeUuid();
  return {
    protocolVersion: PROTOCOL_VERSION,
    commandId,
    idempotencyKey: options.idempotencyKey ?? makeId("retry"),
    type,
    actorId: options.actorId,
    companyId: options.companyId,
    expectedVersion: options.expectedVersion ?? null,
    receivedAt: (options.now ?? new Date()).toISOString(),
    payload,
  };
}

export function describeApiError(error: unknown): string {
  if (error instanceof Error) return error.message;
  return "An unexpected terminal error occurred.";
}

function assertProtocolVersion(value: object): void {
  const protocolVersion =
    "protocolVersion" in value && typeof value.protocolVersion === "string"
      ? value.protocolVersion
      : "";
  if (protocolVersion !== PROTOCOL_VERSION) {
    throw new ProtocolVersionError(protocolVersion);
  }
}

async function readJson(response: Response): Promise<unknown> {
  try {
    return await response.json();
  } catch {
    return null;
  }
}

function makeId(prefix: string): string {
  return `${prefix}-${makeUuid()}`;
}

function makeUuid(): string {
  if (typeof globalThis.crypto?.randomUUID === "function") {
    return globalThis.crypto.randomUUID();
  }
  return "xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx".replace(/[xy]/g, (character) => {
    const random = Math.floor(Math.random() * 16);
    const value = character === "x" ? random : (random & 0x3) | 0x8;
    return value.toString(16);
  });
}
