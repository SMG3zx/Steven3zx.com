import { component$ } from "@builder.io/qwik";
import type { DocumentHead, RequestHandler } from "@builder.io/qwik-city";

export const onRequest: RequestHandler = ({ status }) => {
  status(404);
};

export default component$(() => {
  return (
    <main class="page page-narrow">
      <p class="muted">404</p>
      <h1>This page does not exist</h1>
      <p class="lead">
        The address may be mistyped, or the project was removed.
      </p>
      <div>
        <a class="button button-primary" href="/projects">
          Go to projects
        </a>
      </div>
    </main>
  );
});

export const head: DocumentHead = {
  title: "Not found · Janus",
};
