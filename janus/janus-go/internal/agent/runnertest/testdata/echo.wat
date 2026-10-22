;; Echo copies stdin to stdout.
(module
  (import "wasi_snapshot_preview1" "fd_read" (func $fd_read (param i32 i32 i32 i32) (result i32)))
  (import "wasi_snapshot_preview1" "fd_write" (func $fd_write (param i32 i32 i32 i32) (result i32)))
  (memory (export "memory") 1)
  ;; 0: read iovec, 16: bytes read, 32: write iovec, 48: bytes written, 64: buffer
  (func (export "_start")
    (loop $more
      (i32.store (i32.const 0) (i32.const 64))
      (i32.store (i32.const 4) (i32.const 4096))
      (i32.store (i32.const 16) (i32.const 0))
      (drop (call $fd_read (i32.const 0) (i32.const 0) (i32.const 1) (i32.const 16)))
      (if (i32.eqz (i32.load (i32.const 16))) (then (return)))
      (i32.store (i32.const 32) (i32.const 64))
      (i32.store (i32.const 36) (i32.load (i32.const 16)))
      (drop (call $fd_write (i32.const 1) (i32.const 32) (i32.const 1) (i32.const 48)))
      (br $more))))
