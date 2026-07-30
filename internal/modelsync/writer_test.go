package modelsync

import (
	"testing"

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

func TestBuildGetUmodelDataRequestUsesReadOnlyGraphAPI(t *testing.T) {
	params, request, err := buildGetUmodelDataRequest("default-cms-1876202723954089-cn-hangzhou", []string{"sre"})
	if err != nil {
		t.Fatalf("buildGetUmodelDataRequest() error = %v", err)
	}
	if got, want := tea.StringValue(params.Action), "GetUmodelData"; got != want {
		t.Errorf("action = %q, want %q", got, want)
	}
	if got, want := tea.StringValue(params.Version), "2024-03-30"; got != want {
		t.Errorf("version = %q, want %q", got, want)
	}
	if got, want := tea.StringValue(params.Method), "POST"; got != want {
		t.Errorf("method = %q, want %q", got, want)
	}
	if got, want := tea.StringValue(params.Pathname), "/workspace/default-cms-1876202723954089-cn-hangzhou/umodel/graph"; got != want {
		t.Errorf("pathname = %q, want %q", got, want)
	}
	if got, want := tea.StringValue(request.Query["method"]), "ListData"; got != want {
		t.Errorf("query method = %q, want %q", got, want)
	}
	body, ok := request.Body.(map[string]any)
	if !ok {
		t.Fatalf("body type = %T, want map[string]any", request.Body)
	}
	content, ok := body["content"].(map[string]any)
	if !ok {
		t.Fatalf("content = %#v, want map[string]any", body["content"])
	}
	filter, ok := content["filter"].(map[string]any)
	if !ok {
		t.Fatalf("filter = %#v, want map[string]any", content["filter"])
	}
	domains, ok := filter["domains"].([]string)
	if !ok || len(domains) != 1 || domains[0] != "sre" {
		t.Errorf("domains = %#v, want [sre]", filter["domains"])
	}
}

func TestBuildGetUmodelDataRequestRejectsUnsafeWorkspace(t *testing.T) {
	_, _, err := buildGetUmodelDataRequest("workspace/../../other", []string{"sre"})
	if err == nil {
		t.Fatal("buildGetUmodelDataRequest() error = nil, want workspace validation failure")
	}
}
