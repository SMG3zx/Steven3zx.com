import { component$ } from "@builder.io/qwik";
import { routeLoader$, type DocumentHead } from "@builder.io/qwik-city";
import { sourceLabel, type ProjectSummary } from "~/lib/projects";
import { gatewayFromEnv } from "~/lib/server";
import { loadSignedInUser } from "~/lib/session";

export const useProjects = routeLoader$(async ({ request, url, redirect }) => {
  const cookie = request.headers.get("cookie") ?? "";
  const gateway = gatewayFromEnv();
  const session = await loadSignedInUser(gateway, cookie, url.pathname);
  if ("redirectTo" in session) {
    throw redirect(302, session.redirectTo);
  }
  return gateway.get<ProjectSummary[]>("/api/projects", { cookie });
});

export default component$(() => {
  const projects = useProjects();
  return (
    <main class="page">
      <div class="row row-between">
        <h1>Projects</h1>
        <a class="button button-primary" href="/projects/new">
          New project
        </a>
      </div>
      <div class="table-wrap">
        <table>
          <caption class="visually-hidden">Your projects</caption>
          <thead>
            <tr>
              <th scope="col">Name</th>
              <th scope="col">Source</th>
            </tr>
          </thead>
          <tbody>
            {projects.value.length === 0 ? (
              <tr>
                <td colSpan={2} class="muted">
                  No projects yet.
                </td>
              </tr>
            ) : (
              projects.value.map((project) => (
                <tr key={project.id}>
                  <td>
                    <a
                      href={`/projects/${project.id}`}
                      style="font-weight: 600"
                    >
                      {project.name}
                    </a>
                  </td>
                  <td class="muted">{sourceLabel(project)}</td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
    </main>
  );
});

export const head: DocumentHead = {
  title: "Projects · Janus",
};
