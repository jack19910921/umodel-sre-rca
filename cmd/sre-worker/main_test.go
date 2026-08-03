package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jack/umodel-sre-rca/internal/config"
)

func TestValidateWorkerConfigRejectsMissingFeishuDeliveryConfig(t *testing.T) {
	cfg := config.Config{}
	cfg.Worker.Enabled = true
	if err := validateWorkerConfig(cfg); err == nil {
		t.Fatal("validateWorkerConfig() error = nil, want missing Feishu delivery config")
	}
}

func TestFeishuBaseURLIncludesOpenAPIPrefix(t *testing.T) {
	const want = "https://open.feishu.cn/open-apis"
	if feishuBaseURL != want {
		t.Fatalf("feishuBaseURL = %q, want %q", feishuBaseURL, want)
	}
}

func TestRunLoopGivesEveryWorkerCallABoundedContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time, 1)
	worker := &recordingWorker{calls: 0, onCall: func(call int, deadline time.Time, ok bool) error {
		if !ok {
			t.Fatalf("call %d did not receive a context deadline", call)
		}
		remaining := time.Until(deadline)
		if remaining <= 0 || remaining > 200*time.Millisecond {
			t.Fatalf("call %d deadline remaining = %s, want a bounded timeout", call, remaining)
		}
		if call == 1 {
			ticks <- time.Now()
		} else {
			cancel()
		}
		return nil
	}}

	runLoop(ctx, worker, 10*time.Millisecond, 100*time.Millisecond, ticks, func(string, ...any) {})
	if got := worker.CallCount(); got != 2 {
		t.Fatalf("worker calls = %d, want 2", got)
	}
}

func TestRunLoopKeepsPollingAfterOrdinaryError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time, 1)
	worker := &recordingWorker{onCall: func(call int, _ time.Time, _ bool) error {
		switch call {
		case 1:
			ticks <- time.Now()
			return errors.New("temporary provider error")
		case 2:
			cancel()
			return nil
		default:
			return nil
		}
	}}

	runLoop(ctx, worker, 10*time.Millisecond, 100*time.Millisecond, ticks, func(string, ...any) {})
	if got := worker.CallCount(); got != 2 {
		t.Fatalf("worker calls = %d, want 2 after ordinary error", got)
	}
}

type recordingWorker struct {
	mu     sync.Mutex
	calls  int
	onCall func(int, time.Time, bool) error
}

func (w *recordingWorker) RunOne(ctx context.Context) error {
	deadline, ok := ctx.Deadline()
	w.mu.Lock()
	w.calls++
	call := w.calls
	w.mu.Unlock()
	return w.onCall(call, deadline, ok)
}

func (w *recordingWorker) CallCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.calls
}
