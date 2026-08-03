package provider

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jack/umodel-sre-rca/internal/evidence"
)

type fakeCloudAPI struct {
	output   []byte
	requests []AliyunRequest
}

func (f *fakeCloudAPI) Call(_ context.Context, request AliyunRequest) ([]byte, error) {
	f.requests = append(f.requests, request)
	return f.output, nil
}

func TestActionTrailProviderBuildsChangeEvidence(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/aliyun-actiontrail-events.json")
	if err != nil {
		t.Fatal(err)
	}
	cloud := &fakeCloudAPI{output: raw}
	provider := NewAliyunProvider(AliyunConfig{Workspace: "demo", Region: "cn-hangzhou"}, cloud)
	got, err := provider.Resolve(context.Background(), evidence.Binding{ID: "security_group_change", Provider: "aliyun.actiontrail", QueryTemplate: "security_group_change_window_v1"}, evidence.Selectors{"security_group_id": "sg-demo"}, evidence.Window{Start: time.Unix(100, 0), End: time.Unix(200, 0)})
	if err != nil || len(got) != 1 || got[0].Type != "change" || !strings.Contains(got[0].Summary, "RevokeSecurityGroup") {
		t.Fatalf("got=%#v err=%v", got, err)
	}
	if len(cloud.requests) != 1 || cloud.requests[0].Service != "actiontrail" || cloud.requests[0].Operation != "LookupEvents" {
		t.Fatalf("requests=%#v, want one fixed ActionTrail read request", cloud.requests)
	}
}

func TestAliyunProviderRejectsUnsafeSelectorBeforeCloudRequest(t *testing.T) {
	cloud := &fakeCloudAPI{}
	provider := NewAliyunProvider(AliyunConfig{Workspace: "demo", Region: "cn-hangzhou"}, cloud)
	_, err := provider.Resolve(context.Background(), evidence.Binding{ID: "context", Provider: "aliyun.umodel", QueryTemplate: "endpoint_context_v1"}, evidence.Selectors{
		"endpoint_id": "blog-http",
		"account_id":  "unsafe value with spaces",
	}, evidence.Window{Start: time.Unix(100, 0), End: time.Unix(200, 0)})
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("Resolve() error = %v, want selector validation", err)
	}
	if len(cloud.requests) != 0 {
		t.Fatalf("requests=%#v, want no cloud request for an unsafe selector", cloud.requests)
	}
}

func TestAliyunProviderSeparatesNginxAccessAndErrorLogQueries(t *testing.T) {
	cloud := &fakeCloudAPI{output: []byte(`{"logs":[]}`)}
	provider := NewAliyunProvider(AliyunConfig{SLSProject: "sre-rca-demo", SLSLogstore: "nginx"}, cloud)
	window := evidence.Window{Start: time.Unix(100, 0), End: time.Unix(200, 0)}
	selectors := evidence.Selectors{"endpoint_id": "blog-http", "instance_id": "i-bp17eb4oiqsmy10fq9mu"}

	if _, err := provider.Resolve(context.Background(), evidence.Binding{ID: "access", Provider: "aliyun.sls", QueryTemplate: "nginx_access_by_window_v1"}, selectors, window); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Resolve(context.Background(), evidence.Binding{ID: "error", Provider: "aliyun.sls", QueryTemplate: "nginx_error_by_window_v1"}, selectors, window); err != nil {
		t.Fatal(err)
	}

	if len(cloud.requests) != 2 {
		t.Fatalf("requests=%#v, want two SLS queries", cloud.requests)
	}
	for _, request := range cloud.requests {
		if got, want := request.Operation, "GetLogs"; got != want {
			t.Fatalf("SLS operation = %q, want generated SDK GetLogs", got)
		}
	}
	if got := cloud.requests[0].Body["query"]; got != "endpoint_id:\"blog-http\" AND log_kind:\"access\" AND instance_id:\"i-bp17eb4oiqsmy10fq9mu\"" {
		t.Fatalf("access query = %v", got)
	}
	if got := cloud.requests[1].Body["query"]; got != "endpoint_id:\"blog-http\" AND log_kind:\"error\" AND instance_id:\"i-bp17eb4oiqsmy10fq9mu\"" {
		t.Fatalf("error query = %v", got)
	}
}

