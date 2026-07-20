package evidence

import (
	"context"
	"fmt"

	"github.com/jack/umodel-sre-rca/internal/domain"
)

type Service struct {
	registry  Registry
	incidents IncidentSource
	providers map[string]Provider
	lookup    EvidenceLookup
}

func NewService(registry Registry, incidents IncidentSource, providers map[string]Provider) *Service {
	return &Service{registry: registry, incidents: incidents, providers: providers}
}

func (s *Service) WithEvidenceLookup(lookup EvidenceLookup) *Service {
	s.lookup = lookup
	return s
}

func (s *Service) Context(ctx context.Context, incidentID string) ([]domain.Evidence, error) {
	return s.resolve(ctx, ContextClass, incidentID)
}

func (s *Service) Metrics(ctx context.Context, incidentID string) ([]domain.Evidence, error) {
	return s.resolve(ctx, MetricsClass, incidentID)
}

func (s *Service) Logs(ctx context.Context, incidentID string) ([]domain.Evidence, error) {
	return s.resolve(ctx, LogsClass, incidentID)
}

func (s *Service) Changes(ctx context.Context, incidentID string) ([]domain.Evidence, error) {
	return s.resolve(ctx, ChangesClass, incidentID)
}

func (s *Service) Open(ctx context.Context, evidenceID string) (domain.Evidence, error) {
	if evidenceID == "" {
		return domain.Evidence{}, fmt.Errorf("evidence id is required")
	}
	if s.lookup == nil {
		return domain.Evidence{}, fmt.Errorf("evidence lookup is not configured")
	}
	return s.lookup.OpenEvidence(ctx, evidenceID)
}

func (s *Service) resolve(ctx context.Context, class EvidenceClass, incidentID string) ([]domain.Evidence, error) {
	if incidentID == "" {
		return nil, fmt.Errorf("incident id is required")
	}
	contextData, err := s.incidents.ResolveIncident(ctx, incidentID)
	if err != nil {
		return nil, fmt.Errorf("resolve incident %s: %w", incidentID, err)
	}
	var evidence []domain.Evidence
	for _, binding := range s.registry.BindingsFor(class) {
		selectors, err := resolveSelectors(binding, contextData.Selectors)
		if err != nil {
			return nil, err
		}
		provider := s.providers[binding.Provider]
		if provider == nil {
			return nil, fmt.Errorf("provider %q is not configured", binding.Provider)
		}
		items, err := provider.Resolve(ctx, binding, selectors, contextData.Window)
		if err != nil {
			return nil, fmt.Errorf("resolve binding %q: %w", binding.ID, err)
		}
		evidence = append(evidence, items...)
	}
	return evidence, nil
}

func resolveSelectors(binding Binding, incidentSelectors Selectors) (Selectors, error) {
	selectors := make(Selectors, len(binding.SelectorMapping))
	for selector, field := range binding.SelectorMapping {
		value := incidentSelectors[field]
		if value == "" {
			return nil, fmt.Errorf("binding %q: missing selector %q", binding.ID, field)
		}
		selectors[selector] = value
	}
	return selectors, nil
}
