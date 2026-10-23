export class ApiError extends Error {
  readonly status: number;

  constructor(status: number, path: string) {
    super(`gateway answered ${status} for ${path}`);
    this.name = "ApiError";
    this.status = status;
  }
}

type ApiOptions = {
  baseUrl: string;
  fetch?: typeof fetch;
};

type RequestOptions = {
  /** The visitor's Cookie header, forwarded so the gateway can identify them. */
  cookie?: string;
};

type SendOptions = RequestOptions & {
  body?: unknown;
};

export function createApi({ baseUrl, fetch: fetchImpl = fetch }: ApiOptions) {
  return {
    async get<T>(path: string, { cookie }: RequestOptions): Promise<T> {
      const headers = new Headers();
      if (cookie) {
        headers.set("cookie", cookie);
      }
      const response = await fetchImpl(`${baseUrl}${path}`, { headers });
      if (!response.ok) {
        throw new ApiError(response.status, path);
      }
      return (await response.json()) as T;
    },

    /**
     * Sends a request and returns the raw response, whatever its status. Callers
     * that forward the gateway's cookies or show its error messages need that.
     */
    async send(method: string, path: string, { cookie, body }: SendOptions): Promise<Response> {
      const headers = new Headers();
      if (cookie) {
        headers.set("cookie", cookie);
      }
      if (body !== undefined) {
        headers.set("content-type", "application/json");
      }
      return fetchImpl(`${baseUrl}${path}`, {
        method,
        headers,
        body: body === undefined ? undefined : JSON.stringify(body),
      });
    },
  };
}
