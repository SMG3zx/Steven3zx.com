import { Mastra } from "@mastra/core/mastra";
import { factoryAgent } from "./agents/factory-agent";

export const mastra = new Mastra({
  agents: { factoryAgent },
  server: {
    port: 4111,
    host: "127.0.0.1",
  },
});
