import { component$ } from "@builder.io/qwik";
import {
  Form,
  routeAction$,
  routeLoader$,
  type DocumentHead,
} from "@builder.io/qwik-city";
import type { RequestEventAction } from "@builder.io/qwik-city/middleware/request-handler";
import { register, signIn, type AuthResult } from "~/lib/auth";
import { gatewayFromEnv } from "~/lib/server";
import { loadSignedInUser, safeTarget } from "~/lib/session";

export const useSignedIn = routeLoader$(async ({ request, url, redirect }) => {
  const cookie = request.headers.get("cookie") ?? "";
  const target = url.searchParams.get("next") ?? "/projects";
  const session = await loadSignedInUser(gatewayFromEnv(), cookie, target);
  if ("user" in session) {
    throw redirect(302, safeTarget(target, "/projects"));
  }
  return {
    mode: url.searchParams.get("mode") === "register" ? "register" : "signin",
    next: target,
  };
});

function credentialsFrom(form: Record<string, unknown>) {
  return {
    email: String(form.email ?? ""),
    password: String(form.password ?? ""),
  };
}

/** Applies a sign-in or registration result: the cookie and a redirect, or the message to show. */
function completeAuth(
  result: AuthResult,
  next: string,
  event: RequestEventAction,
) {
  if (!result.ok) {
    const status =
      result.status >= 400 && result.status < 500 ? result.status : 500;
    return event.fail(status, { message: result.message });
  }
  event.headers.set("set-cookie", result.setCookie);
  throw event.redirect(303, safeTarget(next, "/projects"));
}

export const useSignInAction = routeAction$(async (form, event) => {
  const next = String(form.next ?? "/projects");
  const result = await signIn(gatewayFromEnv(), credentialsFrom(form));
  return completeAuth(result, next, event);
});

export const useRegisterAction = routeAction$(async (form, event) => {
  const next = String(form.next ?? "/projects");
  const result = await register(gatewayFromEnv(), credentialsFrom(form));
  return completeAuth(result, next, event);
});

export default component$(() => {
  const signedIn = useSignedIn();
  const signInAction = useSignInAction();
  const registerAction = useRegisterAction();
  const registering = signedIn.value.mode === "register";
  const action = registering ? registerAction : signInAction;
  const message = (action.value as { message?: string } | undefined)?.message;
  const next = encodeURIComponent(signedIn.value.next);

  return (
    <main class="page page-narrow">
      <nav class="tabs" aria-label="Sign in or register">
        <a
          class="tab"
          href={`/sign-in?next=${next}`}
          aria-selected={!registering}
        >
          Sign in
        </a>
        <a
          class="tab"
          href={`/sign-in?mode=register&next=${next}`}
          aria-selected={registering}
        >
          Register
        </a>
      </nav>
      <section
        class="card"
        style="max-width: 420px; margin: 0 auto; width: 100%"
      >
        <h1 style="font-size: 1.5rem">
          {registering ? "Create your account" : "Sign in to Janus"}
        </h1>
        {message ? (
          <p class="alert" role="alert">
            {message}
          </p>
        ) : null}
        <Form action={action} class="stack">
          <input type="hidden" name="next" value={signedIn.value.next} />
          <div class="field">
            <label for="email">Email</label>
            <input
              id="email"
              name="email"
              type="email"
              autoComplete="email"
              required
            />
          </div>
          <div class="field">
            <label for="password">Password</label>
            <input
              id="password"
              name="password"
              type="password"
              autoComplete={registering ? "new-password" : "current-password"}
              minLength={registering ? 12 : undefined}
              required
            />
            {registering ? <p class="muted">At least 12 characters.</p> : null}
          </div>
          <button type="submit" class="button button-primary button-block">
            {registering ? "Create account" : "Sign in"}
          </button>
        </Form>
        <div class="divider">or</div>
        <a class="button button-block" href="/auth/google">
          Continue with Google
        </a>
        <a class="button button-block" href="/auth/github">
          Continue with GitHub
        </a>
      </section>
    </main>
  );
});

export const head: DocumentHead = {
  title: "Sign in · Janus",
};
