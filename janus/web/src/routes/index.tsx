import { component$ } from "@builder.io/qwik";
import { routeLoader$, type DocumentHead } from "@builder.io/qwik-city";
import { gatewayFromEnv } from "~/lib/server";
import { loadSignedInUser, safeTarget } from "~/lib/session";

export const useHomeRedirect = routeLoader$(
  async ({ request, url, redirect }) => {
    const cookie = request.headers.get("cookie") ?? "";
    const target = url.searchParams.get("next") ?? "/projects";
    const session = await loadSignedInUser(gatewayFromEnv(), cookie, target);
    if ("user" in session) {
      throw redirect(302, safeTarget(target, "/projects"));
    }
    return null;
  },
);

export default component$(() => {
  useHomeRedirect();
  return (
    <main class="page split">
      <section class="main stack">
        <h1>Turn a repository into a running WebAssembly service</h1>
        <p class="lead">
          Import a GitHub project, build it to WASM in isolated containers, and
          deploy it to a machine you control.
        </p>
        <p class="muted">
          Your repositories are read, never run, during import.
        </p>
      </section>
      <section class="aside card" aria-labelledby="sign-in-title">
        <h2 id="sign-in-title">Sign in</h2>
        <a class="button button-block" href="/auth/google">
          Continue with Google
        </a>
        <a class="button button-block" href="/auth/github">
          Continue with GitHub
        </a>
        <div class="divider">or</div>
        <a href="/sign-in">Sign in with email</a>
        <a href="/sign-in?mode=register">Create an account</a>
      </section>
    </main>
  );
});

export const head: DocumentHead = {
  title: "Janus",
};
