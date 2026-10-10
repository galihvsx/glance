// Typed same-origin fetch wrapper for the glance API.
//
// Cookie auth (glance_session) — credentials: "include" is always set so the
// session cookie is sent on every request. Non-2xx responses throw ApiError
// with the spec §5 envelope's error.message parsed when present
// ({"error":{"code":…,"message":…,"details":…}}); the envelope's code and
// details ride along on ApiError so callers can branch on machine-readable
// errors (e.g. open_blockers) without reparsing.

export class ApiError extends Error {
  readonly status: number;
  readonly body: string;
  readonly code?: string;
  readonly details?: unknown;

  constructor(status: number, body: string, code?: string, details?: unknown) {
    super(body || `Request failed with status ${status}`);
    this.name = "ApiError";
    this.status = status;
    this.body = body;
    this.code = code;
    this.details = details;
  }
}

// errorEnvelopeFields parses the spec §5 error envelope, returning the
// human message plus the machine-readable code and structured details
// when present.
function errorEnvelopeFields(
  res: Response,
  text: string,
): { message: string; code?: string; details?: unknown } {
  try {
    const parsed: unknown = JSON.parse(text);
    if (typeof parsed === "object" && parsed !== null && "error" in parsed) {
      const err = (parsed as { error: unknown }).error;
      if (typeof err === "object" && err !== null) {
        const e = err as {
          message?: unknown;
          code?: unknown;
          details?: unknown;
        };
        const message =
          typeof e.message === "string" && e.message.length > 0
            ? e.message
            : typeof err === "string" && (err as string).length > 0
              ? (err as string)
              : res.statusText || `Request failed with status ${res.status}`;
        return {
          message,
          code: typeof e.code === "string" ? e.code : undefined,
          details: "details" in e ? e.details : undefined,
        };
      }
      if (typeof err === "string" && err.length > 0) return { message: err };
    }
  } catch {
    // Not JSON — fall through to the status text.
  }
  return { message: res.statusText || `Request failed with status ${res.status}` };
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  // FormData bodies (file uploads) must NOT get a Content-Type header:
  // the browser sets multipart/form-data with the boundary itself.
  const isForm =
    typeof FormData !== "undefined" && init.body instanceof FormData;
  const res = await fetch(path, {
    ...init,
    // Cookie auth: always include credentials, even if a caller passes init.
    credentials: "include",
    headers: {
      ...(isForm ? {} : { "Content-Type": "application/json" }),
      ...(init.headers ?? {}),
    },
  });

  const text = await res.text();
  if (!res.ok) {
    const parsed = errorEnvelopeFields(res, text);
    throw new ApiError(res.status, parsed.message, parsed.code, parsed.details);
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
  patch: <T>(path: string, body?: unknown): Promise<T> =>
    request<T>(path, {
      method: "PATCH",
      body: body === undefined ? undefined : JSON.stringify(body),
    }),
  put: <T>(path: string, body?: unknown): Promise<T> =>
    request<T>(path, {
      method: "PUT",
      body: body === undefined ? undefined : JSON.stringify(body),
    }),
  del: <T = void>(path: string, body?: unknown): Promise<T> =>
    request<T>(path, {
      method: "DELETE",
      body: body === undefined ? undefined : JSON.stringify(body),
    }),
  /**
   * POST a multipart FormData body (file uploads). Unlike `post`, this
   * must NOT set Content-Type: the browser sets it with the multipart
   * boundary. Cookie auth still applies via `request`.
   */
  postForm: <T>(path: string, form: FormData): Promise<T> =>
    request<T>(path, { method: "POST", body: form }),
};
