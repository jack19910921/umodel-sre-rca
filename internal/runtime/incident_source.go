package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/jack/umodel-sre-rca/internal/config"
	"github.com/jack/umodel-sre-rca/internal/domain"
	"github.com/jack/umodel-sre-rca/internal/evidence"
)

const incidentEvidenceWindow = 30 * time.Minute

type incidentRepository interface {
	IncidentByID(context.Context, string) (domain.Incident, error)
}

type IncidentSource struct {
	repo     incidentRepository
	bindings map[string]config.IncidentBinding
}

func NewIncidentSource(repo incidentRepository, bindings []config.IncidentBinding) (*IncidentSource, error) {
	if repo == nil {
		return nil, fmt.Errorf("incident repository is required")
	}
	if err := config.ValidateIncidentBindings(bindings); err != nil {
		return nil, err
	}
	byKey := make(map[string]config.IncidentBinding, len(bindings))
	for _, binding := range bindings {
		byKey[config.IncidentBindingKey(binding)] = binding
	}
	return &IncidentSource{repo: repo, bindings: byKey}, nil
}

func (s *IncidentSource) ResolveIncident(ctx context.Context, incidentID string) (evidence.IncidentContext, error) {
	if incidentID == "" {
		return evidence.IncidentContext{}, fmt.Errorf("incident id is required")
	}
	incident, err := s.repo.IncidentByID(ctx, incidentID)
	if err != nil {
		return evidence.IncidentContext{}, err
	}
	binding, found := s.bindings[config.IncidentBindingKey(config.IncidentBinding{
		Workspace: incident.Workspace, RuleID: incident.RuleID, ResourceID: incident.ResourceID,
	})]
	if !found {
		return evidence.IncidentContext{}, fmt.Errorf("no incident binding for workspace=%q rule_id=%q resource_id=%q", incident.Workspace, incident.RuleID, incident.ResourceID)
	}
	selectors := make(evidence.Selectors, len(binding.Selectors))
	for name, value := range binding.Selectors {
		selectors[name] = value
	}
	return evidence.IncidentContext{
		Selectors: selectors,
		Window:    evidence.Window{Start: incident.AlertAt.Add(-incidentEvidenceWindow), End: incident.AlertAt},
	}, nil
}
