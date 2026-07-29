package modelsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigAcceptsEndpointOnlyDryRun(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sre-sync.yaml")
	raw := `workspace: default-cms-1876202723954089-cn-hangzhou
region: cn-hangzhou
ecs_ram_role_name: sre-rca
relation: {}
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

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if got, want := len(cfg.Endpoints), 1; got != want {
		t.Fatalf("endpoint count = %d, want %d", got, want)
	}
	if cfg.Relation.Type != "" {
		t.Errorf("relation type = %q, want empty safe default", cfg.Relation.Type)
	}
}

func TestLoadConfigRejectsApplyUnsafeRelationDestination(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sre-sync.yaml")
	raw := `workspace: default-cms-1876202723954089-cn-hangzhou
region: cn-hangzhou
relation:
  type: runs_on
  destination_domain: sre
endpoints:
  - endpoint_id: blog-http
    service_name: blog
    ecs_entity_id: d713389806398932cf6b1ff1eaa86a8b
`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("LoadConfig() error = nil, want unsafe relation destination rejection")
	}
}

func TestLoadConfigRejectsMissingECSRAMRole(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sync.yaml")
	content := `workspace: default-cms-1876202723954089-cn-hangzhou
region: cn-hangzhou
endpoints:
  - endpoint_id: blog-http
    service_name: blog
    ecs_entity_id: acs-entity
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "ecs_ram_role_name") {
		t.Fatalf("LoadConfig() error = %v, want missing ecs_ram_role_name error", err)
	}
}
