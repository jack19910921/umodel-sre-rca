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
