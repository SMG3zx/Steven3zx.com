import { defineConfig, type UserConfig } from "vite";
import { qwikVite } from "@builder.io/qwik/optimizer";
import { qwikCity } from "@builder.io/qwik-city/vite";
import tsconfigPaths from "vite-tsconfig-paths";

// The manager's HTTP API. In development, Vite forwards /api to it so the UI
// can be served from the Vite dev server with the same relative URLs as in production.
const apiTarget = process.env.FCM_API ?? "http://127.0.0.1:8080";

export default defineConfig((): UserConfig => {
  return {
    plugins: [qwikCity(), qwikVite(), tsconfigPaths({ root: "." })],
    server: {
      proxy: { "/api": apiTarget },
    },
    preview: {
      proxy: { "/api": apiTarget },
    },
  };
});
