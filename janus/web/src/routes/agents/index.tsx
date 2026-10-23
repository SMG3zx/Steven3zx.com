import { component$ } from "@builder.io/qwik";
import {
  Form,
  routeAction$,
  routeLoader$,
  type DocumentHead,
} from "@builder.io/qwik-city";
import { agentState, type Agent } from "~/lib/agents";
import { gatewayFromEnv } from "~/lib/server";
import { loadSignedInUser } from "~/lib/session";

export const useAgents = routeLoader$(async ({ request, url, redirect }) => {
  const cookie = request.headers.get("cookie") ?? "";
  const gateway = gatewayFromEnv();
  const session = await loadSignedInUser(gateway, cookie, url.pathname);
  if ("redirectTo" in session) {
    throw redirect(302, session.redirectTo);
  }
  return gateway.get<Agent[]>("/api/agents", { cookie });
});

/** Pause, resume or remove one Agent. The gateway's refusal message is shown as is. */
export const useAgentChange = routeAction$(async (form, event) => {
  const cookie = event.request.headers.get("cookie") ?? "";
  const id = encodeURIComponent(String(form.id ?? ""));
  const intent = String(form.intent ?? "");
  const request =
    intent === "remove"
      ? { method: "DELETE", path: `/api/agents/${id}` }
      : {
          method: "POST",
          path: `/api/agents/${id}/${intent === "resume" ? "resume" : "pause"}`,
        };
  const response = await gatewayFromEnv().send(request.method, request.path, {
    cookie,
  });
  if (!response.ok) {
    return event.fail(
      response.status >= 400 && response.status < 500 ? response.status : 500,
      {
        message: "The Agent could not be changed. Try again.",
      },
    );
  }
  return { done: true };
});

/** Issues a single-use enrollment code for the command line. */
export const useEnrollmentCode = routeAction$(async (_form, event) => {
  const cookie = event.request.headers.get("cookie") ?? "";
  const response = await gatewayFromEnv().send(
    "POST",
    "/api/agents/enrollment-codes",
    { cookie },
  );
  if (!response.ok) {
    return event.fail(500, {
      message: "No enrollment code could be created. Try again.",
    });
  }
  const body = (await response.json()) as { code: string; expires_at: string };
  return { code: body.code };
});

export default component$(() => {
  const agents = useAgents();
  const change = useAgentChange();
  const enrollment = useEnrollmentCode();
  const message = (change.value as { message?: string } | undefined)?.message;
  const code = (enrollment.value as { code?: string } | undefined)?.code;
  const failure = (enrollment.value as { message?: string } | undefined)
    ?.message;

  return (
    <main class="page">
      <div class="row row-between">
        <h1>Agents</h1>
        <Form action={enrollment}>
          <button type="submit" class="button button-primary">
            Create enrollment code
          </button>
        </Form>
      </div>
      {code ? (
        <section class="card" aria-live="polite">
          <h2>Enrollment code</h2>
          <p>
            Run <code>janus-agent enroll {code}</code> on the machine that
            should host your deployments.
          </p>
          <p class="muted">Valid for 15 minutes, once.</p>
        </section>
      ) : null}
      {failure ? (
        <p class="alert" role="alert">
          {failure}
        </p>
      ) : null}
      {message ? (
        <p class="alert" role="alert">
          {message}
        </p>
      ) : null}
      <div class="table-wrap">
        <table>
          <caption class="visually-hidden">Your agents</caption>
          <thead>
            <tr>
              <th scope="col">Name</th>
              <th scope="col">Status</th>
              <th scope="col">Deployments</th>
              <th scope="col">
                <span class="visually-hidden">Actions</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {agents.value.length === 0 ? (
              <tr>
                <td colSpan={4} class="muted">
                  No Agents yet. Create an enrollment code and run it on the
                  machine that should host your deployments.
                </td>
              </tr>
            ) : (
              agents.value.map((agent) => {
                const state = agentState(agent);
                return (
                  <tr key={agent.id}>
                    <td style="font-weight: 600">{agent.name}</td>
                    <td>
                      <span
                        class={`badge ${state.tone === "ok" ? "badge-ok" : ""}`}
                      >
                        {state.label}
                      </span>
                    </td>
                    <td class="muted">{agent.deployments.length}</td>
                    <td style="text-align: right; white-space: nowrap">
                      <a
                        class="button"
                        href={`/agents/${encodeURIComponent(agent.id)}`}
                        aria-label={`Runs of ${agent.name}`}
                      >
                        Runs
                      </a>{" "}
                      <Form action={change} style="display: inline">
                        <input type="hidden" name="id" value={agent.id} />
                        <input
                          type="hidden"
                          name="intent"
                          value={state.canPause ? "pause" : "resume"}
                        />
                        <button type="submit" class="button">
                          {state.canPause ? "Pause" : "Resume"}
                        </button>
                      </Form>{" "}
                      <Form action={change} style="display: inline">
                        <input type="hidden" name="id" value={agent.id} />
                        <input type="hidden" name="intent" value="remove" />
                        <button
                          type="submit"
                          class="button-link"
                          style="color: var(--color-danger)"
                        >
                          Remove {agent.name}
                        </button>
                      </Form>
                    </td>
                  </tr>
                );
              })
            )}
          </tbody>
        </table>
      </div>
    </main>
  );
});

export const head: DocumentHead = {
  title: "Agents · Janus",
};
