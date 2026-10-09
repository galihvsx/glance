// Typed same-origin fetch wrapper for the glance API.
//
// Cookie auth (glance_session) — credentials: "include" is always set so the
// session cookie is sent on every request. Non-2xx responses throw ApiError
// with the spec §5 envelope's error.message parsed when present
// ({"error":{"code":…,"message":…,"details":…}}).

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
    throw new ApiError(res.status, errorMessage(res, text));
  }
  return (text ? (JSON.parse(text) as T) : (undefined as T));
}

// errorMessage extracts the human message from an error response: prefer
// the spec §5 envelope's error.message, fall back to a legacy flat string
// error, then to the HTTP status text.
function errorMessage(res: Response, text: string): string {
  try {
    const parsed: unknown = JSON.parse(text);
    if (typeof parsed === "object" && parsed !== null && "error" in parsed) {
      const err = (parsed as { error: unknown }).error;
      if (typeof err === "object" && err !== null && "message" in err) {
        const message = (err as { message: unknown }).message;
        if (typeof message === "string" && message.length > 0) return message;
      }
      if (typeof err === "string" && err.length > 0) return err;
    }
  } catch {
    // Not JSON — fall through to the status text.
  }
  return res.statusText || `Request failed with status ${res.status}`;
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
