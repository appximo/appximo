;; identity.wat — the source of truth for testdata/wasm/identity.wasm
;;
;; No WASM toolchain (tinygo/wat2wasm) is available in this environment, so the
;; binary is hand-assembled by gen.go (run: `go run testdata/wasm/gen.go`).
;; This .wat documents exactly what those bytes encode.
;;
;; ABI expected by pkg/extensions.WasmRunner.Execute:
;;   - export "memory"
;;   - export "alloc"  (size i32) -> (ptr i32)          ; bump allocator
;;   - export "free"   (ptr i32, size i32) -> ()         ; no-op here
;;   - the transform fn (ptr i32, len i32) -> (ptr i32, len i32)
;;     returns a (ptr,len) pair pointing at the output in linear memory.
(module
  (memory (export "memory") 1)                 ;; 1 page = 64 KiB
  (global $heap (mut i32) (i32.const 1024))    ;; bump pointer (skip first 1 KiB)

  ;; alloc: return current heap, then bump it by size.
  (func (export "alloc") (param $size i32) (result i32)
    (local $p i32)
    (local.set $p (global.get $heap))
    (global.set $heap (i32.add (global.get $heap) (local.get $size)))
    (local.get $p))

  ;; free: no-op (the runner closes the whole instance per call).
  (func (export "free") (param i32) (param i32))

  ;; transform: identity — output IS the input region (same ptr/len).
  (func (export "transform") (param $ptr i32) (param $len i32) (result i32 i32)
    (local.get $ptr) (local.get $len))

  ;; loop_forever: same signature as transform but never returns — used to test
  ;; that Execute cancels a runaway module via the context deadline.
  (func (export "loop_forever") (param i32) (param i32) (result i32 i32)
    (loop $l (br $l))
    (unreachable))
)
