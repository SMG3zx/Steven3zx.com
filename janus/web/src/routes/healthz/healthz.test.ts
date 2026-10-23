import { describe, expect, test } from "bun:test";
import { onGet } from "./index";

describe("GET /healthz", () => {
  test("answers 200 with a status body, without calling the gateway", () => {
    let sent: { status: number; body: unknown } | undefined;
    const event = {
      json: (status: number, body: unknown) => {
        sent = { status, body };
      },
    };

    // The handler only needs json(); the rest of RequestEvent is unused here.
    onGet(event as unknown as Parameters<typeof onGet>[0]);

    expect(sent).toEqual({ status: 200, body: { status: "ok" } });
  });
});
