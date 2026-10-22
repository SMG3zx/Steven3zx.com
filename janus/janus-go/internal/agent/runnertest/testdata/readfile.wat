;; ReadFile reads a path from stdin, opens it under the first preopened
;; directory (fd 3) and writes the file to stdout. When the file cannot be
;; opened it exits with the WASI error number as its status.
(module
  (import "wasi_snapshot_preview1" "fd_read" (func $fd_read (param i32 i32 i32 i32) (result i32)))
  (import "wasi_snapshot_preview1" "fd_write" (func $fd_write (param i32 i32 i32 i32) (result i32)))
  (import "wasi_snapshot_preview1" "path_open"
    (func $path_open (param i32 i32 i32 i32 i32 i64 i64 i32 i32) (result i32)))
  (import "wasi_snapshot_preview1" "proc_exit" (func $proc_exit (param i32)))
  (memory (export "memory") 1)
  ;; 0: stdin iovec, 16: path length, 20: opened fd, 32: file iovec, 40: bytes
  ;; read, 48: write iovec, 56: bytes written, 256: path, 1024: file contents
  (func (export "_start")
    (local $err i32)
    (i32.store (i32.const 0) (i32.const 256))
    (i32.store (i32.const 4) (i32.const 512))
    (drop (call $fd_read (i32.const 0) (i32.const 0) (i32.const 1) (i32.const 16)))
    (local.set $err (call $path_open (i32.const 3) (i32.const 0) (i32.const 256) (i32.load (i32.const 16))
      (i32.const 0) (i64.const 2) (i64.const 0) (i32.const 0) (i32.const 20)))
    (if (local.get $err) (then (call $proc_exit (local.get $err))))
    (i32.store (i32.const 32) (i32.const 1024))
    (i32.store (i32.const 36) (i32.const 4096))
    (drop (call $fd_read (i32.load (i32.const 20)) (i32.const 32) (i32.const 1) (i32.const 40)))
    (i32.store (i32.const 48) (i32.const 1024))
    (i32.store (i32.const 52) (i32.load (i32.const 40)))
    (drop (call $fd_write (i32.const 1) (i32.const 48) (i32.const 1) (i32.const 56)))))
