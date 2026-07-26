package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAllowsGatewayOrEvidenceConfigWithoutCallbackOrFeishuSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(`
state_db: /tmp/state.db
evidence_bindings_path: /tmp/evidence-bindings.yaml
incident_bindings_path: /tmp/incident-bindings.yaml
aliyun:
  ecs_ram_role_name: sre-rca
  workspace: workspace
  region: cn-hangzhou
  sls_project: project
  sls_logstore: logstore
worker:
  enabled: true
  max_turns: 8
  timeout_seconds: 300
  poll_seconds: 15
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("Load() error = %v, want child-safe config load", err)
	}
}

func TestLoadAcceptsECSRAMRoleRuntimeConfig(t *testing.T) {
	cfg, err := Load("../../testdata/valid-config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Aliyun.ECSRAMRoleName != "sre-rca" || cfg.Worker.MaxTurns != 8 {
		t.Fatalf("unexpected config: %#v", cfg)
	}
}

func TestLoadWorkerDoesNotRequireAliyunCLIProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(`
state_db: /var/lib/sre-rca/state.db
aliyun:
  workspace: workspace
  region: cn-hangzhou
  sls_project: project
  sls_logstore: logstore
evidence_bindings_path: /etc/sre-rca/evidence-bindings.yaml
incident_bindings_path: /etc/sre-rca/incident-bindings.yaml
worker:
  enabled: true
  max_turns: 8
  poll_seconds: 30
`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(path); err != nil {
		t.Fatalf("Load() error = %v, want ECS RAM Role runtime without an aliyun CLI profile", err)
	}
}

func TestLoadRejectsEnabledWorkerWithoutCompositionPaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(`
callback_token: test-token
state_db: /tmp/state.db
aliyun:
  ecs_ram_role_name: sre-rca
worker:
  enabled: true
  max_turns: 8
`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "evidence_bindings_path") {
		t.Fatalf("Load() error = %v, want worker composition validation", err)
	}
}

func TestLoadRejectsEnabledWorkerWithInvalidPollInterval(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(`
callback_token: test-token
state_db: /tmp/state.db
evidence_bindings_path: /tmp/evidence-bindings.yaml
incident_bindings_path: /tmp/incident-bindings.yaml
aliyun:
  ecs_ram_role_name: sre-rca
  workspace: workspace
  region: cn-hangzhou
  sls_project: project
  sls_logstore: logstore
worker:
  enabled: true
  max_turns: 8
  timeout_seconds: 300
  poll_seconds: 0
`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "poll_seconds") {
		t.Fatalf("Load() error = %v, want poll_seconds validation", err)
	}
}

func TestLoadIncidentBindingsRejectsDuplicateTupleAndEmptySelector(t *testing.T) {
	path := filepath.Join(t.TempDir(), "incident-bindings.yaml")
	if err := os.WriteFile(path, []byte(`
incident_bindings:
  - workspace: ws
    rule_id: rule
    resource_id: resource
    selectors:
      endpoint_id: blog-http
  - workspace: ws
    rule_id: rule
    resource_id: resource
    selectors:
      instance_id: ""
`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadIncidentBindings(path)
	if err == nil || (!strings.Contains(err.Error(), "duplicate") && !strings.Contains(err.Error(), "empty")) {
		t.Fatalf("LoadIncidentBindings() error = %v, want invalid binding validation", err)
	}
}
