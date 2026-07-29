package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jack/umodel-sre-rca/internal/modelsync"
)

type recordedWriter struct {
	called    bool
	workspace string
	plan      modelsync.Plan
}

func (w *recordedWriter) Upsert(_ context.Context, workspace string, plan modelsync.Plan) error {
	w.called = true
	w.workspace = workspace
	w.plan = plan
	return nil
}

func TestRunDryRunPrintsPlanWithoutCreatingWriter(t *testing.T) {
	configPath := writeSyncConfig(t)
	oldFactory := newWriter
	defer func() { newWriter = oldFactory }()
	newWriter = func(string, string) (modelsync.Writer, error) {
		t.Fatal("newWriter must not be called in dry-run mode")
		return nil, nil
	}

	var stdout, stderr bytes.Buffer
	if got := run([]string{"--config", configPath}, &stdout, &stderr); got != 0 {
		t.Fatalf("run() = %d, stderr = %s", got, stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if got, want := result["mode"], "dry-run"; got != want {
		t.Errorf("mode = %q, want %q", got, want)
	}
	if got, want := int(result["element_count"].(float64)), 1; got != want {
		t.Errorf("element_count = %d, want %d", got, want)
	}
}

func TestRunApplyUsesDedicatedWriter(t *testing.T) {
	configPath := writeSyncConfig(t)
	writer := &recordedWriter{}
	oldFactory := newWriter
	defer func() { newWriter = oldFactory }()
	newWriter = func(region, role string) (modelsync.Writer, error) {
		if region != "cn-hangzhou" || role != "sre-rca" {
			t.Fatalf("writer args = %q, %q", region, role)
		}
		return writer, nil
	}

	var stdout, stderr bytes.Buffer
	if got := run([]string{"--config", configPath, "--apply"}, &stdout, &stderr); got != 0 {
		t.Fatalf("run() = %d, stderr = %s", got, stderr.String())
	}
	if !writer.called {
		t.Fatal("writer was not called")
	}
	if got, want := writer.workspace, "default-cms-1876202723954089-cn-hangzhou"; got != want {
		t.Errorf("workspace = %q, want %q", got, want)
	}
	if got, want := len(writer.plan.Elements), 1; got != want {
		t.Errorf("element count = %d, want %d", got, want)
	}
}

func writeSyncConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sre-sync.yaml")
	raw := `workspace: default-cms-1876202723954089-cn-hangzhou
region: cn-hangzhou
ecs_ram_role_name: sre-rca
endpoints:
  - endpoint_id: blog-http
    service_name: blog
    service_url: https://jack-sre.com/
    environment: prod
    instance_id: i-bp17eb4oiqsmy10fq9mu
    ecs_entity_id: d713389806398932cf6b1ff1eaa86a8b
    region_id: cn-hangzhou
    account_id: "1876202723954089"
`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
