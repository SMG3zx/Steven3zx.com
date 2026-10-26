# firecracker-manager

A small web-based manager for [Firecracker](https://firecracker-microvm.github.io/) microVMs.

- **`firecracker-manager/`** (this directory's Go module): an HTTP API that persists VM definitions, starts one `firecracker` process per VM, configures and boots each guest through Firecracker's API socket, and streams the serial console.
- **`web/`**: a Qwik UI built with Bun that talks to that API. It is built as a static site and served by the manager.

## Requirements

- Linux with KVM (`/dev/kvm` must be readable and writable by the user running the manager).
- A `firecracker` binary on `PATH`, or passed with `-firecracker`.
- Go (module targets the Go version in `go.mod`), Bun 1.4+ for the UI.
- Guest kernel images (`vmlinux`) and ext4 root filesystems on the host. The manager takes absolute paths to them.

## Run it

Build the UI, then start the manager, which serves both the UI and the API on one port:

```sh
cd web && bun install && bun run build && cd ..
go run ./cmd/firecracker-manager -web-dir web/dist
```

Open http://127.0.0.1:8080.

For UI development, run the manager without `-web-dir` and start the Vite dev server. It proxies `/api` to the manager (set `FCM_API` to point elsewhere):

```sh
go run ./cmd/firecracker-manager
cd web && bun run dev
```

### Flags

| Flag | Default | Meaning |
| --- | --- | --- |
| `-addr` | `127.0.0.1:8080` | Listen address |
| `-data-dir` | `$XDG_DATA_HOME/firecracker-manager` or `~/.local/share/firecracker-manager` | VM list and per-VM files |
| `-firecracker` | `firecracker` | Firecracker binary |
| `-web-dir` | empty | Built UI to serve (optional) |
| `-images-dir` | `<data-dir>/images` | Folder whose `kernels/` and `rootfs/` subfolders feed the form dropdowns |

## API

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/api/host` | KVM access and Firecracker version |
| GET | `/api/vms` | List VMs |
| POST | `/api/vms` | Create a VM (`name`, `kernelPath`, `rootfsPath`, `vcpus`, `memoryMiB`, optional `bootArgs`) |
| GET | `/api/vms/{id}` | Get one VM |
| DELETE | `/api/vms/{id}` | Delete a stopped VM and its files |
| POST | `/api/vms/{id}/start` | Boot the VM |
| POST | `/api/vms/{id}/stop` | Ctrl-Alt-Del, then kill after 10s |
| POST | `/api/vms/{id}/pause` | Pause vCPUs |
| POST | `/api/vms/{id}/resume` | Resume |
| POST | `/api/vms/{id}/console/input` | Send bytes to the serial console (`{"data": "..."}`) |
| GET | `/api/vms/{id}/console` | Server-sent events stream of console output |

Errors are JSON: `{"error": "..."}`, with 400 for invalid input, 404 for unknown VMs, 409 for actions the current state does not allow.

## Images and dropdowns

The create form suggests kernels and root filesystems found in `<images-dir>/kernels` and `<images-dir>/rootfs`, plus a few boot-argument presets. Every field is a text input with those suggestions attached, so you can also type any absolute path or boot-args string. The manager creates both subfolders on startup. Symlinks work.

## Per-VM files

Each VM gets `<data-dir>/vms/<id>/` containing `api.sock` (Firecracker's API socket) and `console.log` (serial output, truncated on each start). The Unix socket path must fit in 107 bytes, so keep `-data-dir` short. The manager refuses to start if it does not.

## Limitations

- No authentication. The default listen address is loopback only; do not expose it without adding auth.
- Firecracker runs directly, not under `jailer`, and VMs get no network interface yet.
- VM processes are not adopted after a manager restart. All VMs are shown as stopped, and a leftover Firecracker process would need to be stopped by hand. A normal shutdown (SIGINT or SIGTERM) stops running VMs.
- Images are referenced by host path. There is no upload or image-building step; files are added to the images folders by hand.

## Tests

```sh
go test ./...
cd web && bun run build.types
```
