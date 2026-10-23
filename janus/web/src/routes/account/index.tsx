import { component$ } from "@builder.io/qwik";
import {
  Form,
  routeAction$,
  routeLoader$,
  type DocumentHead,
} from "@builder.io/qwik-city";
import { gatewayFromEnv } from "~/lib/server";
import { loadSignedInUser } from "~/lib/session";

export type Me = { id: string; email: string; display_name: string };

export const useAccount = routeLoader$(async ({ request, url, redirect }) => {
  const cookie = request.headers.get("cookie") ?? "";
  const session = await loadSignedInUser(
    gatewayFromEnv(),
    cookie,
    url.pathname,
  );
  if ("redirectTo" in session) {
    throw redirect(302, session.redirectTo);
  }
  return session.user as unknown as Me;
});

/**
 * Disconnects GitHub, or deletes the account. Deletion is refused while imports or
 * builds are unfinished, and then the page offers to cancel them and try again.
 */
export const useAccountChange = routeAction$(async (form, event) => {
  const cookie = event.request.headers.get("cookie") ?? "";
  const gateway = gatewayFromEnv();
  const intent = String(form.intent ?? "");

  if (intent === "disconnect-github") {
    const response = await gateway.send("POST", "/auth/github/disconnect", {
      cookie,
      redirect: "manual",
    });
    if (!response.ok && response.status !== 303) {
      return event.fail(500, {
        message: "GitHub could not be disconnected. Try again.",
      });
    }
    throw event.redirect(303, "/account");
  }

  const cancel =
    intent === "delete-cancel-jobs" ? "?cancel_active_jobs=true" : "";
  const response = await gateway.send("DELETE", `/api/account${cancel}`, {
    cookie,
  });
  if (response.status === 204) {
    const setCookie = response.headers.get("set-cookie");
    if (setCookie) {
      event.headers.set("set-cookie", setCookie);
    }
    throw event.redirect(303, "/");
  }
  if (response.status === 409) {
    const body = (await response.json().catch(() => ({}))) as {
      error?: string;
    };
    return event.fail(409, {
      message: body.error ?? "Unfinished imports or builds stop the deletion.",
      canCancel: true,
    });
  }
  return event.fail(500, {
    message: "The account could not be deleted. Try again.",
  });
});

export default component$(() => {
  const account = useAccount();
  const change = useAccountChange();
  const failure = change.value as
    | { message?: string; canCancel?: boolean }
    | undefined;

  return (
    <main class="page page-narrow">
      <h1>Account</h1>
      <section class="card" aria-labelledby="profile">
        <h2 id="profile">Profile</h2>
        <p>{account.value.email}</p>
      </section>
      <section class="card" aria-labelledby="connections">
        <h2 id="connections">Connections</h2>
        <Form action={change}>
          <input type="hidden" name="intent" value="disconnect-github" />
          <button type="submit" class="button">
            Disconnect GitHub
          </button>
        </Form>
      </section>
      <section class="card card-danger" aria-labelledby="danger">
        <h2 id="danger" style="color: var(--color-danger)">
          Delete account
        </h2>
        <p class="muted">
          This removes your projects, builds, deployments and agents. It cannot
          be undone.
        </p>
        {failure?.message ? (
          <p class="alert" role="alert">
            {failure.message}
          </p>
        ) : null}
        <Form action={change} class="stack">
          <label class="check">
            <input type="checkbox" name="confirm" required />I understand that
            deleting my account is permanent.
          </label>
          <input type="hidden" name="intent" value="delete" />
          <button type="submit" class="button button-danger">
            Delete my account
          </button>
        </Form>
        {failure?.canCancel ? (
          <Form action={change} class="stack">
            <p class="muted">
              Or cancel the unfinished imports and builds, then delete the
              account.
            </p>
            <input type="hidden" name="intent" value="delete-cancel-jobs" />
            <button type="submit" class="button button-danger">
              Cancel unfinished work and delete
            </button>
          </Form>
        ) : null}
      </section>
    </main>
  );
});

export const head: DocumentHead = {
  title: "Account · Janus",
};
