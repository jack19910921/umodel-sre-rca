package evidence

import (
	"context"
	"testing"
	"time"

	"github.com/jack/umodel-sre-rca/internal/domain"
)

type fakeIncidentSource struct{}

func (fakeIncidentSource) ResolveIncident(context.Context, string) (IncidentContext, error) {
	return IncidentContext{
		Selectors: Selectors{"endpoint_id": "blog-health", "instance_id": "i-demo"},
		Window:    Window{Start: time.Unix(100, 0), End: time.Unix(200, 0)},
	}, nil
}

type fakeProvider struct{ source string }

func (f fakeProvider) Resolve(_ context.Context, binding Binding, _ Selectors, _ Window) ([]domain.Evidence, error) {
	return []domain.Evidence{{ID: binding.ID + "-evidence", Type: "log", Source: f.source, Summary: binding.QueryTemplate}}, nil
}

func TestServiceReturnsOnlyBindingsForRequestedEvidenceClass(t *testing.T) {
	registry, err := LoadRegistry("../../testdata/evidence-bindings-valid.yaml")
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(registry, fakeIncidentSource{}, map[string]Provider{
		"aliyun.sls": fakeProvider{source: "sls"},
	})
	got, err := service.Logs(context.Background(), "inc-1")
	if err != nil || len(got) != 1 || got[0].ID != "nginx_access_log-evidence" {
		t.Fatalf("got=%#v err=%v", got, err)
	}
}
