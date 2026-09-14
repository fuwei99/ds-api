package devid

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/dop251/goja"
)

type timer struct {
	id   int64
	at   time.Time
	ivl  time.Duration
	fn   goja.Callable
	args []goja.Value
}

type eventLoop struct {
	vm     *goja.Runtime
	timers map[int64]*timer
	nextID int64
}

func newEventLoop(vm *goja.Runtime) (*eventLoop, error) {
	el := &eventLoop{vm: vm, timers: map[int64]*timer{}}
	if err := vm.Set("__setTimeout", func(call goja.FunctionCall) goja.Value {
		return el.set(call, false)
	}); err != nil {
		return nil, err
	}
	if err := vm.Set("__clearTimeout", func(call goja.FunctionCall) goja.Value {
		el.clear(call.Argument(0))
		return goja.Undefined()
	}); err != nil {
		return nil, err
	}
	if err := vm.Set("__setInterval", func(call goja.FunctionCall) goja.Value {
		return el.set(call, true)
	}); err != nil {
		return nil, err
	}
	if err := vm.Set("__clearInterval", func(call goja.FunctionCall) goja.Value {
		el.clear(call.Argument(0))
		return goja.Undefined()
	}); err != nil {
		return nil, err
	}
	return el, nil
}

func (el *eventLoop) set(call goja.FunctionCall, interval bool) goja.Value {
	fn, ok := goja.AssertFunction(call.Argument(0))
	if !ok {
		return goja.Undefined()
	}
	var delay time.Duration
	if !goja.IsUndefined(call.Argument(1)) && !goja.IsNull(call.Argument(1)) {
		delay = time.Duration(call.Argument(1).ToInteger()) * time.Millisecond
	}
	if delay < 0 {
		delay = 0
	}
	el.nextID++
	start := min2(2, len(call.Arguments))
	t := &timer{
		id:   el.nextID,
		at:   time.Now().Add(delay),
		fn:   fn,
		args: call.Arguments[start:],
	}
	if interval {
		t.ivl = delay
	}
	el.timers[t.id] = t
	return el.vm.ToValue(t.id)
}

func (el *eventLoop) clear(idVal goja.Value) {
	id := idVal.ToInteger()
	delete(el.timers, id)
}

func min2(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (el *eventLoop) nextDue(now time.Time) (*timer, time.Time) {
	var due *timer
	var nextAt time.Time
	for _, t := range el.timers {
		if !t.at.After(now) && (due == nil || t.at.Before(due.at)) {
			due = t
		}
		if nextAt.IsZero() || t.at.Before(nextAt) {
			nextAt = t.at
		}
	}
	return due, nextAt
}

func (el *eventLoop) fire(t *timer) {
	if t.ivl > 0 {
		t.at = time.Now().Add(t.ivl)
	} else {
		delete(el.timers, t.id)
	}
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "[devid] timer callback panic: %v\n", r)
		}
	}()
	_, _ = t.fn(goja.Undefined(), t.args...)
}

func (el *eventLoop) runUntil(deadline time.Time, cond func() bool, grace time.Duration) {
	graceEnd := time.Time{}
	for {
		now := time.Now()
		if now.After(deadline) {
			return
		}
		if cond() {
			if graceEnd.IsZero() {
				graceEnd = now.Add(grace)
			}
		} else if !graceEnd.IsZero() {
			return
		}
		if !graceEnd.IsZero() && now.After(graceEnd) {
			return
		}
		due, nextAt := el.nextDue(now)
		if due == nil {
			if len(el.timers) == 0 {
				return
			}
			w := time.Until(nextAt)
			if !graceEnd.IsZero() {
				if d := time.Until(graceEnd); d < w {
					w = d
				}
			}
			if d := time.Until(deadline); d < w {
				w = d
			}
			if w > 0 {
				time.Sleep(w)
			}
			continue
		}
		el.fire(due)
	}
}

// captureStack 用 goja 的调用栈伪装 V8 格式的 Error.stack，
// 避免 SDK 的 cdp 采集器把非 V8 栈判定为异常环境。
func captureStack(vm *goja.Runtime, header string) string {
	cs := vm.CaptureCallStack(64, nil)
	var sb strings.Builder
	sb.WriteString(header)
	for i := 1; i < len(cs); i++ {
		fr := cs[i]
		name := fr.FuncName()
		if name == "" {
			name = "<anonymous>"
		}
		pos := fr.Position().String()
		if pos == "" || pos == ":0:0" {
			pos = "fp-1.min.js:1:1"
		}
		fmt.Fprintf(&sb, "\n    at %s (%s)", name, pos)
	}
	return sb.String()
}
