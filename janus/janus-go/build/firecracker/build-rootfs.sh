#!/bin/sh
# Builds the read-only root filesystems the WASM Builds and Checks boot in, one per
# Language, as ext4 images. They replace the Dockerfiles that built the builder
# images. Each image is a Debian bookworm root with the toolchain of its
# Dockerfile and /sbin/janus-init as PID 1.
#
#   sudo build/firecracker/build-rootfs.sh [rust|go|js|all]
#
# Needs root for debootstrap and chroot, plus debootstrap, e2fsprogs and network
# access. Images are written to $OUT_DIR (default data/firecracker/rootfs), owned
# by the user who ran sudo. Point JANUS_WASM_RUST_ROOTFS, JANUS_WASM_GO_ROOTFS and
# JANUS_WASM_JS_ROOTFS at them.
set -eu

if [ "$(id -u)" -ne 0 ]; then
	echo "run with sudo: chroot and debootstrap need root" >&2
	exit 1
fi

here=$(cd "$(dirname "$0")" && pwd)
out_dir=${OUT_DIR:-data/firecracker/rootfs}
owner=${SUDO_USER:-root}
mirror=${DEBIAN_MIRROR:-http://deb.debian.org/debian}

# The toolchain versions match the images these replace.
go_version=1.27.3
javy_version=v9.1.0
javy_sha256=a68b122d48eb3dfc1b801d4e14c39271fde3638243d3272d206e376ac9189e39
bun_version=1.4.3

# bootstrap makes a minimal bookworm root in $1 with janus-init at /sbin/janus-init.
bootstrap() {
	root=$1
	rm -rf "$root"
	debootstrap --variant=minbase --include=ca-certificates bookworm "$root" "$mirror"
	mkdir -p "$root/root" "$root/src" "$root/job" "$root/out" "$root/workspace"
	install -m 0755 "$here/janus-init" "$root/sbin/janus-init"
	cp /etc/resolv.conf "$root/etc/resolv.conf"
}

# seal removes what the install left behind, then writes the ext4 image of $1.
seal() {
	root=$1
	image=$2
	chroot "$root" /bin/sh -c 'apt-get clean && rm -rf /var/lib/apt/lists/* /root/.cache /tmp/*'
	rm -f "$root/etc/resolv.conf"
	size=$(( $(du -sm "$root" | cut -f1) + 512 ))
	mkdir -p "$out_dir"
	rm -f "$image"
	mkfs.ext4 -q -F -d "$root" "$image" "${size}M"
	chown "$owner" "$image"
	echo "wrote $image (${size}M)"
}

build_go() {
	root=$work/go
	bootstrap "$root"
	chroot "$root" /bin/sh -c 'apt-get update -qq && apt-get install -y -qq --no-install-recommends git gcc libc6-dev >/dev/null'
	curl -fsSL "https://go.dev/dl/go${go_version}.linux-amd64.tar.gz" | tar -C "$root/usr/local" -xzf -
	seal "$root" "$out_dir/janus-go-wasm.ext4"
}

build_rust() {
	root=$work/rust
	bootstrap "$root"
	chroot "$root" /bin/sh -c 'apt-get update -qq && apt-get install -y -qq --no-install-recommends curl gcc libc6-dev >/dev/null'
	chroot "$root" /bin/sh -c 'export RUSTUP_HOME=/usr/local/rustup CARGO_HOME=/usr/local/cargo && curl -sSf https://sh.rustup.rs | sh -s -- -y --profile minimal --default-toolchain stable && /usr/local/cargo/bin/rustup target add wasm32-unknown-unknown wasm32-wasip1'
	seal "$root" "$out_dir/janus-rust-wasm.ext4"
}

build_js() {
	root=$work/js
	bootstrap "$root"
	chroot "$root" /bin/sh -c 'apt-get update -qq && apt-get install -y -qq --no-install-recommends curl unzip >/dev/null'
	curl -fsSL -o "$root/tmp/bun.zip" "https://github.com/oven-sh/bun/releases/download/bun-v${bun_version}/bun-linux-x64.zip"
	chroot "$root" /bin/sh -c 'cd /tmp && unzip -q bun.zip && install -m 0755 bun-linux-x64/bun /usr/local/bin/bun'
	curl -fsSL -o "$root/tmp/javy.gz" "https://github.com/bytecodealliance/javy/releases/download/${javy_version}/javy-x86_64-linux-${javy_version}.gz"
	echo "${javy_sha256}  $root/tmp/javy.gz" | sha256sum -c -
	chroot "$root" /bin/sh -c 'gunzip /tmp/javy.gz && install -m 0755 /tmp/javy /usr/local/bin/javy'
	seal "$root" "$out_dir/janus-js-wasm.ext4"
}

work=$(mktemp -d /var/tmp/janus-rootfs.XXXXXX)
trap 'rm -rf "$work"' EXIT

case "${1:-all}" in
rust) build_rust ;;
go) build_go ;;
js) build_js ;;
all) build_rust && build_go && build_js ;;
*)
	echo "usage: $0 [rust|go|js|all]" >&2
	exit 2
	;;
esac
