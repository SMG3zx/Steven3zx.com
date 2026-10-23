import { component$ } from "@builder.io/qwik";
import { routeLoader$, type DocumentHead } from "@builder.io/qwik-city";
import { agentState, type Agent } from "~/lib/agents";
import {
  formatBytes,
  formatDuration,
  formatStarted,
  loadAgentRuns,
  runStatus,
} from "~/lib/runs";
import { gatewayFromEnv } from "~/lib/server";
import { loadSignedInUser } from "~/lib/session";

/** The Agent and the runs it reported. Another Account's Agent is a 404, never a leak. */
export const useAgentRuns = routeLoader$(
  async ({ request, url, params, redirect, error }) => {
    const cookie = request.headers.get("cookie") ?? "";
    const gateway = gatewayFromEnv();
    const session = await loadSignedInUser(gateway, cookie, url.pathname);
    if ("redirectTo" in session) {
      throw redirect(302, session.redirectTo);
    }
    const agents = await gateway.get<Agent[]>("/api/agents", { cookie });
    const agent = agents.find((candidate) => candidate.id === params.id);
    if (!agent) {
      throw error(404, "Agent not found");
    }
    const history = await loadAgentRuns(gateway, cookie, agent.id);
    if (history.kind === "missing") {
      throw error(404, "Agent not found");
    }
    return { agent, history };
  },
);

export default component$(() => {
  const { agent, history } = useAgentRuns().value;
  const state = agentState(agent);

  return (
    <main class="page">
      <div>
        <p class="muted">
          <a href="/agents">Agents</a> / {agent.name}
        </p>
        <h1>{agent.name}</h1>
        <p>
          <span class={`badge ${state.tone === "ok" ? "badge-ok" : ""}`}>
            {state.label}
          </span>{" "}
          <span class="muted">
            {agent.deployments.length} deployment
            {agent.deployments.length === 1 ? "" : "s"}
          </span>
        </p>
      </div>

      <section class="stack" aria-labelledby="runs">
        <h2 id="runs">Runs</h2>
        {history.kind === "unavailable" ? (
          <p class="alert" role="alert">
            Run history is unavailable. The control plane is not connected to
            the tailnet, so it receives no run reports from Agents.
          </p>
        ) : history.runs.length === 0 ? (
          <p class="muted">
            This Agent has not reported any runs yet. Each request a deployment
            serves is one run.
          </p>
        ) : (
          <>
            <div class="table-wrap">
              <table>
                <caption class="visually-hidden">
                  Runs reported by {agent.name}, newest first
                </caption>
                <thead>
                  <tr>
                    <th scope="col">Started</th>
                    <th scope="col">Deployment</th>
                    <th scope="col">Status</th>
                    <th scope="col">Duration</th>
                    <th scope="col">Fuel</th>
                    <th scope="col">Peak memory</th>
                    <th scope="col">Network in / out</th>
                  </tr>
                </thead>
                <tbody>
                  {history.runs.map((run) => {
                    const status = runStatus(run);
                    return (
                      <tr key={run.run_id}>
                        <td style="white-space: nowrap">
                          {formatStarted(run.started_at)}
                        </td>
                        <td class="muted">
                          {run.deployment_id.slice(0, 8)}
                          {run.probe ? " · health check" : ""}
                        </td>
                        <td>
                          <span
                            class={`badge ${status.tone === "ok" ? "badge-ok" : status.tone === "bad" ? "badge-bad" : ""}`}
                          >
                            {status.label}
                          </span>
                        </td>
                        <td>{formatDuration(run.duration_ms)}</td>
                        <td>{run.fuel.toLocaleString("en-US")}</td>
                        <td>{formatBytes(run.peak_memory_bytes)}</td>
                        <td>
                          {formatBytes(run.network_bytes_in)} /{" "}
                          {formatBytes(run.network_bytes_out)}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
            <p class="muted">
              The newest 100 runs. Local runs are free; nothing here is billed.
            </p>
          </>
        )}
      </section>
    </main>
  );
});

export const head: DocumentHead = ({ resolveValue }) => {
  const { agent } = resolveValue(useAgentRuns);
  return { title: `${agent.name} runs · Janus` };
};
