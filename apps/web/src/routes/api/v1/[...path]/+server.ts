import { env } from "$env/dynamic/public";
import { PROTOCOL_VERSION } from "@ascent/protocol";
import { json, type RequestHandler } from "@sveltejs/kit";

const allowedPaths = new Set(["dev/session", "game", "commands", "events"]);

const proxy: RequestHandler = async ({ fetch, params, request, url }) => {
  const path = params.path ?? "";
  if (!allowedPaths.has(path)) {
    return json(
      { error: { code: "not_found", message: "Unknown ASCENT API route." } },
      { status: 404 },
    );
  }

  const apiBase = env.PUBLIC_ASCENT_API_URL || "http://127.0.0.1:8080";
  const target = new URL(`/api/v1/${path}${url.search}`, apiBase);
  const headers = new Headers();
  for (const name of ["accept", "content-type", "cookie", "x-request-id"]) {
    const value = request.headers.get(name);
    if (value) headers.set(name, value);
  }

  try {
    const upstream = await fetch(target, {
      method: request.method,
      headers,
      body:
        request.method === "GET" || request.method === "HEAD" ? undefined : await request.text(),
      redirect: "manual",
    });
    const responseHeaders = new Headers();
    for (const name of ["content-type", "cache-control", "set-cookie"]) {
      const value = upstream.headers.get(name);
      if (value) responseHeaders.set(name, value);
    }
    return new Response(upstream.body, {
      status: upstream.status,
      headers: responseHeaders,
    });
  } catch {
    return json(
      {
        protocolVersion: PROTOCOL_VERSION,
        error: {
          code: "upstream_unavailable",
          message: "The ASCENT authority is unavailable.",
        },
      },
      { status: 502 },
    );
  }
};

export const GET = proxy;
export const POST = proxy;
