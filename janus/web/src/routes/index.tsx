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
    <main>
      <h1>Turn a repository into a running WebAssembly service</h1>
      <p>
        Import a GitHub project, build it to WASM in isolated containers, and
        deploy it to a machine you control.
      </p>
      <a href="/auth/google">Continue with Google</a>
      <a href="/auth/github">Continue with GitHub</a>
    </main>
  );
});

export const head: DocumentHead = {
  title: "Janus",
};
