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
  };
}
