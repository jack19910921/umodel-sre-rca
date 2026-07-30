package modelsync

import (
	"testing"
	"time"

	"github.com/alibabacloud-go/tea/tea"
)

func TestBuildUpsertRequestUsesOnlyTheDedicatedUModelWriteAPI(t *testing.T) {
	plan := Plan{Elements: []map[string]any{{
		"__domain__":      "sre",
		"__entity_type__": "sre.service_endpoint",
	}}}

	params, request, err := buildUpsertRequest("default-cms-1876202723954089-cn-hangzhou", plan)
	if err != nil {
		t.Fatalf("buildUpsertRequest() error = %v", err)
	}
	if got, want := tea.StringValue(params.Action), "UpsertUmodelData"; got != want {
		t.Errorf("action = %q, want %q", got, want)
	}
	if got, want := tea.StringValue(params.Version), "2024-03-30"; got != want {
		t.Errorf("version = %q, want %q", got, want)
	}
	if got, want := tea.StringValue(params.Style), "ROA"; got != want {
		t.Errorf("style = %q, want %q", got, want)
	}
	if got, want := tea.StringValue(params.Method), "PATCH"; got != want {
		t.Errorf("method = %q, want %q", got, want)
	}
	if got, want := tea.StringValue(params.Pathname), "/workspace/default-cms-1876202723954089-cn-hangzhou/umodel/data"; got != want {
		t.Errorf("pathname = %q, want %q", got, want)
	}
	body, ok := request.Body.(map[string]any)
	if !ok {
		t.Fatalf("body type = %T, want map[string]any", request.Body)
	}
	if got, want := tea.StringValue(request.Query["method"]), "upsert"; got != want {
		t.Errorf("query method = %q, want %q", got, want)
	}
	if _, ok := body["method"]; ok {
		t.Errorf("body must not contain method: %#v", body)
	}
	elements, ok := body["elements"].([]map[string]any)
	if !ok || len(elements) != 1 {
		t.Fatalf("body elements = %#v, want one element", body["elements"])
	}
}

func TestBuildUpsertRequestRejectsUnsafeWorkspace(t *testing.T) {
	_, _, err := buildUpsertRequest("workspace/../../other", Plan{})
	if err == nil {
		t.Fatal("buildUpsertRequest() error = nil, want workspace validation failure")
	}
}

func TestBuildGetEntityStoreDataRequestUsesSupportedReadOnlyAPI(t *testing.T) {
	params, request, err := buildGetEntityStoreDataRequest(
		"default-cms-1876202723954089-cn-hangzhou",
		"sre",
		"sre.service_endpoint",
		time.Unix(1_785_367_354, 0).UTC(),
	)
	if err != nil {
		t.Fatalf("buildGetEntityStoreDataRequest() error = %v", err)
	}
	if got, want := tea.StringValue(params.Action), "GetEntityStoreData"; got != want {
		t.Errorf("action = %q, want %q", got, want)
	}
	if got, want := tea.StringValue(params.Version), "2024-03-30"; got != want {
		t.Errorf("version = %q, want %q", got, want)
	}
	if got, want := tea.StringValue(params.Method), "POST"; got != want {
		t.Errorf("method = %q, want %q", got, want)
	}
	if got, want := tea.StringValue(params.Style), "ROA"; got != want {
		t.Errorf("style = %q, want %q", got, want)
	}
	if got, want := tea.StringValue(params.Pathname), "/workspace/default-cms-1876202723954089-cn-hangzhou/entitiesAndRelations"; got != want {
		t.Errorf("pathname = %q, want %q", got, want)
	}
	if got, want := tea.StringValue(params.ReqBodyType), "json"; got != want {
		t.Errorf("request body type = %q, want %q", got, want)
	}
	if len(request.Query) != 0 {
		t.Errorf("query parameters = %#v, want none", request.Query)
	}
	body, ok := request.Body.(map[string]any)
	if !ok {
		t.Fatalf("body type = %T, want map[string]any", request.Body)
	}
	if got, want := body["query"], ".entity with(domain='sre', type='sre.service_endpoint') | limit 0, 10"; got != want {
		t.Errorf("body query = %#v, want %#v", got, want)
	}
	if got, want := body["from"], int64(1_785_194_554); got != want {
		t.Errorf("body from = %#v, want %#v", got, want)
	}
	if got, want := body["to"], int64(1_785_367_354); got != want {
		t.Errorf("body to = %#v, want %#v", got, want)
	}
}

func TestBuildGetEntityStoreDataRequestRejectsUnsafeWorkspace(t *testing.T) {
	_, _, err := buildGetEntityStoreDataRequest("workspace/../../other", "sre", "sre.service_endpoint", time.Now())
	if err == nil {
		t.Fatal("buildGetEntityStoreDataRequest() error = nil, want workspace validation failure")
	}
}
