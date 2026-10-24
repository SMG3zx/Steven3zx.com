import { component$ } from "@builder.io/qwik";
import {
  Form,
  routeAction$,
  routeLoader$,
  type DocumentHead,
} from "@builder.io/qwik-city";
import type { RequestEventAction } from "@builder.io/qwik-city/middleware/request-handler";
import { agentState, type Agent } from "~/lib/agents";
import {
  buildLabel,
  deploymentLabel,
  entrypointCandidates,
  goDiagnoses,
  targetsFor,
  type Build,
  type Deployment,
} from "~/lib/detail";
import { loadRepositoryChoices } from "~/lib/github";
import { commitLine } from "~/lib/projects";
import { gatewayFromEnv } from "~/lib/server";
import { loadSignedInUser } from "~/lib/session";

type ProjectDetail = {
  id: string;
  name: string;
  description: string;
  repository?: string;
  branch?: string;
  commit?: string;
  language?: string;
  snapshot_id?: string;
  entry_point?: string;
  metadata_json?: string;
};

type Snapshot = {
  id: string;
  branch?: string;
  commit?: string;
  entry_point?: string;
  created_at?: string;
};

type Run = {
  id: string;
  status: string;
  stdout?: string;
  stderr?: string;
  error?: string;
};

/**
 * The project and the things its page can show. The build, deployment and run
 * a visitor is looking at come from the query string, which the actions set.
 */
export const useProjectDetail = routeLoader$(
  async ({ request, url, params, redirect, error }) => {
    const cookie = request.headers.get("cookie") ?? "";
    const gateway = gatewayFromEnv();
    const session = await loadSignedInUser(
      gateway,
      cookie,
      url.pathname + url.search,
    );
    if ("redirectTo" in session) {
      throw redirect(302, session.redirectTo);
    }
    const id = encodeURIComponent(params.id);
    const project = await gateway
      .get<ProjectDetail>(`/api/projects/${id}`, { cookie })
      .catch(() => null);
    if (!project) {
      throw error(404, "Project not found");
    }
    const snapshots = await gateway.get<Snapshot[]>(
      `/api/projects/${id}/snapshots`,
      { cookie },
    );
    // Agents need the fleet; without it the page still shows the project.
    const agents = await gateway
      .get<Agent[]>("/api/agents", { cookie })
      .catch(() => [] as Agent[]);

    const buildId = url.searchParams.get("build");
    const build = buildId
      ? await gateway
          .get<Build>(`/api/wasm-builds/${encodeURIComponent(buildId)}`, {
            cookie,
          })
          .catch(() => null)
      : null;
    const deploymentId = url.searchParams.get("deployment");
    const deployment = deploymentId
      ? await gateway
          .get<Deployment>(
            `/api/deployments/${encodeURIComponent(deploymentId)}`,
            { cookie },
          )
          .catch(() => null)
      : null;
    const runId = url.searchParams.get("run");
    const run = runId
      ? await gateway
          .get<Run>(`/api/wasm-runs/${encodeURIComponent(runId)}`, { cookie })
          .catch(() => null)
      : null;

    // Projects without a revision import from GitHub, so they need the repository choices.
    const choices = project.snapshot_id
      ? null
      : await loadRepositoryChoices(
          gateway,
          cookie,
          url.searchParams.get("repository") ?? "",
          url.searchParams.get("branch") ?? "",
        ).catch(() => null);

    return { project, snapshots, agents, build, deployment, run, choices };
  },
);

function failed(event: RequestEventAction, status: number, message: string) {
  return event.fail(status >= 400 && status < 500 ? status : 500, { message });
}

