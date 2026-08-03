package modelsync

import (
	"testing"
	"time"
)

func TestBuildPlanWritesOnlySREEndpointAndOptionalACSTopology(t *testing.T) {
	now := time.Date(2026, 7, 29, 1, 2, 3, 0, time.UTC)
	endpoint := Endpoint{
		EndpointID:  "blog-http",
		ServiceName: "blog",
		ServiceURL:  "https://jack-sre.com/",
		Environment: "prod",
		InstanceID:  "i-bp17eb4oiqsmy10fq9mu",
		ECSEntityID: "d713389806398932cf6b1ff1eaa86a8b",
		RegionID:    "cn-hangzhou",
		AccountID:   "1876202723954089",
	}

	plan, err := BuildPlan(endpoint, Relation{Type: "runs_on"}, now)
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	if got, want := len(plan.Elements), 2; got != want {
		t.Fatalf("len(plan.Elements) = %d, want %d", got, want)
	}

	entity := plan.Elements[0]
	if got, want := entity["__domain__"], "sre"; got != want {
		t.Errorf("entity domain = %q, want %q", got, want)
	}
	if got, want := entity["__entity_type__"], "sre.service_endpoint"; got != want {
		t.Errorf("entity type = %q, want %q", got, want)
	}
	if got, want := entity["endpoint_id"], endpoint.EndpointID; got != want {
		t.Errorf("entity endpoint_id = %q, want %q", got, want)
	}
	if got, want := entity["ecs_entity_id"], endpoint.ECSEntityID; got != want {
		t.Errorf("entity ecs_entity_id = %q, want %q", got, want)
	}
	if got, want := entity["__method__"], "Update"; got != want {
		t.Errorf("entity method = %q, want %q", got, want)
	}
	if got, want := entity["__last_observed_time__"], now.Unix(); got != want {
		t.Errorf("entity last observed time = %#v, want Unix seconds %d", got, want)
	}

	relation := plan.Elements[1]
	if got, want := relation["__src_domain__"], "sre"; got != want {
		t.Errorf("relation source domain = %q, want %q", got, want)
	}
	if got, want := relation["__dest_domain__"], "acs"; got != want {
		t.Errorf("relation destination domain = %q, want %q", got, want)
	}
	if got, want := relation["__dest_entity_type__"], "acs.ecs.instance"; got != want {
		t.Errorf("relation destination type = %q, want %q", got, want)
	}
	if got, want := relation["__dest_entity_id__"], endpoint.ECSEntityID; got != want {
		t.Errorf("relation destination id = %q, want %q", got, want)
	}
	if got, want := relation["__relation_type__"], "runs_on"; got != want {
		t.Errorf("relation type = %q, want %q", got, want)
	}
	if got, want := relation["__last_observed_time__"], now.Unix(); got != want {
		t.Errorf("relation last observed time = %#v, want Unix seconds %d", got, want)
	}
}

func TestBuildPlanSkipsRelationWhenNoSchemaRelationTypeIsConfigured(t *testing.T) {
	plan, err := BuildPlan(Endpoint{
		EndpointID:  "blog-http",
		ServiceName: "blog",
		ECSEntityID: "d713389806398932cf6b1ff1eaa86a8b",
	}, Relation{}, time.Now())
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	if got, want := len(plan.Elements), 1; got != want {
		t.Fatalf("len(plan.Elements) = %d, want %d", got, want)
	}
}

func TestBuildPlanRejectsUnsafeCrossDomainTarget(t *testing.T) {
	_, err := BuildPlan(Endpoint{
		EndpointID:  "blog-http",
		ServiceName: "blog",
		ECSEntityID: "d713389806398932cf6b1ff1eaa86a8b",
	}, Relation{Type: "runs_on", DestinationDomain: "sre"}, time.Now())
	if err == nil {
		t.Fatal("BuildPlan() error = nil, want destination domain rejection")
	}
}

func TestBuildRelationExpirationPlanProducesOnlyTheNamedRelation(t *testing.T) {
	now := time.Date(2026, 7, 31, 0, 12, 13, 0, time.UTC)
	endpoint := Endpoint{
		EndpointID:  "blog-http",
		ServiceName: "blog",
		ECSEntityID: "d713389806398932cf6b1ff1eaa86a8b",
	}

	plan, err := BuildRelationExpirationPlan(endpoint, "related_to", now)
	if err != nil {
		t.Fatalf("BuildRelationExpirationPlan() error = %v", err)
	}
	if got, want := len(plan.Elements), 1; got != want {
		t.Fatalf("len(plan.Elements) = %d, want %d", got, want)
	}

	relation := plan.Elements[0]
	for key, want := range map[string]any{
		"__src_domain__":         "sre",
		"__src_entity_type__":    "sre.service_endpoint",
		"__src_entity_id__":      "b0ccfdf2b903a8279c8a109aec71cba2",
		"__dest_domain__":        "acs",
		"__dest_entity_type__":   "acs.ecs.instance",
		"__dest_entity_id__":     "d713389806398932cf6b1ff1eaa86a8b",
		"__relation_type__":      "related_to",
		"__method__":             "Expire",
		"__last_observed_time__": now.Unix(),
	} {
		if got := relation[key]; got != want {
			t.Errorf("relation %s = %#v, want %#v", key, got, want)
		}
	}
	if _, found := relation["__keep_alive_seconds__"]; found {
		t.Error("expiry relation must omit __keep_alive_seconds__")
	}
	if _, found := relation["__domain__"]; found {
		t.Error("expiry plan must not contain an entity element")
	}
}
