package provider

import (
	"testing"

	"github.com/alibabacloud-go/tea/tea"
)

func TestBuildEntityStoreDataRequestUsesCMSROAJSONContract(t *testing.T) {
	params, request, err := buildEntityStoreDataRequest(map[string]string{
		"Workspace": "default-cms-1876202723954089-cn-hangzhou",
		"From":      "1785500000",
		"To":        "1785503600",
		"Query":     ".topo | graph-call getNeighborNodes('sequence_out', 1, [(:\"sre@sre.service_endpoint\" {__entity_id__: 'b0ccfdf2b903a8279c8a109aec71cba2'})]) | where relationType = 'runs_on'",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := tea.StringValue(params.Style), "ROA"; got != want {
		t.Fatalf("style = %q, want %q", got, want)
	}
	if got, want := tea.StringValue(params.Pathname), "/workspace/default-cms-1876202723954089-cn-hangzhou/entitiesAndRelations"; got != want {
		t.Fatalf("pathname = %q, want %q", got, want)
	}
	body, ok := request.Body.(map[string]any)
	if !ok {
		t.Fatalf("body type = %T", request.Body)
	}
	if got, want := body["from"], int64(1785500000); got != want {
		t.Errorf("from = %#v, want %#v", got, want)
	}
	if got, want := body["to"], int64(1785503600); got != want {
		t.Errorf("to = %#v, want %#v", got, want)
	}
}

func TestNewAliyunSDKClientSetsRegionalSLSEndpoint(t *testing.T) {
	client, err := NewAliyunSDKClient("cn-hangzhou", "sre-rca")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := tea.StringValue(client.sls.Endpoint), "cn-hangzhou.log.aliyuncs.com"; got != want {
		t.Fatalf("SLS endpoint = %q, want %q", got, want)
	}
}