/** Every change the project page makes. Each one ends with a redirect back to the page. */
export const useProjectAction = routeAction$(async (form, event) => {
  const cookie = event.request.headers.get("cookie") ?? "";
  const gateway = gatewayFromEnv();
  const id = encodeURIComponent(event.params.id);
  const intent = String(form.intent ?? "");
  const pageUrl = `/projects/${id}`;

  if (intent === "import") {
    const response = await gateway.send("POST", `/api/projects/${id}/imports`, {
      cookie,
      body: {
        repository: String(form.repository ?? ""),
        branch: String(form.branch ?? ""),
        commit: String(form.commit ?? ""),
      },
    });
    if (!response.ok) {
      return failed(
        event,
        response.status,
        "The import could not be started. Try again.",
      );
    }
    const started = (await response.json()) as { job_id: string };
    throw event.redirect(
      303,
      `/projects/${id}/import?job=${encodeURIComponent(started.job_id)}`,
    );
  }

  if (intent === "entrypoint") {
    const response = await gateway.send(
      "POST",
      `/api/projects/${id}/entrypoint`,
      {
        cookie,
        body: {
          snapshot_id: String(form.snapshot_id ?? ""),
          entry_point: String(form.entry_point ?? ""),
        },
      },
    );
    if (!response.ok)
      return failed(
        event,
        response.status,
        "The entrypoint could not be saved.",
      );
    throw event.redirect(303, pageUrl);
  }

  if (intent === "activate") {
    const snapshot = encodeURIComponent(String(form.snapshot_id ?? ""));
    const response = await gateway.send(
      "POST",
      `/api/projects/${id}/snapshots/${snapshot}/activate`,
      { cookie },
    );
    if (!response.ok)
      return failed(
        event,
        response.status,
        "That revision could not be activated.",
      );
    throw event.redirect(303, pageUrl);
  }

  if (intent === "build") {
    const response = await gateway.send(
      "POST",
      `/api/projects/${id}/wasm-builds`,
      {
        cookie,
        body: { target: String(form.target ?? "") },
      },
    );
    if (!response.ok)
      return failed(event, response.status, "The build could not be started.");
    const started = (await response.json()) as { job_id: string };
    throw event.redirect(
      303,
      `${pageUrl}?build=${encodeURIComponent(started.job_id)}`,
    );
  }

  if (intent === "retry") {
    const response = await gateway.send(
      "POST",
      `/api/wasm-builds/${encodeURIComponent(String(form.build ?? ""))}/retry`,
      { cookie },
    );
    if (!response.ok)
      return failed(event, response.status, "The build could not be retried.");
    const started = (await response.json()) as { job_id: string };
    throw event.redirect(
      303,
      `${pageUrl}?build=${encodeURIComponent(started.job_id)}`,
    );
  }

  if (intent === "run") {
    const build = encodeURIComponent(String(form.build ?? ""));
    const response = await gateway.send(
      "POST",
      `/api/wasm-builds/${build}/runs`,
      { cookie, body: { args: [] } },
    );
    if (!response.ok)
      return failed(event, response.status, "The run could not be started.");
    const started = (await response.json()) as { run_id: string };
    throw event.redirect(
      303,
      `${pageUrl}?build=${build}&run=${encodeURIComponent(started.run_id)}`,
    );
  }

  if (intent === "deploy") {
    const response = await gateway.send(
      "POST",
      `/api/projects/${id}/deployments`,
      {
        cookie,
        body: {
          build_id: String(form.build_id ?? ""),
          agent_id: String(form.agent_id ?? ""),
          deploy_anyway: form.deploy_anyway === "on",
        },
      },
    );
    if (!response.ok)
      return failed(
        event,
        response.status,
        "The deployment could not be started.",
      );
    const started = (await response.json()) as { id: string };
    throw event.redirect(
      303,
      `${pageUrl}?deployment=${encodeURIComponent(started.id)}`,
    );
  }

  if (intent === "rollback" || intent === "stop") {
    const deployment = encodeURIComponent(String(form.deployment ?? ""));
    const response = await gateway.send(
      "POST",
      `/api/deployments/${deployment}/${intent}`,
      { cookie },
    );
    if (!response.ok)
      return failed(
        event,
        response.status,
        "The deployment could not be changed.",
      );
    const changed = (await response.json().catch(() => ({}))) as {
      id?: string;
    };
    const shown =
      intent === "rollback" && changed.id
        ? changed.id
        : String(form.deployment ?? "");
    throw event.redirect(
      303,
      `${pageUrl}?deployment=${encodeURIComponent(shown)}`,
    );
  }

  return failed(event, 400, "That change is not supported.");
});

