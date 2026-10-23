import { component$ } from "@builder.io/qwik";
import {
  Form,
  routeAction$,
  routeLoader$,
  type DocumentHead,
} from "@builder.io/qwik-city";
import { importLabel, type ImportJob } from "~/lib/projects";
import { gatewayFromEnv } from "~/lib/server";
import { loadSignedInUser } from "~/lib/session";

export const useImport = routeLoader$(
  async ({ request, url, params, redirect }) => {
    const cookie = request.headers.get("cookie") ?? "";
    const gateway = gatewayFromEnv();
    const session = await loadSignedInUser(gateway, cookie, url.pathname);
    if ("redirectTo" in session) {
      throw redirect(302, session.redirectTo);
    }
    const job = url.searchParams.get("job") ?? "";
    if (!job) {
      throw redirect(302, `/projects/${encodeURIComponent(params.id)}`);
    }
    return gateway.get<ImportJob>(`/api/imports/${encodeURIComponent(job)}`, {
      cookie,
    });
  },
);

/** Starts the import again after it failed. */
export const useRetryImport = routeAction$(async (form, event) => {
  const cookie = event.request.headers.get("cookie") ?? "";
  const job = encodeURIComponent(String(form.job ?? ""));
  const response = await gatewayFromEnv().send(
    "POST",
    `/api/imports/${job}/retry`,
    { cookie },
  );
  if (!response.ok) {
    return event.fail(
      response.status >= 400 && response.status < 500 ? response.status : 500,
      {
        message: "The import could not be retried. Try again.",
      },
    );
  }
  const retried = (await response.json()) as { job_id?: string };
  throw event.redirect(
    303,
    `/projects/${encodeURIComponent(event.params.id)}/import?job=${encodeURIComponent(retried.job_id ?? String(form.job ?? ""))}`,
  );
});

export default component$(() => {
  const job = useImport().value;
  const retry = useRetryImport();
  const progress = importLabel(job);
  const message = (retry.value as { message?: string } | undefined)?.message;

  return (
    <main class="page page-narrow">
      <h1>Importing {job.repository}</h1>
      <section class="card stack" aria-labelledby="status" aria-live="polite">
        <h2 id="status">Progress</h2>
        <p>
          <span
            class={`badge ${progress.failed ? "" : progress.done ? "badge-ok" : ""}`}
          >
            {progress.label}
          </span>
        </p>
        <p class="muted">
          {job.branch} at {job.commit.slice(0, 7)}
        </p>
        {message ? (
          <p class="alert" role="alert">
            {message}
          </p>
        ) : null}
        {progress.failed ? (
          <Form action={retry}>
            <input type="hidden" name="job" value={job.id} />
            <button type="submit" class="button button-primary">
              Try again
            </button>
          </Form>
        ) : null}
        {progress.done && !progress.failed ? (
          <a class="button button-primary" href={`/projects/${job.project_id}`}>
            Open project
          </a>
        ) : null}
      </section>
      <p class="muted">
        <a href={`/projects/${job.project_id}`}>Back to the project</a>
      </p>
    </main>
  );
});

export const head: DocumentHead = ({ resolveValue }) => {
  const job = resolveValue(useImport);
  const done = importLabel(job).done;
  return {
    title: "Import · Janus",
    meta: done ? [] : [{ httpEquiv: "refresh", content: "3" }],
  };
};
