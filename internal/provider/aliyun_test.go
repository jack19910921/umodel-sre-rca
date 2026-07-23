package provider

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jack/umodel-sre-rca/internal/evidence"
)

type fakeExec struct{ output []byte }

func (f fakeExec) Run(context.Context, string, ...string) ([]byte, error) { return f.output, nil }

func TestAliyunRunnerRejectsWriteAction(t *testing.T) {
	_, err := NewAliyunRunner("sre-ecs-role", fakeExec{}).Run(context.Background(), "ecs", "AuthorizeSecurityGroup", nil)
	if err == nil || !strings.Contains(err.Error(), "not allowlisted") {
		t.Fatalf("err=%v", err)
	}
}

func TestActionTrailProviderBuildsChangeEvidence(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/aliyun-actiontrail-events.json")
	if err != nil {
		t.Fatal(err)
	}
	provider := NewAliyunProvider(AliyunConfig{Profile: "sre-ecs-role", Workspace: "demo", Region: "cn-hangzhou"}, NewAliyunRunner("sre-ecs-role", fakeExec{output: raw}))
	got, err := provider.Resolve(context.Background(), evidence.Binding{ID: "security_group_change", Provider: "aliyun.actiontrail", QueryTemplate: "security_group_change_window_v1"}, evidence.Selectors{"security_group_id": "sg-demo"}, evidence.Window{Start: time.Unix(100, 0), End: time.Unix(200, 0)})
	if err != nil || len(got) != 1 || got[0].Type != "change" || !strings.Contains(got[0].Summary, "RevokeSecurityGroup") {
		t.Fatalf("got=%#v err=%v", got, err)
	}
}

func TestAliyunProviderRejectsUnsafeSelectorBeforeCloudRequest(t *testing.T) {
	provider := NewAliyunProvider(AliyunConfig{Profile: "sre-ecs-role", Workspace: "demo", Region: "cn-hangzhou"}, NewAliyunRunner("sre-ecs-role", fakeExec{}))
	_, err := provider.Resolve(context.Background(), evidence.Binding{ID: "context", Provider: "aliyun.umodel", QueryTemplate: "endpoint_context_v1"}, evidence.Selectors{
		"endpoint_id": "blog-http",
		"account_id":  "unsafe value with spaces",
	}, evidence.Window{Start: time.Unix(100, 0), End: time.Unix(200, 0)})
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("Resolve() error = %v, want selector validation", err)
	}
}