export default component$(() => {
  const data = useProjectDetail().value;
  const action = useProjectAction();
  const message = (action.value as { message?: string } | undefined)?.message;
  const { project, snapshots, agents, build, deployment, run, choices } = data;
  const buildState = build ? buildLabel(build) : null;
  const deploymentState = deployment ? deploymentLabel(deployment) : null;
  const candidates = entrypointCandidates(project.metadata_json ?? "");
  const goBlocked = goDiagnoses(project.metadata_json ?? "");
  const targets = targetsFor(project.language ?? "");
  const readyAgents = agents.filter((agent) => agentState(agent).canPause);

  return (
    <main class="page">
      <div>
        <p class="muted">
          <a href="/projects">Projects</a> / {project.name}
        </p>
        <h1>{project.name}</h1>
        {project.repository ? (
          <p class="muted">
            {project.repository} · {project.branch} · entrypoint{" "}
            {project.entry_point || "not chosen"}
          </p>
        ) : (
          <p class="muted">Empty project</p>
        )}
      </div>

      {message ? (
        <p class="alert" role="alert">
          {message}
        </p>
      ) : null}

      {choices ? (
        <section class="card stack" aria-labelledby="import">
          <h2 id="import">Import from GitHub</h2>
          {!choices.connected ? (
            <a class="button" href="/auth/github/connect">
              Connect GitHub to import repositories
            </a>
          ) : (
            <>
              <form method="get" class="row">
                <label class="visually-hidden" for="repository-pick">
                  Repository
                </label>
                <select
                  id="repository-pick"
                  name="repository"
                  style="width: auto"
                >
                  <option value="">Choose a repository</option>
                  {choices.repositories.map((repo) => (
                    <option
                      key={repo.full_name}
                      value={repo.full_name}
                      selected={repo.full_name === choices.repository}
                    >
                      {repo.full_name}
                    </option>
                  ))}
                </select>
                <button type="submit" class="button">
                  Show branches
                </button>
              </form>
              {choices.branches.length > 0 ? (
                <form method="get" class="row">
                  <input
                    type="hidden"
                    name="repository"
                    value={choices.repository}
                  />
                  <label class="visually-hidden" for="branch-pick">
                    Branch
                  </label>
                  <select id="branch-pick" name="branch" style="width: auto">
                    {choices.branches.map((name) => (
                      <option
                        key={name}
                        value={name}
                        selected={name === choices.branch}
                      >
                        {name}
                      </option>
                    ))}
                  </select>
                  <button type="submit" class="button">
                    Show commits
                  </button>
                </form>
              ) : null}
              {choices.commits.length > 0 ? (
                <Form action={action} class="stack">
                  <input type="hidden" name="intent" value="import" />
                  <input
                    type="hidden"
                    name="repository"
                    value={choices.repository}
                  />
                  <input type="hidden" name="branch" value={choices.branch} />
                  <div class="field">
                    <label for="commit">Commit</label>
                    <select id="commit" name="commit">
                      {choices.commits.map((commit) => (
                        <option key={commit.sha} value={commit.sha}>
                          {commitLine(commit)}
                        </option>
                      ))}
                    </select>
                  </div>
                  <button type="submit" class="button button-primary">
                    Import selected commit
                  </button>
                </Form>
              ) : null}
              <p class="muted">
                Import reads the repository and does not run its code.
              </p>
            </>
          )}
        </section>
      ) : null}

      <div class="split">
        <div class="main stack">
          <section class="card stack" aria-labelledby="builds">
            <h2 id="builds">WASM builds</h2>
            {project.snapshot_id ? (
              <Form action={action} class="row">
                <input type="hidden" name="intent" value="build" />
                <label class="visually-hidden" for="target">
                  Target
                </label>
                <select id="target" name="target" style="width: auto">
                  {targets.map((target) => (
                    <option key={target} value={target}>
                      {target}
                    </option>
                  ))}
                </select>
                <button type="submit" class="button button-primary">
                  Build WASM
                </button>
              </Form>
            ) : (
              <p class="muted">Import a GitHub commit first, then build it.</p>
            )}
            {build && buildState ? (
              <div class="stack" aria-live="polite">
                <p>
                  <span
                    class={`badge ${buildState.failed ? "" : buildState.done ? "badge-ok" : ""}`}
                  >
                    {buildState.label}
                  </span>{" "}
                  <span class="muted">{build.target}</span>
                </p>
                {buildState.failed && build.log ? (
                  <details open>
                    <summary>Build log</summary>
                    <pre style="white-space: pre-wrap">{build.log}</pre>
                  </details>
                ) : null}
                {buildState.canRetry ? (
                  <Form action={action}>
                    <input type="hidden" name="intent" value="retry" />
                    <input type="hidden" name="build" value={build.id} />
                    <button type="submit" class="button">
                      Retry build
                    </button>
                  </Form>
                ) : null}
                {build.status === "complete" ? (
                  <div class="row">
                    <a
                      class="button"
                      href={`/api/wasm-builds/${build.id}/artifact`}
                    >
                      Download {build.artifact_name || "artifact"}
                    </a>
                    <Form action={action}>
                      <input type="hidden" name="intent" value="run" />
                      <input type="hidden" name="build" value={build.id} />
                      <button type="submit" class="button">
                        Run
                      </button>
                    </Form>
                  </div>
                ) : null}
                {run ? (
                  <div class="card" aria-label="Run output">
                    <p>
                      Run {run.status}
                      {run.error ? `: ${run.error}` : ""}
                    </p>
                    {run.stdout ? (
                      <pre style="white-space: pre-wrap">{run.stdout}</pre>
                    ) : null}
                    {run.stderr ? (
                      <pre style="white-space: pre-wrap">{run.stderr}</pre>
                    ) : null}
                  </div>
                ) : null}
              </div>
            ) : null}
          </section>

          <section class="card stack" aria-labelledby="deployments">
            <h2 id="deployments">Deployment</h2>
            {deployment && deploymentState ? (
              <div class="stack">
                <p>
                  <span
                    class={`badge ${deploymentState.label === "Live" ? "badge-ok" : ""}`}
                  >
                    {deploymentState.label}
                  </span>
                </p>
                <div class="row">
                  {deploymentState.canRollback ? (
                    <Form action={action}>
                      <input type="hidden" name="intent" value="rollback" />
                      <input
                        type="hidden"
                        name="deployment"
                        value={deployment.id}
                      />
                      <button type="submit" class="button">
                        Roll back
                      </button>
                    </Form>
                  ) : null}
                  {deploymentState.canStop ? (
                    <Form action={action}>
                      <input type="hidden" name="intent" value="stop" />
                      <input
                        type="hidden"
                        name="deployment"
                        value={deployment.id}
                      />
                      <button type="submit" class="button">
                        Stop
                      </button>
                    </Form>
                  ) : null}
                </div>
              </div>
            ) : (
              <p class="muted">Nothing is deployed yet.</p>
            )}
            {build && build.status === "complete" && readyAgents.length > 0 ? (
              <Form action={action} class="stack">
                <input type="hidden" name="intent" value="deploy" />
                <input type="hidden" name="build_id" value={build.id} />
                <div class="field">
                  <label for="agent_id">Agent</label>
                  <select id="agent_id" name="agent_id">
                    {readyAgents.map((agent) => (
                      <option key={agent.id} value={agent.id}>
                        {agent.name}
                      </option>
                    ))}
                  </select>
                </div>
                <label class="check">
                  <input type="checkbox" name="deploy_anyway" /> Deploy even if
                  the checks fail
                </label>
                <button type="submit" class="button button-primary">
                  Deploy
                </button>
              </Form>
            ) : null}
          </section>
        </div>

        <aside class="aside stack">
          <section class="card stack" aria-labelledby="entrypoint">
            <h2 id="entrypoint">Entrypoint</h2>
            {project.snapshot_id ? (
              <Form action={action} class="stack">
                <input type="hidden" name="intent" value="entrypoint" />
                <input
                  type="hidden"
                  name="snapshot_id"
                  value={project.snapshot_id}
                />
                <div class="field">
                  <label for="entry_point">Entrypoint file</label>
                  {candidates.length > 0 ? (
                    <select id="entry_point" name="entry_point">
                      {candidates.map((path) => (
                        <option
                          key={path}
                          value={path}
                          selected={path === project.entry_point}
                        >
                          {path}
                        </option>
                      ))}
                    </select>
                  ) : (
                    <input
                      id="entry_point"
                      name="entry_point"
                      value={project.entry_point ?? ""}
                    />
                  )}
                </div>
                <button type="submit" class="button">
                  Save entrypoint
                </button>
                {goBlocked.module.length > 0 ||
                goBlocked.packages.length > 0 ? (
                  <div class="stack" role="note">
                    <p class="muted">Not buildable as WASM:</p>
                    <ul>
                      {goBlocked.module.map((reason) => (
                        <li key={reason}>{reason}</li>
                      ))}
                      {goBlocked.packages.map((pkg) => (
                        <li key={pkg.dir}>
                          <code>{pkg.dir}</code>: {pkg.reasons.join("; ")}
                        </li>
                      ))}
                    </ul>
                  </div>
                ) : null}
              </Form>
            ) : (
              <p class="muted">No revision yet.</p>
            )}
          </section>

          <section class="card stack" aria-labelledby="snapshots">
            <h2 id="snapshots">Revisions</h2>
            {snapshots.length === 0 ? (
              <p class="muted">No revisions yet.</p>
            ) : (
              <ul style="margin: 0; padding-left: 1.1rem; display: flex; flex-direction: column; gap: 0.5rem">
                {snapshots.map((snapshot) => (
                  <li key={snapshot.id}>
                    {snapshot.branch ?? "revision"} @{" "}
                    {(snapshot.commit ?? "").slice(0, 7)}{" "}
                    {snapshot.id === project.snapshot_id ? (
                      <span class="badge badge-ok">Active</span>
                    ) : (
                      <Form action={action} style="display: inline">
                        <input type="hidden" name="intent" value="activate" />
                        <input
                          type="hidden"
                          name="snapshot_id"
                          value={snapshot.id}
                        />
                        <button type="submit" class="button-link">
                          Activate
                        </button>
                      </Form>
                    )}
                  </li>
                ))}
              </ul>
            )}
          </section>
        </aside>
      </div>
    </main>
  );
});

export const head: DocumentHead = ({ resolveValue }) => {
  const data = resolveValue(useProjectDetail);
  // A build still running is looked at again every few seconds, as the import page does.
  const pending = data.build !== null && !buildLabel(data.build).done;
  return {
    title: `${data.project.name} · Janus`,
    meta: pending ? [{ httpEquiv: "refresh", content: "3" }] : [],
  };
};