func TestBuildNginxLogsRequestQuotesEverySelectorValue(t *testing.T) {
	request, err := buildNginxLogsRequest(
		AliyunConfig{SLSProject: "sre-rca-demo", SLSLogstore: "nginx"},
		"access",
		evidence.Selectors{"endpoint_id": "blog-http", "instance_id": "i-demo"},
		evidence.Window{Start: time.Unix(100, 0), End: time.Unix(200, 0)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := request.Service, "sls"; got != want {
		t.Fatalf("service = %q, want %q", got, want)
	}
	if got, want := request.Operation, "GetLogs"; got != want {
		t.Fatalf("operation = %q, want %q", got, want)
	}
	if got, want := request.Body["query"], "endpoint_id:\"blog-http\" AND log_kind:\"access\" AND instance_id:\"i-demo\""; got != want {
		t.Fatalf("query = %#v, want %#v", got, want)
	}
}

func TestAliyunProviderBuildsECSCPUWindowMetricRequest(t *testing.T) {
	cloud := &fakeCloudAPI{output: []byte(`{"Datapoints":"[]"}`)}
	provider := NewAliyunProvider(AliyunConfig{Region: "cn-hangzhou"}, cloud)
	window := evidence.Window{Start: time.Unix(100, 0), End: time.Unix(200, 0)}

	_, err := provider.Resolve(context.Background(), evidence.Binding{
		ID: "ecs_cpu", Provider: "aliyun.cloudmonitor", QueryTemplate: "ecs_normal_state_window_v1",
	}, evidence.Selectors{"instance_id": "i-bp17eb4oiqsmy10fq9mu", "region_id": "cn-hangzhou"}, window)
	if err != nil {
		t.Fatal(err)
	}
	if len(cloud.requests) != 1 {
		t.Fatalf("requests=%#v, want one CMS request", cloud.requests)
	}
	request := cloud.requests[0]
	if request.Service != "cms" || request.Operation != "DescribeMetricList" {
		t.Fatalf("request=%#v, want CMS DescribeMetricList", request)
	}
	if got, want := request.Query["Namespace"], "acs_ecs_dashboard"; got != want {
		t.Fatalf("Namespace=%q, want %q", got, want)
	}
	if got, want := request.Query["MetricName"], "cpu_total"; got != want {
		t.Fatalf("MetricName=%q, want %q", got, want)
	}
	if got, want := request.Query["Period"], "60"; got != want {
		t.Fatalf("Period=%q, want %q", got, want)
	}
	if got, want := request.Query["Dimensions"], "[{\"instanceId\":\"i-bp17eb4oiqsmy10fq9mu\"}]"; got != want {
		t.Fatalf("Dimensions=%q, want %q", got, want)
	}
}

func TestAliyunProviderSummarizesECSCPUWithoutReturningRawDatapoints(t *testing.T) {
	cloud := &fakeCloudAPI{output: []byte(`{"body":{"Datapoints":"[{\"Average\":91.5,\"Maximum\":100,\"Minimum\":80},{\"Average\":95,\"Maximum\":99,\"Minimum\":90}]"}}`)}
	provider := NewAliyunProvider(AliyunConfig{Region: "cn-hangzhou"}, cloud)

	got, err := provider.Resolve(context.Background(), evidence.Binding{
		ID: "ecs_cpu", Provider: "aliyun.cloudmonitor", QueryTemplate: "ecs_normal_state_window_v1",
	}, evidence.Selectors{"instance_id": "i-bp17eb4oiqsmy10fq9mu"}, evidence.Window{Start: time.Unix(100, 0), End: time.Unix(200, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("evidence=%#v, want one metric", got)
	}
	if got, want := got[0].Summary, "云监控 ECS CPU：2 个数据点，平均值 93.25%，最大值 100.00%，最小值 80.00%"; got != want {
		t.Fatalf("summary=%q, want %q", got, want)
	}
}

func TestAliyunProviderSummarizesNginxAccessWithoutReturningRawLogs(t *testing.T) {
	cloud := &fakeCloudAPI{output: []byte(`{"body":[{"http_code":"200"},{"http_code":"502"},{"http_code":"503"}]}`)}
	provider := NewAliyunProvider(AliyunConfig{SLSProject: "sre-rca-demo", SLSLogstore: "nginx"}, cloud)

	got, err := provider.Resolve(context.Background(), evidence.Binding{
		ID: "access", Provider: "aliyun.sls", QueryTemplate: "nginx_access_by_window_v1",
	}, evidence.Selectors{"endpoint_id": "blog-http", "instance_id": "i-bp17eb4oiqsmy10fq9mu"}, evidence.Window{Start: time.Unix(100, 0), End: time.Unix(200, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("evidence=%#v, want one log evidence", got)
	}
	if got, want := got[0].Summary, "Nginx access 日志：3 条，5xx 响应 2 条"; got != want {
		t.Fatalf("summary=%q, want %q", got, want)
	}
}

func TestAliyunProviderResolvesConfiguredRunsOnTopologyAsContextEvidence(t *testing.T) {
	cloud := &fakeCloudAPI{output: []byte(`{"data":[["runs_on"]]}`)}
	provider := NewAliyunProvider(AliyunConfig{Workspace: "default-cms-1876202723954089-cn-hangzhou"}, cloud)
	window := evidence.Window{Start: time.Unix(100, 0), End: time.Unix(200, 0)}

	got, err := provider.Resolve(context.Background(), evidence.Binding{
		ID: "endpoint_runs_on_topology", Provider: "aliyun.umodel", QueryTemplate: "endpoint_topology_v1",
	}, evidence.Selectors{"endpoint_id": "blog-http"}, window)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Type != "context" || !strings.Contains(got[0].Summary, "topology") {
		t.Fatalf("evidence = %#v", got)
	}
	if len(cloud.requests) != 1 {
		t.Fatalf("requests = %#v, want one EntityStore request", cloud.requests)
	}
	request := cloud.requests[0]
	if request.Service != "cms" || request.Operation != "GetEntityStoreData" {
		t.Fatalf("request = %#v, want CMS GetEntityStoreData", request)
	}
	if got, want := request.Query["Query"], ".topo | graph-call getNeighborNodes('sequence_out', 1, [(:\"sre@sre.service_endpoint\" {__entity_id__: 'b0ccfdf2b903a8279c8a109aec71cba2'})]) | where relationType = 'runs_on'"; got != want {
		t.Fatalf("topology query = %q, want %q", got, want)
	}
}
