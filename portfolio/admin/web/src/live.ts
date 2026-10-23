import { api, Unauthorized } from "./api";
import type { LiveMessage } from "./types";

/** What the open views need: the agent topics to send, and the container whose log to follow ("" for none). */
export interface Subscription {
  topics: string[];
  logs: string;
}

const RETRY_MIN_MS = 1000;
const RETRY_MAX_MS = 15000;

/**
 * One WebSocket to /admin/api/live. It reconnects with backoff and resends the
 * current subscription on every open. If a reconnect finds the session gone,
 * it reports that instead of retrying forever.
 */
export class LiveConnection {
  private ws: WebSocket | null = null;
  private sub: Subscription = { topics: [], logs: "" };
  private retryMs = RETRY_MIN_MS;
  private retryTimer: number | undefined;
  private closed = false;

  constructor(
    private readonly onMessage: (m: LiveMessage) => void,
    private readonly onAuthLost: () => void,
    private readonly onConnected: (connected: boolean) => void,
  ) {}

  start() {
    this.open();
  }

  subscribe(sub: Subscription) {
    this.sub = sub;
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify(sub));
  }

  close() {
    this.closed = true;
    window.clearTimeout(this.retryTimer);
    this.ws?.close();
  }

  private open() {
    const scheme = location.protocol === "https:" ? "wss" : "ws";
    const ws = new WebSocket(`${scheme}://${location.host}/admin/api/live`);
    this.ws = ws;
    ws.onopen = () => {
      this.retryMs = RETRY_MIN_MS;
      this.onConnected(true);
      ws.send(JSON.stringify(this.sub));
    };
    ws.onmessage = (e) => this.onMessage(JSON.parse(e.data) as LiveMessage);
    ws.onclose = () => {
      this.onConnected(false);
      if (this.closed) return;
      this.retryTimer = window.setTimeout(() => this.reconnect(), this.retryMs);
      this.retryMs = Math.min(this.retryMs * 2, RETRY_MAX_MS);
    };
  }

  // A closed socket does not say why it closed, so a reconnect first asks
  // whether the session still exists. An expired session means sign-in, not retry.
  private async reconnect() {
    try {
      await api.me();
    } catch (e) {
      if (e instanceof Unauthorized) {
        this.onAuthLost();
        return;
      }
    }
    if (!this.closed) this.open();
  }
}
