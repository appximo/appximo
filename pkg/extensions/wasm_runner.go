package extensions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	zlog "github.com/rs/zerolog/log"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

const (
	// wasmMemoryPages caps each module's linear memory at 16 MiB (256 × 64 KiB),
	// so a guest cannot exhaust host memory.
	wasmMemoryPages = 256
	// wasmDefaultTimeout bounds a single Execute when the caller's context carries
	// no deadline of its own.
	wasmDefaultTimeout = 500 * time.Millisecond
	// wasmHostModule is the import module name under which the host functions
	// (appximo_log, appximo_now) are exported to guests.
	wasmHostModule = "appximo"
	// wasmMaxLogBytes caps a single appximo_log message. A guest controls the
	// (ptr,len) it passes, so without this it could log up to its full 16 MiB of
	// linear memory per call and flood the log/disk.
	wasmMaxLogBytes = 4096
)

// wasmTenantKey carries the tenant ID through the context into host functions.
type wasmTenantKey struct{}

// WasmRunner executes sandboxed WASM (Capa 3) modules. Each module is compiled
// once and cached per (tenant, module-hash); instances are created and destroyed
// per Execute call. Guests get NO filesystem, network, clock-walltime, or syscall
// access — only the two host functions appximo_log and appximo_now. Execution
// time is bounded by the caller's context deadline (or wasmDefaultTimeout), and
// the runtime is configured to abort a running guest when that context is done.
type WasmRunner struct {
	runtime      wazero.Runtime
	compiled     sync.Map // cacheKey(string) -> wazero.CompiledModule
	modules      sync.Map // moduleName(string) -> []byte (pre-loaded module bytes)
	compileCount int64    // number of real compilations (cache misses); read in tests
	// sem bounds concurrent Execute calls. Each instance gets its own 16 MiB linear
	// memory, so without a cap N concurrent guests = N × 16 MiB. Mirrors the JS
	// sandbox's limit (GOMAXPROCS×2).
	sem chan struct{}
}

// NewWasmRunner builds a runner with a 16 MiB per-module memory cap and registers
// the host functions. The provided ctx is used only to instantiate the host
// module; per-call deadlines are passed to Execute.
func NewWasmRunner(ctx context.Context) (*WasmRunner, error) {
	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().
		WithMemoryLimitPages(wasmMemoryPages).
		// Abort a running guest when the Execute context is cancelled/expired.
		WithCloseOnContextDone(true),
	)
	wr := &WasmRunner{runtime: rt, sem: make(chan struct{}, runtime.GOMAXPROCS(0)*2)}
	if err := wr.registerHostFunctions(ctx); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("wasm host functions: %w", err)
	}
	return wr, nil
}

// registerHostFunctions installs the minimal host ABI. Deliberately tiny: no FS,
// no network, no syscalls — just logging and a monotonic-ish clock.
func (wr *WasmRunner) registerHostFunctions(ctx context.Context) error {
	_, err := wr.runtime.NewHostModuleBuilder(wasmHostModule).
		// appximo_log(ptr, len): read a UTF-8 string from guest memory → zerolog.
		NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, ptr, length uint32) {
			if length > wasmMaxLogBytes {
				length = wasmMaxLogBytes // bound the message a guest can emit
			}
			if buf, ok := m.Memory().Read(ptr, length); ok {
				tid, _ := ctx.Value(wasmTenantKey{}).(string)
				zlog.Info().Str("tenant", tid).Str("source", "wasm").Msg(string(buf))
			}
		}).
		Export("appximo_log").
		// appximo_now(): current time in unix milliseconds.
		NewFunctionBuilder().
		WithFunc(func(context.Context) int64 {
			return time.Now().UnixMilli()
		}).
		Export("appximo_now").
		Instantiate(ctx)
	return err
}

// cacheKey is tenantID + ":" + first 8 hex chars of sha256(wasmBytes). Including
// the tenant scopes the compiled-module cache per tenant.
func (wr *WasmRunner) cacheKey(tenantID string, wasmBytes []byte) string {
	sum := sha256.Sum256(wasmBytes)
	return tenantID + ":" + hex.EncodeToString(sum[:])[:8]
}

// getOrCompile returns the cached compiled module for key, compiling and caching
// it on first use. Concurrent first-uses compile in parallel but only one wins
// the cache; the losers are closed.
func (wr *WasmRunner) getOrCompile(ctx context.Context, key string, wasmBytes []byte) (wazero.CompiledModule, error) {
	if cm, ok := wr.compiled.Load(key); ok {
		return cm.(wazero.CompiledModule), nil
	}
	cm, err := wr.runtime.CompileModule(ctx, wasmBytes)
	if err != nil {
		return nil, err
	}
	if actual, loaded := wr.compiled.LoadOrStore(key, cm); loaded {
		_ = cm.Close(ctx) // another goroutine won the race
		return actual.(wazero.CompiledModule), nil
	}
	atomic.AddInt64(&wr.compileCount, 1)
	return cm, nil
}

