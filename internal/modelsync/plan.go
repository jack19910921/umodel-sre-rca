// Package modelsync constructs narrowly-scoped UModel upsert payloads for the
// customer-owned SRE domain. It deliberately never constructs an update for
// native CloudMonitor domains such as acs.
package modelsync

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jack/umodel-sre-rca/internal/umodelid"
)

const (
	sreDomain          = "sre"
	endpointEntityType = "sre.service_endpoint"
	acsDomain          = "acs"
	ecsEntityType      = "acs.ecs.instance"
	keepAliveSeconds   = 172800
)

// Endpoint is the static, customer-owned SRE projection of a service endpoint.
// It retains the native ECS entity ID as a reference; it does not duplicate or
// update any native ECS fields.
type Endpoint struct {
	EndpointID  string `yaml:"endpoint_id"`
	ServiceName string `yaml:"service_name"`
	ServiceURL  string `yaml:"service_url"`
	Environment string `yaml:"environment"`
	InstanceID  string `yaml:"instance_id"`
	ECSEntityID string `yaml:"ecs_entity_id"`
	RegionID    string `yaml:"region_id"`
	AccountID   string `yaml:"account_id"`
}

// Relation opts into writing an instance relation only after its type has been
// created and confirmed in the UModel schema. Leaving Type empty is safe and
// creates no relation record.
type Relation struct {
	Type                  string `yaml:"type"`
	DestinationDomain     string `yaml:"destination_domain"`
	DestinationEntityType string `yaml:"destination_entity_type"`
}

// Plan is a complete idempotent UModel upsert payload, ready for a dedicated
// writer boundary. Elements are intentionally raw because the UModel API owns
// the system field contract.
type Plan struct {
	Elements []map[string]any
}

// BuildPlan creates one SRE endpoint element and, only when explicitly
// configured, one relation from it to the preexisting native ECS entity.
func BuildPlan(endpoint Endpoint, relation Relation, observedAt time.Time) (Plan, error) {
	if strings.TrimSpace(endpoint.EndpointID) == "" {
		return Plan{}, errors.New("endpoint_id is required")
	}
	if strings.TrimSpace(endpoint.ServiceName) == "" {
		return Plan{}, errors.New("service_name is required")
	}
	if strings.TrimSpace(endpoint.ECSEntityID) == "" {
		return Plan{}, errors.New("ecs_entity_id is required")
	}
	if observedAt.IsZero() {
		return Plan{}, errors.New("observed time is required")
	}

	endpointID := EndpointEntityID(endpoint.EndpointID)
	observedAt = observedAt.UTC()
	observedAtUnix := observedAt.Unix()
	entity := map[string]any{
		"__domain__":             sreDomain,
		"__entity_type__":        endpointEntityType,
		"__entity_id__":          endpointID,
		"__method__":             "Update",
		"__last_observed_time__": observedAtUnix,
		"__keep_alive_seconds__": keepAliveSeconds,
		"endpoint_id":            endpoint.EndpointID,
		"service_name":           endpoint.ServiceName,
		"service_url":            endpoint.ServiceURL,
		"environment":            endpoint.Environment,
		"instance_id":            endpoint.InstanceID,
		"ecs_entity_id":          endpoint.ECSEntityID,
		"region_id":              endpoint.RegionID,
		"account_id":             endpoint.AccountID,
	}
	plan := Plan{Elements: []map[string]any{entity}}

	if strings.TrimSpace(relation.Type) == "" {
		return plan, nil
	}
	if !isSafeRelationType(relation.Type) {
		return Plan{}, fmt.Errorf("unsafe relation type %q", relation.Type)
	}
	if relation.DestinationDomain == "" {
		relation.DestinationDomain = acsDomain
	}
	if relation.DestinationEntityType == "" {
		relation.DestinationEntityType = ecsEntityType
	}
	if relation.DestinationDomain != acsDomain || relation.DestinationEntityType != ecsEntityType {
		return Plan{}, errors.New("relation destination must be acs:acs.ecs.instance")
	}
	plan.Elements = append(plan.Elements, map[string]any{
		"__src_domain__":         sreDomain,
		"__src_entity_type__":    endpointEntityType,
		"__src_entity_id__":      endpointID,
		"__dest_domain__":        acsDomain,
		"__dest_entity_type__":   ecsEntityType,
		"__dest_entity_id__":     endpoint.ECSEntityID,
		"__relation_type__":      relation.Type,
		"__method__":             "Update",
		"__last_observed_time__": observedAtUnix,
		"__keep_alive_seconds__": keepAliveSeconds,
	})
	return plan, nil
}

// BuildRelationExpirationPlan produces only the explicitly named directed
// topology relation with the documented Expire lifecycle method. It never
// includes an endpoint entity element, so an expiry operation cannot refresh
// or alter custom SRE entity data.
func BuildRelationExpirationPlan(endpoint Endpoint, relationType string, observedAt time.Time) (Plan, error) {
	if strings.TrimSpace(endpoint.EndpointID) == "" {
		return Plan{}, errors.New("endpoint_id is required")
	}
	if strings.TrimSpace(endpoint.ECSEntityID) == "" {
		return Plan{}, errors.New("ecs_entity_id is required")
	}
	if observedAt.IsZero() {
		return Plan{}, errors.New("observed time is required")
	}
	if !isSafeRelationType(relationType) {
		return Plan{}, fmt.Errorf("unsafe relation type %q", relationType)
	}

	return Plan{Elements: []map[string]any{{
		"__src_domain__":         sreDomain,
		"__src_entity_type__":    endpointEntityType,
		"__src_entity_id__":      EndpointEntityID(endpoint.EndpointID),
		"__dest_domain__":        acsDomain,
		"__dest_entity_type__":   ecsEntityType,
		"__dest_entity_id__":     endpoint.ECSEntityID,
		"__relation_type__":      relationType,
		"__method__":             "Expire",
		"__last_observed_time__": observedAt.UTC().Unix(),
	}}}, nil
}

func deterministicEntityID(endpointID string) string {
	return EndpointEntityID(endpointID)
}

// EndpointEntityID is the stable EntityStore identity shared by the static
// SRE endpoint projection and read-only topology evidence queries.
func EndpointEntityID(endpointID string) string {
	return umodelid.EndpointEntityID(endpointID)
}

func isSafeRelationType(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for index, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9' && index > 0) || char == '_' {
			continue
		}
		return false
	}
	return true
}
