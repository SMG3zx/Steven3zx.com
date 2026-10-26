import { component$, useSignal, useVisibleTask$, $ } from "@builder.io/qwik";
import { Link, type DocumentHead } from "@builder.io/qwik-city";
import { Topbar } from "~/components/topbar/topbar";
import {
  allowedActions,
  api,
  errorMessage,
  type VM,
  type VMAction,
} from "~/lib/api";
import { appendConsole, stripAnsi } from "~/lib/console";

const POLL_MS = 1500;

export default component$(() => {
  // The page is prerendered without a query string, so the VM ID is read on the
  // client. Until then id is undefined and the page shows a loading state.
  const id = useSignal<string>();
  const vm = useSignal<VM>();
  const error = useSignal("");
  const output = useSignal("");
  const line = useSignal("");
  const screen = useSignal<HTMLElement>();

  const refresh = $(async () => {
    try {
      vm.value = await api.getVM(id.value ?? "");
      error.value = "";
    } catch (err) {
      error.value = errorMessage(err);
    }
  });

  useVisibleTask$(({ cleanup }) => {
    const vmID = new URLSearchParams(window.location.search).get("id") ?? "";
    id.value = vmID;
    if (!vmID) {
      return;
    }
    void refresh();
    const timer = setInterval(() => void refresh(), POLL_MS);

    // The server replays the log tail on every new connection, so the buffer
    // is cleared on open to avoid duplicating output after a reconnect.
    const stream = new EventSource(api.consoleURL(vmID));
    stream.onopen = () => {
      output.value = "";
    };
    stream.onmessage = (event: MessageEvent<string>) => {
      output.value = appendConsole(output.value, JSON.parse(event.data) as string);
      requestAnimationFrame(() => {
        if (screen.value) {
          screen.value.scrollTop = screen.value.scrollHeight;
        }
      });
    };

    cleanup(() => {
      clearInterval(timer);
      stream.close();
    });
  });

  const act = $(async (action: VMAction) => {
    error.value = "";
    try {
      vm.value = await api.action(id.value ?? "", action);
    } catch (err) {
      error.value = errorMessage(err);
    }
    await refresh();
  });

  const sendLine = $(async () => {
    const text = line.value;
    line.value = "";
    try {
      await api.sendInput(id.value ?? "", `${text}\n`);
    } catch (err) {
      error.value = errorMessage(err);
    }
  });

  if (id.value === undefined) {
    return (
      <>
        <Topbar />
        <main class="page">
          <p class="muted">Loading…</p>
        </main>
      </>
    );
  }

  if (id.value === "") {
    return (
      <>
        <Topbar />
        <main class="page">
          <p class="muted">
            No VM selected. <Link href="/">Back to the list</Link>
          </p>
        </main>
      </>
    );
  }

  const current = vm.value;

  return (
    <>
      <Topbar>
        <Link href="/">← All VMs</Link>
      </Topbar>

      <main class="page">
        {!current ? (
          <p class="muted">{error.value || "Loading…"}</p>
        ) : (
          <>
            <section class="card">
              <div class="actions" style="justify-content: space-between; align-items: center;">
                <h2 style="margin: 0;">
                  {current.spec.name} <span class={`state ${current.state}`}>{current.state}</span>
                </h2>
                <div class="actions">
                  {allowedActions(current.state).map((action) => (
                    <button key={action} onClick$={() => act(action)}>
                      {action}
                    </button>
                  ))}
                </div>
              </div>
              {current.lastError && <p class="error">{current.lastError}</p>}
              {error.value && <p class="error">{error.value}</p>}
            </section>

            <section class="card">
              <h2>Details</h2>
              <dl class="facts">
                <dt>ID</dt>
                <dd class="mono">{current.id}</dd>
                <dt>vCPUs</dt>
                <dd>{current.spec.vcpus}</dd>
                <dt>Memory</dt>
                <dd>{current.spec.memoryMiB} MiB</dd>
                <dt>Kernel</dt>
                <dd class="mono">{current.spec.kernelPath}</dd>
                <dt>Root filesystem</dt>
                <dd class="mono">{current.spec.rootfsPath}</dd>
                <dt>Boot args</dt>
                <dd class="mono">{current.spec.bootArgs}</dd>
                {current.pid && (
                  <>
                    <dt>PID</dt>
                    <dd>{current.pid}</dd>
                  </>
                )}
                <dt>Created</dt>
                <dd>{new Date(current.createdAt).toLocaleString()}</dd>
                {current.startedAt && (
                  <>
                    <dt>Started</dt>
                    <dd>{new Date(current.startedAt).toLocaleString()}</dd>
                  </>
                )}
              </dl>
            </section>

            <section class="card">
              <h2>Serial console</h2>
              <pre class="console" ref={screen}>
                {stripAnsi(output.value) || "No output yet. Start the VM to see its console."}
              </pre>
              <form preventdefault:submit onSubmit$={sendLine} class="console-input">
                <input
                  bind:value={line}
                  placeholder="Type to send a line to the guest"
                  aria-label="Console input"
                />
                <button type="submit" disabled={current.state !== "running"}>
                  Send
                </button>
              </form>
            </section>
          </>
        )}
      </main>
    </>
  );
});

export const head: DocumentHead = {
  title: "VM · Firecracker Manager",
};
