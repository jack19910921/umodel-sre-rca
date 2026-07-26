package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jack/umodel-sre-rca/internal/config"
	"github.com/jack/umodel-sre-rca/internal/domain"
	"github.com/jack/umodel-sre-rca/internal/provider"
)

type fakeEvidenceRepository struct{ fakeIncidentRepository }

func (fakeEvidenceRepository) OpenEvidence(context.Context, string) (domain.Evidence, error) {
	return domain.Evidence{}, nil
}

type fakeRuntimeCloud struct{}

func (fakeRuntimeCloud) Call(context.Context, provider.AliyunRequest) ([]byte, error) {
	return []byte(`{"RequestId":"req","Events":[]}`), nil
}

func TestNewEvidenceServiceComposesReviewedSDKProviders(t *testing.T) {
	dir := t.TempDir()
	writeRuntimeFixture(t, filepath.Join(dir, "bindings.yaml"), `
bindings:
  - id: context
    evidence_class: context
    target: sre.service_endpoint
    provider: aliyun.umodel
    query_template: endpoint_context_v1
    selector_mapping: {endpoint_id: endpoint_id}
  - id: probe
    evidence_class: metrics
    target: sre.service_endpoint
    provider: aliyun.synthetic_probe
    query_template: availability_window_v1
    selector_mapping: {probe_task_id: probe_task_id}
  - id: ecs
    evidence_class: metrics
    target: acs.ecs.instance
    provider: aliyun.cloudmonitor
    query_template: ecs_normal_state_window_v1
    selector_mapping: {instance_id: instance_id, region_id: region_id}
  - id: logs
    evidence_class: logs
    target: sre.service_endpoint
    provider: aliyun.sls
    query_template: nginx_access_by_window_v1
    selector_mapping: {endpoint_id: endpoint_id}
  - id: changes
    evidence_class: changes
    target: acs.ecs.securitygroup
    provider: aliyun.actiontrail
    query_template: security_group_change_window_v1
    selector_mapping: {security_group_id: security_group_id}
`)
	writeRuntimeFixture(t, filepath.Join(dir, "incident-bindings.yaml"), `
incident_bindings:
  - workspace: ws
    rule_id: rule
    resource_id: resource
    selectors:
      endpoint_id: blog-http
      probe_task_id: probe-1
      instance_id: i-demo
      region_id: cn-hangzhou
      account_id: '123456789'
      security_group_id: sg-demo
`)
	cfg := config.Config{}
	cfg.Worker.Enabled = true
	cfg.Aliyun.ECSRAMRoleName = "sre-rca"
	cfg.Aliyun.Workspace = "workspace"
	cfg.Aliyun.Region = "cn-hangzhou"
	cfg.Aliyun.SLSProject = "project"
	cfg.Aliyun.SLSLogstore = "logstore"
	cfg.EvidenceBindingsPath = filepath.Join(dir, "bindings.yaml")
	cfg.IncidentBindingsPath = filepath.Join(dir, "incident-bindings.yaml")
	repo := fakeEvidenceRepository{fakeIncidentRepository{incident: domain.Incident{
		ID: "incident-1", Workspace: "ws", RuleID: "rule", ResourceID: "resource", AlertAt: time.Now().UTC(),
	}}}

	service, err := newEvidenceService(cfg, repo, fakeRuntimeCloud{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Context(context.Background(), "incident-1"); err != nil {
		t.Fatalf("Context() error = %v", err)
	}
	if _, err := service.Metrics(context.Background(), "incident-1"); err != nil {
		t.Fatalf("Metrics() error = %v", err)
	}
	if _, err := service.Logs(context.Background(), "incident-1"); err != nil {
		t.Fatalf("Logs() error = %v", err)
	}
	if _, err := service.Changes(context.Background(), "incident-1"); err != nil {
		t.Fatalf("Changes() error = %v", err)
	}
}

func writeRuntimeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
