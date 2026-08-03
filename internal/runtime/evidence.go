package runtime

import (
	"context"
	"fmt"

	"github.com/jack/umodel-sre-rca/internal/config"
	"github.com/jack/umodel-sre-rca/internal/domain"
	"github.com/jack/umodel-sre-rca/internal/evidence"
	"github.com/jack/umodel-sre-rca/internal/provider"
)

type evidenceRepository interface {
	incidentRepository
	OpenEvidence(context.Context, string) (domain.Evidence, error)
}

var newAliyunEvidenceClient = func(region, ecsRAMRoleName string) (provider.AliyunAPI, error) {
	return provider.NewAliyunSDKClient(region, ecsRAMRoleName)
}

// NewEvidenceService creates the fixed, customer-hosted evidence path for the
// worker and the manually-invoked, read-only evidence CLI. The worker itself
// retains its separate worker.enabled start gate.
func NewEvidenceService(cfg config.Config, repo evidenceRepository) (*evidence.Service, error) {
	cloud, err := newAliyunEvidenceClient(cfg.Aliyun.Region, cfg.Aliyun.ECSRAMRoleName)
	if err != nil {
		return nil, err
	}
	return newEvidenceService(cfg, repo, cloud)
}

func newEvidenceService(cfg config.Config, repo evidenceRepository, cloud provider.AliyunAPI) (*evidence.Service, error) {
	if cloud == nil {
		return nil, fmt.Errorf("aliyun cloud client is required")
	}
	registry, err := evidence.LoadRegistry(cfg.EvidenceBindingsPath)
	if err != nil {
		return nil, err
	}
	bindings, err := config.LoadIncidentBindings(cfg.IncidentBindingsPath)
	if err != nil {
		return nil, err
	}
	incidents, err := NewIncidentSource(repo, bindings)
	if err != nil {
		return nil, err
	}
	aliyun := provider.NewAliyunProvider(provider.AliyunConfig{
		Workspace: cfg.Aliyun.Workspace, Region: cfg.Aliyun.Region, ECSRAMRoleName: cfg.Aliyun.ECSRAMRoleName,
		SLSProject: cfg.Aliyun.SLSProject, SLSLogstore: cfg.Aliyun.SLSLogstore,
	}, cloud)
	providers := map[string]evidence.Provider{
		"aliyun.umodel":          aliyun,
		"aliyun.synthetic_probe": aliyun,
		"aliyun.cloudmonitor":    aliyun,
		"aliyun.sls":             aliyun,
		"aliyun.actiontrail":     aliyun,
	}
	return evidence.NewService(registry, incidents, providers).WithEvidenceLookup(repo), nil
}
