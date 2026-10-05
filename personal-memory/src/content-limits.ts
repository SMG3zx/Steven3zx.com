export const DEFAULT_MAX_CONTENT_BYTES = 1_000_000;

function configuredLimit() {
  const raw = Number(process.env.MAX_CONTENT_BYTES ?? DEFAULT_MAX_CONTENT_BYTES);
  if (!Number.isSafeInteger(raw) || raw < 1_024 || raw > 10_000_000) {
    throw new Error("MAX_CONTENT_BYTES must be an integer between 1024 and 10000000");
  }
  return raw;
}

export class ContentTooLargeError extends Error {
  readonly bytes: number;
  readonly maxBytes: number;

  constructor(bytes: number, maxBytes = configuredLimit()) {
    super(`content is too large (${bytes} bytes); maximum is ${maxBytes} bytes`);
    this.name = "ContentTooLargeError";
    this.bytes = bytes;
    this.maxBytes = maxBytes;
  }
}

export function validateContent(content: string) {
  const maxBytes = configuredLimit();
  const bytes = Buffer.byteLength(content, "utf8");
  if (bytes > maxBytes) throw new ContentTooLargeError(bytes, maxBytes);
  return bytes;
}
