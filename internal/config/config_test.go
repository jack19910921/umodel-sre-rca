package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRejectsMissingCallbackToken(t *testing.T) {
	_, err := Load("../../testdata/invalid-config.yaml")
	if err == nil || !strings.Contains(err.Error(), "callback_token") {
		t.Fatalf("Load() error = %v, want callback_token validation", err)
	}
}

func TestLoadAcceptsEcsRAMRoleProfile(t *testing.T) {
	cfg, err := Load("../../testdata/valid-config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Aliyun.Profile != "sre-ecs-role" || cfg.Worker.MaxTurns != 8 {
		t.Fatalf("unexpected config: %#v", cfg)
	}
}

func TestLoadRejectsEnabledWorkerWithoutCompositionPaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(`
callback_token: test-token
state_db: /tmp/state.db
aliyun:
  profile: sre-ecs-role
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
