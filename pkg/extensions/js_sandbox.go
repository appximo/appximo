package extensions

import (
	"context"
	"errors"
	"fmt"
	"regexp"
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
}

// NewJSSandbox returns a sandbox with the default 500 ms timeout.
func NewJSSandbox() *JSSandbox {
	return &JSSandbox{timeout: 500 * time.Millisecond}
}

// HookResult is the outcome of a hook script execution.
type HookResult struct {
	Proceed bool
	Data    map[string]any
	Error   string
}

// RunHook executes script inside a fresh Goja VM.
// payload is the mutable request data; userCtx carries role/user_id/tenant_id.
// The script may read and modify `data`, and must set `result.proceed` to
// false (with `result.error`) to abort the operation.
func (s *JSSandbox) RunHook(
	ctx context.Context,
	script string,
	payload map[string]any,
	userCtx map[string]any,
) (*HookResult, error) {
	if payload == nil {
		payload = make(map[string]any)
	}

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

	done := make(chan *HookResult, 1)
	go func() {
		_, err := vm.RunString(script)
		if err != nil {
			done <- &HookResult{Proceed: false, Error: err.Error()}
			return
		}

		raw := vm.Get("result").Export()
		m, ok := raw.(map[string]any)
		if !ok {
			done <- &HookResult{Proceed: false, Error: "result is not an object"}
			return
		}

		proceed, _ := m["proceed"].(bool)
		data, _ := m["data"].(map[string]any)
		if data == nil {
			data = payload // fall back to the original map (JS may have mutated it)
		}

		errVal := ""
		if m["error"] != nil {
			errVal = fmt.Sprint(m["error"])
		}

		done <- &HookResult{Proceed: proceed, Data: data, Error: errVal}
	}()

	select {
	case res := <-done:
		return res, nil
	case <-time.After(s.timeout):
		vm.Interrupt("execution timeout")
		return nil, errors.New("hook timeout: exceeded 500ms")
	case <-ctx.Done():
		vm.Interrupt("context cancelled")
		return nil, ctx.Err()
	}
}
