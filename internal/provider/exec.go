package provider

import (
	"context"
	"fmt"
)

// Executor is intentionally narrower than os/exec: it cannot invoke a shell.
type Executor interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

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
