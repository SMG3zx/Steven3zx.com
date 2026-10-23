import { component$ } from "@builder.io/qwik";
import {
  Form,
  routeAction$,
  routeLoader$,
  type DocumentHead,
} from "@builder.io/qwik-city";
import type { RequestEventAction } from "@builder.io/qwik-city/middleware/request-handler";
import { commitLine, type Commit } from "~/lib/projects";
import { gatewayFromEnv } from "~/lib/server";
import { loadSignedInUser } from "~/lib/session";

type Repository = { full_name: string; private: boolean };
type Repositories = { connected: boolean; repositories: Repository[] };
type Branches = { branches: { name: string }[] };

export const useNewProjectForm = routeLoader$(
  async ({ request, url, redirect }) => {
    const cookie = request.headers.get("cookie") ?? "";
    const gateway = gatewayFromEnv();
    const session = await loadSignedInUser(gateway, cookie, url.pathname);
    if ("redirectTo" in session) {
      throw redirect(302, session.redirectTo);
    }
    const repos = await gateway.get<Repositories>("/api/github/repos", {
      cookie,
    });
    const repository = url.searchParams.get("repository") ?? "";
    if (!repos.connected || !repository) {
      return {
        connected: repos.connected,
        repositories: repos.repositories,
        repository,
        branch: "",
        branches: [] as string[],
        commits: [] as Commit[],
      };
    }
    const branches = await gateway.get<Branches>(
      `/api/github/branches?repository=${encodeURIComponent(repository)}`,
      { cookie },
    );
    const names = branches.branches.map((branch) => branch.name);
    const branch = url.searchParams.get("branch") || names[0] || "";
    const commits = await gateway.get<Commit[]>(
      `/api/github/commits?repository=${encodeURIComponent(repository)}&branch=${encodeURIComponent(branch)}`,
      { cookie },
    );
    return {
      connected: true,
      repositories: repos.repositories,
      repository,
      branch,
      branches: names,
      commits,
    };
  },
);

function failed(event: RequestEventAction, status: number, message: string) {
  return event.fail(status >= 400 && status < 500 ? status : 500, { message });
}

/** Creates the project, then starts its import when a GitHub commit was chosen. */
export const useCreateProject = routeAction$(async (form, event) => {
  const cookie = event.request.headers.get("cookie") ?? "";
  const gateway = gatewayFromEnv();
  const name = String(form.name ?? "").trim();
  const description = String(form.description ?? "");
  if (!name) {
    return failed(event, 400, "Give the project a name.");
  }
  const created = await gateway.send("POST", "/api/projects", {
    cookie,
    body: { name, description },
  });
  if (!created.ok) {
    return failed(
      event,
      created.status,
      "The project could not be created. Try again.",
    );
  }
  const project = (await created.json()) as { id: string };
  if (form.intent !== "import") {
    throw event.redirect(303, `/projects/${project.id}`);
  }
  const started = await gateway.send(
    "POST",
    `/api/projects/${project.id}/imports`,
    {
      cookie,
      body: {
        repository: String(form.repository ?? ""),
        branch: String(form.branch ?? ""),
        commit: String(form.commit ?? ""),
      },
    },
  );
  if (!started.ok) {
    return failed(
      event,
      started.status,
      "The import could not be started. Try again.",
    );
  }
  const job = (await started.json()) as { job_id: string };
  throw event.redirect(
    303,
    `/projects/${project.id}/import?job=${encodeURIComponent(job.job_id)}`,
  );
});

export default component$(() => {
  const data = useNewProjectForm().value;
  const create = useCreateProject();
  const message = (create.value as { message?: string } | undefined)?.message;

  return (
    <main class="page page-narrow">
      <h1>New project</h1>
      {message ? (
        <p class="alert" role="alert">
          {message}
        </p>
      ) : null}

      <Form action={create} class="card stack">
        <div class="field">
          <label for="name">Project name</label>
          <input
            id="name"
            name="name"
            required
            maxLength={120}
            placeholder="hello-wasm"
          />
        </div>
        <div class="field">
          <label for="description">Description</label>
          <input id="description" name="description" maxLength={2000} />
        </div>
        <fieldset
          class="stack"
          style="border: 1px solid var(--color-border); border-radius: var(--radius-card); padding: 1rem"
        >
          <legend class="label">Start from</legend>
          <label class="check">
            <input type="radio" name="intent" value="blank" checked /> Empty
            project
          </label>
          <label class="check">
            <input type="radio" name="intent" value="import" /> Import the
            GitHub commit chosen below
          </label>
        </fieldset>
        {data.connected && data.repository && data.commits.length > 0 ? (
          <div class="field">
            <label for="commit">Commit</label>
            <select id="commit" name="commit">
              {data.commits.map((commit) => (
                <option key={commit.sha} value={commit.sha}>
                  {commitLine(commit)}
                </option>
              ))}
            </select>
            <input type="hidden" name="repository" value={data.repository} />
            <input type="hidden" name="branch" value={data.branch} />
          </div>
        ) : null}
        <button type="submit" class="button button-primary">
          Create project
        </button>
      </Form>

      <section class="card stack" aria-labelledby="github">
        <h2 id="github">GitHub repository</h2>
        {!data.connected ? (
          <a class="button" href="/auth/github/connect">
            Connect GitHub to import repositories
          </a>
        ) : (
          <>
            <form method="get" class="stack">
              <div class="field">
                <label for="repository">Repository</label>
                <select id="repository" name="repository">
                  <option value="">Choose a repository</option>
                  {data.repositories.map((repo) => (
                    <option
                      key={repo.full_name}
                      value={repo.full_name}
                      selected={repo.full_name === data.repository}
                    >
                      {`${repo.full_name}${repo.private ? " (private)" : ""}`}
                    </option>
                  ))}
                </select>
              </div>
              <button type="submit" class="button">
                Show branches
              </button>
            </form>
            {data.repository && data.branches.length > 0 ? (
              <form method="get" class="stack">
                <input
                  type="hidden"
                  name="repository"
                  value={data.repository}
                />
                <div class="field">
                  <label for="branch">Branch</label>
                  <select id="branch" name="branch">
                    {data.branches.map((name) => (
                      <option
                        key={name}
                        value={name}
                        selected={name === data.branch}
                      >
                        {name}
                      </option>
                    ))}
                  </select>
                </div>
                <button type="submit" class="button">
                  Show commits
                </button>
              </form>
            ) : null}
            <p class="muted">
              Import reads the repository and does not run its code.
            </p>
          </>
        )}
      </section>
    </main>
  );
});

export const head: DocumentHead = {
  title: "New project · Janus",
};
