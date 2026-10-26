import { staticAdapter } from "@builder.io/qwik-city/adapters/static/vite";
import { extendConfig } from "@builder.io/qwik-city/vite";
import baseConfig from "../../vite.config";

// Produces a fully static site in dist/. The manager serves that directory,
// so the UI and the API share one origin in production.
export default extendConfig(baseConfig, () => {
  return {
    build: {
      ssr: true,
      rollupOptions: {
        input: ["src/entry.preview.tsx", "@qwik-city-plan"],
      },
    },
    plugins: [staticAdapter({ origin: "http://localhost" })],
  };
});
