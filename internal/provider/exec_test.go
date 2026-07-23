package provider

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestOSExecutorCapsStdoutWithoutShell(t *testing.T) {
	executor := OSExecutor{}
	output, err := executor.Run(context.Background(), "/usr/bin/head", "-c", "1048577", "/dev/zero")
	if err == nil {
		t.Fatal("Run() error = nil, want output cap error")
	}
	if len(output) != maxCommandOutputBytes {
		t.Fatalf("stdout length = %d, want %d", len(output), maxCommandOutputBytes)
	}

	output, err = executor.Run(context.Background(), "/usr/bin/printf", "%s", "$HOME")
	if err != nil || string(output) != "$HOME" {
		t.Fatalf("Run() = %q, %v; want literal argument without shell expansion", output, err)
	}
}

func TestOSExecutorInheritsContextTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := (OSExecutor{}).Run(ctx, "/bin/sleep", "1")
	if err == nil || (!strings.Contains(err.Error(), "deadline") && !strings.Contains(err.Error(), "signal")) {
		t.Fatalf("Run() error = %v, want context timeout", err)
	}
}
