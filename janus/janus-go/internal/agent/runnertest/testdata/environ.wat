;; Environ writes its environment to stdout, each variable ending in a NUL byte.
(module
  (import "wasi_snapshot_preview1" "environ_sizes_get" (func $sizes (param i32 i32) (result i32)))
  (import "wasi_snapshot_preview1" "environ_get" (func $get (param i32 i32) (result i32)))
  (import "wasi_snapshot_preview1" "fd_write" (func $fd_write (param i32 i32 i32 i32) (result i32)))
  (memory (export "memory") 1)
  ;; 0: count, 4: buffer size, 16: write iovec, 24: bytes written,
  ;; 1024: pointers, 4096: variables
  (func (export "_start")
    (drop (call $sizes (i32.const 0) (i32.const 4)))
    (drop (call $get (i32.const 1024) (i32.const 4096)))
    (i32.store (i32.const 16) (i32.const 4096))
    (i32.store (i32.const 20) (i32.load (i32.const 4)))
    (drop (call $fd_write (i32.const 1) (i32.const 16) (i32.const 1) (i32.const 24)))))
