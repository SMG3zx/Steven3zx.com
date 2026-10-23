import { component$ } from "@builder.io/qwik";
import { routeLoader$, type DocumentHead } from "@builder.io/qwik-city";
import { gatewayFromEnv } from "~/lib/server";
import { loadSignedInUser } from "~/lib/session";
import { sourceLabel, type ProjectSummary } from "~/lib/projects";

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
    <main>
      <h1>Projects</h1>
      {projects.value.length === 0 ? (
        <p>No projects yet.</p>
      ) : (
        <ul>
          {projects.value.map((project) => (
            <li key={project.id}>
              {project.name} <span>{sourceLabel(project)}</span>
            </li>
          ))}
        </ul>
      )}
    </main>
  );
});

export const head: DocumentHead = {
  title: "Projects · Janus",
};
