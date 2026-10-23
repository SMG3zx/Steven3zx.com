import { createQwikCity } from "@builder.io/qwik-city/middleware/bun";
import qwikCityPlan from "@qwik-city-plan";
import { manifest } from "@qwik-client-manifest";
import render from "./entry.ssr";
import { readConfig } from "~/lib/config";

// Fail at startup, not on the first request, when the environment is wrong.
readConfig(process.env);

const { router, notFound, staticFile } = createQwikCity({
  render,
  qwikCityPlan,
  manifest,
  getOrigin: () => process.env.JANUS_WEB_URL ?? null,
});

const port = Number(process.env.PORT ?? 3000);

Bun.serve({
  port,
  async fetch(request) {
    return (
      (await staticFile(request)) ||
      (await router(request)) ||
      notFound(request)
    );
  },
});

console.log(`janus-web listening on :${port}`);
