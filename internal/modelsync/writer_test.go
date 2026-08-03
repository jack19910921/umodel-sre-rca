package modelsync

import (
	"context"
	"testing"
	"time"

	"github.com/alibabacloud-go/tea/tea"
	sls "github.com/aliyun/aliyun-log-go-sdk"
)

func TestBuildEntityStoreLogGroupUsesDocumentedSpecialFields(t *testing.T) {
	plan := Plan{Elements: []map[string]any{{
		"__domain__":             "sre",
		"__entity_type__":        "sre.service_endpoint",
		"__entity_id__":          "endpoint-1",
		"__last_observed_time__": int64(1_785_367_354),
		"__method__":             "Update",
		"endpoint_id":            "blog-http",
	}}}

	group, err := buildEntityStoreLogGroup(plan, time.Unix(1_785_367_354, 0).UTC())
	if err != nil {
		t.Fatalf("buildEntityStoreLogGroup() error = %v", err)
	}
	if got, want := tea.StringValue(group.Topic), "umodel-entity-store"; got != want {
		t.Errorf("topic = %q, want %q", got, want)
	}
	if len(group.Logs) != 1 {
		t.Fatalf("log count = %d, want 1", len(group.Logs))
	}
	if got, want := group.Logs[0].GetTime(), uint32(1_785_367_354); got != want {
		t.Errorf("log time = %d, want %d", got, want)
	}
	fields := logContents(group.Logs[0])
	for key, want := range map[string]string{
		"__domain__": "sre", "__entity_type__": "sre.service_endpoint",
		"__entity_id__": "endpoint-1", "__last_observed_time__": "1785367354",
		"__method__": "Update", "endpoint_id": "blog-http",
	} {
		if got := fields[key]; got != want {
			t.Errorf("field %q = %q, want %q", key, got, want)
		}
	}
}

func TestBuildEntityStoreLogGroupRejectsMissingRequiredField(t *testing.T) {
	_, err := buildEntityStoreLogGroup(Plan{Elements: []map[string]any{{
		"__domain__": "sre",
	}}}, time.Now())
	if err == nil {
		t.Fatal("buildEntityStoreLogGroup() error = nil, want validation failure")
	}
}

func TestCMSWriterUpsertWritesRelationToTopoLogstore(t *testing.T) {
	fake := &recordingLogClient{}
	writer := &CMSWriter{logClient: fake}
	plan := Plan{Elements: []map[string]any{
		{
			"__domain__": "sre", "__entity_type__": "sre.service_endpoint",
			"__entity_id__": "endpoint-1", "__last_observed_time__": 1,
		},
		{
			"__src_domain__": "sre", "__src_entity_type__": "sre.service_endpoint", "__src_entity_id__": "endpoint-1",
			"__dest_domain__": "acs", "__dest_entity_type__": "acs.ecs.instance", "__dest_entity_id__": "ecs-1",
			"__relation_type__": "related_to", "__last_observed_time__": 1,
		},
	}}
	if err := writer.Upsert(context.Background(), "default-cms-1876202723954089-cn-hangzhou", plan); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if got, want := len(fake.calls), 2; got != want {
		t.Fatalf("PutLogs call count = %d, want %d", got, want)
	}
	if got, want := fake.calls[0].logstore, "default-cms-1876202723954089-cn-hangzhou__entity"; got != want {
		t.Errorf("entity logstore = %q, want %q", got, want)
	}
	if got, want := fake.calls[1].logstore, "default-cms-1876202723954089-cn-hangzhou__topo"; got != want {
		t.Errorf("relation logstore = %q, want %q", got, want)
	}
	if got, want := len(fake.calls[1].group.Logs), 1; got != want {
		t.Fatalf("relation log count = %d, want %d", got, want)
	}
	fields := logContents(fake.calls[1].group.Logs[0])
	for key, want := range map[string]string{
		"__src_domain__": "sre", "__src_entity_type__": "sre.service_endpoint", "__src_entity_id__": "endpoint-1",
		"__dest_domain__": "acs", "__dest_entity_type__": "acs.ecs.instance", "__dest_entity_id__": "ecs-1",
		"__relation_type__": "related_to", "__last_observed_time__": "1",
	} {
		if got := fields[key]; got != want {
			t.Errorf("relation field %q = %q, want %q", key, got, want)
		}
	}
}

func TestCMSWriterUpsertWritesToEntityLogstore(t *testing.T) {
	fake := &recordingLogClient{}
	writer := &CMSWriter{logClient: fake}
	plan := Plan{Elements: []map[string]any{{
		"__domain__": "sre", "__entity_type__": "sre.service_endpoint",
		"__entity_id__": "endpoint-1", "__last_observed_time__": 1,
	}}}
	if err := writer.Upsert(context.Background(), "default-cms-1876202723954089-cn-hangzhou", plan); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if got, want := fake.project, "default-cms-1876202723954089-cn-hangzhou"; got != want {
		t.Errorf("project = %q, want %q", got, want)
	}
	if got, want := fake.logstore, "default-cms-1876202723954089-cn-hangzhou__entity"; got != want {
		t.Errorf("logstore = %q, want %q", got, want)
	}
}

type recordingLogClient struct {
	project  string
	logstore string
	group    *sls.LogGroup
	calls    []putLogsCall
}

func (c *recordingLogClient) PutLogs(project, logstore string, group *sls.LogGroup) error {
	c.project, c.logstore, c.group = project, logstore, group
	c.calls = append(c.calls, putLogsCall{project: project, logstore: logstore, group: group})
	return nil
}

type putLogsCall struct {
	project  string
	logstore string
	group    *sls.LogGroup
}

func logContents(log *sls.Log) map[string]string {
	fields := make(map[string]string, len(log.Contents))
	for _, content := range log.Contents {
		fields[content.GetKey()] = content.GetValue()
	}
	return fields
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

func TestBuildGetEntityStoreRelationRequestUsesDocumentedNeighborTraversal(t *testing.T) {
	params, request, err := buildGetEntityStoreRelationRequest(
		"default-cms-1876202723954089-cn-hangzhou",
		Endpoint{EndpointID: "blog-http", ServiceName: "blog", ECSEntityID: "d713389806398932cf6b1ff1eaa86a8b"},
		Relation{Type: "related_to"},
		time.Unix(1_785_367_354, 0).UTC(),
	)
	if err != nil {
		t.Fatalf("buildGetEntityStoreRelationRequest() error = %v", err)
	}
	if got, want := tea.StringValue(params.Action), "GetEntityStoreData"; got != want {
		t.Errorf("action = %q, want %q", got, want)
	}
	body, ok := request.Body.(map[string]any)
	if !ok {
		t.Fatalf("body type = %T, want map[string]any", request.Body)
	}
	if got, want := body["query"], ".topo | graph-call getNeighborNodes('sequence_out', 1, [(:\"sre@sre.service_endpoint\" {__entity_id__: 'b0ccfdf2b903a8279c8a109aec71cba2'})]) | where relationType = 'related_to' | extend dest_id = json_extract_scalar(destNode, '$.properties.__entity_id__') | where dest_id = 'd713389806398932cf6b1ff1eaa86a8b'"; got != want {
		t.Errorf("body query = %#v, want %#v", got, want)
	}
}

func TestBuildGetEntityStoreDataRequestRejectsUnsafeWorkspace(t *testing.T) {
	_, _, err := buildGetEntityStoreDataRequest("workspace/../../other", "sre", "sre.service_endpoint", time.Now())
	if err == nil {
		t.Fatal("buildGetEntityStoreDataRequest() error = nil, want workspace validation failure")
	}
}
