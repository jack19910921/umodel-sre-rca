package provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// Executor is intentionally narrower than os/exec: it cannot invoke a shell.
type Executor interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

const maxCommandOutputBytes = 1 << 20

var errCommandOutputTooLarge = errors.New("command stdout exceeds 1 MiB")

// OSExecutor runs a fixed binary with explicit arguments. It intentionally
// never starts a shell, so bindings cannot introduce command interpolation.
type OSExecutor struct{}

func (OSExecutor) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	var output cappedBuffer
	command := exec.CommandContext(ctx, name, args...)
	command.Stdout = &output
	command.Stderr = &output
	err := command.Run()
	if output.exceeded {
		return output.Bytes(), errCommandOutputTooLarge
	}
	if err != nil {
		return output.Bytes(), err
	}
	return output.Bytes(), nil
}

type cappedBuffer struct {
	buffer   bytes.Buffer
	exceeded bool
}

func (b *cappedBuffer) Write(input []byte) (int, error) {
	remaining := maxCommandOutputBytes - b.buffer.Len()
	if remaining <= 0 {
		b.exceeded = true
		return 0, errCommandOutputTooLarge
	}
	if len(input) > remaining {
		_, _ = b.buffer.Write(input[:remaining])
		b.exceeded = true
		return remaining, errCommandOutputTooLarge
	}
	return b.buffer.Write(input)
}

func (b *cappedBuffer) Bytes() []byte { return b.buffer.Bytes() }

type AliyunRunner struct {
	profile string
	exec    Executor
}

var allowedActions = map[string]map[string]bool{
	"cms":         {"GetEntityStoreData": true, "GetUmodelData": true, "DescribeMetricList": true},
	"sls":         {"GetLogs": true},
	"actiontrail": {"LookupEvents": true},
}

func NewAliyunRunner(profile string, exec Executor) *AliyunRunner {
	return &AliyunRunner{profile: profile, exec: exec}
}

func (r *AliyunRunner) Run(ctx context.Context, product, action string, args []string) ([]byte, error) {
	if !allowedActions[product][action] {
		return nil, fmt.Errorf("%s:%s not allowlisted", product, action)
	}
	if r.profile == "" || r.exec == nil {
		return nil, fmt.Errorf("aliyun runner profile and executor are required")
	}
	command := []string{"--profile", r.profile, product, action, "--output", "json"}
	command = append(command, args...)
	return r.exec.Run(ctx, "aliyun", command...)
}
