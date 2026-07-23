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

// NewEvidenceService creates the fixed, customer-hosted evidence path. Its
// provider registry is intentionally static: no callback payload can select a
// URL, command, credential, or arbitrary cloud API.
func NewEvidenceService(cfg config.Config, repo evidenceRepository) (*evidence.Service, error) {
	if !cfg.Worker.Enabled {
		return nil, fmt.Errorf("worker runtime composition is disabled")
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
		Profile: cfg.Aliyun.Profile, Workspace: cfg.Aliyun.Workspace, Region: cfg.Aliyun.Region,
		SLSProject: cfg.Aliyun.SLSProject, SLSLogstore: cfg.Aliyun.SLSLogstore,
	}, provider.NewAliyunRunner(cfg.Aliyun.Profile, provider.OSExecutor{}))
	providers := map[string]evidence.Provider{
		"aliyun.umodel":          aliyun,
		"aliyun.synthetic_probe": aliyun,
		"aliyun.cloudmonitor":    aliyun,
		"aliyun.sls":             aliyun,
		"aliyun.actiontrail":     aliyun,
	}
	return evidence.NewService(registry, incidents, providers).WithEvidenceLookup(repo), nil
}
