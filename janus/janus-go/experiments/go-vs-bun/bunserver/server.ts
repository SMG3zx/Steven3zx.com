// The Bun side of the comparison: Elysia plus nats.js.
import { Elysia, t } from "elysia";
import { connect, StringCodec } from "nats";

const port = Number(process.env.PORT ?? 8082);
const nc = await connect({ servers: process.env.NATS ?? "127.0.0.1:4333" });
const sc = StringCodec();

function fib(n: number): number {
  return n < 2 ? n : fib(n - 1) + fib(n - 2);
}

new Elysia()
  .get("/ping", () => ({ ok: true }))
  .post(
    "/validate",
    ({ body }) => ({ name: body.name, age: body.age, tags: body.tags.length }),
    {
      body: t.Object({
        name: t.String({ minLength: 1 }),
        age: t.Integer({ minimum: 0, maximum: 150 }),
        tags: t.Array(t.String(), { maxItems: 20 }),
      }),
    },
  )
  .get("/nats", async () => {
    const m = await nc.request("bench.echo", sc.encode('{"n":1}'), { timeout: 2000 });
    return new Response(m.data, { headers: { "content-type": "application/json" } });
  })
  .get("/cpu", () => ({ fib: fib(27), h: 0 }))
  .listen({ hostname: "127.0.0.1", port });
console.log(`bun listening on ${port}`);