// CompileCount returns how many distinct modules have actually been compiled
// (cache misses). Used by tests to assert the cache prevents recompilation.
func (wr *WasmRunner) CompileCount() int64 { return atomic.LoadInt64(&wr.compileCount) }

// Execute runs functionName (default "transform") in the given module, passing
// input through the (ptr,len) ABI and returning the bytes the function points to.
//
// ABI: the module must export "memory", "alloc"(size i32)->(ptr i32), and
// functionName as (ptr i32, len i32)->(outPtr i32, outLen i32). "free" is called
// if exported. The returned slice is copied out of guest memory before the
// instance is closed.
//
// Timeout: if ctx has no deadline, wasmDefaultTimeout is applied. A guest that
// runs past the deadline is aborted and Execute returns an error.
func (wr *WasmRunner) Execute(ctx context.Context, tenantID string, wasmBytes []byte, functionName string, input []byte) ([]byte, error) {
	if functionName == "" {
		functionName = "transform"
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, wasmDefaultTimeout)
		defer cancel()
	}
	ctx = context.WithValue(ctx, wasmTenantKey{}, tenantID)

	// Bound concurrency: each instance allocates up to 16 MiB, so cap how many run
	// at once (nil sem only for a zero-value runner — never via NewWasmRunner).
	if wr.sem != nil {
		select {
		case wr.sem <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		defer func() { <-wr.sem }()
	}

	compiled, err := wr.getOrCompile(ctx, wr.cacheKey(tenantID, wasmBytes), wasmBytes)
	if err != nil {
		return nil, fmt.Errorf("wasm compile: %w", err)
	}

	// Anonymous module name → multiple instances of the same module may run
	// concurrently without colliding in the runtime namespace.
	mod, err := wr.runtime.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithName(""))
	if err != nil {
		return nil, fmt.Errorf("wasm instantiate: %w", err)
	}
	defer mod.Close(ctx) //nolint:errcheck

	alloc := mod.ExportedFunction("alloc")
	fn := mod.ExportedFunction(functionName)
	if alloc == nil || fn == nil {
		return nil, fmt.Errorf("wasm module must export 'alloc' and %q", functionName)
	}
	free := mod.ExportedFunction("free")

	inLen := uint64(len(input))
	allocRes, err := alloc.Call(ctx, inLen)
	if err != nil {
		return nil, fmt.Errorf("wasm alloc: %w", err)
	}
	ptr := allocRes[0]
	if len(input) > 0 && !mod.Memory().Write(uint32(ptr), input) {
		return nil, errors.New("wasm: input does not fit in module memory")
	}

	out, err := fn.Call(ctx, ptr, inLen)
	if err != nil {
		return nil, fmt.Errorf("wasm execute %q: %w", functionName, err)
	}
	if len(out) != 2 {
		return nil, fmt.Errorf("wasm %q must return (ptr,len); got %d results", functionName, len(out))
	}
	outPtr, outLen := uint32(out[0]), uint32(out[1])
	result, ok := mod.Memory().Read(outPtr, outLen)
	if !ok {
		return nil, errors.New("wasm: output region out of bounds")
	}
	if free != nil {
		_, _ = free.Call(ctx, ptr, inLen)
	}

	// Copy out of guest memory before the deferred Close frees it.
	cp := make([]byte, len(result))
	copy(cp, result)
	return cp, nil
}

// RegisterModule pre-loads a module under a name so hooks can reference it by
// HookConfig.WasmModule. A defensive copy of wasmBytes is stored.
func (wr *WasmRunner) RegisterModule(name string, wasmBytes []byte) {
	wr.modules.Store(name, append([]byte(nil), wasmBytes...))
}

// ExecuteNamed resolves a pre-loaded module by name and runs functionName on it.
// Used by the HookRunner for HookConfig.Type == "wasm".
func (wr *WasmRunner) ExecuteNamed(ctx context.Context, tenantID, moduleName, functionName string, input []byte) ([]byte, error) {
	v, ok := wr.modules.Load(moduleName)
	if !ok {
		return nil, fmt.Errorf("wasm module %q not registered", moduleName)
	}
	return wr.Execute(ctx, tenantID, v.([]byte), functionName, input)
}

// Close releases the runtime and all cached compiled modules.
func (wr *WasmRunner) Close(ctx context.Context) error {
	return wr.runtime.Close(ctx)
}
