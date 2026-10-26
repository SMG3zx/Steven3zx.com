import { component$, useSignal, useVisibleTask$, $ } from "@builder.io/qwik";
import type { DocumentHead } from "@builder.io/qwik-city";
import { Topbar } from "~/components/topbar/topbar";
import {
  allowedActions,
  api,
  errorMessage,
  type Catalog,
  type HostInfo,
  type Spec,
  type VM,
  type VMAction,
} from "~/lib/api";

const POLL_MS = 2000;

export default component$(() => {
  const vms = useSignal<VM[]>([]);
  const host = useSignal<HostInfo>();
  const images = useSignal<Catalog>();
  const error = useSignal("");
  const creating = useSignal(false);

  const refresh = $(async () => {
    try {
      vms.value = await api.listVMs();
      error.value = "";
    } catch (err) {
      error.value = errorMessage(err);
    }
  });

  useVisibleTask$(({ cleanup }) => {
    void refresh();
    api
      .host()
      .then((h) => (host.value = h))
      .catch((err) => (error.value = errorMessage(err)));
    api
      .images()
      .then((c) => (images.value = c))
      .catch((err) => (error.value = errorMessage(err)));
    const timer = setInterval(() => void refresh(), POLL_MS);
    cleanup(() => clearInterval(timer));
  });

  const act = $(async (id: string, action: VMAction) => {
    error.value = "";
    try {
      await api.action(id, action);
    } catch (err) {
      error.value = errorMessage(err);
    }
    await refresh();
  });

  const remove = $(async (vm: VM) => {
    if (!confirm(`Delete ${vm.spec.name}? Its state directory will be removed.`)) {
      return;
    }
    error.value = "";
    try {
      await api.deleteVM(vm.id);
    } catch (err) {
      error.value = errorMessage(err);
    }
    await refresh();
  });

  const create = $(async (_: Event, form: HTMLFormElement) => {
    const data = new FormData(form);
    const spec: Spec = {
      name: String(data.get("name") ?? "").trim(),
      kernelPath: String(data.get("kernelPath") ?? "").trim(),
      rootfsPath: String(data.get("rootfsPath") ?? "").trim(),
      vcpus: Number(data.get("vcpus")),
      memoryMiB: Number(data.get("memoryMiB")),
      bootArgs: String(data.get("bootArgs") ?? "").trim(),
    };
    creating.value = true;
    error.value = "";
    try {
      await api.createVM(spec);
      form.reset();
      await refresh();
    } catch (err) {
      error.value = errorMessage(err);
    } finally {
      creating.value = false;
    }
  });

  return (
    <>
      <Topbar>
        <div class="host">
          {host.value ? (
            <>
              <span>
                KVM: {host.value.kvm ? "available" : `unavailable (${host.value.kvmError})`}
              </span>
              <span>
                Firecracker:{" "}
                {host.value.firecracker ?? `not found (${host.value.firecrackerError})`}
              </span>
            </>
          ) : (
            <span>Checking host…</span>
          )}
        </div>
      </Topbar>

      <main class="page">
        <section class="card">
          <h2>New VM</h2>
          <form preventdefault:submit onSubmit$={create}>
            <div class="grid-form">
              <label>
                Name
                <input name="name" required placeholder="web-1" pattern="[a-z0-9][a-z0-9\-]{0,62}" />
              </label>
              <label>
                vCPUs
                <input name="vcpus" type="number" min="1" max="32" value="1" required />
              </label>
              <label>
                Memory (MiB)
                <input name="memoryMiB" type="number" min="128" max="32768" value="256" required />
              </label>
              <label>
                Kernel image
                <input
                  name="kernelPath"
                  {...{ list: "kernel-options" }}
                  required
                  autocomplete="off"
                  placeholder="/absolute/path/to/vmlinux"
                />
                <datalist id="kernel-options">
                  {images.value?.kernels.map((img) => (
                    <option key={img.path} value={img.path}>
                      {img.name}
                    </option>
                  ))}
                </datalist>
                <span class="hint">
                  {images.value?.kernels.length
                    ? `Pick one or type a path. Found in ${images.value.dir}/kernels`
                    : `Type a path, or add kernels to ${images.value?.dir ?? "the images"}/kernels`}
                </span>
              </label>
              <label>
                Root filesystem
                <input
                  name="rootfsPath"
                  {...{ list: "rootfs-options" }}
                  required
                  autocomplete="off"
                  placeholder="/absolute/path/to/rootfs.ext4"
                />
                <datalist id="rootfs-options">
                  {images.value?.rootfs.map((img) => (
                    <option key={img.path} value={img.path}>
                      {img.name}
                    </option>
                  ))}
                </datalist>
                <span class="hint">
                  {images.value?.rootfs.length
                    ? `Pick one or type a path. Found in ${images.value.dir}/rootfs`
                    : `Type a path, or add images to ${images.value?.dir ?? "the images"}/rootfs`}
                </span>
              </label>
              <label>
                Kernel boot args
                <input
                  name="bootArgs"
                  {...{ list: "bootargs-options" }}
                  autocomplete="off"
                  placeholder="Leave empty for the default"
                />
                <datalist id="bootargs-options">
                  {images.value?.bootArgs.map((args) => (
                    <option key={args} value={args} />
                  ))}
                </datalist>
                <span class="hint">Pick a preset or type your own. Empty uses the default.</span>
              </label>
            </div>
            <div class="form-actions">
              <button class="primary" type="submit" disabled={creating.value}>
                {creating.value ? "Creating…" : "Create VM"}
              </button>
            </div>
          </form>
        </section>

        <section class="card">
          <h2>Virtual machines</h2>
          {vms.value.length === 0 ? (
            <p class="muted">No VMs yet. Create one above.</p>
          ) : (
            <div class="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>State</th>
                    <th class="num">vCPUs</th>
                    <th class="num">Memory</th>
                    <th>Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {vms.value.map((vm) => (
                    <tr key={vm.id}>
                      <td>
                        {/* A plain anchor: the client router drops the ?id= query on navigation. */}
                        <a href={`/vm/?id=${vm.id}`}>{vm.spec.name}</a>
                        {vm.lastError && <div class="error">{vm.lastError}</div>}
                      </td>
                      <td>
                        <span class={`state ${vm.state}`}>{vm.state}</span>
                      </td>
                      <td class="num">{vm.spec.vcpus}</td>
                      <td class="num">{vm.spec.memoryMiB} MiB</td>
                      <td>
                        <div class="actions">
                          {allowedActions(vm.state).map((action) => (
                            <button key={action} onClick$={() => act(vm.id, action)}>
                              {action}
                            </button>
                          ))}
                          <button
                            class="danger"
                            disabled={vm.state !== "stopped" && vm.state !== "error"}
                            onClick$={() => remove(vm)}
                          >
                            delete
                          </button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          {error.value && <p class="error">{error.value}</p>}
        </section>
      </main>
    </>
  );
});

export const head: DocumentHead = {
  title: "Firecracker Manager",
};
