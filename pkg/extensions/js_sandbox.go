package extensions

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"strconv"
	"time"

	"github.com/dop251/goja"
)

var (
	emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
	nitRe   = regexp.MustCompile(`^\d{9,10}$`)
)

// JSSandbox executes untrusted JS hook scripts in a time-limited Goja VM.
type JSSandbox struct {
	timeout time.Duration
	// sem limits concurrent VM executions to GOMAXPROCS*2 to prevent N tenants
	// saturating all CPU cores with hook execution.
	sem chan struct{}
}

// NewJSSandbox returns a sandbox with 80 ms watchdog and GOMAXPROCS*2 concurrency limit.
func NewJSSandbox() *JSSandbox {
	return &JSSandbox{
		timeout: 500 * time.Millisecond,
		sem:     make(chan struct{}, runtime.GOMAXPROCS(0)*2),
	}
}

// HookResult is the outcome of a hook script execution.
type HookResult struct {
	Proceed bool
	Data    map[string]any
	Error   string
}

type runResult struct {
	hook *HookResult
	err  error
}

// RunHook executes script inside a fresh Goja VM with an 80 ms watchdog interrupt.
// payload is the mutable request data; userCtx carries role/user_id/tenant_id.
// The script may read and modify `data`, and must set `result.proceed` to
// false (with `result.error`) to abort the operation.
// Returns (*goja.InterruptedError, nil) if the watchdog fires.
func (s *JSSandbox) RunHook(
	ctx context.Context,
	script string,
	payload map[string]any,
	userCtx map[string]any,
) (*HookResult, error) {
	if payload == nil {
		payload = make(map[string]any)
	}

	// Acquire concurrency slot — prevents hook storms from saturating CPU cores.
	select {
	case s.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-s.sem }()

	vm := goja.New()

	vm.Set("data", payload)
	vm.Set("user", userCtx)
	vm.Set("result", map[string]any{
		"proceed": true,
		"data":    payload,
		"error":   "",
	})

	// Allowed utilities — ONLY these. No require, fetch, os, fs.
	vm.Set("parseFloat", strconv.ParseFloat)
	vm.Set("parseInt", strconv.Atoi)
	vm.Set("now", func() int64 { return time.Now().Unix() })
	vm.Set("formatMoney", func(v float64) string { return fmt.Sprintf("%.2f", v) })
	vm.Set("isValidEmail", func(addr string) bool { return emailRe.MatchString(addr) })
	vm.Set("isValidNIT", func(nit string) bool { return nitRe.MatchString(nit) })

	done := make(chan runResult, 1)

	// 80 ms watchdog: fires vm.Interrupt before the goroutine is scheduled away.
	wdog := time.AfterFunc(80*time.Millisecond, func() {
		vm.Interrupt("hook timeout")
	})

	go func() {
		_, err := vm.RunString(script)
		if err != nil {
			var ie *goja.InterruptedError
			if errors.As(err, &ie) {
				done <- runResult{err: ie}
				return
			}
			done <- runResult{hook: &HookResult{Proceed: false, Error: err.Error()}}
			return
		}

		raw := vm.Get("result").Export()
		m, ok := raw.(map[string]any)
		if !ok {
			done <- runResult{hook: &HookResult{Proceed: false, Error: "result is not an object"}}
			return
		}

		proceed, _ := m["proceed"].(bool)
		data, _ := m["data"].(map[string]any)
		if data == nil {
			data = payload
		}

		errVal := ""
		if m["error"] != nil {
			errVal = fmt.Sprint(m["error"])
		}

		done <- runResult{hook: &HookResult{Proceed: proceed, Data: data, Error: errVal}}
	}()

	select {
	case r := <-done:
		wdog.Stop()
		if r.err != nil {
			return nil, r.err
		}
		return r.hook, nil
	case <-time.After(s.timeout): // hard fallback in case interrupt is delayed
		vm.Interrupt("execution timeout")
		<-done // drain so goroutine is not leaked
		wdog.Stop()
		return nil, errors.New("hook timeout: exceeded 500ms")
	case <-ctx.Done():
		vm.Interrupt("context cancelled")
		<-done
		wdog.Stop()
		return nil, ctx.Err()
	}
}
