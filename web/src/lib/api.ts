// Typed same-origin fetch wrapper for the glance API.
//
// Cookie auth (glance_session) — credentials: "include" is always set so the
// session cookie is sent on every request. Non-2xx responses throw ApiError
// with the {"error": ...} message parsed when present.

export class ApiError extends Error {
  readonly status: number;
  readonly body: string;

  constructor(status: number, body: string) {
    super(body || `Request failed with status ${status}`);
    this.name = "ApiError";
    this.status = status;
    this.body = body;
  }
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const res = await fetch(path, {
    ...init,
    // Cookie auth: always include credentials, even if a caller passes init.
    credentials: "include",
    headers: { "Content-Type": "application/json", ...(init.headers ?? {}) },
  });

  const text = await res.text();
  if (!res.ok) {
    let message = text;
    try {
      const parsed: unknown = JSON.parse(text);
      if (
        typeof parsed === "object" &&
        parsed !== null &&
        "error" in parsed &&
        typeof (parsed as { error: unknown }).error === "string"
      ) {
        message = (parsed as { error: string }).error;
      }
    } catch {
      // Keep the raw body as the message.
    }
    throw new ApiError(res.status, message);
  }
  return (text ? (JSON.parse(text) as T) : (undefined as T));
}

export const api = {
  get: <T>(path: string): Promise<T> => request<T>(path),
  post: <T>(path: string, body?: unknown): Promise<T> =>
    request<T>(path, {
      method: "POST",
      body: body === undefined ? undefined : JSON.stringify(body),
    }),
};
